package app

// smoke_v2_test.go —— 本轮四个需求的集成测试（真实启动嵌入内核，动态端口）：
//  1. 白名单 Set/Get/持久化/热应用（内核 REST /rules 生效 + runtime.yaml 落盘顺序）；
//  2. 自定义节点导入/重名跳过/删除（节点并入策略组并真实热应用）；
//  3. 订阅改名 + 改 URL（httptest 本地服务器重新拉取 + 热应用）；
//  4. 测速 URL 为 http 版随 runtime.yaml 下发。
//
// 全程 --no-sysproxy 语义（不触碰系统设置），数据目录用 t.TempDir()，
// 端口用动态空闲端口，绝不影响正在运行的用户 GUI（7890/9097）。

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/constant"
	"go.yaml.in/yaml/v3"

	"kunpeng/internal/configgen"
)

const smokeSubYAML = `# 测试订阅
proxies:
  - name: "🇭🇰 香港 IEPL 01"
    type: ss
    server: hk1.example.com
    port: 8388
    cipher: aes-128-gcm
    password: "pass:word"
  - name: "🇯🇵 日本 02"
    type: vmess
    server: jp1.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    alterId: 0
    cipher: auto
`

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newSmokeApp 预置一份本地订阅数据目录并创建应用（不启动内核）。
func newSmokeApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	const id = "test0001"
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", id+".yaml"), []byte(smokeSubYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	profilesYAML := fmt.Sprintf(`profiles:
    - id: %s
      name: 测试订阅
      url: https://unused.invalid/sub
      updatedAt: "2026-01-01T00:00:00Z"
      nodeCount: 2
`, id)
	if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte(profilesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"mode": "rule", "mixedPort": 17891, "systemProxy": false,
		"autoConnect": false, "activeProfileId": id, "secret": "smoke-secret",
	}
	stateJSON, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), stateJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	geoIP, err := os.ReadFile(filepath.Join("..", "..", "resources", "geodata", "GEOIP.metadb"))
	if err != nil {
		t.Skipf("缺少 geodata: %v", err)
	}
	geoSite, err := os.ReadFile(filepath.Join("..", "..", "resources", "geodata", "GEOSITE.dat"))
	if err != nil {
		t.Skipf("缺少 geodata: %v", err)
	}

	constant.SetHomeDir(dir) // 隔离 mihomo 全局路径
	a, err := New(Options{
		DataDir:        dir,
		Port:           freePort(t),
		NoSysProxy:     true,
		ControllerAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		PresetURL:      "", // 已有本地订阅，不联网导入
		GeoIPBytes:     geoIP,
		GeoSiteBytes:   geoSite,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(a.Shutdown)
	return a, id
}

// readRuntime 读 runtime.yaml 并抽取 rules（解析后的字符串数组）。
func readRuntimeRules(t *testing.T, a *App) []string {
	t.Helper()
	data, err := os.ReadFile(a.runtimePath())
	if err != nil {
		t.Fatalf("读取 runtime.yaml: %v", err)
	}
	var cfg struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Rules
}

// restRulePayloads 通过内核 REST /rules 取当前生效规则的 payload。
func restRulePayloads(t *testing.T, a *App) []string {
	t.Helper()
	all, err := a.Rules()
	if err != nil {
		t.Fatalf("GET /rules: %v", err)
	}
	rs, _ := all["rules"].([]any)
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		m, _ := r.(map[string]any)
		p, _ := m["payload"].(string)
		out = append(out, p)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestSmokeV2Whitelist(t *testing.T) {
	a, _ := newSmokeApp(t)
	if err := a.CoreStart(); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}
	if !a.core.Running() {
		t.Fatal("内核应已运行")
	}

	// 非法输入：报错且不改动现有列表
	if err := a.WhitelistSet([]string{"bad cidr/24"}); err == nil {
		t.Fatal("非法 CIDR 应报错")
	}
	if err := a.WhitelistSet([]string{"https://x.com/a"}); err == nil {
		t.Fatal("带协议头与路径的输入应报错")
	}
	many := make([]string, configgen.WhitelistLimit+1)
	for i := range many {
		many[i] = fmt.Sprintf("d%d.example.com", i)
	}
	if err := a.WhitelistSet(many); err == nil {
		t.Fatal("超过上限应报错")
	}
	if got := a.WhitelistGet(); len(got) != 0 {
		t.Fatalf("失败设置不应改动列表: %v", got)
	}

	// 正常设置：*.example.com + 10.0.0.0/8
	if err := a.WhitelistSet([]string{"*.example.com", " 10.0.0.0/8 "}); err != nil {
		t.Fatalf("WhitelistSet: %v", err)
	}
	got := a.WhitelistGet()
	if len(got) != 2 || got[0] != "*.example.com" || got[1] != "10.0.0.0/8" {
		t.Fatalf("WhitelistGet = %v", got)
	}

	// runtime.yaml：白名单规则在私有网段 4 条之后、DOMAIN-SUFFIX,cn 之前
	rules := readRuntimeRules(t, a)
	wantPrefix := []string{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,example.com,DIRECT", // *.example.com 展开去重后为一条
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,cn,DIRECT",
	}
	if len(rules) < len(wantPrefix) {
		t.Fatalf("规则数不足: %v", rules)
	}
	for i, w := range wantPrefix {
		if rules[i] != w {
			t.Fatalf("runtime 规则[%d] = %q 期望 %q", i, rules[i], w)
		}
	}

	// 内核热应用生效：REST /rules 里能看到白名单规则
	payloads := restRulePayloads(t, a)
	if !contains(payloads, "example.com") || !contains(payloads, "10.0.0.0/8") {
		t.Fatalf("内核 /rules 未生效白名单: %v", payloads)
	}

	// state.json 持久化
	data, err := os.ReadFile(a.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"whitelist"`) {
		t.Fatalf("state.json 未持久化 whitelist: %s", data)
	}

	// 清空白名单 → 规则恢复
	if err := a.WhitelistSet(nil); err != nil {
		t.Fatalf("清空白名单: %v", err)
	}
	rules = readRuntimeRules(t, a)
	if rules[4] != "DOMAIN-SUFFIX,cn,DIRECT" {
		t.Fatalf("清空后白名单规则应消失: %v", rules[:6])
	}
}

