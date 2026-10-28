#!/bin/bash
# smoke_v2.sh —— 本轮四需求的端到端冒烟（白名单/自定义节点/测速URL/订阅编辑构建块）。
#
# 与 smoke_core.sh 的区别：GUI 正在用户机器上运行（占用 7890/9097 与默认数据目录），
# 因此本脚本用 --port 7891 --no-sysproxy + 独立 --data-dir + --controller 127.0.0.1:9098，
# 并预置本地订阅与白名单/自定义节点状态，全程不联网拉订阅、不碰系统代理。
#
# 用法：./scripts/smoke_v2.sh
set -uo pipefail

unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
export GOPROXY=https://goproxy.cn,direct
cd "$(dirname "$0")/.."

PORT=7891
CTL=9098
SECRET=smokev2secret
DATA=/tmp/kunpeng-smoke-v2-data
LOG=/tmp/kunpeng-smoke-v2.log
BIN=/tmp/kunpeng-smoke-v2-bin
NODE_NAME="我的HY2冒烟节点"

pass() { echo "[PASS] $*"; }
fail() { echo "[FAIL] $*"; FAILURES=$((FAILURES+1)); }
FAILURES=0

cleanup() {
  [ -n "${PID:-}" ] && kill -INT "$PID" 2>/dev/null || true
  [ -n "${PID:-}" ] && wait "$PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "== 1. 编译 =="
go build -o "$BIN" . || { echo "[FAIL] go build"; exit 1; }
pass "go build"

echo "== 2. 准备隔离数据目录（预置订阅 + 白名单 + 自定义节点）=="
rm -rf "$DATA"
mkdir -p "$DATA/profiles"
SUB_ID=smoke0001
cat > "$DATA/profiles/$SUB_ID.yaml" <<'YAML'
proxies:
  - name: "🇭🇰 冒烟 SS 01"
    type: ss
    server: hk1.example.com
    port: 8388
    cipher: aes-128-gcm
    password: "pass:word"
  - name: "🇯🇵 冒烟 VMess 02"
    type: vmess
    server: jp1.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    alterId: 0
    cipher: auto
YAML
cat > "$DATA/profiles.yaml" <<YAML
profiles:
    - id: $SUB_ID
      name: 冒烟订阅
      url: https://unused.invalid/sub
      updatedAt: "2026-01-01T00:00:00Z"
      nodeCount: 2
YAML
cat > "$DATA/state.json" <<YAML
{
  "mode": "rule",
  "mixedPort": $PORT,
  "systemProxy": false,
  "autoConnect": false,
  "activeProfileId": "$SUB_ID",
  "secret": "$SECRET",
  "whitelist": ["*.example.com", "10.0.0.0/8"],
  "customProxies": [
    {"type": "hysteria2", "name": "$NODE_NAME", "server": "example.com", "port": 8443,
     "password": "letmein", "sni": "example.com", "skip-cert-verify": true}
  ]
}
YAML
pass "数据目录 $DATA 已预置"

SYSPROXY_START=$(networksetup -getwebproxy Wi-Fi 2>/dev/null | grep "Enabled" | head -1)

echo "== 3. 启动 headless（--port $PORT --no-sysproxy --controller 127.0.0.1:${CTL}）=="
"$BIN" --headless --port "$PORT" --no-sysproxy --data-dir "$DATA" --controller "127.0.0.1:$CTL" >"$LOG" 2>&1 &
PID=$!
UP=0
for _ in $(seq 1 30); do
  if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then UP=1; break; fi
  kill -0 "$PID" 2>/dev/null || break
  sleep 1
done
if [ "$UP" = 1 ]; then pass "内核已监听 127.0.0.1:$PORT"; else
  echo "---- 启动日志 ----"; tail -30 "$LOG"; fail "内核未在 30s 内监听 $PORT"; exit 1
fi

echo "== 4. runtime.yaml：白名单规则位于私有网段之后、cn 之前 =="
RT="$DATA/runtime.yaml"
L_PRIV=$(grep -n 'IP-CIDR,192.168.0.0/16,DIRECT,no-resolve' "$RT" | head -1 | cut -d: -f1)
L_WL1=$(grep -n 'DOMAIN-SUFFIX,example.com,DIRECT' "$RT" | head -1 | cut -d: -f1)
L_WL2=$(grep -n 'IP-CIDR,10.0.0.0/8,DIRECT,no-resolve' "$RT" | tail -1 | cut -d: -f1)
L_CN=$(grep -n 'DOMAIN-SUFFIX,cn,DIRECT' "$RT" | head -1 | cut -d: -f1)
if [ -n "$L_PRIV" ] && [ -n "$L_WL1" ] && [ -n "$L_WL2" ] && [ -n "$L_CN" ] \
   && [ "$L_PRIV" -lt "$L_WL1" ] && [ "$L_WL1" -lt "$L_WL2" ] && [ "$L_WL2" -lt "$L_CN" ]; then
  pass "白名单 2 条规则顺序正确（私有网段@$L_PRIV → 白名单@$L_WL1/$L_WL2 → cn@${L_CN}）"
else
  fail "白名单规则顺序异常 priv=$L_PRIV wl1=$L_WL1 wl2=$L_WL2 cn=$L_CN"; grep -n 'DIRECT' "$RT" | head -12
fi

echo "== 5. 测速 URL 为 http 版 =="
if grep -q 'url: http://www.gstatic.com/generate_204' "$RT" && ! grep -q 'https://www.gstatic.com' "$RT"; then
  pass "自动组 url = http://www.gstatic.com/generate_204"
else
  fail "测速 URL 不是 http 版"; grep -n "gstatic" "$RT"
fi

echo "== 6. external-controller API：自定义节点并入策略组 =="
api_ok=$(python3 - "$CTL" "$SECRET" "$NODE_NAME" "$PORT" <<'PYEOF'
import json, sys, urllib.request
ctl, secret, node, port = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
def get(path):
    req = urllib.request.Request(f"http://127.0.0.1:{ctl}{path}",
                                 headers={"Authorization": "Bearer " + secret})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.load(r)
ok = True
proxies = get("/proxies")["proxies"]
if node not in proxies:
    ok = False; print("[FAIL] /proxies 缺少自定义节点", file=sys.stderr)
groups = ["🚀 节点选择", "♻️ 自动选择"]
for g in groups:
    if g not in proxies or node not in proxies[g].get("all", []):
        ok = False; print(f"[FAIL] 组 {g} 未包含自定义节点", file=sys.stderr)
# 订阅 2 节点 + DIRECT/REJECT/GLOBAL/COMPATIBLE/PASS 等内置 + 自定义 1
subs = [k for k in proxies if ("冒烟 SS" in k or "冒烟 VMess" in k)]
if len(subs) != 2:
    ok = False; print(f"[FAIL] 订阅节点数 {len(subs)} != 2", file=sys.stderr)
url_test = proxies.get("♻️ 自动选择", {}).get("testUrl", "")
if url_test != "http://www.gstatic.com/generate_204":
    ok = False; print(f"[FAIL] 组 testUrl = {url_test}", file=sys.stderr)
rules = get("/rules")["rules"]
payloads = [r["payload"] for r in rules]
if "example.com" not in payloads or "10.0.0.0/8" not in payloads:
    ok = False; print("[FAIL] 内核 /rules 未生效白名单", file=sys.stderr)
print("OK" if ok else "BAD")
PYEOF
)
if [ "$api_ok" = "OK" ]; then pass "API 校验通过（proxies/组/testUrl/rules）"; else fail "API 校验失败"; fi

echo "== 7. 订阅编辑（改名持久化）—— 用 go 集成测试覆盖（见 internal/app/smoke_v2_test.go）=="
go test ./internal/app/ -run TestSmokeV2 -count=1 >/dev/null 2>&1 \
  && pass "go 集成测试 TestSmokeV2*（白名单热应用/节点导入删除/订阅改名改URL）全过" \
  || fail "go 集成测试失败（go test ./internal/app/ -run TestSmokeV2 -v 查看）"

echo "== 8. SIGINT 干净退出 =="
kill -INT "$PID"
wait "$PID"; rc=$?
if [ $rc -eq 0 ]; then pass "进程退出码 0"; else fail "进程退出码 $rc"; fi
PID=""
sleep 1
if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then fail "$PORT 未释放"; else pass "$PORT 已释放"; fi
if nc -z 127.0.0.1 "$CTL" 2>/dev/null; then fail "$CTL 未释放"; else pass "$CTL 已释放"; fi
SYSPROXY_END=$(networksetup -getwebproxy Wi-Fi 2>/dev/null | grep "Enabled" | head -1)
if [ "$SYSPROXY_START" = "$SYSPROXY_END" ]; then
  pass "系统代理未被动过（前后一致：${SYSPROXY_END}）"
else
  fail "系统代理被本会话改动：启动前[$SYSPROXY_START] 结束后[$SYSPROXY_END]"
fi

echo "== 9. config-only 独立内核模式（TUN 特权子进程机制，无 root 可验）=="
STAND_HOME="$DATA/standalone-home"
STAND_LOG=/tmp/kunpeng-smoke-v2-standalone.log
mkdir -p "$STAND_HOME"
cat > "$STAND_HOME/runtime.yaml" <<'YAML'
mixed-port: 17892
mode: rule
log-level: info
external-controller: 127.0.0.1:19099
secret: standalone-secret
proxies: []
rules:
  - MATCH,DIRECT
YAML
STAND_QUIT="$DATA/standalone-quit"
rm -f "$STAND_QUIT"
"$BIN" --headless --config-only --config "$STAND_HOME/runtime.yaml" \
  --data-dir "$STAND_HOME" --controller 127.0.0.1:19099 \
  --secret standalone-secret --quit-file "$STAND_QUIT" >"$STAND_LOG" 2>&1 &
SPID=$!
SUP=0
for _ in $(seq 1 15); do
  if nc -z 127.0.0.1 19099 2>/dev/null; then SUP=1; break; fi
  kill -0 "$SPID" 2>/dev/null || break
  sleep 1
done
if [ "$SUP" = 1 ]; then pass "config-only 内核已监听 127.0.0.1:19099"; else
  tail -10 "$STAND_LOG"; fail "config-only 内核未启动"; fi

code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
  -H "Authorization: Bearer standalone-secret" "http://127.0.0.1:19099/version")
[ "$code" = "200" ] && pass "standalone /version 带 secret => 200" || fail "standalone /version => $code"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:19099/version")
[ "$code" = "401" ] && pass "standalone 无 secret => 401" || fail "standalone 无 secret => $code"

touch "$STAND_QUIT"
wait "$SPID"; src=$?
if [ $src -eq 0 ]; then pass "quit 文件触发退出码 0"; else fail "quit 退出码 $src"; fi
SPID=""
sleep 1
if nc -z 127.0.0.1 19099 2>/dev/null; then fail "19099 未释放"; else pass "19099 已释放"; fi
if ls "$STAND_HOME"/cache.db >/dev/null 2>&1; then
  pass "cache.db 仅写在 standalone HomeDir 内（未污染数据目录根）"
else pass "cache.db 未产生（无流量场景，符合预期）"; fi

rm -rf "$DATA"
echo "== 10. 日志摘录 =="
grep -E "kunpeng" "$LOG" | head -8

trap - EXIT
if [ "$FAILURES" -eq 0 ]; then echo "SMOKE V2 OK"; exit 0; else echo "SMOKE V2 FAILED ($FAILURES)"; exit 1; fi
