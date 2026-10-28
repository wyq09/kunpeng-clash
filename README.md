# 鲲鹏 Clash（KunpengClash）

[![CI](https://github.com/wyq09/kunpeng-clash/actions/workflows/ci.yml/badge.svg)](https://github.com/wyq09/kunpeng-clash/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/wyq09/kunpeng-clash?include_prereleases)](https://github.com/wyq09/kunpeng-clash/releases)

一个把**断网自愈**放在第一位的 Clash 桌面客户端。

- 基于 [mygo](https://github.com/egoist/mygo)（Go + 系统 WebView，单二进制，无 Electron）；
- [mihomo](https://github.com/MetaCubeX/mihomo) 内核以**库形式内嵌**，单进程运行，没有独立的内核文件；
- 功能与深色界面参考 FlClash，但不是它的分支，是独立实现。

## 为什么做它

常见客户端在内核异常退出后，系统代理仍指向已经失效的端口，结果是**整机断网**。
鲲鹏 Clash 的处理方式：

1. 内核意外退出 → **立即清除系统代理**，保证还能直连上网；
2. 指数退避自动重启内核（1s / 2s / 4s / 8s / 16s，最多 5 次）；
3. 重启成功后，按你之前的选择自动恢复系统代理。

正常退出应用或手动停止内核时，同样会还原系统代理，不留后台进程。

## 功能

| 页面 | 能做什么 |
|---|---|
| 仪表盘 | 实时上下行速率、连接数、内存；一键启动内核、开关系统代理 |
| 代理 | 策略组与节点切换、延迟测速；支持「自动选择」与手动指定 |
| 配置 | 订阅添加 / 刷新 / 定时自动更新、扫码导入、混合端口与出站模式（规则 / 全局 / 直连） |
| 实时连接 | 当前所有连接的目标、命中规则、链路与流量 |
| 工具 | 连通性测试、运行日志、主题切换、数据目录 |

## 下载安装

到 [Releases](https://github.com/wyq09/kunpeng-clash/releases) 下载对应平台的安装包：

| 平台 | 文件 | 支持程度 |
|---|---|---|
| macOS（Apple Silicon / Intel） | `kunpengclash-<版本>-arm64.dmg` / `-amd64.dmg` | 完整支持 |
| Windows（x64 / ARM64） | `kunpengclash-setup-<版本>-<架构>.exe` | 实验性：内核与界面可用，系统代理与虚拟网卡暂未适配 |
| Linux（x64 / ARM64） | `.deb` 或 `.tar.gz` | 实验性：同上 |

**macOS 首次打开**：安装包未做 Apple 开发者签名，若提示「无法打开」，在 Finder 中**右键应用 → 打开**即可。

## 快速上手

1. 打开应用，进入「配置」页，粘贴你的订阅链接（或扫码导入）；
2. 回到「仪表盘」点击「启动」；
3. 打开「系统代理」开关，浏览器等应用即走代理。

默认端口：

| 端口 | 用途 |
|---|---|
| 7890 | 混合代理端口（HTTP + SOCKS5 同端口），配置页可改；默认允许局域网设备连接 |
| 9097 | 内部控制接口，仅监听 127.0.0.1 并带随机密钥 |

7890 被其他代理软件占用时，启动会明确报错，请在配置页换一个端口——本应用不会抢占或结束其他软件。

## 从源码构建

环境：Go（版本见 `go.mod`，会自动下载工具链）、Node.js 20.19+。

```sh
npm install
npm run dev        # 完整应用开发模式（打开窗口 + 热重载）
npm run dev:web    # 只调前端，浏览器里用 Mock 数据
npm run typecheck  # TS 类型检查
go test ./...      # Go 测试
npm run build      # 打包当前平台到 build/
npm run build -- -platform darwin/arm64,windows/amd64,linux/amd64   # 交叉打包
```

修改了 `main.go` 里的服务接口后，运行 `npm run generate` 重新生成前端调用代码 `src/mygo.ts`。

**自用预置订阅（可选）**：首启动且本地没有订阅时，会自动导入环境变量 `KUNPENG_PRESET_SUB`
指定的订阅；也可以在构建时写进二进制：`-ldflags "-X main.presetSubscriptionURL=<订阅链接>"`。
请不要把私人订阅链接提交进仓库。

架构与实现细节见 [DESIGN.md](DESIGN.md)。

## 发布流程

推送到 `main` 分支后，GitHub Actions 会自动：

1. 运行类型检查与 Go 测试；
2. 并行打包 macOS / Windows / Linux 共 6 个平台架构；
3. 以 `package.json` 中的 `version` 创建 Release（如 `v0.0.1`），上传全部安装包。

版本号不变时，会用最新提交覆盖该版本的安装包；要发新版本，修改 `package.json` 的 `version` 后推送即可。

## 致谢

- [mihomo](https://github.com/MetaCubeX/mihomo) —— 代理内核
- [mygo](https://github.com/egoist/mygo) —— 桌面应用框架
- [FlClash](https://github.com/chen08209/FlClash) —— 界面与功能参考
