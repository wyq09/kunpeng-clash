// Package configgen 按 DESIGN.md §6 从订阅原文生成 mihomo 运行配置 runtime.yaml。
//
// 全程使用 yaml.v3 结构化序列化（mihomo 依赖树中的 go.yaml.in/yaml/v3），
// 不做字符串拼接，节点名中的特殊字符（emoji、冒号、引号）安全。
package configgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"go.yaml.in/yaml/v3"
)

// 组名与规则固定常量（DESIGN.md §6）。
// HealthURL 用 http 版：实测早高峰节点对 https://www.gstatic.com 的
// TLS 握手成功率明显低于 http 版（26 vs 22 通过），http 204 更能反映真实连通性。
const (
	GroupSelect = "🚀 节点选择"
	GroupAuto   = "♻️ 自动选择"
	HealthURL   = "http://www.gstatic.com/generate_204"
)

// WhitelistLimit 白名单条数上限（防止误粘贴超大列表拖垮内核规则表）。
const WhitelistLimit = 200

// 分流规则规模上限：组数 / 每组域名数 / 每组节点数。
const (
	MaxRouteGroups   = 20
	RouteDomainLimit = 200
	RouteNodeLimit   = 100
)

// DefaultBypassDomains 系统代理默认排除域名（DESIGN.md §8）。
var DefaultBypassDomains = []string{
	"127.0.0.1", "localhost", "192.168.0.0/16", "10.0.0.0/8",
	"172.16.0.0/12", "*.local", "*.cn", "*.lan",
}

// RouteGroup 一条分流规则：Domains 里的域名/IP 段强制经由 Nodes 中的节点访问。
// Auto=true 生成 url-test 组（组内自动选最快），false 生成 select 组（手动挑）。
type RouteGroup struct {
	Name    string
	Domains []string
	Nodes   []string
	Auto    bool
}

// Options 生成参数。
type Options struct {
	MixedPort          int              // mixed-port（默认 7890）
	Mode               string           // rule/global/direct，默认 rule
	ExternalController string           // 默认 127.0.0.1:9097
	Secret             string           // external-controller 密钥
	IPv6               bool             // 默认 false
	Tun                bool             // 虚拟网卡（TUN 特权子进程模式置 true）
	Whitelist          []string         // 直连白名单（规则模式插入在私有网段之后）
	CustomProxies      []map[string]any // 自定义节点（追加到订阅节点之后并入策略组）
	RouteGroups        []RouteGroup     // 分流规则（域名 → 指定节点组，仅规则模式生效）
}

type profileCfg struct {
	StoreSelected bool `yaml:"store-selected"`
	StoreFakeIP   bool `yaml:"store-fake-ip"`
}

type dnsCfg struct {
	Enable                bool     `yaml:"enable"`
	IPv6                  bool     `yaml:"ipv6"`
	EnhancedMode          string   `yaml:"enhanced-mode"`
	FakeIPRange           string   `yaml:"fake-ip-range"`
	FakeIPFilter          []string `yaml:"fake-ip-filter"`
	DefaultNameserver     []string `yaml:"default-nameserver"`
	ProxyServerNameserver []string `yaml:"proxy-server-nameserver"`
	Nameserver            []string `yaml:"nameserver"`
}

type tunCfg struct {
	Enable              bool     `yaml:"enable"`
	Stack               string   `yaml:"stack"`
	DNSHijack           []string `yaml:"dns-hijack"`
	AutoRoute           bool     `yaml:"auto-route"`
	AutoDetectInterface bool     `yaml:"auto-detect-interface"`
}

type proxyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
	Proxies   []string `yaml:"proxies"`
}

