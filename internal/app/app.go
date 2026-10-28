// Package app 组装 subscription/configgen/corectl/sysproxy，
// 是 main.go IPC 契约的权威实现层（DESIGN.md §2/§4）。
//
// JSON 载荷类型在此定义，main.go 用类型别名保持契约文本不变。
package app

import (
	"fmt"
	"sync"

	"kunpeng/internal/corectl"
)

// ---------------------------------------------------------------------------
// IPC 载荷类型（JSON 字段与 main.go 骨架完全一致）
// ---------------------------------------------------------------------------

// Profile 一个订阅（远程配置）。
type Profile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	URL               string `json:"url"`
	Active            bool   `json:"active"`
	UploadBytes       uint64 `json:"uploadBytes"`
	DownloadBytes     uint64 `json:"downloadBytes"`
	TotalBytes        uint64 `json:"totalBytes"`
	Expire            string `json:"expire"` // RFC3339，无期限为 ""
	UpdatedAt         string `json:"updatedAt"`
	NodeCount         int    `json:"nodeCount"`
	AutoUpdate        bool   `json:"autoUpdate"`        // 自动更新订阅
	UpdateIntervalMin int    `json:"updateIntervalMin"` // 自动更新间隔（分钟），默认 1440
}

// CoreStatus 内核与系统的整体状态。
type CoreStatus struct {
	Running     bool   `json:"running"`
	Version     string `json:"version"`
	Mode        string `json:"mode"` // "rule" | "global" | "direct"
	MixedPort   int    `json:"mixedPort"`
	SystemProxy bool   `json:"systemProxy"`
	Tun         bool   `json:"tun"`
	AutoConnect bool   `json:"autoConnect"`
	ProfileID   string `json:"profileId"` // 当前激活的订阅，空为无
	Message     string `json:"message"`   // 最近一次错误信息，空为正常
}

// Traffic 实时速率（字节/秒）。
type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// LogEntry 一条内核日志。
type LogEntry struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

// ConnectionsSnapshot mihomo /connections 的快照（结构见 mihomo 文档）。
type ConnectionsSnapshot struct {
	Data map[string]any `json:"data"`
}

// MemoryInfo 内核内存占用（字节）。
type MemoryInfo struct {
	Inuse   uint64 `json:"inuse"`
	OSLimit uint64 `json:"osLimit"`
}

// DelayResult 单节点延迟测试结果（毫秒，-1 表示失败）。
type DelayResult struct {
	Name    string `json:"name"`
	Delay   int    `json:"delay"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// App
// ---------------------------------------------------------------------------

// Options 应用构造参数。
type Options struct {
	DataDir        string // 为空用默认 ~/Library/Application Support/KunpengClash
	Headless       bool   // 无窗口模式（事件回调可为空，Status 打到 stderr）
	Port           int    // --port 会话级端口覆盖（0 = 不覆盖，不持久化）
	NoSysProxy     bool   // --no-sysproxy：本次会话禁用系统代理写入
	ControllerAddr string // external-controller 监听地址（为空用默认 127.0.0.1:9097；测试隔离用）
	PresetURL      string // 首启动自动导入的预置订阅
	GeoIPBytes     []byte // 内嵌 GEOIP.metadb（main.go go:embed）
	GeoSiteBytes   []byte // 内嵌 GEOSITE.dat

	EmitTraffic     func(Traffic)
	EmitLog         func(LogEntry)
	EmitConnections func(ConnectionsSnapshot)
	EmitMemory      func(MemoryInfo)
	EmitStatus      func(CoreStatus)
}

// App 持有全部服务共享的运行时。
type App struct {
	opt     Options
	dataDir string
	ctlAddr string // external-controller 地址（Options.ControllerAddr 生效值）

	mu        sync.Mutex // 保护 profiles/state/status/tunActive
	profiles  []profileRecord
	state     appState
	tunActive bool // 特权子进程内核接管中（TUN 模式）

	core   *corectl.Controller
	wasSys bool // 内核运行期间系统代理是否开启（看门狗恢复用）

	updater *autoUpdater // 订阅自动更新调度器

	logMu sync.Mutex
	logs  []LogEntry // 环形缓冲，最多 500 条
}

// DataDir 返回应用数据目录。
func (a *App) DataDir() string { return a.dataDir }

// Secret 返回 external-controller 密钥（smoke 测试与 UI 用）。
func (a *App) Secret() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Secret
}

// MixedPort 返回当前混合代理端口。
func (a *App) MixedPort() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.MixedPort
}

// ControllerAddr 返回 external-controller 监听地址。
func (a *App) ControllerAddr() string { return a.ctlAddr }

// errCoreStopped 内核未运行时的统一错误文案（前端据此禁用交互）。
var errCoreStopped = fmt.Errorf("内核未运行")
