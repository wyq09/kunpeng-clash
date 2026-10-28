package configgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"

	"kunpeng/internal/nodelink"

	"go.yaml.in/yaml/v3"
)

const sampleSubscription = `# comment
port: 7890
proxies:
  - name: "🇭🇰 香港 IEPL 01"
    type: ss
    server: hk1.example.com
    port: 8388
    cipher: aes-128-gcm
    password: "pass:word"
  - name: '🇯🇵 日本 [B]:02'
    type: vmess
    server: jp1.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
    alterId: 0
    cipher: auto
rules:
  - MATCH,DIRECT
`

// releaseGeo 把仓库内真实 geodata 拷进临时目录。
// DNS fallback-filter(geoip) 会让 ParseWithBytes 预加载 MMDB——生产路径
// 由 app.releaseGeodata 先释放，测试同样先释放，否则会去 GitHub 下载超时。
func releaseGeo(t *testing.T, dir string) {
	t.Helper()
	for _, f := range []string{"GEOIP.metadb", "GEOSITE.dat"} {
		src := filepath.Join("../../resources/geodata", f)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Skipf("缺少 %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGenerateAndParse 验证生成的 runtime.yaml 能被 mihomo ParseWithBytes 接受
// （direct 模式，不含 GEOSITE/GEOIP 规则，但 DNS fallback-filter 需要 MMDB）。
func TestGenerateAndParse(t *testing.T) {
	dir := t.TempDir()
	releaseGeo(t, dir)
	constant.SetHomeDir(dir) // 隔离 mihomo 全局路径
	raw, err := Generate([]byte(sampleSubscription), Options{
		MixedPort: 17891,
		Mode:      "direct",
		Secret:    "unit-secret",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := executor.ParseWithBytes(raw); err != nil {
		t.Fatalf("mihomo ParseWithBytes 拒绝生成的配置: %v\nconfig:\n%s", err, raw)
	}

	// 结构断言
	var cfg struct {
		MixedPort          int              `yaml:"mixed-port"`
		ExternalController string           `yaml:"external-controller"`
		Secret             string           `yaml:"secret"`
		Mode               string           `yaml:"mode"`
		Proxies            []map[string]any `yaml:"proxies"`
		ProxyGroups        []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MixedPort != 17891 || cfg.ExternalController != "127.0.0.1:9097" ||
		cfg.Secret != "unit-secret" || cfg.Mode != "direct" {
		t.Fatalf("基础字段不符: %+v", cfg)
	}
	if len(cfg.Proxies) != 2 {
		t.Fatalf("节点数不符: %d", len(cfg.Proxies))
	}
	if len(cfg.ProxyGroups) != 2 ||
		cfg.ProxyGroups[0].Name != GroupSelect || cfg.ProxyGroups[1].Name != GroupAuto {
		t.Fatalf("策略组不符: %+v", cfg.ProxyGroups)
	}
	// 含特殊字符的节点名必须完整保留
	if len(cfg.ProxyGroups[1].Proxies) != 2 ||
		cfg.ProxyGroups[1].Proxies[1] != "🇯🇵 日本 [B]:02" {
		t.Fatalf("节点名丢失或被改写: %v", cfg.ProxyGroups[1].Proxies)
	}
}

// TestRuleModeRules rule 模式必须包含 GEOSITE/GEOIP 兜底规则与选择组 MATCH。
// （yaml.v3 会把含 emoji 的字符串写成 \UXXXX 转义——这是合法 YAML，
// mihomo 能正常反序列化，因此断言走解析后的值而不是子串。）
func TestRuleModeRules(t *testing.T) {
	raw, err := Generate([]byte(sampleSubscription), Options{Mode: "rule", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,cn,DIRECT",
		"DOMAIN-KEYWORD,baidu,DIRECT",
		"DOMAIN-KEYWORD,tencent,DIRECT",
		"GEOSITE,cn,DIRECT",
		"GEOIP,CN,DIRECT,no-resolve",
		"MATCH," + GroupSelect,
	}
	if len(cfg.Rules) != len(want) {
		t.Fatalf("规则数量不符: %v", cfg.Rules)
	}
	for i, r := range want {
		if cfg.Rules[i] != r {
			t.Fatalf("规则[%d]=%q 期望 %q", i, cfg.Rules[i], r)
		}
	}
}

// TestNoProxiesErrors 无节点订阅应报错。
func TestNoProxiesErrors(t *testing.T) {
	if _, err := Generate([]byte("proxies: []\n"), Options{Mode: "rule", Secret: "s"}); err == nil {
		t.Fatal("空 proxies 应返回错误")
	}
}

// TestRandomSecret 密钥长度与唯一性。
func TestRandomSecret(t *testing.T) {
	a, _ := RandomSecret()
	b, _ := RandomSecret()
	if len(a) != 48 || a == b {
		t.Fatalf("随机密钥异常: %s %s", a, b)
	}
}

// TestParseRejectedByMihomo 保底：乱配置必须被 mihomo 拒绝（证明 Parse 真在干活）。
func TestParseRejectedByMihomo(t *testing.T) {
	constant.SetHomeDir(t.TempDir())
	if _, err := executor.ParseWithBytes([]byte("mixed-port: not-a-port\n")); err == nil {
		t.Fatal("非法配置应被 ParseWithBytes 拒绝")
	}
}

// TestHealthURLIsHTTP 测速 URL 必须是 http 版（早高峰 https 握手成功率低，
// http 204 更能反映真实连通性）；组 url 与默认测速值都跟随该常量。
func TestHealthURLIsHTTP(t *testing.T) {
	if HealthURL != "http://www.gstatic.com/generate_204" {
		t.Fatalf("HealthURL 应为 http 版: %s", HealthURL)
	}
	raw, err := Generate([]byte(sampleSubscription), Options{Mode: "rule", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		ProxyGroups []struct {
			Name string `yaml:"name"`
			URL  string `yaml:"url"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, g := range cfg.ProxyGroups {
		if g.Name == GroupAuto && g.URL != HealthURL {
			t.Fatalf("自动组 url = %s 期望 %s", g.URL, HealthURL)
		}
	}
	if strings.Contains(string(raw), "https://www.gstatic.com") {
		t.Fatal("配置中出现 https 版测速 URL")
	}
}

// TestWhitelistToRules 各形态转换。
func TestWhitelistToRules(t *testing.T) {
	got := WhitelistToRules([]string{
		"example.com",   // 域名 → DOMAIN-SUFFIX（含子域）
		"*.Example.com", // 通配 + 大写 → 规范化后与上一条相同 → 去重
		"a.example.com", // 精确 → DOMAIN
		"10.0.0.0/8",    // IP 段 → IP-CIDR,no-resolve
		"",              // 空串忽略
		"example.com",   // 重复项忽略
		"bad/cidr",      // 非法 CIDR 跳过（不让坏规则毒化整份配置）
		"bad cidr/24",   // 同上
	})
	want := []string{
		"DOMAIN-SUFFIX,example.com,DIRECT",
		"DOMAIN,a.example.com,DIRECT",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
	}
	if len(got) != len(want) {
		t.Fatalf("WhitelistToRules = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule[%d] = %q 期望 %q", i, got[i], want[i])
		}
	}
	// *.example.com 与 example.com 展开为同一条规则时应去重
	dup := WhitelistToRules([]string{"example.com", "*.example.com"})
	if len(dup) != 1 {
		t.Fatalf("同域名通配应去重: %v", dup)
	}
}

// TestNormalizeWhitelist 规范化与上限。
func TestNormalizeWhitelist(t *testing.T) {
	got, err := NormalizeWhitelist([]string{"  A.com ", "", "a.com", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a.com" || got[1] != "10.0.0.0/8" {
		t.Fatalf("NormalizeWhitelist = %v", got)
	}
	if _, err := NormalizeWhitelist([]string{"not a domain"}); err == nil {
		t.Fatal("含空格的域名应报错")
	}
	if _, err := NormalizeWhitelist([]string{"999.1.1.1/99"}); err == nil {
		t.Fatal("非法 CIDR 应报错")
	}
	many := make([]string, WhitelistLimit+1)
	for i := range many {
		many[i] = fmt.Sprintf("d%d.example.com", i)
	}
	if _, err := NormalizeWhitelist(many); err == nil {
		t.Fatal("超过上限应报错")
	}
}

// TestGenerateWithWhitelist 白名单规则必须出现在私有网段 4 条之后、
// DOMAIN-SUFFIX,cn 之前，且能被 mihomo 接受。
func TestGenerateWithWhitelist(t *testing.T) {
	dir := t.TempDir()
	releaseGeo(t, dir)
	constant.SetHomeDir(dir)
	raw, err := Generate([]byte(sampleSubscription), Options{
		Mode:      "rule",
		Secret:    "s",
		Whitelist: []string{"*.example.com", "10.0.0.0/8", "a.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ParseWithBytes(raw); err != nil {
		t.Fatalf("含白名单的配置被 mihomo 拒绝: %v", err)
	}
	var cfg struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,example.com,DIRECT",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"DOMAIN,a.example.com,DIRECT",
		"DOMAIN-SUFFIX,cn,DIRECT",
	}
	if len(cfg.Rules) < len(want) {
		t.Fatalf("规则太少: %v", cfg.Rules)
	}
	for i, w := range want {
		if cfg.Rules[i] != w {
			t.Fatalf("规则[%d] = %q 期望 %q（白名单必须在私有网段之后、cn 之前）", i, cfg.Rules[i], w)
		}
	}
}

const customNodeHysteria2 = "hysteria2://letmein@example.com:8443?sni=example.com&insecure=1#我的HY2"

// TestGenerateWithCustomProxies 自定义节点追加到 proxies 末尾并并入两个策略组；
// 与订阅节点重名时自动加后缀而不是让 mihomo 拒绝整份配置。
func TestGenerateWithCustomProxies(t *testing.T) {
	dir := t.TempDir()
	releaseGeo(t, dir)
	constant.SetHomeDir(dir)
	ps, ok, failed := nodelink.Parse(customNodeHysteria2)
	if ok != 1 || failed != 0 {
		t.Fatalf("样例链接解析失败: ok=%d failed=%d", ok, failed)
	}
	custom := []map[string]any{ps[0]}
	// 与订阅节点重名的第二个自定义节点
	custom = append(custom, map[string]any{"type": "ss", "name": "🇭🇰 香港 IEPL 01",
		"server": "dup.example.com", "port": 8388, "cipher": "aes-128-gcm", "password": "p"})

	raw, err := Generate([]byte(sampleSubscription), Options{
		Mode: "rule", Secret: "s", CustomProxies: custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ParseWithBytes(raw); err != nil {
		t.Fatalf("含自定义节点的配置被 mihomo 拒绝: %v\n%s", err, raw)
	}
	var cfg struct {
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Proxies) != 4 { // 订阅 2 + 自定义 2（重名节点改后缀保留）
		t.Fatalf("proxies 数量不符: %d", len(cfg.Proxies))
	}
	if _, ok := cfg.Proxies[2]["password"]; !ok || cfg.Proxies[2]["type"] != "hysteria2" {
		t.Fatalf("自定义 hysteria2 节点未追加到末尾: %v", cfg.Proxies[2])
	}
	var inSelect, inAuto, dupFound bool
	var autoNames []string
	for _, g := range cfg.ProxyGroups {
		for _, n := range g.Proxies {
			if n == "我的HY2" {
				switch g.Name {
				case GroupSelect:
					inSelect = true
				case GroupAuto:
					inAuto = true
				}
			}
			if strings.HasPrefix(n, "🇭🇰 香港 IEPL 01-") {
				dupFound = true
			}
		}
		if g.Name == GroupAuto {
			autoNames = g.Proxies
		}
	}
	if !inSelect || !inAuto {
		t.Fatalf("自定义节点未并入策略组: select=%v auto=%v", inSelect, inAuto)
	}
	if !dupFound {
		t.Fatalf("重名自定义节点未保留（应加后缀）: %v", autoNames)
	}
}

// 确保 geodata 资源文件存在（生产路径由 main.go go:embed 提供）。
func TestGeoDataResourcesExist(t *testing.T) {
	root := "../../resources/geodata"
	for _, f := range []string{"GEOIP.metadb", "GEOSITE.dat"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("resources/geodata/%s 缺失: %v", f, err)
		}
	}
}

// TestGenerateTunEnabled Options.Tun=true 时 tun.enable 必须为 true 且其余
// TUN 模板字段（mixed 栈 / dns-hijack / auto-route）不变，并能被 mihomo 接受。
func TestGenerateTunEnabled(t *testing.T) {
	dir := t.TempDir()
	releaseGeo(t, dir)
	constant.SetHomeDir(dir)
	raw, err := Generate([]byte(sampleSubscription), Options{
		Mode: "rule", Secret: "s", MixedPort: 17895, ExternalController: "127.0.0.1:19099",
		Tun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ParseWithBytes(raw); err != nil {
		t.Fatalf("TUN 配置被 mihomo 拒绝: %v", err)
	}
	var cfg struct {
		TUN struct {
			Enable              bool     `yaml:"enable"`
			Stack               string   `yaml:"stack"`
			DNSHijack           []string `yaml:"dns-hijack"`
			AutoRoute           bool     `yaml:"auto-route"`
			AutoDetectInterface bool     `yaml:"auto-detect-interface"`
		} `yaml:"tun"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.TUN.Enable || cfg.TUN.Stack != "mixed" || !cfg.TUN.AutoRoute ||
		!cfg.TUN.AutoDetectInterface || len(cfg.TUN.DNSHijack) == 0 || cfg.TUN.DNSHijack[0] != "any:53" {
		t.Fatalf("TUN 模板字段不符: %+v", cfg.TUN)
	}
	// 默认关闭
	raw2, err := Generate([]byte(sampleSubscription), Options{Mode: "rule", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg2 struct {
		TUN struct {
			Enable bool `yaml:"enable"`
		} `yaml:"tun"`
	}
	if err := yaml.Unmarshal(raw2, &cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.TUN.Enable {
		t.Fatal("默认 tun.enable 应为 false")
	}
}
