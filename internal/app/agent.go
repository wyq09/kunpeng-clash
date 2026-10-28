package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"kunpeng/internal/agentapi"
	"kunpeng/internal/configgen"
)

// ---------------------------------------------------------------------------
// 智能体接入网关（DESIGN §5 AgentService）：本机 127.0.0.1 HTTP + MCP + CLI，
// 令牌鉴权；工具清单见 agent_tools.go，教程文案见 agent_guide.go。
// ---------------------------------------------------------------------------

// AppVersion 应用版本（main 在启动时注入，教程与 MCP serverInfo 用）。
var AppVersion = "dev"

// agentPorts 网关候选端口：优先上次成功的端口（保持 MCP HTTP 配置长期有效）。
var agentPorts = []int{9096, 9099, 9100, 9101, 9102, 9103, 9104, 9105, 9106, 9107, 9108}

// AgentTool 能力清单条目（UI 展示用）。
type AgentTool struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Group       string `json:"group"`
	Description string `json:"description"`
	Mutates     bool   `json:"mutates"`
	Danger      bool   `json:"danger"`
}

// AgentCall 一次智能体调用记录。
type AgentCall struct {
	Tool       string `json:"tool"`
	Title      string `json:"title"`
	Source     string `json:"source"` // mcp / mcp-stdio / http / cli
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	At         string `json:"at"`
	DurationMs int    `json:"durationMs"`
	Mutates    bool   `json:"mutates"`
}

// AgentInfo 智能体页所需的全部信息（接入地址、令牌、可直接复制的配置）。
type AgentInfo struct {
	Enabled       bool        `json:"enabled"`
	Running       bool        `json:"running"`
	BaseURL       string      `json:"baseUrl"`
	MCPURL        string      `json:"mcpUrl"`
	Token         string      `json:"token"`
	CLIPath       string      `json:"cliPath"`
	Prompt        string      `json:"prompt"`        // 一键复制发给智能体的接入指令
	MCPConfig     string      `json:"mcpConfig"`     // stdio 方式 MCP 配置 JSON
	MCPHTTPConfig string      `json:"mcpHttpConfig"` // HTTP 方式 MCP 配置 JSON
	CLIExample    string      `json:"cliExample"`    // 命令行示例
	HTTPExample   string      `json:"httpExample"`   // curl 示例
	Tools         []AgentTool `json:"tools"`
	Activity      []AgentCall `json:"activity"`      // 最近调用，新的在前
	Error         string      `json:"error"`         // 网关启动失败原因
	Port          int         `json:"port"`          // 实际监听端口（未运行时为将要使用的端口）
	PreferredPort int         `json:"preferredPort"` // 设定端口（自定义或默认 9096）
	PortCustom    bool        `json:"portCustom"`    // 是否为用户自定义端口
	PortWarning   string      `json:"portWarning"`   // 设定端口被占用、临时改用其他端口时的提示
}

type agentRuntime struct {
	mu       sync.Mutex
	reg      *agentapi.Registry
	srv      *agentapi.Server
	err      string
	activity []AgentCall
}

// startAgent 按 state 启动网关（已运行则幂等）。
func (a *App) startAgent() {
	if a.startAgentLocked() {
		a.writeAgentEndpoint()
	}
}

// startAgentLocked 启动监听；返回是否新启动（发现文件由调用方在释放锁后写入）。
func (a *App) startAgentLocked() bool {
	a.agent.mu.Lock()
	defer a.agent.mu.Unlock()
	if a.agent.srv != nil {
		return false
	}
	if a.agent.reg == nil {
		a.agent.reg = a.buildAgentTools()
	}
	a.mu.Lock()
	ports := agentPorts
	if p := a.state.AgentPort; p > 0 {
		ports = append([]int{p}, agentPorts...)
	}
	a.mu.Unlock()
	srv, err := agentapi.Start(a.agentConfig(a.agent.reg), ports)
	if err != nil {
		a.agent.err = err.Error()
		log.Printf("[kunpeng][agent] %v", err)
		return false
	}
	a.agent.srv = srv
	a.agent.err = ""
	port := portOf(srv.Addr())
	a.mu.Lock()
	changed := a.state.AgentPort != port && !a.state.AgentPortFixed
	if changed {
		a.state.AgentPort = port
	}
	a.mu.Unlock()
	if changed {
		_ = a.saveState()
	}
	log.Printf("[kunpeng][agent] 智能体网关已启动: http://%s", srv.Addr())
	return true
}

func (a *App) agentConfig(reg *agentapi.Registry) agentapi.Config {
	return agentapi.Config{
		Registry: reg,
		Name:     "鲲鹏 Clash",
		Version:  AppVersion,
		Token:    a.agentToken,
		Guide:    a.agentGuide,
		Skill:    a.agentSkill,
		Brief:    a.agentBrief,
		OnCall:   a.recordAgentCall,
	}
}

