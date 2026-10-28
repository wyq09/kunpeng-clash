#!/bin/bash
# smoke_core.sh —— 内核层端到端冒烟测试（DESIGN.md §11.2）
#
# 用法：./scripts/smoke_core.sh
# 前置：端口 7891 可用（7890 留给用户自己的 Clash Verge，绝不占用）。
# 步骤：headless 启动（--no-sysproxy，不动系统代理）→ 204 探活 →
#       google HTML → external-controller API → 预置订阅导入 →
#       SIGINT 退出 → 端口释放检查。
set -uo pipefail

# 清掉 shell 残留代理变量后再测试。
# 注意：测试走 -x 指定代理时不要加 --noproxy '*'（那会连 -x 一起禁用，
# 导致请求直连：gstatic 在国内可直连会假 204，google 直连必超时）。
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
export GOPROXY=https://goproxy.cn,direct
cd "$(dirname "$0")/.."

PORT=7891
CTL=9097
DATA_DIR="$HOME/Library/Application Support/KunpengClash"
LOG=/tmp/kunpeng-smoke.log
BIN=/tmp/kunpeng-smoke-bin

pass() { echo "[PASS] $*"; }
fail() { echo "[FAIL] $*"; FAILURES=$((FAILURES+1)); }
FAILURES=0

echo "== 1. 编译 =="
go build -o "$BIN" . || { echo "[FAIL] go build"; exit 1; }
pass "go build"

echo "== 2. 启动 headless（--port $PORT --no-sysproxy）=="
"$BIN" --headless --port "$PORT" --no-sysproxy >"$LOG" 2>&1 &
PID=$!
cleanup() { kill -INT "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT

# 等待 mixed-port 监听（首次运行含订阅拉取，最长 90s）
UP=0
for _ in $(seq 1 90); do
  if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then UP=1; break; fi
  kill -0 "$PID" 2>/dev/null || break
  sleep 1
done
if [ "$UP" = 1 ]; then pass "内核已监听 127.0.0.1:$PORT"; else
  echo "---- 启动日志 ----"; tail -30 "$LOG"; fail "内核未在 90s 内监听 $PORT"; exit 1
fi

SECRET=$(sed -n 's/.*"secret": *"\([^"]*\)".*/\1/p' "$DATA_DIR/state.json")
[ -n "$SECRET" ] && pass "读取 secret（state.json）" || fail "state.json 无 secret"

echo "== 3. 代理出网：generate_204 期望 204（启动后 url-test 选节点需要几秒，轮询等待）=="
code=000
for _ in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
    -x "http://127.0.0.1:$PORT" https://www.gstatic.com/generate_204)
  [ "$code" = "204" ] && break
  sleep 2
done
if [ "$code" = "204" ]; then pass "generate_204 => 204"; else fail "generate_204 => $code（60s 内未就绪）"; fi

echo "== 4. 代理出网：https://www.google.com 期望 HTML =="
html=$(curl -s --max-time 20 -x "http://127.0.0.1:$PORT" https://www.google.com | head -c 400)
echo "$html" | head -1
if echo "$html" | grep -qi '<!doctype html\|<html'; then pass "google 返回 HTML"; else fail "google 未返回 HTML"; fi

echo "== 5. external-controller API（带 secret）=="
api=$(curl -s --max-time 5 -H "Authorization: Bearer $SECRET" "http://127.0.0.1:$CTL/proxies")
if echo "$api" | head -c 200 | grep -q 'proxies'; then
  pass "GET /proxies 返回 JSON（前 80 字节: $(echo "$api" | head -c 80)…）"
else fail "GET /proxies 异常: $(echo "$api" | head -c 120)"; fi

echo "== 6. 无 secret 访问应 401 =="
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$CTL/version")
[ "$code" = "401" ] && pass "无 secret => 401" || fail "无 secret => $code"

echo "== 7. 预置订阅自动导入（profiles.yaml 含 飞鸟云）=="
if grep -q "飞鸟云" "$DATA_DIR/profiles.yaml" 2>/dev/null; then
  pass "profiles.yaml 含「飞鸟云」"
else fail "profiles.yaml 无「飞鸟云」"; cat "$DATA_DIR/profiles.yaml" 2>/dev/null | head -5; fi

echo "== 8. SIGINT 干净退出 =="
kill -INT "$PID"
wait "$PID"; rc=$?
if [ $rc -eq 0 ]; then pass "进程退出码 0"; else fail "进程退出码 $rc"; fi

sleep 1
if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then fail "$PORT 未释放"; else pass "$PORT 已释放"; fi
if nc -z 127.0.0.1 "$CTL" 2>/dev/null; then fail "$CTL 未释放"; else pass "$CTL 已释放"; fi
if networksetup -getwebproxy Wi-Fi 2>/dev/null | grep -q "Enabled: Yes"; then
  fail "系统代理被意外开启（本会话 --no-sysproxy）"
else pass "系统代理保持关闭（--no-sysproxy 未触碰系统设置）"; fi

echo "== 9. 关键日志摘录 =="
grep -E "kunpeng|RESTful|Mixed" "$LOG" | head -12

trap - EXIT
if [ "$FAILURES" -eq 0 ]; then echo "SMOKE OK"; exit 0; else echo "SMOKE FAILED ($FAILURES)"; exit 1; fi
