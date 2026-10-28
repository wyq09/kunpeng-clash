package corectl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/hub/route"
)

// startSession 开启一个事件会话：事件流采集 + 看门狗 + 健康检查。
//
// 两条独立生命周期：
//   - streamsCtx：/traffic /logs /memory /connections 采集与健康检查，
//     崩溃处理时先杀掉（重启后由新会话重建）；
//   - lifecycleCtx：看门狗本体。崩溃 → 退避重启期间必须保持存活，
//     否则 restartLoop 会因 ctx 取消而直接放弃（重启永不发生）。
//     只有 Stop()/Shutdown() 才终止它。
func (c *Controller) startSession() {
	c.stopSessionLocked()
	c.stopLifecycleLocked()
	sctx, scancel := context.WithCancel(context.Background())
	lctx, lcancel := context.WithCancel(context.Background())
	c.streamsCancel = scancel
	c.lifecycleCancel = lcancel
	go c.consumeTraffic(sctx)
	go c.consumeLogs(sctx)
	go c.consumeMemory(sctx)
	go c.pollConnections(sctx)
	go c.watchdog(lctx)
	go c.healthCheck(sctx)
}

// stopStreamSessionLocked 结束事件流与健康检查（崩溃处理路径用；
// 看门狗自身必须存活以执行退避重启）。
func (c *Controller) stopStreamSessionLocked() {
	if c.streamsCancel != nil {
		c.streamsCancel()
		c.streamsCancel = nil
	}
}

// stopLifecycleLocked 结束看门狗（仅 Stop/Shutdown 路径）。
func (c *Controller) stopLifecycleLocked() {
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
		c.lifecycleCancel = nil
	}
}

// stopSessionLocked 结束当前会话（Stop 路径：流 + 看门狗全部终止）。
func (c *Controller) stopSessionLocked() {
	c.stopStreamSessionLocked()
	c.stopLifecycleLocked()
}

// streamURL 构造带鉴权的 external-controller URL。
func (c *Controller) streamURL(path string) string {
	return "http://" + c.opt.ControllerAddr + path
}

// openStream 发起长连接 GET（不读超时），失败返回错误。
func (c *Controller) openStream(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.streamURL(path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.opt.Secret)
	return c.client.Do(req)
}

// reconnectDelay 事件流断线重连间隔。
const reconnectDelay = 2 * time.Second

// streamLoop 通用的“连接-读行-断线重连”骨架。
// handle 每收到一行 JSON 调一次；返回 false 时结束循环（ctx 取消时自动结束）。
func (c *Controller) streamLoop(ctx context.Context, path string, handle func(line []byte)) {
	for ctx.Err() == nil {
		resp, err := c.openStream(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(reconnectDelay)
			continue
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() && ctx.Err() == nil {
			handle(scanner.Bytes())
		}
		_ = resp.Body.Close()
		if ctx.Err() == nil {
			// 内核重启期间流会断开；等 API 恢复后重连。
			c.waitAPI(ctx, 30*time.Second)
		}
	}
}

// waitAPI 等待 /version 可用（内核重启期间轮询），超时返回。
func (c *Controller) waitAPI(ctx context.Context, max time.Duration) {
	deadline := time.Now().Add(max)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.streamURL("/version"), nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+c.opt.Secret)
			if resp, err := c.client.Do(req); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

type trafficMsg struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// consumeTraffic 消费 /traffic JSON-lines 流，每秒回调一次。
func (c *Controller) consumeTraffic(ctx context.Context) {
	c.streamLoop(ctx, "/traffic", func(line []byte) {
		var t trafficMsg
		if json.Unmarshal(line, &t) != nil {
			return
		}
		if c.opt.OnTraffic != nil {
			c.opt.OnTraffic(t.Up, t.Down)
		}
	})
}

type logMsg struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

// consumeLogs 消费 /logs?level=info 流。
func (c *Controller) consumeLogs(ctx context.Context) {
	c.streamLoop(ctx, "/logs?level=info", func(line []byte) {
		var l logMsg
		if json.Unmarshal(line, &l) != nil {
			return
		}
		if c.opt.OnLog != nil {
			c.opt.OnLog(l.Type, l.Payload)
		}
	})
}

type memoryMsg struct {
	Inuse   uint64 `json:"inuse"`
	OSLimit uint64 `json:"oslimit"`
}

// consumeMemory 消费 /memory 流；服务端每秒推，客户端节流到每 5s。
func (c *Controller) consumeMemory(ctx context.Context) {
	count := 0
	c.streamLoop(ctx, "/memory", func(line []byte) {
		var m memoryMsg
		if json.Unmarshal(line, &m) != nil {
			return
		}
		count++
		if count%5 != 1 { // 第 1 次立即推，其后每 5 秒一次
			return
		}
		if c.opt.OnMemory != nil {
			c.opt.OnMemory(m.Inuse, m.OSLimit)
		}
	})
}

// pollConnections 每 1s 轮询 /connections 快照（该端点非 WS 时返回单份快照）。
func (c *Controller) pollConnections(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		<-tick.C
		resp, err := c.openStream(ctx, "/connections")
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue // 下一秒重试即自动重连
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		var snapshot map[string]any
		if json.Unmarshal(body, &snapshot) != nil {
			continue
		}
		if c.opt.OnConnections != nil {
			c.opt.OnConnections(snapshot)
		}
	}
}

// versionOK 探测 external-controller 是否存活。
func (c *Controller) versionOK() error {
	resp, err := c.openStream(context.Background(), "/version")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API 返回 HTTP %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return nil
}

// Ping 探测 external-controller 是否存活（TUN 切换轮询用）。
func (c *Controller) Ping() error { return c.versionOK() }

// Handoff 把内核移交给同地址的外部内核（TUN 特权子进程模式切换用）：
// 终止看门狗（避免把移交期间的 REST 短暂失联误判为崩溃而重启内嵌内核），
// 关闭入站监听与 external-controller，**保留事件流会话**——特权内核以同一
// 地址与密钥接管 REST 后，事件流循环自动重连续采（traffic/log/memory/connections
// 与健康检查全部无缝切换到特权内核）。
func (c *Controller) Handoff() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.stopLifecycleLocked()
	c.closeInboundLocked()
	executor.Shutdown()
	route.ReCreateServer(&route.Config{}) // 空地址 = 关闭现有 API 服务
	c.running = false
	c.message = ""
}

// ReloadFromFile 让内核从磁盘绝对路径重载配置，对应 mihomo REST
// PUT /configs?force=true body {"path": ..., "payload": ""}。
// 注意 mihomo IsSafePath 校验：path 必须是内核 HomeDir 的子路径——TUN 模式下
// 特权内核 HomeDir 为 <dataDir>/tun，因此传 <dataDir>/tun/runtime.yaml。
func (c *Controller) ReloadFromFile(path string) error {
	body, _ := json.Marshal(map[string]string{"path": path, "payload": ""})
	return c.apiJSON("PUT", "/configs?force=true", body, nil)
}
