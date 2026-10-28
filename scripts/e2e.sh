#!/bin/bash
# 鲲鹏 Clash 端到端测试（DESIGN.md §11）
#
# 流程：headless 启动应用服务层 -> 等内核就绪 -> 经 7891 代理出网(204)
#       -> 9097 API 检查 -> SIGINT 清理 -> 断言系统代理已还原。
#
# 约定（与后端 --headless 实现的契约）：
#   go run . --headless --mixed-port 7891
#     --headless      不开窗口/不进 mygo 事件循环，直接跑服务层并保持进程
#     --mixed-port N  本次会话混合端口（默认 7890；e2e 固定用 7891，避免与
#                     用户机器上已占用 7890 的代理软件冲突）
#   headless 模式下应用自行完成：预置订阅导入+激活、内核启动、系统代理管理；
#   退出时（含 SIGINT）停内核并还原系统代理。
#
# 本脚本只做「读取/断言」，自身不写系统代理；断言方式为
# 先快照初始状态，结束后比对是否一致（还原语义）。
#
# 用法：scripts/e2e.sh
# 退出码：0 通过；2 前置条件不满足（缺依赖/功能未实现，属"跳过"而非回归）；1 测试失败。
set -u
cd "$(dirname "$0")/.."

PORT=7891            # e2e 混合端口（7890 常被用户软件占用，固定避开）
API=9097             # mihomo external-controller
LOG=/tmp/kunpeng-e2e.log
APP_PID=""

pass() { echo "[PASS] $*"; }
fail() { echo "[FAIL] $*"; exit 1; }
skip() { echo "[SKIP] $*"; exit 2; }

cleanup() {
  if [ -n "$APP_PID" ] && kill -0 "$APP_PID" 2>/dev/null; then
    echo "==> 向应用发送 SIGINT"
    kill -INT "$APP_PID" 2>/dev/null
    for _ in $(seq 1 20); do kill -0 "$APP_PID" 2>/dev/null || break; sleep 0.5; done
    kill -0 "$APP_PID" 2>/dev/null && { echo "  SIGINT 未退出，强制 kill"; kill -9 "$APP_PID" 2>/dev/null; }
  fi
}
trap cleanup EXIT

# ---------- 0. 环境前置 ----------
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
export GOPROXY=https://goproxy.cn,direct

command -v go >/dev/null   || skip "缺 go"
command -v curl >/dev/null || skip "缺 curl"
networksetup -getwebproxy Wi-Fi >/dev/null 2>&1 || skip "本机无 Wi-Fi 网络服务（system proxy 断言依赖它）"

port_busy() { nc -z 127.0.0.1 "$1" 2>/dev/null; }
port_busy "$PORT" && skip "端口 $PORT 已被占用，无法安全测试"
port_busy "$API"  && skip "端口 $API 已被占用，无法安全测试"

# ---------- 1. 前置：快照系统代理初始状态（只读） ----------
proxy_state() { networksetup -getwebproxy "$1" | tr '\n' ' '; }
BEFORE_PROXY="$(proxy_state Wi-Fi)"
echo "==> 初始系统代理状态(Wi-Fi): $BEFORE_PROXY"

# ---------- 2. 检测 --headless 是否已实现 ----------
if ! grep -q -- "--headless" main.go 2>/dev/null; then
  skip "main.go 尚未实现 --headless 标志（DESIGN.md §11，待后端完成）。
      缺：入口处解析 --headless（不开窗口，直接跑服务层：导入并激活预置订阅、
      启动内核、按 state 记忆决定系统代理），供本脚本驱动。"
fi

# ---------- 3. headless 启动 ----------
echo "==> go run . --headless --mixed-port $PORT（日志: $LOG）"
go run . --headless --mixed-port "$PORT" >"$LOG" 2>&1 &
APP_PID=$!

# go run 会派生真正的应用子进程；SIGINT 发给 go run 时子进程收不到，
# 转而定位编译产物进程
find_app_pid() { pgrep -f "exe/kunpeng|kunpeng(-darwin)?$" | grep -v $$ | head -1; }

# ---------- 4. 等内核就绪（最多 60s） ----------
echo "==> 等待内核就绪（127.0.0.1:$API/version）"
READY=""
for _ in $(seq 1 120); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$API/version" || true)
  if [ "$code" = "200" ] || [ "$code" = "401" ]; then READY=1; break; fi
  kill -0 "$APP_PID" 2>/dev/null || { tail -20 "$LOG"; fail "headless 进程提前退出"; }
  sleep 0.5
done
[ -n "$READY" ] || { tail -20 "$LOG"; fail "$API 未在 60s 内就绪"; }
pass "内核 API 就绪（HTTP $code）"

# ---------- 5. 9097 API 检查 ----------
VER=$(curl -s --max-time 3 "http://127.0.0.1:$API/version")
echo "==> /version: $VER"
echo "$VER" | grep -q '"version"' || fail "/version 返回异常: $VER"
pass "external-controller API 可用"

# ---------- 6. 经 7891 代理出网 ----------
echo "==> 经 http://127.0.0.1:$PORT 请求 generate_204"
HTTP_CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
  -x "http://127.0.0.1:$PORT" https://www.gstatic.com/generate_204 || echo 000)
[ "$HTTP_CODE" = "204" ] || { tail -20 "$LOG"; fail "代理出网失败（期望 204，实际 $HTTP_CODE）"; }
pass "代理出网 204"

# ---------- 7. SIGINT 清理 + 系统代理还原断言 ----------
kill -INT "$APP_PID" 2>/dev/null
for _ in $(seq 1 20); do kill -0 "$APP_PID" 2>/dev/null || break; sleep 0.5; done
if kill -0 "$APP_PID" 2>/dev/null; then
  CHILD=$(find_app_pid); [ -n "$CHILD" ] && kill -INT "$CHILD" 2>/dev/null
  sleep 2
  kill -0 "$APP_PID" 2>/dev/null && fail "SIGINT 后进程未退出"
fi
pass "SIGINT 后进程退出"

sleep 1
AFTER_PROXY="$(proxy_state Wi-Fi)"
if [ "$BEFORE_PROXY" = "$AFTER_PROXY" ]; then
  pass "系统代理已还原: $AFTER_PROXY"
else
  echo "  before: $BEFORE_PROXY"
  echo "  after : $AFTER_PROXY"
  fail "系统代理未还原（泄漏！）"
fi

echo "==> E2E 通过"
exit 0
