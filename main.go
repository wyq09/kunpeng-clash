// KunpengClash —— 基于 mygo + mihomo 的 macOS 代理客户端。
//
// 本文件是前后端共享契约的权威定义：
//   - 每个导出服务（ProfilesService/CoreService/ProxyService/AppService）
//     的导出方法都会被 `mygo generate` 转成 src/mygo.ts 里的类型化函数；
//   - 每个导出事件由 Go 主动推送给前端；
//   - 载荷类型的 JSON 字段定义在 internal/app（本文件用类型别名引用，
//     字段名与骨架版完全一致），方法实现委托给 internal/app。
//
// 修改任何服务方法签名后必须重新运行 `npm run generate`。
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/egoist/mygo"

	"kunpeng/internal/agentapi"
	"kunpeng/internal/app"
	"kunpeng/internal/corectl"

	_ "embed"
)

// ---------------------------------------------------------------------------
// 内嵌 geodata（首启动释放到数据目录，见 DESIGN.md §4/§10）
// ---------------------------------------------------------------------------

//go:embed resources/geodata/GEOIP.metadb
var geoipMetadb []byte

// debugDevtools KUNPENG_DEVTOOLS=1 时打开 WebView DevTools（排查前端用）。
var debugDevtools *bool

//go:embed resources/geodata/GEOSITE.dat
var geositeDat []byte

// 预置订阅：首启动且无本地订阅时自动导入（DESIGN §9）。订阅含私人 token，不入库；
// 由环境变量 KUNPENG_PRESET_SUB 或 -ldflags "-X main.presetSubscriptionURL=..." 注入。
var presetSubscriptionURL = ""

func presetURL() string {
	if v := os.Getenv("KUNPENG_PRESET_SUB"); v != "" {
		return v
	}
	return presetSubscriptionURL
}

// ---------------------------------------------------------------------------
// 数据类型（JSON 契约别名；字段定义见 internal/app/app.go，完全一致）
// ---------------------------------------------------------------------------

type (
	// Profile 一个订阅（远程配置）。
	Profile = app.Profile
	// RouteRule 一条分流规则：一组域名强制走一组指定节点（挂在具体订阅下）。
	RouteRule = app.RouteRule
	// CoreStatus 内核与系统的整体状态。
	CoreStatus = app.CoreStatus
	// Traffic 实时速率（字节/秒）。
	Traffic = app.Traffic
	// LogEntry 一条内核日志。
	LogEntry = app.LogEntry
	// ConnectionsSnapshot mihomo /connections 的快照（结构见 mihomo 文档）。
	ConnectionsSnapshot = app.ConnectionsSnapshot
	// MemoryInfo 内核内存占用（字节）。
	MemoryInfo = app.MemoryInfo
	// DelayResult 单节点延迟测试结果（毫秒，-1 表示失败）。
	DelayResult = app.DelayResult
	// AgentInfo 智能体接入页信息（地址、令牌、可直接复制的接入指令与 MCP 配置、能力清单、调用记录）。
	AgentInfo = app.AgentInfo
	// AgentTool 一项对智能体开放的能力。
	AgentTool = app.AgentTool
	// AgentCall 一次智能体调用记录。
	AgentCall = app.AgentCall
)

// ---------------------------------------------------------------------------
// 事件（Go → 前端推送）
// ---------------------------------------------------------------------------

var (
	// TrafficEvent 每秒推送一次实时速率。
	TrafficEvent = mygo.NewEvent[Traffic]("traffic")
	// LogEvent 推送内核日志。
	LogEvent = mygo.NewEvent[LogEntry]("log")
	// ConnectionsEvent 每秒推送一次连接快照。
	ConnectionsEvent = mygo.NewEvent[ConnectionsSnapshot]("connections")
	// MemoryEvent 推送内核内存占用。
	MemoryEvent = mygo.NewEvent[MemoryInfo]("memory")
	// StatusEvent 内核状态变化时推送。
	StatusEvent = mygo.NewEvent[CoreStatus]("status")
	// AgentEvent 智能体每调用一次工具推送一次（UI 实时提示并刷新数据）。
	AgentEvent = mygo.NewEvent[AgentCall]("agent")
)

