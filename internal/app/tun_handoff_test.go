package app

// TUN 特权核启动失败排查：用户现场特权核报「混合代理端口已被占用」。
// 本测试验证 Handoff（GUI 内嵌核移交）后 mixed-port 是否真正释放。
import (
	"net"
	"testing"
	"time"
)

func portOpenLocal(t *testing.T, port int) bool {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestHandoffReleasesMixedPort(t *testing.T) {
	a, _ := newSmokeApp(t)
	// 显式启动内嵌核（装置 autoConnect=false）
	if err := a.CoreStart(); err != nil {
		t.Fatalf("CoreStart: %v", err)
	}
	port := a.MixedPort()
	if !portOpenLocal(t, port) {
		t.Fatalf("前置失败：内嵌核 mixed-port %d 未监听", port)
	}
	// 移交（与 enableTun 相同路径）
	a.core.Handoff()
	// 给热应用与监听 Close 留足时间
	deadline := time.Now().Add(3 * time.Second)
	for {
		if !portOpenLocal(t, port) {
			t.Logf("Handoff 后 %d 端口已释放（耗时内）", port)
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("BUG 复现：Handoff 后 mixed-port %d 仍在 LISTEN —— 特权核将因端口占用启动即死", port)
}