// RuntimeConfig 与 DESIGN.md §6 的 runtime.yaml 一一对应。
type RuntimeConfig struct {
	MixedPort          int              `yaml:"mixed-port"`
	AllowLan           bool             `yaml:"allow-lan"`
	BindAddress        string           `yaml:"bind-address"`
	Mode               string           `yaml:"mode"`
	LogLevel           string           `yaml:"log-level"`
	ExternalController string           `yaml:"external-controller"`
	Secret             string           `yaml:"secret"`
	IPv6               bool             `yaml:"ipv6"`
	Profile            profileCfg       `yaml:"profile"`
	DNS                dnsCfg           `yaml:"dns"`
	TUN                tunCfg           `yaml:"tun"`
	Proxies            []map[string]any `yaml:"proxies,omitempty"`
	ProxyGroups        []proxyGroup     `yaml:"proxy-groups"`
	Rules              []string         `yaml:"rules"`
}

// NewDefault 返回按 DESIGN.md §6 填好的骨架（proxies/groups 留空）。
func NewDefault(opt Options) *RuntimeConfig {
	if opt.MixedPort <= 0 {
		opt.MixedPort = 7890
	}
	if opt.Mode == "" {
		opt.Mode = "rule"
	}
	if opt.ExternalController == "" {
		opt.ExternalController = "127.0.0.1:9097"
	}
	cfg := &RuntimeConfig{
		MixedPort:          opt.MixedPort,
		AllowLan:           true, // DESIGN §6：固定开启（局域网共享）
		BindAddress:        "*",
		Mode:               opt.Mode,
		LogLevel:           "info",
		ExternalController: opt.ExternalController,
		Secret:             opt.Secret,
		IPv6:               opt.IPv6,
		Profile:            profileCfg{StoreSelected: true, StoreFakeIP: true},
		DNS: dnsCfg{
			Enable:       true,
			IPv6:         false,
			EnhancedMode: "fake-ip",
			FakeIPRange:  "198.18.0.1/16",
			FakeIPFilter: []string{"*.lan", "*.local", "+.msftconnecttest.com", "+.msftncsi.com"},
			// bootstrap：解析 DoH 服务器域名用
			DefaultNameserver: []string{"223.5.5.5", "119.29.29.29"},
			// 代理节点服务器域名专用（直连国内 DoH，绝不能走国外 fallback——直连必被墙）
			ProxyServerNameserver: []string{"https://dns.alidns.com/dns-query", "https://doh.pub/dns-query"},
			// 本地解析只服务 DIRECT 流量（按规则国外域名全走代理、fake-ip 下不本地解析），
			// 因此不需要国外 fallback；保留 fallback 反而会在墙内拖死所有解析。
			Nameserver: []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"},
		},
		// TUN：GUI 内嵌内核无 root 时不可用，置位时由特权子进程承载内核；
		// 模板其余字段沿用：mixed 栈、DNS 劫持 any:53、自动路由与接口探测。
		TUN: tunCfg{Enable: opt.Tun, Stack: "mixed", DNSHijack: []string{"any:53"}, AutoRoute: true, AutoDetectInterface: true},
	}
	cfg.Rules = insertWhitelistRules(RulesWithMode(opt.Mode), WhitelistToRules(opt.Whitelist))
	return cfg
}

// privateCIDRCount 规则模式前缀里私有/环回网段直连规则的条数
// （白名单规则必须插在它们之后、国内分流规则之前）。
const privateCIDRCount = 4

// insertWhitelistRules 把白名单规则插到私有网段规则之后；空列表原样返回。
func insertWhitelistRules(rules, whitelist []string) []string {
	if len(whitelist) == 0 || len(rules) <= privateCIDRCount {
		return rules
	}
	out := make([]string, 0, len(rules)+len(whitelist))
	out = append(out, rules[:privateCIDRCount]...)
	out = append(out, whitelist...)
	out = append(out, rules[privateCIDRCount:]...)
	return out
}

// NormalizeWhitelist 规范化用户输入的白名单：trim、去空、去重、统一小写；
// 超过上限报错；非法 IP 段报错。返回规范化后的列表。
func NormalizeWhitelist(items []string) ([]string, error) {
	return normalizeItemsList(items, WhitelistLimit, "白名单")
}