func TestSmokeV2CustomNodes(t *testing.T) {
	a, _ := newSmokeApp(t)
	if err := a.CoreStart(); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}
	base, err := a.ProxiesAll()
	if err != nil {
		t.Fatalf("GET /proxies: %v", err)
	}
	baseProxies, _ := base["proxies"].(map[string]any)
	baseCount := len(baseProxies)

	// 导入：1 条 hysteria2 有效 + 1 条非法行
	raw := "hysteria2://letmein@example.com:8443?sni=example.com&insecure=1#我的HY2测试\nhttps://not-a-node.example/x\n"
	added, err := a.NodesAddFromLinks(raw)
	if err != nil || added != 1 {
		t.Fatalf("AddFromLinks added=%d err=%v", added, err)
	}
	if names := a.NodesList(); len(names) != 1 || names[0] != "我的HY2测试" {
		t.Fatalf("NodesList = %v", names)
	}

	// 内核全量 proxies +1，且两个策略组都包含它
	all, err := a.ProxiesAll()
	if err != nil {
		t.Fatal(err)
	}
	proxies, _ := all["proxies"].(map[string]any)
	if len(proxies) != baseCount+1 {
		t.Fatalf("proxies 数量 %d 期望 %d", len(proxies), baseCount+1)
	}
	if _, ok := proxies["我的HY2测试"]; !ok {
		t.Fatal("内核 proxies 缺少自定义节点")
	}
	for _, group := range []string{configgen.GroupSelect, configgen.GroupAuto} {
		g, ok := proxies[group].(map[string]any)
		if !ok {
			t.Fatalf("缺少组 %s", group)
		}
		// mihomo REST 里组的成员字段是 all（见 adapter/outboundgroup/selector.go）。
		gp, _ := g["all"].([]any)
		found := false
		for _, n := range gp {
			if s, _ := n.(string); s == "我的HY2测试" {
				found = true
			}
		}
		if !found {
			t.Fatalf("组 %s 未包含自定义节点: %v", group, gp)
		}
	}
	// runtime.yaml 同步落盘
	runtimeData, _ := os.ReadFile(a.runtimePath())
	if !strings.Contains(string(runtimeData), "我的HY2测试") {
		t.Fatal("runtime.yaml 未包含自定义节点")
	}
	// state.json 持久化
	stateData, _ := os.ReadFile(a.statePath())
	if !strings.Contains(string(stateData), `"customProxies"`) {
		t.Fatal("state.json 未持久化 customProxies")
	}

	// 重名再导入：全部跳过
	if _, err := a.NodesAddFromLinks("hysteria2://letmein@example.com:8443?sni=x#我的HY2测试"); err == nil {
		t.Fatal("重名导入应报错")
	}
	if len(a.NodesList()) != 1 {
		t.Fatal("重名导入不应改变列表")
	}

	// 删除并热应用恢复
	if err := a.NodesRemove("我的HY2测试"); err != nil {
		t.Fatalf("NodesRemove: %v", err)
	}
	if len(a.NodesList()) != 0 {
		t.Fatal("删除后列表应为空")
	}
	all, err = a.ProxiesAll()
	if err != nil {
		t.Fatal(err)
	}
	proxies, _ = all["proxies"].(map[string]any)
	if _, exists := proxies["我的HY2测试"]; exists {
		t.Fatal("删除后内核仍有该节点")
	}
	if len(proxies) != baseCount {
		t.Fatalf("删除后 proxies 数量应恢复 %d，实际 %d", baseCount, len(proxies))
	}
	if err := a.NodesRemove("不存在的节点"); err == nil {
		t.Fatal("删除不存在的节点应报错")
	}

	// 空输入
	if _, err := a.NodesAddFromLinks("https://bad\n\n"); err == nil {
		t.Fatal("全部无法解析应报错")
	}
}

