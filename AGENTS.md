# 子代理工作规则（必读）

## 网络环境（违反会卡死构建）

- 所有 Bash 命令先执行：`unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY`。
  本机 shell 残留指向 127.0.0.1:7890 的死代理变量，会导致一切网络命令失败。
- Go 命令必须带 `GOPROXY=https://goproxy.cn,direct`（GitHub/proxy.golang.org 直连超时）。
- npm 官方源直连可用，不要设镜像。
- mygo 库源码：`$(go env GOMODCACHE)/github.com/egoist/mygo@v0.7.1`（docs/ 与 examples/ 有全部文档与示例）。
- mihomo 库源码：`$(go env GOMODCACHE)/github.com/metacubex/mihomo@v1.19.33`。
- 查文档优先读本地源码，不要 WebFetch GitHub（本机直连 GitHub 超时）。

## 架构契约

- **main.go 是 IPC 契约权威**：服务方法签名与 JSON 字段不许擅自改动；需要新增字段/方法时，
  在 main.go 补充定义并同步 DESIGN.md，然后重跑 `npm run generate`。
- 各组件职责见 DESIGN.md §2/§4；实现代码放 internal/ 对应包，main.go 只保留薄壳（把调用委托给 internal/app）。
- 不要引入 Electron；不要把 mihomo 当独立进程跑（必须库内嵌）。

## 系统安全约束（用户在睡觉，机器上别留烂摊子）

- **禁止**改动用户全局环境：不杀用户进程（FlClash/clash-verge-service 不碰）、不删用户数据、
  不改 ~/.zshrc、不装系统级软件。
- 系统代理测试：只允许「写入→验证→立即还原」成对出现；结束任何工作时系统代理必须还原为
  `networksetup -getwebproxy Wi-Fi` 显示 `Enabled: No` 的初始状态。
- 测试端口 7890 若被占用不要抢，换 7891 测试并在报告中注明。
- 所有测试产物写进项目目录或 /tmp，不要散落 HOME。

## 开发命令

```sh
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
export GOPROXY=https://goproxy.cn,direct

go build ./...            # Go 编译检查
npm run generate          # 重新生成 src/mygo.ts（改了 main.go 契约后）
npm run typecheck         # TS 检查
npm run dev:web           # 仅前端 vite dev（浏览器调试用）
npm run dev               # mygo dev（完整应用 + live reload，会开窗口）
npm run build             # 打包 .app + .dmg 到 build/
```

- `npm run dev` / `mygo dev` 会打开真实窗口；子代理无人值守时**不要**依赖窗口交互测试，
  用 `--headless`（DESIGN.md §11）或纯 API 层测试。
- 提交单位：每个功能一个 commit（git 已初始化，直接提交，不要 push）。

## UI 风格（前端代理适用）

- 参考基准 = 用户提供的 FlClash 截图（深色 #121212 系、左侧 220px 侧栏、内容区卡片化、
  主色蓝 #4078dc 系、大数字、圆角 16-20px、无花哨渐变）。五页：仪表盘/代理/配置/实时连接/工具。
- 语言：简体中文。字体系统栈。图标用内联 SVG，不用 emoji 做图标（国旗 emoji 除外，节点名自带）。
- 流量图表用轻量 SVG 自绘（sparkline），不引入图表库。
- 避免不必要的次要标签和描述，让布局本身来传达信息
  （Avoid unnecessary secondary labels and descriptions. Let the layout do the explaining.）。
- 附加硬规则（用户全局约束）：不用紫蓝渐变铺底、不用霓虹发光、不做玻璃拟态、
  不堆彩色标签、不做弹跳动画。整体克制、深色、信息密度高。

## 测试工具（用户全局约束）

- API/逻辑回归：普通 node 脚本（.mjs）。
- UI 自动化：ego-browser 技能（`ego-browser skill` 获取用法）；**禁止 Playwright**。
- 直接操作电脑用 Kimi CU，不用 HID 脚本。
