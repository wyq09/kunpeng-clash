package app

import (
	"net"
	"testing"
)

// portFree 语义防回归：有监听=被占=false；无监听=true。
func TestPortFreeSemantics(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:17997")
	if err != nil {
		t.Skipf("端口被占: %v", err)
	}
	defer ln.Close()
	if portFree(17997) {
		t.Fatal("监听中的端口 portFree 应为 false")
	}
	if !portFree(17996) {
		t.Fatal("无监听端口 portFree 应为 true")
	}
}