// normalizeItemsList 域名/IP 段清单的通用规范化（白名单与分流规则共用）。
func normalizeItemsList(items []string, limit int, what string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" || seen[s] {
			continue
		}
		if strings.Contains(s, "/") {
			if _, _, err := net.ParseCIDR(s); err != nil {
				return nil, fmt.Errorf("非法 IP 段 %q（应为如 10.0.0.0/8）", s)
			}
		} else {
			if strings.ContainsAny(s, " \t/@?#") {
				return nil, fmt.Errorf("非法%v项 %q（域名/IP 段，不含协议头与路径）", what, s)
			}
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > limit {
		return nil, fmt.Errorf("%s最多 %d 条（当前 %d 条）", what, limit, len(out))
	}
	return out, nil
}

// NormalizeRouteGroups 校验并规范化分流规则：组数/每组域名数/节点数上限、
// 组名（去空白、非空、不含逗号——规则串按逗号切分、长度≤32）、域名清单规范化。
// 返回规范化后的副本（不改入参）。
func NormalizeRouteGroups(groups []RouteGroup) ([]RouteGroup, error) {
	if len(groups) > MaxRouteGroups {
		return nil, fmt.Errorf("分流规则最多 %d 组（当前 %d 组）", MaxRouteGroups, len(groups))
	}
	seenNames := map[string]bool{}
	out := make([]RouteGroup, 0, len(groups))
	for i, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			return nil, fmt.Errorf("第 %d 组缺少组名", i+1)
		}
		if len([]rune(name)) > 32 {
			return nil, fmt.Errorf("组名 %q 超过 32 个字符", name)
		}
		if strings.Contains(name, ",") || strings.Contains(name, "/") {
			return nil, fmt.Errorf("组名 %q 不能包含逗号或斜杠", name)
		}
		if seenNames[name] {
			return nil, fmt.Errorf("组名重复: %q", name)
		}
		seenNames[name] = true
		domains, err := normalizeItemsList(g.Domains, RouteDomainLimit, "分流域名")
		if err != nil {
			return nil, fmt.Errorf("组 %q: %w", name, err)
		}
		if len(domains) == 0 {
			return nil, fmt.Errorf("组 %q 没有域名", name)
		}
		nodes := make([]string, 0, len(g.Nodes))
		seenNodes := map[string]bool{}
		for _, n := range g.Nodes {
			if n = strings.TrimSpace(n); n != "" && !seenNodes[n] {
				seenNodes[n] = true
				nodes = append(nodes, n)
			}
		}
		if len(nodes) == 0 {
			return nil, fmt.Errorf("组 %q 没有选择节点", name)
		}
		if len(nodes) > RouteNodeLimit {
			return nil, fmt.Errorf("组 %q 节点数超过 %d", name, RouteNodeLimit)
		}
		out = append(out, RouteGroup{Name: name, Domains: domains, Nodes: nodes, Auto: g.Auto})
	}
	return out, nil
}

// reservedGroupNames 生成组名时必须避开的名字（固定组与 mihomo 内建策略）。
var reservedGroupNames = map[string]bool{
	GroupSelect: true, GroupAuto: true,
	"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "GLOBAL": true,
}

// buildRouteGroups 生成分流策略组与前置规则（仅保留真实存在的节点）。
//   - 节点只保留出现在 available 里的：订阅更新后节点改名/消失的条目自动失效，
//     不让 mihomo 因「规则引用不存在的代理」拒绝整份配置；
//   - 整组节点失效（或组名与保留名冲突到无法命名）时跳过该组及其规则；
//   - 组名空/冲突时自动命名「分流组N」，与固定组、节点名、其他分流组去重。
func buildRouteGroups(available []string, groups []RouteGroup) ([]proxyGroup, []string) {
	avail := map[string]bool{}
	for _, n := range available {
		avail[n] = true
	}
	used := map[string]bool{}
	for k := range reservedGroupNames {
		used[k] = true
	}
	for _, n := range available {
		used[n] = true // 组名不得与任何节点名相同（mihomo 按名字索引出站）
	}

	var outGroups []proxyGroup
	var outRules []string
	seenRule := map[string]bool{}
	for i, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			name = fmt.Sprintf("分流组%d", i+1)
		}
		base := name
		for suffix := 2; used[name]; suffix++ {
			name = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[name] = true

		nodes := make([]string, 0, len(g.Nodes))
		seen := map[string]bool{}
		for _, n := range g.Nodes {
			if avail[n] && !seen[n] {
				seen[n] = true
				nodes = append(nodes, n)
			}
		}
		if len(nodes) == 0 {
			continue
		}
		pg := proxyGroup{Name: name, Proxies: nodes}
		if g.Auto {
			pg.Type = "url-test"
			pg.URL = HealthURL
			pg.Interval = 300
			pg.Tolerance = 80
		} else {
			pg.Type = "select"
		}
		outGroups = append(outGroups, pg)
		for _, r := range itemsToRules(g.Domains, name) {
			if seenRule[r] {
				continue
			}
			seenRule[r] = true
			outRules = append(outRules, r)
		}
	}
	return outGroups, outRules
}