// ---------------------------------------------------------------------------
// 服务（前端 → Go 调用；全部委托 internal/app）
// ---------------------------------------------------------------------------

// ProfilesService 订阅（配置）管理。
type ProfilesService struct{ app *App }

// List 返回全部订阅。
func (p ProfilesService) List() ([]Profile, error) { return p.app.Impl.ProfilesList(), nil }

// Add 拉取 url 并新增订阅（name 为空时取订阅文件名）。
func (p ProfilesService) Add(url, name string) (Profile, error) {
	return p.app.Impl.ProfileAdd(url, name)
}

// Update 重新拉取订阅内容与流量信息。
func (p ProfilesService) Update(id string) (Profile, error) { return p.app.Impl.ProfileUpdate(id) }

// Remove 删除订阅。
func (p ProfilesService) Remove(id string) error { return p.app.Impl.ProfileRemove(id) }

// Activate 激活订阅：拉取内容、生成内核配置并热应用。
func (p ProfilesService) Activate(id string) error { return p.app.Impl.ProfileActivate(id) }

// Current 返回当前激活订阅。
func (p ProfilesService) Current() (Profile, error) { return p.app.Impl.ProfileCurrent() }

// Edit 修改订阅的名称、URL 与自动更新设置（url 变更后立即重新拉取）。
func (p ProfilesService) Edit(id, name, url string, autoUpdate bool, updateIntervalMin int) (Profile, error) {
	return p.app.Impl.ProfileEdit(id, name, url, autoUpdate, updateIntervalMin)
}

// NodesService 自定义节点管理（分享链接导入，独立于订阅，重启保留）。
type NodesService struct{ app *App }

// AddFromLinks 解析多行节点分享链接（vless:// vmess:// ss:// hysteria2:// trojan://），
// 追加为自定义节点（与订阅节点合并进策略组），返回成功导入的条数。
func (n NodesService) AddFromLinks(raw string) (int, error) { return n.app.Impl.NodesAddFromLinks(raw) }

// List 返回全部自定义节点名称。
func (n NodesService) List() ([]string, error) { return n.app.Impl.NodesList(), nil }

// Remove 删除指定名称的自定义节点。
func (n NodesService) Remove(name string) error { return n.app.Impl.NodesRemove(name) }

// RulesService 分流规则（域名 → 指定节点组）。规则挂在具体订阅（配置）下：
// 激活哪个订阅，哪份规则生效；这些域名只能在该组节点里跑（组内无 DIRECT、不回落）。
// 仅规则（rule）模式生效，与直连白名单语义一致。
type RulesService struct{ app *App }

// List 返回某订阅挂载的全部分流规则。
func (s RulesService) List(profileID string) ([]RouteRule, error) {
	return s.app.Impl.RouteRulesList(profileID)
}

// Save 全量保存某订阅的分流规则并热应用（仅当该订阅为激活订阅时）。
// 每组：name 组名（不含逗号）、domains 域名/IP 段列表、nodes 允许的节点、auto 组内自动测速。
func (s RulesService) Save(profileID string, rules []RouteRule) error {
	return s.app.Impl.RouteRulesSave(profileID, rules)
}

// NodeNames 返回分流规则编辑器可选的节点名（该订阅真实节点 + 自定义节点）。
func (s RulesService) NodeNames(profileID string) ([]string, error) {
	return s.app.Impl.RouteRuleNodes(profileID)
}

// CoreService 内核生命周期与系统设置。
type CoreService struct{ app *App }

// Status 返回当前内核与系统状态。
func (c CoreService) Status() (CoreStatus, error) { return c.app.Impl.CoreStatusNow(), nil }

// Start 启动内核（使用当前激活订阅的配置）。
func (c CoreService) Start() error { return c.app.Impl.CoreStart() }

// Stop 停止内核并清理系统代理。
func (c CoreService) Stop() error { return c.app.Impl.CoreStop() }

// Restart 重启内核。
func (c CoreService) Restart() error { return c.app.Impl.CoreRestart() }

