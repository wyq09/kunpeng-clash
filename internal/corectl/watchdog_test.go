package corectl

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/hub/route"
)

func netDial(network, addr string) (net.Conn, error) {
	return net.DialTimeout(network, addr, time.Second)
}

// TestWatchdogCrashAutoRestart 实测看门狗完整链路：
// 关闭 external-controller 模拟内核失联 → 看门狗判定崩溃（清代理回调）→
// 退避重启 → API 恢复（OnRecovered 回调）。退避与轮询周期注入短值加速。
func TestWatchdogCrashAutoRestart(t *testing.T) {
	oldInterval, oldLimit := watchdogInterval, watchdogFailLimit
	watchdogInterval = 150 * time.Millisecond
	watchdogFailLimit = 2
	defer func() {
		watchdogInterval = oldInterval
		watchdogFailLimit = oldLimit
	}()

	const (
		ctlPort    = 19098
		mixedPort  = 17893
		testSecret = "watchdog-secret"
	)
	var crashCalls, recoveredCalls atomic.Int32
	ctl, err := New(Options{
		DataDir:        t.TempDir(),
		ControllerAddr: fmt.Sprintf("127.0.0.1:%d", ctlPort),
		Secret:         testSecret,
		OnCrash:        func(string) { crashCalls.Add(1) },
		OnRecovered:    func() { recoveredCalls.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctl.backoff = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}

	cfg := []byte(fmt.Sprintf(`mixed-port: %d
mode: rule
log-level: info
external-controller: 127.0.0.1:%d
secret: %s
proxies: []
rules:
  - MATCH,DIRECT
`, mixedPort, ctlPort, testSecret))

	if err := ctl.Start(cfg); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}
	defer ctl.Shutdown()
	if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", mixedPort), 5*time.Second); err != nil {
		t.Fatalf("mixed-port 未监听: %v", err)
	}

	// 模拟内核失联：仅关掉 external-controller（running 状态机仍为 true）。
	route.ReCreateServer(&route.Config{})
	if code := apiCode(t, ctlPort, "/version", testSecret); code == http.StatusOK {
		t.Fatal("关闭 API 失败，测试前置不成立")
	}

	// 看门狗应在 ~interval*limit 内判定崩溃并自动重启恢复。
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if recoveredCalls.Load() >= 1 && ctl.Running() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if crashCalls.Load() < 1 {
		t.Fatalf("看门狗未触发 OnCrash（calls=%d）", crashCalls.Load())
	}
	if recoveredCalls.Load() < 1 {
		t.Fatalf("看门狗未自动恢复（OnRecovered=%d, running=%v, msg=%s）",
			recoveredCalls.Load(), ctl.Running(), ctl.Message())
	}
	if code := apiCode(t, ctlPort, "/version", testSecret); code != http.StatusOK {
		t.Fatalf("自动重启后 /version 应为 200，实际 %d", code)
	}
	if err := ctl.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := waitTCPClosed(fmt.Sprintf("127.0.0.1:%d", mixedPort), 3*time.Second); err != nil {
		t.Fatalf("重启后 Stop 端口未释放: %v", err)
	}
}

// TestNextBackoffSequence 看门狗退避序列：1s,2s,4s,8s,16s，第 6 次放弃。
func TestNextBackoffSequence(t *testing.T) {
	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	c, err := New(Options{DataDir: t.TempDir(), ControllerAddr: "127.0.0.1:19099", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	got := c.BackoffDelays()
	if len(got) != 5 {
		t.Fatalf("退避序列长度应为 5: %v", got)
	}
	for i := range delays {
		if got[i] != delays[i] {
			t.Fatalf("退避[%d]=%s 期望 %s", i, got[i], delays[i])
		}
	}
	if NextBackoff(delays, 5) != 0 {
		t.Fatal("超过次数应返回 0（放弃重启）")
	}
}

// TestStartStopIntegration 真实启动嵌入内核：API 可用、鉴权生效、
// Stop 后端口释放。使用独立端口避免与本机 7890/9097 冲突。
func TestStartStopIntegration(t *testing.T) {
	const (
		ctlPort    = 19097
		mixedPort  = 17891
		testSecret = "unit-test-secret"
	)
	dir := t.TempDir()
	ctl, err := New(Options{
		DataDir:        dir,
		ControllerAddr: fmt.Sprintf("127.0.0.1:%d", ctlPort),
		Secret:         testSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := []byte(fmt.Sprintf(`mixed-port: %d
allow-lan: false
mode: rule
log-level: info
external-controller: 127.0.0.1:%d
secret: %s
ipv6: false
proxies: []
rules:
  - MATCH,DIRECT
`, mixedPort, ctlPort, testSecret))

	if err := ctl.Start(cfg); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}
	if !ctl.Running() {
		t.Fatal("Start 后 Running 应为 true")
	}
	defer ctl.Shutdown()

	// API 可用（带 secret）。
	version := apiGet(t, ctlPort, testSecret, "/version", true)
	if version == "" || !json.Valid([]byte(version)) {
		t.Fatalf("/version 响应异常: %q", version)
	}

	// 无 secret 必须被拒。
	if code := apiCode(t, ctlPort, "/version", ""); code != http.StatusUnauthorized {
		t.Fatalf("无 secret 请求应 401，实际 %d", code)
	}

	// mixed-port 已监听。
	if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", mixedPort), 5*time.Second); err != nil {
		t.Fatalf("mixed-port 未监听: %v", err)
	}

	// Stop：Running=false，API 不可达。
	if err := ctl.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ctl.Running() {
		t.Fatal("Stop 后 Running 应为 false")
	}
	if err := waitTCPClosed(fmt.Sprintf("127.0.0.1:%d", ctlPort), 3*time.Second); err != nil {
		t.Fatalf("Stop 后 external-controller 端口未释放: %v", err)
	}
	if err := waitTCPClosed(fmt.Sprintf("127.0.0.1:%d", mixedPort), 3*time.Second); err != nil {
		t.Fatalf("Stop 后 mixed-port 端口未释放: %v", err)
	}
}

func apiGet(t *testing.T, port int, secret, path string, withSecret bool) string {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if withSecret {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

// apiCode 返回状态码；secret 为空则不带鉴权头；连接失败返回 0。
func apiCode(t *testing.T, port int, path, secret string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0 // 连接失败 = API 不可达
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func waitTCP(addr string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		conn, err := netDial("tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 超时", addr)
}

func waitTCPClosed(addr string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		conn, err := netDial("tcp", addr)
		if err != nil {
			return nil // 已无法连接 = 已释放
		}
		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 释放超时", addr)
}