// WhitelistToRules 把用户白名单转换为 mihomo 直连规则：
//   - example.com    → DOMAIN-SUFFIX,example.com,DIRECT（含自身与全部子域）
//   - *.example.com / +.example.com → 等价 DOMAIN-SUFFIX,example.com,DIRECT
//   - a.example.com  → DOMAIN,a.example.com,DIRECT（精确匹配）
//   - 1.2.3.0/24     → IP-CIDR,1.2.3.0/24,DIRECT,no-resolve
//
// 空项忽略、输出去重；无法识别的项跳过（NormalizeWhitelist 已在写入侧拦截）。
func WhitelistToRules(domains []string) []string {
	return itemsToRules(domains, "DIRECT")
}

// itemsToRules 把域名/IP 段清单转换为指向 target（DIRECT 或策略组名）的 mihomo 规则。
// 转换规则同 WhitelistToRules；空项忽略、输出去重、非法项跳过。
func itemsToRules(items []string, target string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		var rule string
		switch {
		case strings.Contains(s, "/"):
			if _, _, err := net.ParseCIDR(s); err != nil {
				continue // 非法 CIDR：不生成坏规则导致整份配置被内核拒绝
			}
			rule = "IP-CIDR," + s + "," + target + ",no-resolve"
		case strings.HasPrefix(s, "*."), strings.HasPrefix(s, "+."):
			host := strings.TrimPrefix(strings.TrimPrefix(s, "*."), "+.")
			if host == "" {
				continue
			}
			rule = "DOMAIN-SUFFIX," + host + "," + target
		case strings.Contains(s, "."):
			// 裸域名（example.com，仅 1 个点）按后缀匹配（含自身+子域）；
			// 带子域前缀的（a.example.com，≥2 个点）按精确域名匹配。
			// 多级后缀（如 example.co.uk）想含子域请写 *.example.co.uk。
			if strings.Count(s, ".") == 1 {
				rule = "DOMAIN-SUFFIX," + s + "," + target
			} else {
				rule = "DOMAIN," + s + "," + target
			}
		default:
			// 单标签（如 localhost）：后缀规则可匹配自身。
			rule = "DOMAIN-SUFFIX," + s + "," + target
		}
		if seen[rule] {
			continue
		}
		seen[rule] = true
		out = append(out, rule)
	}
	return out
}

