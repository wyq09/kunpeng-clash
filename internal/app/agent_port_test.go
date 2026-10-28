package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kunpeng/internal/agentapi"
)

func pingAgent(port int) bool {
	c := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/ping", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func endpointPort(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, agentapi.EndpointFile))
	if err != nil {
		t.Fatalf("读取 agent.json: %v", err)
	}
	var ep agentapi.Endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		t.Fatal(err)
	}
	return portOf(ep.BaseURL[len("http://"):])
}

func TestAgentSetPort(t *testing.T) {
	a, _ := newSmokeApp(t)
	info := a.AgentInfoNow()
	if !info.Running {
		t.Fatalf("网关未启动: %s", info.Error)
	}
	oldPort := info.Port

	target := freePort(t)
	info, err := a.AgentSetPort(target)
	if err != nil {
		t.Fatalf("SetPort: %v", err)
	}
	if info.Port != target || !info.PortCustom || info.PreferredPort != target {
		t.Fatalf("端口信息不符: %+v", info)
	}
	if !pingAgent(target) {
		t.Fatal("新端口无响应")
	}
	if pingAgent(oldPort) {
		t.Fatal("旧端口仍在监听")
	}
	if p := endpointPort(t, a.dataDir); p != target {
		t.Fatalf("agent.json 端口 %d，期望 %d", p, target)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	if _, err := a.AgentSetPort(busyPort); err == nil {
		t.Fatal("占用端口应报错")
	}
	if !pingAgent(target) {
		t.Fatal("改端口失败后原端口应保持可用")
	}
	if _, err := a.AgentSetPort(80); err == nil {
		t.Fatal("越界端口应报错")
	}
	if _, err := a.AgentSetPort(a.MixedPort()); err == nil {
		t.Fatal("与代理端口相同应报错")
	}

	// 自定义端口被占时：启动临时改用备选端口并提示，但不覆盖用户设定
	a.stopAgent()
	hold, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", target))
	if err != nil {
		t.Fatal(err)
	}
	a.startAgent()
	info = a.AgentInfoNow()
	hold.Close()
	if !info.Running || info.Port == target || info.PortWarning == "" {
		t.Fatalf("应临时改用备选端口并给出提示: %+v", info)
	}
	if info.PreferredPort != target {
		t.Fatalf("设定端口被覆盖: %d", info.PreferredPort)
	}

	a.stopAgent()
	a.startAgent()
	if info = a.AgentInfoNow(); info.Port != target || info.PortWarning != "" {
		t.Fatalf("端口空出后应回到设定端口: %+v", info)
	}
}
