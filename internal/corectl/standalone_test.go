package corectl

// standalone_test.go —— config-only 独立内核模式的起停验证（无需 root；
// 不启用 TUN 断言，只验证：能启动、REST 可用、quit 文件能优雅退出、端口释放）。

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunStandaloneStartAndQuit(t *testing.T) {
	// 动态端口，绝不与用户 GUI（7890/9097）或其他测试冲突。
	mixed := freeStandalonePort(t)
	ctl := freeStandalonePort(t)
	secret := "standalone-secret"
	home := t.TempDir()
	cfgPath := filepath.Join(home, "runtime.yaml")
	quitFile := filepath.Join(home, "quit")

	cfg := []byte(fmt.Sprintf(`mixed-port: %d
mode: rule
log-level: info
external-controller: 127.0.0.1:%d
secret: %s
proxies: []
rules:
  - MATCH,DIRECT
`, mixed, ctl, secret))
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- RunStandalone(StandaloneOptions{
			ConfigPath:     cfgPath,
			DataDir:        home,
			ControllerAddr: fmt.Sprintf("127.0.0.1:%d", ctl),
			Secret:         secret,
			QuitFile:       quitFile,
			QuitPoll:       200 * time.Millisecond,
		})
	}()

	// 等待 REST 就绪。
	base := fmt.Sprintf("http://127.0.0.1:%d", ctl)
	client := &http.Client{Timeout: time.Second}
	ready := false
	for i := 0; i < 40; i++ {
		req, _ := http.NewRequest(http.MethodGet, base+"/version", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		t.Fatal("standalone 内核未在 10s 内就绪")
	}

	// 无密钥应 401（鉴权生效）。
	if resp, err := client.Get(base + "/version"); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("无密钥应 401，实际 %d", resp.StatusCode)
		}
	}

	// 触碰 quit 文件 → 优雅退出。
	if err := os.WriteFile(quitFile, []byte("quit"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunStandalone 应正常返回 nil: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quit 文件未触发 10s 内退出")
	}

	// 端口释放。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !portOpen(mixed) && !portOpen(ctl) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("端口未释放 mixed=%v ctl=%v", portOpen(mixed), portOpen(ctl))
}

func TestRunStandaloneRequiresArgs(t *testing.T) {
	if err := RunStandalone(StandaloneOptions{}); err == nil {
		t.Fatal("缺少参数应报错")
	}
}

func freeStandalonePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
