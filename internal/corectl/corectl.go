// Package corectl 以库形式驱动嵌入的 mihomo 内核：配置应用、
// external-controller、事件流采集、看门狗与健康检查（DESIGN.md §7）。
//
// 嵌入方式（以 mihomo v1.19.33 源码为准，与 DESIGN 初稿有两处差异）：
//   - 启动 = constant.SetHomeDir + executor.ParseWithBytes + hub.ApplyConfig
//     （hub.ApplyConfig = route.ReCreateServer + executor.ApplyConfig，
//     比 DESIGN §7 中手工 ReCreateServer 更贴近 mihomo 官方入口）；
//   - 停止 = executor.Shutdown + route.ReCreateServer(&route.Config{})（空地址关闭 API）；
//   - 事件采集用同进程 HTTP 客户端消费 external-controller 的
//     /traffic /logs /memory（服务端对非 WS 请求就是 JSON-lines 流，
//     无需 WebSocket 客户端）与 /connections（轮询 1s），断线自动重连。
package corectl

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/hub/route"
	"go.yaml.in/yaml/v3"
)

// MihomoVersion 嵌入的 mihomo 模块版本（与 go.mod 一致）。
// 注意：库内 constant.Version 是 ldflags 注入值（默认 1.10.0），不可信。
const MihomoVersion = "1.19.33"

// Options 控制器配置。
type Options struct {
	DataDir        string // mihomo HomeDir（geodata/cache 所在）
	ControllerAddr string // external-controller 监听地址
	Secret         string // external-controller 密钥
	// 事件回调（内核未运行时不触发）。
	OnLog         func(typ, payload string)
	OnTraffic     func(up, down int64)
	OnMemory      func(inuse, osLimit uint64)
	OnConnections func(snapshot map[string]any)
	// OnCrash 看门狗判定内核异常（实现方应先清系统代理再推 StatusEvent）。
	OnCrash func(message string)
	// OnRecovered 看门狗自动重启成功（实现方按用户之前的选择恢复系统代理）。
	OnRecovered func()
	// OnMessage 非致命提示（如健康检查自动切换出口）。
	OnMessage func(message string)
}

// Controller mihomo 嵌入驱动的生命周期管理。
type Controller struct {
	opt    Options
	client *http.Client // REST（带超时）
	mu     sync.Mutex

	running bool
	stopped bool   // 用户主动 Stop，看门狗不得重启
	message string // 最近一次错误/提示

	lastCfg []byte // 最近一次成功应用的配置（看门狗重启用）
	// streamsCancel / lifecycleCancel：事件流与看门狗两条独立生命周期
	// （见 streams.go startSession 的注释：崩溃退避重启期间看门狗必须存活）。
	streamsCancel   context.CancelFunc
	lifecycleCancel context.CancelFunc

	restartAttempts int // 连续重启计数（成功后清零）
	backoff         []time.Duration
}

// New 创建控制器并设置 mihomo HomeDir（幂等）。
func New(opt Options) (*Controller, error) {
	if opt.DataDir == "" {
		return nil, fmt.Errorf("corectl: DataDir 不能为空")
	}
	if opt.ControllerAddr == "" {
		opt.ControllerAddr = "127.0.0.1:9097"
	}
	constant.SetHomeDir(opt.DataDir)
	return &Controller{
		opt:     opt,
		backoff: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second},
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy: nil, // 同进程直连 127.0.0.1，绝不吃代理环境变量
			},
		},
	}, nil
}

// Running 报告内核当前是否处于运行状态（按我们的状态机，非探测）。
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Message 返回最近一次错误/提示信息。
func (c *Controller) Message() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.message
}

// setMessage 更新提示信息并通知。
func (c *Controller) setMessage(msg string) {
	c.mu.Lock()
	c.message = msg
	c.mu.Unlock()
	if msg != "" && c.opt.OnMessage != nil {
		c.opt.OnMessage(msg)
	}
}