// SetMode 切换出站模式：rule/global/direct。
func (c CoreService) SetMode(mode string) error { return c.app.Impl.CoreSetMode(mode) }

// SetSystemProxy 开关系统代理（networksetup 写入/清除）。
func (c CoreService) SetSystemProxy(on bool) error { return c.app.Impl.CoreSetSystemProxy(on) }

// SetPort 设置混合代理端口（默认 7890）。
func (c CoreService) SetPort(port int) error { return c.app.Impl.CoreSetPort(port) }

// SetAutoConnect 设置启动应用时自动连接。
func (c CoreService) SetAutoConnect(on bool) error { return c.app.Impl.CoreSetAutoConnect(on) }

// SetTun 开关虚拟网卡（TUN）。开启需要管理员授权：内核将切换为特权子进程运行；
// 关闭时恢复为进程内内核。状态变化通过 StatusEvent 推送。
func (c CoreService) SetTun(on bool) error { return c.app.Impl.CoreSetTun(on) }

// GetWhitelist 返回直连白名单（域名 / 通配符域名 / IP 段）。
func (c CoreService) GetWhitelist() ([]string, error) { return c.app.Impl.WhitelistGet(), nil }

// SetWhitelist 全量设置直连白名单并热应用。
// 每项支持：example.com（含全部子域）、*.example.com（等价上一项）、a.b.com（精确）、10.0.0.0/8（IP 段）。
func (c CoreService) SetWhitelist(domains []string) error { return c.app.Impl.WhitelistSet(domains) }

// ProxyService mihomo REST API 的类型化桥接。
type ProxyService struct{ app *App }

// All 返回 GET /proxies 全量数据。
func (p ProxyService) All() (map[string]any, error) { return p.app.Impl.ProxiesAll() }

// Select 在组 group 中选择节点 name。
func (p ProxyService) Select(group, name string) error { return p.app.Impl.ProxySelect(group, name) }

// GroupDelay 对组 group 做批量延迟测试。
func (p ProxyService) GroupDelay(group, url string, timeoutMs int) (map[string]any, error) {
	return p.app.Impl.ProxyGroupDelay(group, url, timeoutMs)
}

// ProxyDelay 对单个节点做延迟测试。
func (p ProxyService) ProxyDelay(name, url string, timeoutMs int) (DelayResult, error) {
	return p.app.Impl.ProxyDelay(name, url, timeoutMs)
}

// Rules 返回 GET /rules。
func (p ProxyService) Rules() (map[string]any, error) { return p.app.Impl.Rules() }

// DeleteConnection 关闭单个连接（id 来自连接快照）。
func (p ProxyService) DeleteConnection(id string) error { return p.app.Impl.ConnectionDelete(id) }

// CloseAllConnections 关闭全部连接。
func (p ProxyService) CloseAllConnections() error { return p.app.Impl.ConnectionsCloseAll() }

// AppService 工具页杂项。
type AppService struct{ app *App }

// TestURL 通过当前代理端口请求 url，返回 HTTP 状态码（0 表示请求失败）。
func (a AppService) TestURL(url string) (int, error) { return a.app.Impl.TestURL(url) }

// PublicIP 通过当前代理查询公网出口 IP。
func (a AppService) PublicIP() (string, error) { return a.app.Impl.PublicIP() }

// OpenDataDir 在 Finder 中打开应用数据目录。
func (a AppService) OpenDataDir() error { return a.app.Impl.OpenDataDir() }

// GetLogs 返回最近的内核日志（启动时回填用）。
func (a AppService) GetLogs() ([]LogEntry, error) { return a.app.Impl.Logs(), nil }

// CopyText 写入系统剪贴板（WKWebView 内 navigator.clipboard 不总可用）。
func (a AppService) CopyText(text string) error { return a.app.Impl.CopyText(text) }

// AgentService 智能体接入（本机 HTTP / MCP / 命令行网关，DESIGN §5b）。
type AgentService struct{ app *App }

// Info 返回接入地址、令牌、接入指令、MCP 配置、能力清单与最近调用记录。
func (s AgentService) Info() (AgentInfo, error) { return s.app.Impl.AgentInfoNow(), nil }