func (a *App) stopAgent() {
	a.agent.mu.Lock()
	defer a.agent.mu.Unlock()
	if a.agent.srv == nil {
		return
	}
	a.agent.srv.Close()
	a.agent.srv = nil
	agentapi.RemoveEndpoint(a.dataDir)
}

func (a *App) writeAgentEndpoint() {
	base := a.agentBaseURL()
	if base == "" {
		return
	}
	if err := agentapi.WriteEndpoint(a.dataDir, agentapi.Endpoint{
		BaseURL: base, MCPURL: base + "/mcp", Token: a.agentToken(), PID: os.Getpid(), Version: AppVersion,
	}); err != nil {
		log.Printf("[kunpeng][agent] 写入 agent.json 失败: %v", err)
	}
}

func (a *App) agentToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.AgentToken
}

// agentBaseURL 网关地址；未运行时按上次端口给出（便于 UI 展示将要使用的地址）。
func (a *App) agentBaseURL() string {
	a.agent.mu.Lock()
	srv := a.agent.srv
	a.agent.mu.Unlock()
	if srv != nil {
		return "http://" + srv.Addr()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.state.AgentPort
	if p == 0 {
		p = agentPorts[0]
	}
	return fmt.Sprintf("http://127.0.0.1:%d", p)
}

func (a *App) recordAgentCall(r agentapi.CallRecord) {
	c := AgentCall{
		Tool: r.Tool, Title: r.Title, Source: r.Source, OK: r.OK, Error: r.Error,
		At: r.At, DurationMs: int(r.DurationMs), Mutates: r.Mutates,
	}
	a.agent.mu.Lock()
	a.agent.activity = append([]AgentCall{c}, a.agent.activity...)
	if len(a.agent.activity) > 60 {
		a.agent.activity = a.agent.activity[:60]
	}
	a.agent.mu.Unlock()
	if a.opt.EmitAgent != nil {
		a.opt.EmitAgent(c)
	}
}

// ensureAgentToken 首次使用时生成令牌（state 已持久化则沿用）。
func (a *App) ensureAgentTokenLocked() error {
	if a.state.AgentToken != "" {
		return nil
	}
	s, err := configgen.RandomSecret()
	if err != nil {
		return err
	}
	a.state.AgentToken = "kp_" + s
	return nil
}

// ---------------------------------------------------------------------------
// AgentService 实现
// ---------------------------------------------------------------------------

// AgentInfoNow 返回智能体页所需信息。
func (a *App) AgentInfoNow() AgentInfo {
	a.mu.Lock()
	enabled := !a.state.AgentDisabled
	token := a.state.AgentToken
	a.mu.Unlock()
	a.agent.mu.Lock()
	running := a.agent.srv != nil
	errMsg := a.agent.err
	reg := a.agent.reg
	activity := append([]AgentCall(nil), a.agent.activity...)
	a.agent.mu.Unlock()
	if reg == nil {
		reg = a.buildAgentTools()
	}
	base := a.agentBaseURL()
	bin := cliPath()
	info := AgentInfo{
		Enabled: enabled, Running: running, BaseURL: base, MCPURL: base + "/mcp", Token: token,
		CLIPath: bin, Error: errMsg, Activity: activity,
	}
	info.Port = portOf(strings.TrimPrefix(base, "http://"))
	a.mu.Lock()
	info.PreferredPort, info.PortCustom = a.state.AgentPort, a.state.AgentPortFixed
	a.mu.Unlock()
	if info.PreferredPort == 0 {
		info.PreferredPort = agentPorts[0]
	}
	if running && info.PortCustom && info.Port != info.PreferredPort {
		info.PortWarning = fmt.Sprintf("端口 %d 被其他程序占用，已临时改用 %d；用 HTTP 方式接入的智能体需要改地址，或换一个空闲端口", info.PreferredPort, info.Port)
	}
	info.MCPConfig = a.mcpStdioConfig()
	info.MCPHTTPConfig = mustJSON(map[string]any{
		"mcpServers": map[string]any{
			"kunpeng-clash": map[string]any{
				"type": "http", "url": info.MCPURL,
				"headers": map[string]string{"Authorization": "Bearer " + token},
			},
		},
	})
	ctl := a.ctlCmd()
	info.CLIExample = fmt.Sprintf("%s get_status\n%s select_node node=香港\n%s set_route_rule name=AI domains=chatgpt.com,claude.ai nodeKeyword=美国\n%s tools", ctl, ctl, ctl, ctl)
	info.HTTPExample = fmt.Sprintf("curl -s %s/v1/call/get_status \\\n  -H 'Authorization: Bearer %s' -d '{}'", base, token)
	info.Prompt = a.agentPrompt(info)
	for _, t := range reg.Tools() {
		info.Tools = append(info.Tools, AgentTool{Name: t.Name, Title: t.Title, Group: t.Group, Description: t.Desc, Mutates: t.Mutates, Danger: t.Danger})
	}
	return info
}

// AgentSetEnabled 开关智能体接入（持久化）。
func (a *App) AgentSetEnabled(on bool) error {
	a.mu.Lock()
	a.state.AgentDisabled = !on
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		return err
	}
	if on {
		a.startAgent()
		a.agent.mu.Lock()
		e := a.agent.err
		a.agent.mu.Unlock()
		if e != "" {
			return fmt.Errorf("%s", e)
		}
		return nil
	}
	a.stopAgent()
	return nil
}

