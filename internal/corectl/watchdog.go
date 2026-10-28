package corectl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/hub/route"

	"kunpeng/internal/configgen"
)

// 组名与健康检查 URL 来自 configgen（单一事实来源）。
const (
	GroupSelect = configgen.GroupSelect
	GroupAuto   = configgen.GroupAuto
	HealthURL   = configgen.HealthURL
)

func bodyReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return bytes.NewReader(b)
}

// 看门狗参数（DESIGN.md §7）。var 而非 const 仅为测试可注入短周期。
var (
	watchdogInterval   = 5 * time.Second  // /version 轮询周期
	watchdogFailLimit  = 2                // 连续失败次数判定崩溃
	healthInterval     = 60 * time.Second // 出口健康检查周期
	healthFailLimit    = 3                // 连续失败次数触发自动切换
	healthProbeTimeout = 5000             // 延迟测试超时 ms
)

// BackoffDelays 返回看门狗重启退避序列（供单元测试断言）。
func (c *Controller) BackoffDelays() []time.Duration { return c.backoff }

// NextBackoff 返回第 n 次重试前的等待时长；超过序列长度返回 0 表示放弃。
func NextBackoff(delays []time.Duration, attempt int) time.Duration {
	if attempt < 0 || attempt >= len(delays) {
		return 0
	}
	return delays[attempt]
}

// watchdog 监测内核健康：/version 每 5s 探测，连续失败判崩溃 →
// 清系统代理（OnCrash 回调）→ 指数退避重启（最多 5 次）→ 成功后 OnRecovered。
func (c *Controller) watchdog(ctx context.Context) {
	failCount := 0
	tick := time.NewTicker(watchdogInterval)
	defer tick.Stop()
	for ctx.Err() == nil {
		<-tick.C
		probeErr := c.versionOK()
		if probeErr == nil {
			failCount = 0
			continue
		}
		failCount++
		if failCount < watchdogFailLimit {
			continue
		}
		// 判定崩溃：只停事件流与健康检查，看门狗自身必须存活执行退避重启。
		c.mu.Lock()
		if c.stopped || !c.running {
			c.mu.Unlock()
			return
		}
		c.stopStreamSessionLocked()
		executor.Shutdown()
		route.ReCreateServer(&route.Config{}) // 空地址 = 关闭现有 API 服务
		c.running = false
		c.restartAttempts = 0
		c.mu.Unlock()

		msg := fmt.Sprintf("内核失去响应（%s），正在自动恢复…", probeErr)
		c.mu.Lock()
		c.message = msg
		c.mu.Unlock()
		log.Printf("[kunpeng][watchdog] %s", msg)
		if c.opt.OnCrash != nil {
			c.opt.OnCrash(msg)
		}
		c.restartLoop(ctx)
		return
	}
}