// RulesWithMode 生成规则集；direct 模式下全部直连，global 模式全走选择组，
// rule 模式按 DESIGN.md §6。
func RulesWithMode(mode string) []string {
	switch mode {
	case "direct":
		return []string{"MATCH,DIRECT"}
	case "global":
		return []string{"MATCH," + GroupSelect}
	default:
		return []string{
			// 环回/私有网段必须直连：防止对 127.0.0.1:9097 等本机服务的请求被
			// 扔给节点造成 502/回环（系统代理 bypass 之外的内核层保险丝）。
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
	}
}

// Generate 从订阅原文 + 自定义节点 + 白名单生成完整 runtime.yaml 字节流。
// 订阅原文只需其中的 proxies 字段；订阅与自定义节点全为空时返回错误。
func Generate(subscriptionRaw []byte, opt Options) ([]byte, error) {
	var sub struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(subscriptionRaw, &sub); err != nil {
		return nil, fmt.Errorf("订阅 YAML 解析失败: %w", err)
	}
	if len(sub.Proxies) == 0 && len(opt.CustomProxies) == 0 {
		return nil, fmt.Errorf("订阅中没有 proxies 节点，无法生成配置")
	}
	// 节点名去重保序（重名节点会让 mihomo 拒绝整份配置）。
	// 自定义节点与订阅节点重名时自动加序号后缀，保证两边都保留。
	seen := map[string]bool{}
	proxies := make([]map[string]any, 0, len(sub.Proxies)+len(opt.CustomProxies))
	names := make([]string, 0, len(sub.Proxies)+len(opt.CustomProxies))
	addProxy := func(p map[string]any) {
		n, _ := p["name"].(string)
		if n == "" {
			return
		}
		base := n
		for i := 2; seen[n]; i++ {
			n = fmt.Sprintf("%s-%d", base, i)
		}
		seen[n] = true
		p["name"] = n
		proxies = append(proxies, p)
		names = append(names, n)
	}
	for _, p := range sub.Proxies {
		addProxy(p)
	}
	for _, p := range opt.CustomProxies {
		if p == nil {
			continue
		}
		cp := make(map[string]any, len(p))
		for k, v := range p {
			cp[k] = v
		}
		addProxy(cp)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("订阅与自定义节点均缺少 name 字段，无法生成配置")
	}

	cfg := NewDefault(opt)
	cfg.Proxies = proxies

	// 信息类假节点（机场塞的"官网/剩余流量/到期时间/临时节点"等占位项）
	// 不进任何策略组：小白用户永远不该看到或自动选中它们（proxies 仍保留供高级用户）。
	// 自定义节点是用户手动添加的，全部保留、不过滤。
	realNames := make([]string, 0, len(names))
	for _, n := range names {
		if IsInfoNode(n) {
			continue
		}
		realNames = append(realNames, n)
	}
	if len(realNames) == 0 {
		realNames = names // 极端情况：全是"信息节点"时不过滤，保底可用
	}

	cfg.ProxyGroups = []proxyGroup{
		{
			Name:    GroupSelect,
			Type:    "select",
			Proxies: append([]string{GroupAuto, "DIRECT"}, realNames...),
		},
		{
			Name:      GroupAuto,
			Type:      "url-test",
			URL:       HealthURL,
			Interval:  300,
			Tolerance: 80,
			Proxies:   realNames,
		},
	}

	// 分流规则：域名 → 指定节点组。规则插在私有网段 4 条之后、白名单与国内分流之前
	// ——对这些域名「只能走这一组节点」是最高优先级意图；global/direct 模式下
	// 规则列表没有私有网段前缀，自然不插入（与白名单语义一致，见 DESIGN §6）。
	routeGroups, routeRules := buildRouteGroups(realNames, opt.RouteGroups)
	if len(routeGroups) > 0 {
		cfg.ProxyGroups = append(cfg.ProxyGroups, routeGroups...)
	}
	if len(routeRules) > 0 && len(cfg.Rules) > privateCIDRCount {
		rules := make([]string, 0, len(cfg.Rules)+len(routeRules))
		rules = append(rules, cfg.Rules[:privateCIDRCount]...)
		rules = append(rules, routeRules...)
		rules = append(rules, cfg.Rules[privateCIDRCount:]...)
		cfg.Rules = rules
	}
	return yaml.Marshal(cfg)
}

// IsInfoNode 判断节点名是否为机场信息占位节点（非真实可连节点）。
func IsInfoNode(name string) bool {
	for _, kw := range []string{
		"官网", "剩余", "流量", "到期", "过期", "临时", "套餐", "重置",
		"电报", "频道", "群", "工单", "充值", "订阅", "web", "http",
	} {
		if strings.Contains(strings.ToLower(name), kw) {
			return true
		}
	}
	return false
}

// RandomSecret 生成 48 位十六进制随机密钥。
func RandomSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