// AgentResetToken 重新生成令牌（旧令牌立即失效）。
func (a *App) AgentResetToken() (AgentInfo, error) {
	a.mu.Lock()
	a.state.AgentToken = ""
	err := a.ensureAgentTokenLocked()
	a.mu.Unlock()
	if err != nil {
		return AgentInfo{}, err
	}
	if err := a.saveState(); err != nil {
		return AgentInfo{}, err
	}
	a.agent.mu.Lock()
	running := a.agent.srv != nil
	a.agent.mu.Unlock()
	if running {
		a.writeAgentEndpoint()
	}
	return a.AgentInfoNow(), nil
}

// AgentSetPort 自定义网关端口：运行中则先在新端口起好服务再关旧的，失败时保持原状。
func (a *App) AgentSetPort(port int) (AgentInfo, error) {
	if port < 1024 || port > 65535 {
		return AgentInfo{}, fmt.Errorf("端口需在 1024–65535 之间")
	}
	if port == a.MixedPort() {
		return AgentInfo{}, fmt.Errorf("端口 %d 是代理端口，换一个", port)
	}
	a.agent.mu.Lock()
	old := a.agent.srv
	reg := a.agent.reg
	a.agent.mu.Unlock()
	if old != nil && portOf(old.Addr()) == port {
		a.mu.Lock()
		a.state.AgentPort, a.state.AgentPortFixed = port, true
		a.mu.Unlock()
		return a.AgentInfoNow(), a.saveState()
	}
	if old == nil {
		if err := portAvailable(port, 0); err != nil {
			return AgentInfo{}, err
		}
	} else {
		srv, err := agentapi.Start(a.agentConfig(reg), []int{port})
		if err != nil {
			return AgentInfo{}, fmt.Errorf("端口 %d 已被其他程序占用，换一个（如 %d）", port, port+1)
		}
		a.agent.mu.Lock()
		a.agent.srv = srv
		a.agent.err = ""
		a.agent.mu.Unlock()
		old.Close()
	}
	a.mu.Lock()
	a.state.AgentPort, a.state.AgentPortFixed = port, true
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		return AgentInfo{}, err
	}
	if old != nil {
		a.writeAgentEndpoint()
		log.Printf("[kunpeng][agent] 网关端口已改为 %d", port)
	}
	return a.AgentInfoNow(), nil
}

// AgentGuideText 完整教程 Markdown。
func (a *App) AgentGuideText() string { return a.agentGuide() }

// AgentExportSkill 把 SKILL.md 写到 <数据目录>/agent-skill/kunpeng-clash/ 并在 Finder 中定位。
func (a *App) AgentExportSkill() (string, error) {
	dir := filepath.Join(a.dataDir, "agent-skill", "kunpeng-clash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte(a.agentSkill()), 0o644); err != nil {
		return "", err
	}
	if !a.opt.Headless {
		_ = exec.Command("open", "-R", p).Start()
	}
	return p, nil
}

// AgentClearActivity 清空调用记录。
func (a *App) AgentClearActivity() {
	a.agent.mu.Lock()
	a.agent.activity = nil
	a.agent.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// mcpArgs stdio 桥启动参数；非默认数据目录（测试隔离）时带上 --data-dir。
func (a *App) mcpArgs() []string {
	if a.dataDir == defaultDataDir() {
		return []string{"mcp"}
	}
	return []string{"mcp", "--data-dir", a.dataDir}
}

// ctlCmd 命令行调用前缀（含必要的 --data-dir）。
func (a *App) ctlCmd() string {
	s := shellQuote(cliPath()) + " ctl"
	if a.dataDir != defaultDataDir() {
		s += " --data-dir " + shellQuote(a.dataDir)
	}
	return s
}

func (a *App) mcpStdioConfig() string {
	return mustJSON(map[string]any{
		"mcpServers": map[string]any{
			"kunpeng-clash": map[string]any{"command": cliPath(), "args": a.mcpArgs()},
		},
	})
}

func cliPath() string {
	p, err := os.Executable()
	if err != nil {
		return "/Applications/KunpengClash.app/Contents/MacOS/KunpengClash"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func portOf(addr string) int {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0
	}
	var p int
	_, _ = fmt.Sscanf(addr[i+1:], "%d", &p)
	return p
}

func mustJSON(v any) string {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimRight(sb.String(), "\n")
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sortedGroups 工具分组按首次出现顺序。
func sortedGroups(tools []agentapi.Tool) []string {
	seen := map[string]int{}
	for i, t := range tools {
		if _, ok := seen[t.Group]; !ok {
			seen[t.Group] = i
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return seen[out[i]] < seen[out[j]] })
	return out
}
