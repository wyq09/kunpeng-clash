#!/bin/sh
# 鲲鹏 Clash 发布构建：产出 build/darwin-arm64/KunpengClash.app 与 DMG。
#
# 前置：node_modules 已安装（npm install）。
# 用法：scripts/build.sh            # .app + .dmg
#       scripts/build.sh -skip-dmg  # 仅 .app（跳过磁盘映像）
#       scripts/build.sh -debug     # 保留 web inspector 等开发特性
set -e
cd "$(dirname "$0")/.."

# 本机 shell 常残留指向 127.0.0.1:7890 的死代理变量，不清掉会卡死一切网络命令
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
# Go 依赖统一走国内代理（proxy.golang.org 直连超时）
export GOPROXY=https://goproxy.cn,direct

echo "==> [1/3] mygo generate（重生成 src/mygo.ts，保证与 main.go 契约一致）"
npx mygo-cli generate .

echo "==> [2/3] 前端构建 + Go 编译 + .app/.dmg 打包"
npx mygo-cli build .

echo "==> 完成，产物见 build/"
ls -R build | sed -n '1,20p'
