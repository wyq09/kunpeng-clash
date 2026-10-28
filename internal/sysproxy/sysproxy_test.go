package sysproxy

import (
	"fmt"
	"os"
	"testing"
)

// TestManualEnableDisable 手动执行的「写入→验证→立即还原」成对测试
// （AGENTS.md 系统安全约束）。普通 `go test ./...` 自动跳过；
// 手动运行：KUNPENG_SYSPROXY_TEST=1 go test ./internal/sysproxy/ -v -run TestManual
func TestManualEnableDisable(t *testing.T) {
	if os.Getenv("KUNPENG_SYSPROXY_TEST") == "" {
		t.Skip("系统代理会改写本机网络设置；需要时设 KUNPENG_SYSPROXY_TEST=1 手动运行")
	}
	const testPort = 17891

	if Enabled() {
		t.Fatal("测试前置不成立：系统代理当前应为关闭（Enabled: No）")
	}

	// 写入
	if err := Enable(testPort, nil); err != nil {
		t.Fatalf("Enable 失败: %v", err)
	}

	// 验证（失败立即还原）
	if !Enabled() {
		if derr := Disable(); derr != nil {
			t.Fatalf("Enable 后验证失败，且还原也失败: %v / %v", nil, derr)
		}
		t.Fatal("Enable 后 Enabled() 仍为 false")
	}
	svcs, _ := Services()
	svc := svcs[0]
	out, err := run("-getwebproxy", svc)
	if err != nil || !containsField(out, fmt.Sprintf("127.0.0.1")) || !containsField(out, fmt.Sprint(testPort)) {
		t.Fatalf("webproxy 详情异常: %v %s", err, out)
	}

	// 立即还原
	if err := Disable(); err != nil {
		t.Fatalf("Disable 失败: %v", err)
	}
	if Enabled() {
		t.Fatal("还原失败：系统代理仍为开启！请手动执行 networksetup -setwebproxy <svc> off")
	}
	t.Logf("成对测试完成：写入(%s→127.0.0.1:%d) → 验证 → 还原，均通过；服务数=%d", svc, testPort, len(svcs))
}

func containsField(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