// restartLoop 按指数退避重启，成功则恢复事件会话并回调 OnRecovered。
func (c *Controller) restartLoop(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		delay := NextBackoff(c.backoff, attempt)
		if delay == 0 {
			c.mu.Lock()
			c.running = false
			c.message = "内核连续 5 次自动重启失败，已停止；请手动重启"
			c.mu.Unlock()
			if c.opt.OnCrash != nil {
				c.opt.OnCrash("内核连续自动重启失败，请手动重启")
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		log.Printf("[kunpeng][watchdog] 第 %d 次自动重启（退避 %s）", attempt+1, delay)
		if !c.restartAttempt() {
			c.mu.Lock()
			stopped := c.stopped
			c.mu.Unlock()
			if stopped {
				return // 用户在此期间手动停止
			}
			continue
		}
		// 重启成功（restartAttempt→applyLocked 已重建事件会话），记录计数并回调。
		c.mu.Lock()
		c.restartAttempts = attempt + 1
		c.mu.Unlock()
		log.Printf("[kunpeng][watchdog] 内核已在第 %d 次尝试后恢复", attempt+1)
		if c.opt.OnRecovered != nil {
			c.opt.OnRecovered()
		}
		return
	}
}

// healthCheck 每 60s 对当前出口做延迟测试；连续 3 次失败且非自动模式时，
// 把 🚀 节点选择 切到 ♻️ 自动选择（DESIGN.md §7.5）。
func (c *Controller) healthCheck(ctx context.Context) {
	failCount := 0
	tick := time.NewTicker(healthInterval)
	defer tick.Stop()
	for ctx.Err() == nil {
		<-tick.C
		now, err := c.groupNow(GroupSelect)
		if err != nil {
			continue // 内核不可用交给看门狗
		}
		if now == GroupAuto {
			failCount = 0 // url-test 组自己会挑可用节点
			continue
		}
		if _, err := c.ProxyDelay(now, HealthURL, healthProbeTimeout); err != nil {
			failCount++
			if failCount >= healthFailLimit {
				failCount = 0
				if serr := c.Select(GroupSelect, GroupAuto); serr == nil {
					c.setMessage("当前出口连续不可用，已自动切换到 " + GroupAuto)
				}
			}
			continue
		}
		failCount = 0
	}
}

// ---------------------------------------------------------------------------
// REST 桥接（app.ProxyService 与健康检查共用）
// ---------------------------------------------------------------------------

// apiJSON 发起 external-controller REST 请求并解码 JSON。
func (c *Controller) apiJSON(method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(context.Background(), method, c.streamURL(path), bodyReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.opt.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf := make([]byte, 2048)
		n, _ := resp.Body.Read(buf)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(buf[:n]))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// groupNow 查询组当前选中的节点（GET /proxies/{group} → now）。
func (c *Controller) groupNow(group string) (string, error) {
	var out struct {
		Now string `json:"now"`
	}
	if err := c.apiJSON("GET", "/proxies/"+url.PathEscape(group), nil, &out); err != nil {
		return "", err
	}
	return out.Now, nil
}

// All GET /proxies 全量。
func (c *Controller) All() (map[string]any, error) {
	var out map[string]any
	if err := c.apiJSON("GET", "/proxies", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Select PUT /proxies/{group} 选择节点。
func (c *Controller) Select(group, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	return c.apiJSON("PUT", "/proxies/"+url.PathEscape(group), body, nil)
}

// GroupDelay GET /proxies/{group}/delay 批量延迟测试。
func (c *Controller) GroupDelay(group, probeURL string, timeoutMs int) (map[string]any, error) {
	if timeoutMs <= 0 {
		timeoutMs = healthProbeTimeout
	}
	path := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=%d",
		url.PathEscape(group), url.QueryEscape(probeURL), timeoutMs)
	var out map[string]any
	if err := c.apiJSON("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Delay 单节点延迟结果。
type Delay struct {
	Name    string
	Delay   int
	Message string
}

// ProxyDelay GET /proxies/{name}/delay。
func (c *Controller) ProxyDelay(name, probeURL string, timeoutMs int) (Delay, error) {
	if timeoutMs <= 0 {
		timeoutMs = healthProbeTimeout
	}
	path := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=%d",
		url.PathEscape(name), url.QueryEscape(probeURL), timeoutMs)
	var out struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	if err := c.apiJSON("GET", path, nil, &out); err != nil {
		return Delay{Name: name, Message: err.Error()}, err
	}
	return Delay{Name: name, Delay: out.Delay, Message: out.Message}, nil
}

// Rules GET /rules。
func (c *Controller) Rules() (map[string]any, error) {
	var out map[string]any
	if err := c.apiJSON("GET", "/rules", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteConnection DELETE /connections/{id} 关闭单个连接。
func (c *Controller) DeleteConnection(id string) error {
	return c.apiJSON("DELETE", "/connections/"+url.PathEscape(id), nil, nil)
}

// CloseAllConnections DELETE /connections 关闭全部连接。
func (c *Controller) CloseAllConnections() error {
	return c.apiJSON("DELETE", "/connections", nil, nil)
}