// SetEnabled 开关智能体接入（关闭后网关停止监听，持久化）。
func (s AgentService) SetEnabled(on bool) error { return s.app.Impl.AgentSetEnabled(on) }

// ResetToken 重新生成令牌（旧令牌立即失效），返回最新信息。
func (s AgentService) ResetToken() (AgentInfo, error) { return s.app.Impl.AgentResetToken() }

// SetPort 自定义网关端口（1024–65535，持久化）；新端口起不来时保持原端口并报错。
func (s AgentService) SetPort(port int) (AgentInfo, error) { return s.app.Impl.AgentSetPort(port) }

// Guide 返回完整智能体教程（Markdown）。
func (s AgentService) Guide() (string, error) { return s.app.Impl.AgentGuideText(), nil }

// ExportSkill 导出 SKILL.md 到数据目录并在 Finder 中定位，返回文件路径。
func (s AgentService) ExportSkill() (string, error) { return s.app.Impl.AgentExportSkill() }

// ClearActivity 清空调用记录。
func (s AgentService) ClearActivity() error {
	s.app.Impl.AgentClearActivity()
	return nil
}

// ---------------------------------------------------------------------------
// 应用入口
// ---------------------------------------------------------------------------

// App 持有全部服务共享的运行时（内核、订阅存储、系统代理）。
// 实现在 internal/app。
type App struct{ Impl *app.App }

// appVersion 由构建注入（-ldflags "-X main.appVersion=..."），缺省读 package.json 同步的值。
var appVersion = "0.2.0"

// runAgentClient 子命令：ctl（命令行）/ mcp（stdio MCP 桥）。与运行中的应用经本机网关通信。
func runAgentClient(cmd string, args []string) int {
	dataDir := app.DefaultDataDir()
	if v := os.Getenv("KUNPENG_DATA_DIR"); v != "" {
		dataDir = v
	}
	for len(args) >= 2 && (args[0] == "--data-dir" || args[0] == "-data-dir") {
		dataDir, args = args[1], args[2:]
	}
	if cmd == "mcp" {
		if err := agentapi.RunStdioMCP(dataDir, os.Stdin, os.Stdout); err != nil {
			log.Printf("[kunpeng][mcp] %v", err)
			return 1
		}
		return 0
	}
	return agentapi.RunCLI(dataDir, args, os.Stdout, os.Stderr)
}

