package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"kunpeng/internal/agentapi"
)

// 教程 / 技能 / 接入指令文案。工具表从注册表自动生成，永远与实际能力一致。
// 注意：/v1/guide 与 /v1/skill 免鉴权，文案里绝不能出现令牌。

func (a *App) agentRegistry() *agentapi.Registry {
	a.agent.mu.Lock()
	reg := a.agent.reg
	a.agent.mu.Unlock()
	if reg == nil {
		reg = a.buildAgentTools()
	}
	return reg
}

// agentBrief MCP initialize.instructions：几句话讲清怎么用。
func (a *App) agentBrief() string {
	return "鲲鹏 Clash 是用户 Mac 上的代理客户端（mihomo 内核）。先调 get_status 了解现状；" +
		"用户说上不了网先调 check_network；节点/订阅/规则名都支持名称片段模糊匹配。" +
		"标记为影响较大的工具（disconnect、remove_subscription、set_tun）调用前先征得用户同意。完整教程：GET " + a.agentBaseURL() + "/v1/guide"
}

// agentPrompt 一键复制发给智能体的接入指令（含令牌）。
func (a *App) agentPrompt(info AgentInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "请帮我接入并管理我电脑上的代理客户端「鲲鹏 Clash」。它在本机开放了智能体接口，你可以直接调用，无需我手动操作界面。\n\n")
	fmt.Fprintf(&b, "【接入信息】（仅本机 127.0.0.1 可访问）\n")
	fmt.Fprintf(&b, "- HTTP API：%s   请求头：Authorization: Bearer %s\n", info.BaseURL, info.Token)
	fmt.Fprintf(&b, "- MCP（Streamable HTTP）：%s   （同一请求头）\n", info.MCPURL)
	fmt.Fprintf(&b, "- MCP（stdio）：命令 %s  参数 %s   （自动读取令牌，无需配置）\n", shellQuote(info.CLIPath), strings.Join(a.mcpArgs(), " "))
	fmt.Fprintf(&b, "- 命令行：%s <工具名> key=value\n\n", a.ctlCmd())
	fmt.Fprintf(&b, "【请按顺序做】\n")
	fmt.Fprintf(&b, "1. 阅读完整教程（工具清单、参数、场景示例都在里面）：curl -s %s/v1/guide\n", info.BaseURL)
	fmt.Fprintf(&b, "2. 如果你支持 MCP，把下面配置加入你的 MCP 设置（推荐 stdio，应用重启/端口变化都不受影响）：\n%s\n", info.MCPConfig)
	fmt.Fprintf(&b, "   不支持 MCP 就用 HTTP：POST %s/v1/call/<工具名>，请求体是 JSON 参数。\n", info.BaseURL)
	fmt.Fprintf(&b, "3. 调用 get_status 确认连通，用一两句话告诉我当前代理状态。\n")
	fmt.Fprintf(&b, "4. 如果你能保存技能/记忆，把教程存为技能（技能文件：curl -s %s/v1/skill），以后我说「切节点」「加订阅」「让某网站走美国节点」时直接用。\n\n", info.BaseURL)
	fmt.Fprintf(&b, "注意：断开代理、删除订阅、开虚拟网卡这类影响大的操作先问我。令牌等同密码，不要外传。")
	return b.String()
}