// probePort 探测 TCP 端口是否可监听（DESIGN §12：7890 被占则报错提示换端口）。
func probePort(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_ = l.Close()
	return nil
}

// Start 全新启动内核（用户行为）：先探测端口再应用配置。
func (c *Controller) Start(cfgBytes []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = false
	return c.applyLocked(cfgBytes, true)
}

// Apply 热重载配置（内核已运行时替换生效；未运行时等价 Start 但不探测端口）。
func (c *Controller) Apply(cfgBytes []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = false
	return c.applyLocked(cfgBytes, false)
}

// applyLocked 执行应用（调用方持锁）。freshStart 时先做端口探测。
func (c *Controller) applyLocked(cfgBytes []byte, freshStart bool) error {
	if freshStart {
		var head struct {
			MixedPort          int    `yaml:"mixed-port"`
			ExternalController string `yaml:"external-controller"`
		}
		if err := yaml.Unmarshal(cfgBytes, &head); err != nil {
			return fmt.Errorf("解析运行配置失败: %w", err)
		}
		if head.MixedPort > 0 {
			if err := probePort(fmt.Sprintf(":%d", head.MixedPort)); err != nil {
				return fmt.Errorf("混合代理端口 %d 已被占用，请在「设置」更换端口后再试", head.MixedPort)
			}
		}
		ctl := head.ExternalController
		if ctl == "" {
			ctl = c.opt.ControllerAddr
		}
		if err := probePort(ctl); err != nil {
			return fmt.Errorf("external-controller 端口 (%s) 已被占用，可能有其他 Clash 客户端在运行", ctl)
		}
	}

	cfg, err := executor.ParseWithBytes(cfgBytes)
	if err != nil {
		return fmt.Errorf("内核配置解析失败: %w", err)
	}
	hub.ApplyConfig(cfg) // = route.ReCreateServer(cfg.Controller) + executor.ApplyConfig(cfg, true)

	c.lastCfg = cfgBytes
	if !c.running {
		c.running = true
		c.restartAttempts = 0
		c.message = ""
		c.startSession()
	}
	return nil
}

// Stop 优雅停止内核（用户行为）：停事件流与看门狗、关监听、关 external-controller。
func (c *Controller) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if !c.running {
		return nil
	}
	c.stopSessionLocked()
	c.closeInboundLocked()
	executor.Shutdown()                   // 清理 TUN 等组件
	route.ReCreateServer(&route.Config{}) // 空地址 = 关闭现有 API 服务
	c.running = false
	c.message = ""
	return nil
}

// closeInboundLocked 关闭全部入站监听。
//
// 库源码事实：executor.Shutdown() → listener.Cleanup() 只关 TUN；
// mixed/http/socks 等入站监听只有在再次 ApplyConfig（force=true）且端口为 0
// 时才会被 ReCreateMixed 等关闭。因此这里以“端口全 0”的配置热应用一次。
func (c *Controller) closeInboundLocked() {
	if len(c.lastCfg) == 0 {
		return
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(c.lastCfg, &cfg); err != nil {
		return
	}
	for _, k := range []string{"port", "mixed-port", "socks-port", "redir-port", "tproxy-port"} {
		cfg[k] = 0
	}
	bytes, err := yaml.Marshal(cfg)
	if err != nil {
		return
	}
	stop, err := executor.ParseWithBytes(bytes)
	if err != nil {
		return
	}
	executor.ApplyConfig(stop, true)
}

// restartAttempt 看门狗的一次重启尝试（成功返回 true）。
func (c *Controller) restartAttempt() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return false
	}
	if err := c.applyLocked(c.lastCfg, false); err != nil {
		c.message = "内核自动重启失败: " + err.Error()
		return false
	}
	c.message = ""
	return true
}

// Shutdown 退出前清理（幂等，等价于 Stop 但不报错）。
func (c *Controller) Shutdown() {
	_ = c.Stop()
}