func main() {
	app.AppVersion = appVersion
	if len(os.Args) > 1 && (os.Args[1] == "ctl" || os.Args[1] == "mcp") {
		os.Exit(runAgentClient(os.Args[1], os.Args[2:]))
	}
	headless := flag.Bool("headless", false, "无窗口模式：直接启动服务层（导入/激活预置订阅并启动内核），供测试")
	port := flag.Int("port", 0, "本次运行的混合代理端口（会话级覆盖，不持久化）")
	noSysProxy := flag.Bool("no-sysproxy", false, "本次运行禁用系统代理写入（测试用）")
	// 冒烟测试隔离用：GUI 默认路径完全不受影响（均为空时走默认数据目录与 9097）。
	dataDir := flag.String("data-dir", "", "覆盖应用数据目录（测试隔离用）")
	controller := flag.String("controller", "", "覆盖 external-controller 监听地址（测试隔离用）")
	// config-only 独立内核模式（TUN 特权子进程专用，DESIGN §7b）：
	// 跳过一切 state/订阅/系统代理逻辑，按 --config 启动内核，
	// 500ms 轮询 --quit-file 存在即优雅退出；强制 no-sysproxy 语义。
	configOnly := flag.Bool("config-only", false, "独立内核模式：仅按 --config 启动内核并轮询 --quit-file（特权子进程专用）")
	configPath := flag.String("config", "", "config-only 模式的内核配置文件路径")
	secretFlag := flag.String("secret", "", "config-only 模式的 external-controller 密钥")
	quitFile := flag.String("quit-file", "", "config-only 模式的退出标记文件（存在即退出）")
	debugDevtools = flag.Bool("devtools", os.Getenv("KUNPENG_DEVTOOLS") == "1", "打开 WebView DevTools（KUNPENG_DEVTOOLS=1）")
	flag.Parse()

	if *configOnly {
		if *configPath == "" || *dataDir == "" {
			log.Fatalf("[kunpeng][standalone] --config-only 需要 --config 与 --data-dir")
		}
		if err := corectl.RunStandalone(corectl.StandaloneOptions{
			ConfigPath:     *configPath,
			DataDir:        *dataDir,
			ControllerAddr: *controller,
			Secret:         *secretFlag,
			QuitFile:       *quitFile,
		}); err != nil {
			log.Fatalf("[kunpeng][standalone] %v", err)
		}
		return
	}

	impl, err := app.New(app.Options{
		Headless:        *headless,
		Port:            *port,
		NoSysProxy:      *noSysProxy,
		ControllerAddr:  *controller,
		DataDir:         *dataDir,
		PresetURL:       presetURL(),
		GeoIPBytes:      geoipMetadb,
		GeoSiteBytes:    geositeDat,
		EmitTraffic:     func(t Traffic) { _ = TrafficEvent.Broadcast(t) },
		EmitLog:         func(e LogEntry) { _ = LogEvent.Broadcast(e) },
		EmitConnections: func(s ConnectionsSnapshot) { _ = ConnectionsEvent.Broadcast(s) },
		EmitMemory:      func(m MemoryInfo) { _ = MemoryEvent.Broadcast(m) },
		EmitStatus:      func(s CoreStatus) { _ = StatusEvent.Broadcast(s) },
		EmitAgent:       func(c AgentCall) { _ = AgentEvent.Broadcast(c) },
	})
	if err != nil {
		log.Fatalf("[kunpeng] 应用初始化失败: %v", err)
	}
	a := &App{Impl: impl}

	if *headless {
		runHeadless(impl)
		return
	}

	application := mygo.App
	application.SetName("KunpengClash")

	mygo.Bind(ProfilesService{app: a})
	mygo.Bind(CoreService{app: a})
	mygo.Bind(ProxyService{app: a})
	mygo.Bind(NodesService{app: a})
	mygo.Bind(RulesService{app: a})
	mygo.Bind(AppService{app: a})
	mygo.Bind(AgentService{app: a})

	application.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:  "鲲鹏 Clash",
			URL:    "/", // 前端首页（dev 模式映射 devUrl，打包版为内嵌产物）
			Width:  1280,
			Height: 832,
		})
		if debugDevtools != nil && *debugDevtools {
			win.Page().OpenDevTools()
		}
		// autoConnect（默认 true）：进入界面后自动启动内核 + 系统代理。
		go impl.AutoConnect()
	})
	application.OnWindowAllClosed(func() {})
	application.OnWillQuit(func(e *mygo.QuitEvent) {
		impl.Shutdown() // 退出前停内核 + 清系统代理
	})
	if err := application.Run(); err != nil {
		panic(err)
	}
}

// runHeadless 无窗口模式：启动内核（按需开系统代理），阻塞等待 SIGINT 后清理退出。
func runHeadless(impl *app.App) {
	log.Printf("[kunpeng][headless] 数据目录: %s", impl.DataDir())
	log.Printf("[kunpeng][headless] 混合代理端口: %d", impl.MixedPort())
	log.Printf("[kunpeng][headless] external-controller: %s (secret=%s)", impl.ControllerAddr(), impl.Secret())
	if info := impl.AgentInfoNow(); info.Running {
		log.Printf("[kunpeng][headless] 智能体网关: %s（教程 %s/v1/guide）", info.BaseURL, info.BaseURL)
	}

	if err := impl.CoreStart(); err != nil {
		log.Fatalf("[kunpeng][headless] 内核启动失败: %v", err)
	}
	log.Printf("[kunpeng][headless] 内核已启动，可用 -x http://127.0.0.1:%d 测试", impl.MixedPort())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("[kunpeng][headless] 收到退出信号，停止内核并清理…")
	impl.Shutdown()
	log.Printf("[kunpeng][headless] 已退出")
}
