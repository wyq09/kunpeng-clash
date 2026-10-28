package corectl

// 用真实 runtime.yaml（全量 proxies/rules/dns）复现 Handoff 后端口未释放的问题。
// 需要本机存在 ~/Library/Application Support/KunpengClash/runtime.yaml；不存在则跳过。
import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func dialOK(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func TestHandoffWithRealConfig(t *testing.T) {
	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, "Library/Application Support/KunpengClash/runtime.yaml"))
	if err != nil {
		t.Skipf("无真实 runtime.yaml: %v", err)
	}
	// 换端口避免与正在运行的 GUI 冲突
	re := regexp.MustCompile(`mixed-port: \d+`)
	raw = re.ReplaceAll(raw, []byte("mixed-port: 17895"))
	raw = regexp.MustCompile(`external-controller: .*`).ReplaceAll(raw, []byte("external-controller: 127.0.0.1:17998"))
	raw = regexp.MustCompile(`(?m)^secret: .*$`).ReplaceAll(raw, []byte("secret: realtest"))

	dir := t.TempDir()
	// geodata：真实 config 有 GEOSITE/GEOIP 规则，HomeDir 需要有文件
	geoHome := filepath.Join(home, "Library/Application Support/KunpengClash")
	for _, f := range []string{"geoip.metadb", "GEOSITE.dat"} {
		if b, e := os.ReadFile(filepath.Join(geoHome, f)); e == nil {
			_ = os.WriteFile(filepath.Join(dir, f), b, 0o644)
		}
	}

	c, err := New(Options{DataDir: dir, ControllerAddr: "127.0.0.1:17998", Secret: "realtest"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Start(raw); err != nil {
		t.Fatalf("Start(真实配置): %v", err)
	}
	if !dialOK("127.0.0.1:17895") {
		t.Fatal("前置失败：端口未监听")
	}
	t.Log("内核已起（真实配置），执行 Handoff…")
	c.Handoff()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if !dialOK("127.0.0.1:17895") {
			t.Log("PASS：Handoff 后真实配置的 mixed-port 已释放")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("BUG 复现：Handoff 后真实配置的 mixed-port 仍 LISTEN —— closeInboundLocked 对全量配置失效")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