func (a *App) agentGuide() string {
	base := a.agentBaseURL()
	bin := shellQuote(cliPath())
	reg := a.agentRegistry()
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# 鲲鹏 Clash 智能体接入教程\n\n")
	w("> 写给 AI 智能体看的。读完你就能替用户完成代理客户端的全部配置与排障。\n\n")
	w("鲲鹏 Clash 是用户 Mac 上的代理客户端（内嵌 mihomo/Clash Meta 内核）。它在本机开放了一个智能体网关，")
	w("同一套 %d 个工具可经 **MCP**、**HTTP**、**命令行** 三种方式调用，效果完全相同，选你最方便的一种。\n\n", len(reg.Tools()))

	w("## 1. 接入\n\n")
	w("网关只监听本机：`%s`。除 `/v1/guide`、`/v1/skill`、`/v1/ping` 外，所有接口都要带令牌：\n\n", base)
	w("```\nAuthorization: Bearer <令牌>\n```\n\n")
	w("令牌在用户发给你的接入指令里；也可以从 `%s` 的 `token` 字段读取（应用运行时自动写入，仅当前用户可读）。\n\n", filepath.Join(a.dataDir, agentapi.EndpointFile))

	w("### 方式 A：MCP stdio（推荐，零配置令牌）\n\n")
	args := a.mcpArgs()
	quoted := make([]string, len(args))
	for i, s := range args {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	w("```json\n%s\n```\n\n", a.mcpStdioConfig())
	w("它会自动读取 agent.json 里的地址与令牌，应用重启、端口变化、令牌重置都无需改配置。\n\n")
	w("- Claude Code：`claude mcp add kunpeng-clash -- %s %s`\n", bin, strings.Join(args, " "))
	w("- Cursor / Claude Desktop / Cherry Studio 等：把上面 JSON 合并进它们的 MCP 配置文件。\n")
	w("- Codex CLI（~/.codex/config.toml）：\n\n```toml\n[mcp_servers.kunpeng-clash]\ncommand = %q\nargs = [%s]\n```\n\n", cliPath(), strings.Join(quoted, ", "))

	w("### 方式 B：MCP Streamable HTTP\n\n")
	w("```json\n{\n  \"mcpServers\": {\n    \"kunpeng-clash\": {\n      \"type\": \"http\",\n      \"url\": \"%s/mcp\",\n      \"headers\": { \"Authorization\": \"Bearer <令牌>\" }\n    }\n  }\n}\n```\n\n", base)
	w("Claude Code：`claude mcp add --transport http kunpeng-clash %s/mcp --header \"Authorization: Bearer <令牌>\"`\n\n", base)

	w("### 方式 C：HTTP API（任何能发请求的智能体）\n\n")
	w("```sh\n# 列出全部工具（含 JSON Schema）\ncurl -s %s/v1/tools -H 'Authorization: Bearer <令牌>'\n\n", base)
	w("# 调用工具：POST /v1/call/<工具名>，请求体为 JSON 参数（无参数传 {}）\n")
	w("curl -s %s/v1/call/select_node -H 'Authorization: Bearer <令牌>' -d '{\"node\":\"香港\"}'\n```\n\n", base)
	w("返回统一为 `{\"ok\":true,\"result\":…}` 或 `{\"ok\":false,\"error\":\"人话错误 + 下一步建议\"}`。\n\n")

	w("### 方式 D：命令行\n\n")
	ctl := a.ctlCmd()
	w("```sh\n%s get_status\n%s select_node node=香港\n%s set_route_rule name=AI domains=chatgpt.com,claude.ai nodeKeyword=美国\n%s tools        # 列出全部工具\n%s guide        # 打印本教程\n```\n\n", ctl, ctl, ctl, ctl, ctl)
	w("数组参数用逗号分隔；也可传 JSON：`ctl set_port '{\"port\":7891}'`。退出码 0 成功 / 1 工具报错 / 2 应用未运行。\n\n")

	w("## 2. 先懂这几个概念\n\n")
	w("- **订阅**：机场给的链接，内含一批节点。同一时间只有一个订阅处于「激活」状态。\n")
	w("- **🚀 节点选择**：主出口策略组，除了分流和直连之外的流量都走它当前选中的节点。选中「♻️ 自动选择」= 自动测速用最快节点（推荐）。\n")
	w("- **分流规则**：让一个或一组域名**只**走指定的一组节点（如 ChatGPT/Claude 只走美国节点）。挂在订阅下，切换订阅规则跟着切换。\n")
	w("- **直连白名单**：这些域名不走代理。\n")
	w("- **出站模式**：rule 规则分流（推荐，分流与白名单只在此模式生效）/ global 全部代理 / direct 全部直连。\n")
	w("- **系统代理**：浏览器等应用是否走本代理；**虚拟网卡 TUN**：接管所有流量（含命令行），开启需用户输入管理员密码。\n")
	w("- **混合端口**：默认 7890，HTTP 与 SOCKS5 共用。命令行程序可设 `https_proxy=http://127.0.0.1:<端口>`。\n\n")

	w("## 3. 常见任务怎么做\n\n")
	recipes := [][2]string{
		{"看现在什么状态", "`get_status`（summary 字段就是一句话结论）"},
		{"上不了网 / 很慢", "`check_network` → 按返回的 `next` 建议做（通常是 `use_fastest_node` 或 `select_node node=auto`，仍不行 `update_subscription`，再不行 `restart_core`）"},
		{"添加订阅链接", "`add_subscription url=…`（没有激活订阅时会自动启用并连接）"},
		{"用户扔来一段不知道是什么的链接/文本", "`smart_import text=…`（自动识别订阅/节点/base64/clash:// 链接）"},
		{"二维码导入", "`import_qrcode path=/path/to/qr.png`（或 base64=…）"},
		{"添加单个节点", "`add_nodes links=vless://…`"},
		{"切到香港节点", "`select_node node=香港`（多个匹配时自动测速选最快）"},
		{"恢复自动选最快", "`select_node node=auto`"},
		{"让 ChatGPT / Claude 走美国节点", "`set_route_rule name=AI domains=chatgpt.com,openai.com,claude.ai,anthropic.com nodeKeyword=美国,US auto=true`"},
		{"往已有规则加一个域名", "`add_rule_domains name=AI domains=gemini.google.com`"},
		{"某网站走直连", "`add_direct_domains domains=example.com`"},
		{"某网站为什么没走代理 / 打不开", "`which_route domain=example.com`，再 `list_connections keyword=example.com` 看真实链路"},
		{"改端口", "`set_port port=7891`（系统代理自动跟随；端口被占会提示换一个）"},
		{"全局代理 / 恢复规则", "`set_mode mode=global` / `set_mode mode=rule`"},
		{"切换订阅", "`list_subscriptions` → `activate_subscription subscription=<名称或ID>`"},
		{"更新订阅", "`update_subscription`（当前订阅）或 `update_subscription subscription=all`"},
	}
	w("| 用户想要 | 你调用 |\n|---|---|\n")
	for _, r := range recipes {
		w("| %s | %s |\n", r[0], r[1])
	}
	w("\n")

	w("## 4. 全部工具\n\n")
	w("参数里「订阅 / 节点 / 策略组 / 规则名」都支持**名称片段**模糊匹配；匹配不到时错误信息会列出可选项，照着重试即可。\n")
	w("⚠️ = 影响较大（可能断网、删数据或弹出系统密码框），调用前先征得用户同意。✎ = 会修改配置（立即生效，无需重启）。\n\n")
	tools := reg.Tools()
	for _, g := range sortedGroups(tools) {
		w("### %s\n\n", g)
		for _, t := range tools {
			if t.Group != g {
				continue
			}
			mark := ""
			if t.Danger {
				mark = " ⚠️"
			} else if t.Mutates {
				mark = " ✎"
			}
			w("**`%s`** — %s%s\n\n%s\n\n", t.Name, t.Title, mark, t.Desc)
			if len(t.Params) > 0 {
				for _, p := range t.Params {
					req := "可选"
					if p.Required {
						req = "**必填**"
					}
					enum := ""
					if len(p.Enum) > 0 {
						enum = "，取值 " + strings.Join(p.Enum, " / ")
					}
					typ := p.Type
					if typ == "array" {
						typ = "string[]"
					}
					w("- `%s` (%s, %s) %s%s\n", p.Name, typ, req, p.Desc, enum)
				}
				w("\n")
			}
		}
	}

	w("## 5. 行为准则\n\n")
	w("1. 先查后改：改配置前先 `get_status`，改完再查一次确认，用一两句人话向用户汇报结果。\n")
	w("2. 所有修改立即生效、自动持久化，不需要重启应用或内核。\n")
	w("3. 遇到错误先读错误信息——它会告诉你缺什么、可选项有哪些、下一步该调什么。\n")
	w("4. 用户界面会实时提示「智能体刚刚做了什么」，不必担心用户不知情；但 ⚠️ 操作必须先问。\n")
	w("5. 应用未运行时所有接口不可用（连接被拒绝 / CLI 退出码 2）：请用户打开应用（`open -a KunpengClash`）。\n")
	w("6. 令牌等同密码，不要写进公开文件或发给第三方。\n")
	return b.String()
}

func (a *App) agentSkill() string {
	return "---\nname: kunpeng-clash\n" +
		"description: 管理用户 Mac 上的代理客户端「鲲鹏 Clash」——查看网络/代理状态、网络体检、添加订阅（链接/二维码/节点分享链接）、切换节点、" +
		"设置分流规则（指定域名走指定节点组）、直连白名单、修改端口与出站模式。用户提到代理、翻墙、VPN、节点、订阅、机场、上不了网、某网站打不开时使用。\n---\n\n" +
		a.agentGuide()
}