func TestSmokeV2ProfileEdit(t *testing.T) {
	a, id := newSmokeApp(t)
	if err := a.CoreStart(); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}

	// 仅改名（url 不变不重新拉取，离线可测）
	p, err := a.ProfileEdit(id, "改名后的订阅", "", false, 0)
	if err != nil {
		t.Fatalf("改名失败: %v", err)
	}
	if p.Name != "改名后的订阅" {
		t.Fatalf("Name = %s", p.Name)
	}
	if cur, err := a.ProfileCurrent(); err != nil || cur.Name != "改名后的订阅" {
		t.Fatalf("ProfileCurrent = %+v err=%v", cur, err)
	}
	// 改名后 profiles.yaml 落盘
	pdata, _ := os.ReadFile(a.profilesPath())
	if !strings.Contains(string(pdata), "改名后的订阅") {
		t.Fatal("profiles.yaml 未持久化新名称")
	}

	// 改 URL：httptest 本地服务器提供新订阅（不同节点名以验证真的拉取了）
	newSub := `proxies:
  - name: "🇺🇸 美国 NEW"
    type: ss
    server: us.example.com
    port: 8388
    cipher: aes-128-gcm
    password: x
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(newSub))
	}))
	defer srv.Close()

	p, err = a.ProfileEdit(id, "", srv.URL+"/sub", false, 0)
	if err != nil {
		t.Fatalf("改 URL 失败: %v", err)
	}
	if p.URL != srv.URL+"/sub" {
		t.Fatalf("URL 未更新: %s", p.URL)
	}
	if p.NodeCount != 1 {
		t.Fatalf("NodeCount = %d 期望 1", p.NodeCount)
	}
	// 激活订阅 + 内核在跑 → 新节点已热应用
	all, err := a.ProxiesAll()
	if err != nil {
		t.Fatal(err)
	}
	proxies, _ := all["proxies"].(map[string]any)
	if _, ok := proxies["🇺🇸 美国 NEW"]; !ok {
		t.Fatalf("内核未热应用新订阅节点: %v", func() []string {
			keys := make([]string, 0, len(proxies))
			for k := range proxies {
				keys = append(keys, k)
			}
			return keys
		}())
	}

	// 不存在的订阅
	if _, err := a.ProfileEdit("nope", "x", "", false, 0); err == nil {
		t.Fatal("不存在的订阅应报错")
	}

	// 停掉内核后改 URL：拉取成功但热应用跳过（core 未运行）不报错
	if err := a.CoreStop(); err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(newSub))
	}))
	defer srv2.Close()
	if _, err := a.ProfileEdit(id, "再次改名", srv2.URL, false, 0); err != nil {
		t.Fatalf("内核停止时编辑应成功: %v", err)
	}
}

func TestSmokeV2HealthURLIsHTTP(t *testing.T) {
	a, _ := newSmokeApp(t)
	if err := a.CoreStart(); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}
	data, err := os.ReadFile(a.runtimePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "url: http://www.gstatic.com/generate_204") {
		t.Fatal("runtime.yaml 的自动组测速 URL 应为 http 版")
	}
	if strings.Contains(string(data), "https://www.gstatic.com") {
		t.Fatal("runtime.yaml 出现 https 版测速 URL")
	}
}

func TestSmokeV2RouteRules(t *testing.T) {
	a, id := newSmokeApp(t)
	if err := a.CoreStart(); err != nil {
		t.Fatalf("内核启动失败: %v", err)
	}

	// 可选节点清单：订阅真实节点 + 自定义节点，过滤机场信息占位节点
	nodes, err := a.RouteRuleNodes(id)
	if err != nil {
		t.Fatalf("RouteRuleNodes: %v", err)
	}
	if len(nodes) != 2 || !contains(nodes, "🇭🇰 香港 IEPL 01") || !contains(nodes, "🇯🇵 日本 02") {
		t.Fatalf("RouteRuleNodes = %v", nodes)
	}

	// 非法输入全部报错且不落盘
	for i, bad := range [][]RouteRule{
		{{Name: "AI", Domains: nil, Nodes: []string{"🇯🇵 日本 02"}}},              // 无域名
		{{Name: "AI", Domains: []string{"*.chatgpt.com"}, Nodes: nil}},         // 无节点
		{{Name: "AI", Domains: []string{"not a domain"}, Nodes: []string{""}}}, // 非法域名
		{{Name: "A,B", Domains: []string{"a.com"}, Nodes: []string{"n"}}},      // 组名含逗号
	} {
		if err := a.RouteRulesSave(id, bad); err == nil {
			t.Fatalf("非法规则用例 %d 应报错", i)
		}
	}
	if got, err := a.RouteRulesList(id); err != nil || len(got) != 0 {
		t.Fatalf("失败保存不应落盘: %v %v", got, err)
	}

	// 正常保存：域名组 → 指定节点组（挂在订阅下）
	rules := []RouteRule{{
		Name:    "AI 专线",
		Domains: []string{"*.chatgpt.com", " claude.ai ", "10.0.0.0/8"},
		Nodes:   []string{"🇭🇰 香港 IEPL 01", "不存在的节点"},
		Auto:    false,
	}}
	if err := a.RouteRulesSave(id, rules); err != nil {
		t.Fatalf("RouteRulesSave: %v", err)
	}
	saved, err := a.RouteRulesList(id)
	if err != nil || len(saved) != 1 {
		t.Fatalf("RouteRulesList = %v err=%v", saved, err)
	}
	if saved[0].ID == "" || saved[0].Domains[0] != "*.chatgpt.com" ||
		saved[0].Domains[1] != "claude.ai" || len(saved[0].Nodes) != 2 {
		t.Fatalf("规范化结果不符: %+v", saved[0])
	}

	// runtime.yaml：分流规则在私有网段 4 条之后、国内直连之前；组只含存在的节点
	rulesYAML := readRuntimeRules(t, a)
	wantPrefix := []string{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,chatgpt.com,AI 专线",
		"DOMAIN-SUFFIX,claude.ai,AI 专线",
		"IP-CIDR,10.0.0.0/8,AI 专线,no-resolve",
		"DOMAIN-SUFFIX,cn,DIRECT",
	}
	for i, w := range wantPrefix {
		if rulesYAML[i] != w {
			t.Fatalf("runtime 规则[%d] = %q 期望 %q", i, rulesYAML[i], w)
		}
	}
	all, err := a.ProxiesAll()
	if err != nil {
		t.Fatal(err)
	}
	proxies, _ := all["proxies"].(map[string]any)
	g, ok := proxies["AI 专线"].(map[string]any)
	if !ok {
		t.Fatalf("内核缺少分流组: %v", func() []string {
			keys := make([]string, 0, len(proxies))
			for k := range proxies {
				keys = append(keys, k)
			}
			return keys
		}())
	}
	gp, _ := g["all"].([]any)
	if len(gp) != 1 || gp[0] != "🇭🇰 香港 IEPL 01" {
		t.Fatalf("分流组节点应只含存在的节点: %v", gp)
	}
	// 内核 /rules 生效
	payloads := restRulePayloads(t, a)
	if !contains(payloads, "chatgpt.com") || !contains(payloads, "claude.ai") {
		t.Fatalf("内核 /rules 未生效分流规则: %v", payloads)
	}
	// state.json 持久化（挂在订阅 ID 下）
	stateData, _ := os.ReadFile(a.statePath())
	if !strings.Contains(string(stateData), `"routeRules"`) || !strings.Contains(string(stateData), id) {
		t.Fatalf("state.json 未持久化 routeRules: %s", stateData)
	}

	// 挂到非激活订阅的规则不影响当前 runtime：清空激活订阅的规则
	if err := a.RouteRulesSave(id, nil); err != nil {
		t.Fatalf("清空规则: %v", err)
	}
	rulesYAML = readRuntimeRules(t, a)
	if rulesYAML[4] != "DOMAIN-SUFFIX,cn,DIRECT" {
		t.Fatalf("清空后分流规则应消失: %v", rulesYAML[:6])
	}

	// 删除订阅时连带着清除挂载的规则
	if err := a.RouteRulesSave(id, rules); err != nil {
		t.Fatal(err)
	}
	if err := a.ProfileRemove(id); err != nil {
		t.Fatal(err)
	}
	if got, err := a.RouteRulesList(id); err == nil {
		t.Fatalf("订阅删除后规则应一并清除: %v", got)
	}
}
