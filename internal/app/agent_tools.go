package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"kunpeng/internal/agentapi"
	"kunpeng/internal/configgen"
	"kunpeng/internal/qrdecode"
	"kunpeng/internal/sysproxy"
)

// 工具分组（教程与 UI 按此顺序展示）。
const (
	grpStatus = "状态与诊断"
	grpSub    = "订阅与导入"
	grpNode   = "节点与切换"
	grpCore   = "连接与端口"
	grpRoute  = "分流规则"
	grpDirect = "直连白名单"
	grpCustom = "自定义节点"
)

// 常用参数定义。
var (
	pSub = agentapi.Param{Name: "subscription", Type: "string", Desc: "订阅 ID 或名称（支持名称片段）；不填 = 当前激活的订阅"}
)

// buildAgentTools 注册全部对智能体开放的能力。
func (a *App) buildAgentTools() *agentapi.Registry {
	r := agentapi.NewRegistry()

	// ---------------- 状态与诊断 ----------------
	r.Add(agentapi.Tool{
		Name: "get_status", Title: "查看整体状态", Group: grpStatus,
		Desc:    "一次返回连接是否可用、出站模式、代理端口、系统代理/虚拟网卡开关、当前订阅、当前出口节点与延迟、实时速率。任何操作前先调它了解现状。",
		Handler: func(agentapi.Args) (any, error) { return a.agentStatus(), nil },
	})
	r.Add(agentapi.Tool{
		Name: "check_network", Title: "网络体检", Group: grpStatus,
		Desc: "并发探测：本机直连网络、代理出口、Google、GitHub、ChatGPT 的连通性与耗时，并查询代理出口公网 IP，给出结论与下一步建议。用户说「上不了网/很卡」时先调它。",
		Params: []agentapi.Param{
			{Name: "url", Type: "string", Desc: "额外探测的网址（经代理），可选"},
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentCheckNetwork(x.Str("url")), nil },
	})
	r.Add(agentapi.Tool{
		Name: "get_traffic", Title: "实时流量", Group: grpStatus,
		Desc:    "当前上传/下载速率（字节/秒与可读文本）、本次累计流量、连接数、内核内存占用。",
		Handler: func(agentapi.Args) (any, error) { return a.agentTraffic(), nil },
	})
	r.Add(agentapi.Tool{
		Name: "list_connections", Title: "查看实时连接", Group: grpStatus,
		Desc: "列出当前活动连接（目标域名、命中规则、经过的节点链、流量、进程），按开始时间倒序。",
		Params: []agentapi.Param{
			{Name: "keyword", Type: "string", Desc: "按域名/IP/进程/规则/节点过滤，可选"},
			{Name: "limit", Type: "integer", Desc: "最多返回条数，默认 30，最大 300"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			limit, err := x.Int("limit", 30)
			if err != nil {
				return nil, err
			}
			return a.agentConnections(x.Str("keyword"), limit), nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "close_connections", Title: "断开连接", Group: grpStatus, Mutates: true,
		Desc: "断开连接让应用重新建连（切换节点后让旧连接立即走新节点）。给 id 断开单条；给 keyword 断开匹配的；都不给则断开全部。",
		Params: []agentapi.Param{
			{Name: "id", Type: "string", Desc: "连接 ID（来自 list_connections）"},
			{Name: "keyword", Type: "string", Desc: "断开域名/进程/节点包含该关键字的全部连接"},
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentCloseConnections(x.Str("id"), x.Str("keyword")) },
	})
	r.Add(agentapi.Tool{
		Name: "which_route", Title: "某域名会怎么走", Group: grpStatus,
		Desc: "解释一个域名按当前配置会走哪里（分流规则组 / 直连白名单 / 国内直连 / 代理主组），并附上该域名正在进行的真实连接链路。排查「某网站为什么打不开/为什么没走代理」用。",
		Params: []agentapi.Param{
			{Name: "domain", Type: "string", Desc: "域名或网址，如 chatgpt.com", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentWhichRoute(x.Str("domain")) },
	})
	r.Add(agentapi.Tool{
		Name: "test_url", Title: "经代理访问网址", Group: grpStatus,
		Desc: "通过当前代理请求一个网址，返回 HTTP 状态码与耗时。",
		Params: []agentapi.Param{
			{Name: "url", Type: "string", Desc: "完整网址，如 https://www.google.com", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			p := probe(a.proxyClient(8*time.Second), "自定义", normalizeURL(x.Str("url")), "代理")
			return p, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "get_logs", Title: "内核日志", Group: grpStatus,
		Desc: "最近的内核日志（最多 500 条缓存）。",
		Params: []agentapi.Param{
			{Name: "limit", Type: "integer", Desc: "返回最近多少条，默认 50"},
			{Name: "level", Type: "string", Desc: "只看某级别", Enum: []string{"info", "warning", "error", "debug"}},
			{Name: "keyword", Type: "string", Desc: "包含关键字"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			limit, err := x.Int("limit", 50)
			if err != nil {
				return nil, err
			}
			return a.agentLogs(limit, x.Str("level"), x.Str("keyword")), nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "get_runtime_rules", Title: "生效中的规则表", Group: grpStatus,
		Desc: "内核当前实际生效的路由规则（按顺序匹配）。",
		Params: []agentapi.Param{
			{Name: "keyword", Type: "string", Desc: "过滤关键字，可选"},
			{Name: "limit", Type: "integer", Desc: "最多返回条数，默认 100"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			limit, err := x.Int("limit", 100)
			if err != nil {
				return nil, err
			}
			return a.agentRuntimeRules(x.Str("keyword"), limit)
		},
	})

	// ---------------- 订阅与导入 ----------------
	r.Add(agentapi.Tool{
		Name: "list_subscriptions", Title: "订阅列表", Group: grpSub,
		Desc:    "全部订阅：ID、名称、地址、是否激活、节点数、已用/总流量、到期时间、更新时间、自动更新设置。",
		Handler: func(agentapi.Args) (any, error) { return a.agentProfiles(), nil },
	})
	r.Add(agentapi.Tool{
		Name: "smart_import", Title: "智能导入（链接/文本）", Group: grpSub, Mutates: true,
		Desc: "把用户给的任意内容直接丢进来：订阅链接、clash://install-config 链接、节点分享链接（vless/vmess/ss/trojan/hysteria2）、多行混合、或整段 base64。自动识别：订阅链接 → 新增订阅；节点链接 → 加为自定义节点。",
		Params: []agentapi.Param{
			{Name: "text", Type: "string", Desc: "要导入的内容（可多行）", Required: true},
			{Name: "name", Type: "string", Desc: "订阅名称（只导入一个订阅时生效），可选"},
			{Name: "activate", Type: "boolean", Desc: "导入后是否立即启用第一个新订阅；默认：当前没有激活订阅时启用"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			act, err := a.activateDefault(x)
			if err != nil {
				return nil, err
			}
			return a.agentSmartImport(x.Str("text"), x.Str("name"), act)
		},
	})
	r.Add(agentapi.Tool{
		Name: "add_subscription", Title: "添加订阅链接", Group: grpSub, Mutates: true,
		Desc: "拉取订阅链接并新增订阅（同链接重复添加 = 刷新）。",
		Params: []agentapi.Param{
			{Name: "url", Type: "string", Desc: "订阅链接 http(s)://…", Required: true},
			{Name: "name", Type: "string", Desc: "名称，不填自动取机场名"},
			{Name: "activate", Type: "boolean", Desc: "是否立即启用；默认：当前没有激活订阅时启用"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			act, err := a.activateDefault(x)
			if err != nil {
				return nil, err
			}
			u := x.Str("url")
			if !isHTTPURL(u) {
				return nil, errors.New("url 需要以 http:// 或 https:// 开头；节点分享链接请用 add_nodes 或 smart_import")
			}
			return a.agentSmartImport(u, x.Str("name"), act)
		},
	})
	r.Add(agentapi.Tool{
		Name: "import_qrcode", Title: "二维码导入", Group: grpSub, Mutates: true,
		Desc: "识别二维码图片（PNG/JPEG/GIF）里的订阅链接或节点链接并导入。图片给本机路径或 base64 其一即可。",
		Params: []agentapi.Param{
			{Name: "path", Type: "string", Desc: "图片文件绝对路径（支持 ~/）"},
			{Name: "base64", Type: "string", Desc: "图片 base64（可带 data:image/png;base64, 前缀）"},
			{Name: "name", Type: "string", Desc: "订阅名称，可选"},
			{Name: "activate", Type: "boolean", Desc: "是否立即启用；默认：当前没有激活订阅时启用"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			var text string
			var err error
			switch {
			case x.Str("path") != "":
				text, err = qrdecode.FromPath(x.Str("path"))
			case x.Str("base64") != "":
				text, err = qrdecode.FromBase64(x.Str("base64"))
			default:
				return nil, errors.New("请提供 path（图片路径）或 base64（图片内容）其一")
			}
			if err != nil {
				return nil, err
			}
			act, err := a.activateDefault(x)
			if err != nil {
				return nil, err
			}
			res, err := a.agentSmartImport(text, x.Str("name"), act)
			if err != nil {
				return nil, fmt.Errorf("二维码内容：%s；导入失败：%w", truncate(text, 120), err)
			}
			res["qrcodeText"] = truncate(text, 300)
			return res, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "update_subscription", Title: "更新订阅", Group: grpSub, Mutates: true,
		Desc: "重新拉取订阅的节点与流量信息；激活订阅更新后立即生效。subscription 填 all 更新全部。",
		Params: []agentapi.Param{pSub},
		Handler: func(x agentapi.Args) (any, error) {
			if strings.EqualFold(x.Str("subscription"), "all") {
				out := []map[string]any{}
				for _, p := range a.ProfilesList() {
					item := map[string]any{"id": p.ID, "name": p.Name}
					if np, err := a.ProfileUpdate(p.ID); err != nil {
						item["error"] = err.Error()
					} else {
						item["nodeCount"] = np.NodeCount
					}
					out = append(out, item)
				}
				return map[string]any{"results": out}, nil
			}
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			np, err := a.ProfileUpdate(p.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"message": fmt.Sprintf("「%s」已更新，%d 个节点", np.Name, np.NodeCount), "subscription": profileView(np)}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "edit_subscription", Title: "修改订阅", Group: grpSub, Mutates: true,
		Desc: "修改订阅名称、链接（改链接会立即重新拉取）、自动更新开关与间隔。只改传入的字段。",
		Params: []agentapi.Param{
			{Name: "subscription", Type: "string", Desc: "订阅 ID 或名称", Required: true},
			{Name: "name", Type: "string", Desc: "新名称"},
			{Name: "url", Type: "string", Desc: "新订阅链接"},
			{Name: "autoUpdate", Type: "boolean", Desc: "自动更新开关"},
			{Name: "updateIntervalMin", Type: "integer", Desc: "自动更新间隔（分钟，≥30，默认 1440）"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			auto, err := x.Bool("autoUpdate", p.AutoUpdate)
			if err != nil {
				return nil, err
			}
			iv, err := x.Int("updateIntervalMin", p.UpdateIntervalMin)
			if err != nil {
				return nil, err
			}
			np, err := a.ProfileEdit(p.ID, x.Str("name"), x.Str("url"), auto, iv)
			if err != nil {
				return nil, err
			}
			return map[string]any{"message": "已保存", "subscription": profileView(np)}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "activate_subscription", Title: "切换订阅", Group: grpSub, Mutates: true,
		Desc: "启用某个订阅（生成配置并热应用；内核未运行会自动启动）。",
		Params: []agentapi.Param{
			{Name: "subscription", Type: "string", Desc: "订阅 ID 或名称", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			if err := a.activateAndRun(p.ID); err != nil {
				return nil, err
			}
			return map[string]any{"message": fmt.Sprintf("已切换到「%s」", p.Name), "status": a.agentStatus()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "remove_subscription", Title: "删除订阅", Group: grpSub, Mutates: true, Danger: true,
		Desc: "删除订阅及其分流规则；删的是当前订阅会断开代理。",
		Params: []agentapi.Param{
			{Name: "subscription", Type: "string", Desc: "订阅 ID 或名称", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			if err := a.ProfileRemove(p.ID); err != nil {
				return nil, err
			}
			return map[string]any{"message": fmt.Sprintf("已删除「%s」", p.Name)}, nil
		},
	})

	// ---------------- 节点与切换 ----------------
	r.Add(agentapi.Tool{
		Name: "list_proxy_groups", Title: "策略组列表", Group: grpNode,
		Desc:    "全部策略组：🚀 节点选择（主出口）、♻️ 自动选择（测速自动选最快）、以及各分流规则组；含类型、当前选中、节点数。",
		Handler: func(agentapi.Args) (any, error) { return a.agentGroups() },
	})
	r.Add(agentapi.Tool{
		Name: "list_nodes", Title: "节点列表", Group: grpNode,
		Desc: "某策略组里可选的节点，含最近一次延迟（ms，0=超时，null=未测）与当前选中标记。",
		Params: []agentapi.Param{
			{Name: "group", Type: "string", Desc: "策略组名，默认 🚀 节点选择"},
			{Name: "keyword", Type: "string", Desc: "按名称过滤，如 香港 / US / 日本"},
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentNodes(x.Str("group"), x.Str("keyword")) },
	})
	r.Add(agentapi.Tool{
		Name: "select_node", Title: "切换节点", Group: grpNode, Mutates: true,
		Desc: "在策略组里选中节点。node 支持名称片段：如「香港」会在所有含香港的节点中测速并选最快的；填 auto 表示交给 ♻️ 自动选择。切换后自动断开旧连接让其立即走新节点。",
		Params: []agentapi.Param{
			{Name: "node", Type: "string", Desc: "节点名或名称片段；auto = 自动选择；direct = 直连", Required: true},
			{Name: "group", Type: "string", Desc: "策略组名，默认 🚀 节点选择"},
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentSelect(x.Str("group"), x.Str("node")) },
	})
	r.Add(agentapi.Tool{
		Name: "use_fastest_node", Title: "选最快节点", Group: grpNode, Mutates: true,
		Desc: "测速后固定选中最快的节点（可用 keyword 限定地区）。想让它以后自动跟随最快节点，用 select_node node=auto。",
		Params: []agentapi.Param{
			{Name: "keyword", Type: "string", Desc: "只在名称含该关键字的节点里挑，如 日本"},
			{Name: "group", Type: "string", Desc: "策略组名，默认 🚀 节点选择"},
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentFastest(x.Str("group"), x.Str("keyword")) },
	})
	r.Add(agentapi.Tool{
		Name: "test_delay", Title: "节点测速", Group: grpNode,
		Desc: "测速：给 node 测单个节点；否则测整个策略组（默认 ♻️ 自动选择 = 全部节点），按延迟从低到高返回。",
		Params: []agentapi.Param{
			{Name: "group", Type: "string", Desc: "策略组名"},
			{Name: "node", Type: "string", Desc: "单个节点名"},
			{Name: "keyword", Type: "string", Desc: "只返回名称含关键字的结果"},
			{Name: "limit", Type: "integer", Desc: "返回条数，默认 30"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			limit, err := x.Int("limit", 30)
			if err != nil {
				return nil, err
			}
			return a.agentTestDelay(x.Str("group"), x.Str("node"), x.Str("keyword"), limit)
		},
	})

	// ---------------- 连接与端口 ----------------
	r.Add(agentapi.Tool{
		Name: "connect", Title: "一键连接", Group: grpCore, Mutates: true,
		Desc: "启动内核并开启系统代理（等同界面上的大按钮）。",
		Handler: func(agentapi.Args) (any, error) {
			if err := a.CoreStart(); err != nil {
				return nil, err
			}
			if err := a.CoreSetSystemProxy(true); err != nil {
				return map[string]any{"message": "内核已启动，但系统代理开启失败：" + err.Error(), "status": a.agentStatus()}, nil
			}
			return map[string]any{"message": "已连接", "status": a.agentStatus()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "disconnect", Title: "断开代理", Group: grpCore, Mutates: true, Danger: true,
		Desc: "停止内核并清除系统代理（电脑恢复直连）。",
		Handler: func(agentapi.Args) (any, error) {
			if err := a.CoreStop(); err != nil {
				return nil, err
			}
			return map[string]any{"message": "已断开，电脑恢复直连", "status": a.agentStatus()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "restart_core", Title: "重启内核", Group: grpCore, Mutates: true,
		Desc: "重启代理内核（系统代理保持原状态）。连接异常时的通用修复手段。",
		Handler: func(agentapi.Args) (any, error) {
			if err := a.CoreRestart(); err != nil {
				return nil, err
			}
			return map[string]any{"message": "已重启", "status": a.agentStatus()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_mode", Title: "出站模式", Group: grpCore, Mutates: true,
		Desc: "rule = 规则分流（国内直连、国外走代理，推荐）；global = 全部走代理；direct = 全部直连。",
		Params: []agentapi.Param{
			{Name: "mode", Type: "string", Desc: "模式", Required: true, Enum: []string{"rule", "global", "direct"}},
		},
		Handler: func(x agentapi.Args) (any, error) {
			if err := a.CoreSetMode(x.Str("mode")); err != nil {
				return nil, err
			}
			return map[string]any{"message": "出站模式已切换为 " + x.Str("mode")}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_system_proxy", Title: "系统代理开关", Group: grpCore, Mutates: true,
		Desc: "开/关 macOS 系统代理（浏览器等应用是否走本代理）。",
		Params: []agentapi.Param{
			{Name: "on", Type: "boolean", Desc: "true 开启 / false 关闭", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			on, err := x.Bool("on", false)
			if err != nil {
				return nil, err
			}
			if err := a.CoreSetSystemProxy(on); err != nil {
				return nil, err
			}
			return map[string]any{"message": map[bool]string{true: "系统代理已开启", false: "系统代理已关闭"}[on]}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_port", Title: "修改代理端口", Group: grpCore, Mutates: true,
		Desc: "修改混合代理端口（HTTP+SOCKS5 同端口，1024–65535，默认 7890）。系统代理自动跟随新端口。",
		Params: []agentapi.Param{
			{Name: "port", Type: "integer", Desc: "新端口", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			port, err := x.Int("port", 0)
			if err != nil {
				return nil, err
			}
			if err := portAvailable(port, a.MixedPort()); err != nil {
				return nil, err
			}
			if err := a.CoreSetPort(port); err != nil {
				return nil, err
			}
			return map[string]any{"message": fmt.Sprintf("代理端口已改为 %d", port), "proxy": fmt.Sprintf("http://127.0.0.1:%d", port)}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_auto_connect", Title: "启动时自动连接", Group: grpCore, Mutates: true,
		Desc: "打开应用时是否自动连接。",
		Params: []agentapi.Param{
			{Name: "on", Type: "boolean", Desc: "true / false", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			on, err := x.Bool("on", false)
			if err != nil {
				return nil, err
			}
			return map[string]any{"message": "已保存"}, a.CoreSetAutoConnect(on)
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_tun", Title: "虚拟网卡（TUN）", Group: grpCore, Mutates: true, Danger: true,
		Desc: "开/关虚拟网卡模式（接管所有应用流量，包括不认系统代理的命令行/游戏）。开启会在用户屏幕弹出 macOS 管理员密码框，需要用户本人输入。",
		Params: []agentapi.Param{
			{Name: "on", Type: "boolean", Desc: "true / false", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			on, err := x.Bool("on", false)
			if err != nil {
				return nil, err
			}
			if err := a.CoreSetTun(on); err != nil {
				return nil, err
			}
			return map[string]any{"message": map[bool]string{true: "虚拟网卡已开启", false: "虚拟网卡已关闭"}[on]}, nil
		},
	})

	// ---------------- 分流规则 ----------------
	r.Add(agentapi.Tool{
		Name: "list_route_rules", Title: "分流规则列表", Group: grpRoute,
		Desc:   "某订阅下的全部分流规则：每条 = 一组域名强制只走一组指定节点。",
		Params: []agentapi.Param{pSub},
		Handler: func(x agentapi.Args) (any, error) {
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			rules, err := a.RouteRulesList(p.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"subscription": p.Name, "rules": rules, "effective": a.modeNote()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "list_rule_nodes", Title: "可用于分流的节点", Group: grpRoute,
		Desc: "分流规则可选的节点名（该订阅节点 + 自定义节点）。",
		Params: []agentapi.Param{
			pSub,
			{Name: "keyword", Type: "string", Desc: "按名称过滤，可多个（逗号分隔，命中任一）"},
		},
		Handler: func(x agentapi.Args) (any, error) {
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			nodes, err := a.RouteRuleNodes(p.ID)
			if err != nil {
				return nil, err
			}
			if kws := x.List("keyword"); len(kws) > 0 {
				nodes = filterByKeywords(nodes, kws)
			}
			return map[string]any{"subscription": p.Name, "count": len(nodes), "nodes": nodes}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_route_rule", Title: "设置分流规则", Group: grpRoute, Mutates: true,
		Desc: "新建或修改一条分流规则（按 name 匹配）：让一个或一组域名只走指定的一组节点。节点可直接列名字，或用 nodeKeyword 按关键字批量选（如 美国,US）。例：name=AI domains=chatgpt.com,openai.com,claude.ai nodeKeyword=美国 auto=true。仅规则模式生效。",
		Params: []agentapi.Param{
			{Name: "name", Type: "string", Desc: "规则组名（不含逗号/斜杠），如 AI、流媒体", Required: true},
			{Name: "domains", Type: "array", Desc: "域名列表：example.com（含子域）/ *.example.com / a.b.com（精确）/ 1.2.3.0/24"},
			{Name: "nodes", Type: "array", Desc: "节点名列表"},
			{Name: "nodeKeyword", Type: "array", Desc: "按关键字选节点（命中任一即选入），与 nodes 合并"},
			{Name: "auto", Type: "boolean", Desc: "true = 组内自动测速用最快（推荐）；false = 手动挑（默认沿用原值，新建默认 true）"},
			{Name: "append", Type: "boolean", Desc: "true = 域名/节点追加到原有列表；默认 false = 覆盖"},
			pSub,
		},
		Handler: func(x agentapi.Args) (any, error) { return a.agentSetRouteRule(x) },
	})
	r.Add(agentapi.Tool{
		Name: "add_rule_domains", Title: "给分流规则加域名", Group: grpRoute, Mutates: true,
		Desc: "往已有分流规则里追加域名。",
		Params: []agentapi.Param{
			{Name: "name", Type: "string", Desc: "规则组名", Required: true},
			{Name: "domains", Type: "array", Desc: "要追加的域名", Required: true},
			pSub,
		},
		Handler: func(x agentapi.Args) (any, error) {
			return a.agentEditRuleDomains(x.Str("subscription"), x.Str("name"), x.List("domains"), true)
		},
	})
	r.Add(agentapi.Tool{
		Name: "remove_rule_domains", Title: "从分流规则删域名", Group: grpRoute, Mutates: true,
		Desc: "从分流规则里移除域名（删空则整条规则删除）。",
		Params: []agentapi.Param{
			{Name: "name", Type: "string", Desc: "规则组名", Required: true},
			{Name: "domains", Type: "array", Desc: "要移除的域名", Required: true},
			pSub,
		},
		Handler: func(x agentapi.Args) (any, error) {
			return a.agentEditRuleDomains(x.Str("subscription"), x.Str("name"), x.List("domains"), false)
		},
	})
	r.Add(agentapi.Tool{
		Name: "delete_route_rule", Title: "删除分流规则", Group: grpRoute, Mutates: true,
		Desc: "删除一条分流规则。",
		Params: []agentapi.Param{
			{Name: "name", Type: "string", Desc: "规则组名", Required: true},
			pSub,
		},
		Handler: func(x agentapi.Args) (any, error) {
			p, err := a.resolveProfile(x.Str("subscription"))
			if err != nil {
				return nil, err
			}
			rules, _ := a.RouteRulesList(p.ID)
			idx := findRule(rules, x.Str("name"))
			if idx < 0 {
				return nil, fmt.Errorf("没有名为「%s」的分流规则；现有：%s", x.Str("name"), ruleNames(rules))
			}
			rules = append(rules[:idx], rules[idx+1:]...)
			if err := a.RouteRulesSave(p.ID, rules); err != nil {
				return nil, err
			}
			return map[string]any{"message": "已删除", "rules": rules}, nil
		},
	})

	// ---------------- 直连白名单 ----------------
	r.Add(agentapi.Tool{
		Name: "get_direct_list", Title: "直连白名单", Group: grpDirect,
		Desc:    "不走代理、直接连接的域名/IP 段（规则模式生效）。",
		Handler: func(agentapi.Args) (any, error) { return map[string]any{"items": a.WhitelistGet(), "effective": a.modeNote()}, nil },
	})
	r.Add(agentapi.Tool{
		Name: "add_direct_domains", Title: "加入直连", Group: grpDirect, Mutates: true,
		Desc: "让这些域名/IP 段直连不走代理（如公司内网、国内站点被误代理）。",
		Params: []agentapi.Param{
			{Name: "domains", Type: "array", Desc: "example.com / *.example.com / 10.0.0.0/8", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			list := append(a.WhitelistGet(), x.List("domains")...)
			if err := a.WhitelistSet(list); err != nil {
				return nil, err
			}
			return map[string]any{"message": "已加入直连", "items": a.WhitelistGet()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "remove_direct_domains", Title: "移出直连", Group: grpDirect, Mutates: true,
		Desc: "把域名从直连白名单移除。",
		Params: []agentapi.Param{
			{Name: "domains", Type: "array", Desc: "要移除的条目", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			rm := map[string]bool{}
			for _, d := range x.List("domains") {
				rm[normDomain(d)] = true
			}
			keep := []string{}
			removed := 0
			for _, d := range a.WhitelistGet() {
				if rm[normDomain(d)] {
					removed++
					continue
				}
				keep = append(keep, d)
			}
			if removed == 0 {
				return nil, fmt.Errorf("白名单里没有这些条目；现有：%s", strings.Join(a.WhitelistGet(), ", "))
			}
			if err := a.WhitelistSet(keep); err != nil {
				return nil, err
			}
			return map[string]any{"message": fmt.Sprintf("已移除 %d 条", removed), "items": keep}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "set_direct_list", Title: "整体替换直连白名单", Group: grpDirect, Mutates: true,
		Desc: "用新列表整体覆盖直连白名单（传空数组 = 清空）。",
		Params: []agentapi.Param{
			{Name: "domains", Type: "array", Desc: "完整列表", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			if err := a.WhitelistSet(x.List("domains")); err != nil {
				return nil, err
			}
			return map[string]any{"message": "已保存", "items": a.WhitelistGet()}, nil
		},
	})

	// ---------------- 自定义节点 ----------------
	r.Add(agentapi.Tool{
		Name: "add_nodes", Title: "添加节点", Group: grpCustom, Mutates: true,
		Desc: "导入节点分享链接（vless:// vmess:// ss:// trojan:// hysteria2://，多条换行分隔），独立于订阅长期保留，并入所有策略组。",
		Params: []agentapi.Param{
			{Name: "links", Type: "string", Desc: "分享链接，可多行", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			n, err := a.NodesAddFromLinks(x.Str("links"))
			if err != nil {
				return nil, err
			}
			return map[string]any{"message": fmt.Sprintf("已添加 %d 个节点", n), "nodes": a.NodesList()}, nil
		},
	})
	r.Add(agentapi.Tool{
		Name: "list_custom_nodes", Title: "自定义节点列表", Group: grpCustom,
		Desc:    "手动添加的节点名称。",
		Handler: func(agentapi.Args) (any, error) { return map[string]any{"nodes": a.NodesList()}, nil },
	})
	r.Add(agentapi.Tool{
		Name: "remove_custom_node", Title: "删除自定义节点", Group: grpCustom, Mutates: true,
		Desc: "按名称删除自定义节点。",
		Params: []agentapi.Param{
			{Name: "name", Type: "string", Desc: "节点名", Required: true},
		},
		Handler: func(x agentapi.Args) (any, error) {
			if err := a.NodesRemove(x.Str("name")); err != nil {
				return nil, err
			}
			return map[string]any{"message": "已删除", "nodes": a.NodesList()}, nil
		},
	})
	return r
}

// ===========================================================================
// 实现
// ===========================================================================

func (a *App) activateDefault(x agentapi.Args) (bool, error) {
	a.mu.Lock()
	none := a.state.ActiveProfileID == ""
	a.mu.Unlock()
	return x.Bool("activate", none)
}

// activateAndRun 激活订阅；内核未运行则顺带启动（并按用户上次选择恢复系统代理）。
func (a *App) activateAndRun(id string) error {
	if err := a.ProfileActivate(id); err != nil {
		return err
	}
	if a.coreAvailable() {
		return nil
	}
	if err := a.CoreStart(); err != nil {
		return err
	}
	a.mu.Lock()
	sys := a.state.SystemProxy
	a.mu.Unlock()
	if sys && !a.opt.NoSysProxy {
		_ = a.CoreSetSystemProxy(true)
	}
	return nil
}

func profileView(p Profile) map[string]any {
	m := map[string]any{
		"id": p.ID, "name": p.Name, "url": p.URL, "active": p.Active, "nodeCount": p.NodeCount,
		"updatedAt": p.UpdatedAt, "autoUpdate": p.AutoUpdate, "updateIntervalMin": p.UpdateIntervalMin,
	}
	if p.TotalBytes > 0 {
		used := p.UploadBytes + p.DownloadBytes
		m["traffic"] = fmt.Sprintf("已用 %s / 共 %s", humanBytes(float64(used)), humanBytes(float64(p.TotalBytes)))
	}
	if p.Expire != "" {
		m["expire"] = p.Expire
	}
	return m
}

func (a *App) agentProfiles() map[string]any {
	list := []map[string]any{}
	for _, p := range a.ProfilesList() {
		list = append(list, profileView(p))
	}
	return map[string]any{"count": len(list), "subscriptions": list}
}

// resolveProfile 按 ID / 名称 / 名称片段找订阅；空 = 当前激活。
func (a *App) resolveProfile(ref string) (Profile, error) {
	list := a.ProfilesList()
	if ref == "" {
		for _, p := range list {
			if p.Active {
				return p, nil
			}
		}
		return Profile{}, errors.New("当前没有激活的订阅：先用 add_subscription / smart_import 添加，或 activate_subscription 启用")
	}
	for _, p := range list {
		if p.ID == ref || p.Name == ref {
			return p, nil
		}
	}
	var hits []Profile
	for _, p := range list {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(ref)) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	names := []string{}
	for _, p := range list {
		names = append(names, fmt.Sprintf("%s(%s)", p.Name, p.ID))
	}
	if len(hits) > 1 {
		return Profile{}, fmt.Errorf("「%s」匹配到多个订阅，请用 ID：%s", ref, strings.Join(names, "、"))
	}
	return Profile{}, fmt.Errorf("找不到订阅「%s」；现有：%s", ref, strings.Join(names, "、"))
}

// ---------------- 状态 ----------------

func (a *App) agentStatus() map[string]any {
	st := a.CoreStatusNow()
	out := map[string]any{
		"connected":   st.Running,
		"mode":        st.Mode,
		"modeText":    map[string]string{"rule": "规则分流", "global": "全局代理", "direct": "全部直连"}[st.Mode],
		"mixedPort":   st.MixedPort,
		"proxy":       fmt.Sprintf("http://127.0.0.1:%d（HTTP 与 SOCKS5 同端口）", st.MixedPort),
		"systemProxy": st.SystemProxy,
		"tun":         st.Tun,
		"autoConnect": st.AutoConnect,
		"kernel":      "mihomo " + st.Version,
		"appVersion":  AppVersion,
	}
	if st.Message != "" {
		out["lastMessage"] = st.Message
	}
	if p, err := a.ProfileCurrent(); err == nil {
		out["subscription"] = map[string]any{"id": p.ID, "name": p.Name, "nodeCount": p.NodeCount}
	} else {
		out["subscription"] = nil
	}
	if st.Running {
		if tbl, err := a.proxyTable(); err == nil {
			if main, ok := tbl[configgen.GroupSelect]; ok {
				exit := main.Now
				chain := []string{configgen.GroupSelect, main.Now}
				for i := 0; i < 4; i++ {
					g, ok := tbl[exit]
					if !ok || g.All == nil || g.Now == "" {
						break
					}
					exit = g.Now
					chain = append(chain, exit)
				}
				out["selected"] = main.Now
				out["exitNode"] = exit
				out["exitChain"] = strings.Join(chain, " → ")
				if n, ok := tbl[exit]; ok {
					out["exitDelayMs"] = n.delayJSON()
				}
			}
		}
		a.liveMu.Lock()
		t := a.lastTraffic
		a.liveMu.Unlock()
		out["speed"] = fmt.Sprintf("↑ %s/s  ↓ %s/s", humanBytes(float64(t.Up)), humanBytes(float64(t.Down)))
	}
	switch {
	case !st.Running && st.ProfileID == "":
		out["summary"] = "未连接：还没有订阅，先 add_subscription / smart_import 导入"
	case !st.Running:
		out["summary"] = "未连接：调用 connect 一键连接"
	case !st.SystemProxy && !st.Tun:
		out["summary"] = fmt.Sprintf("内核运行中，但系统代理未开启——浏览器不会走代理（可 set_system_proxy on=true，或让程序手动用 127.0.0.1:%d）", st.MixedPort)
	default:
		out["summary"] = "已连接，正常代理中"
	}
	return out
}

func (a *App) agentTraffic() map[string]any {
	a.liveMu.Lock()
	t, m, c := a.lastTraffic, a.lastMemory, a.lastConns
	a.liveMu.Unlock()
	out := map[string]any{
		"upBps": t.Up, "downBps": t.Down,
		"up": humanBytes(float64(t.Up)) + "/s", "down": humanBytes(float64(t.Down)) + "/s",
		"memory": humanBytes(float64(m.Inuse)),
	}
	if c != nil {
		up, _ := c["uploadTotal"].(float64)
		down, _ := c["downloadTotal"].(float64)
		conns, _ := c["connections"].([]any)
		out["uploadTotal"] = humanBytes(up)
		out["downloadTotal"] = humanBytes(down)
		out["connections"] = len(conns)
	}
	if !a.coreAvailable() {
		out["note"] = "内核未运行，数据为最后一次采样"
	}
	return out
}

type connView struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Network  string `json:"network"`
	Rule     string `json:"rule"`
	Chain    string `json:"chain"`
	Upload   string `json:"upload"`
	Download string `json:"download"`
	Process  string `json:"process,omitempty"`
	Start    string `json:"start"`
}

func (a *App) connViews() []connView {
	a.liveMu.Lock()
	c := a.lastConns
	a.liveMu.Unlock()
	raw, _ := c["connections"].([]any)
	out := make([]connView, 0, len(raw))
	for _, x := range raw {
		m, _ := x.(map[string]any)
		if m == nil {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		s := func(mm map[string]any, k string) string { v, _ := mm[k].(string); return v }
		host := s(meta, "host")
		if host == "" {
			host = s(meta, "destinationIP")
		}
		if p := s(meta, "destinationPort"); p != "" {
			host += ":" + p
		}
		var chains []string
		if arr, ok := m["chains"].([]any); ok {
			for i := len(arr) - 1; i >= 0; i-- {
				if v, ok := arr[i].(string); ok {
					chains = append(chains, v)
				}
			}
		}
		rule := s(m, "rule")
		if rp := s(m, "rulePayload"); rp != "" {
			rule += "(" + rp + ")"
		}
		up, _ := m["upload"].(float64)
		down, _ := m["download"].(float64)
		out = append(out, connView{
			ID: s(m, "id"), Host: host, Network: s(meta, "network"), Rule: rule,
			Chain: strings.Join(chains, " → "), Upload: humanBytes(up), Download: humanBytes(down),
			Process: s(meta, "process"), Start: s(m, "start"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out
}

func (c connView) matches(kw string) bool {
	kw = strings.ToLower(kw)
	return strings.Contains(strings.ToLower(c.Host+" "+c.Rule+" "+c.Chain+" "+c.Process), kw)
}

func (a *App) agentConnections(kw string, limit int) map[string]any {
	if limit <= 0 || limit > 300 {
		limit = 300
	}
	all := a.connViews()
	list := []connView{}
	for _, c := range all {
		if kw == "" || c.matches(kw) {
			list = append(list, c)
		}
	}
	total := len(list)
	if len(list) > limit {
		list = list[:limit]
	}
	return map[string]any{"total": total, "shown": len(list), "connections": list}
}

func (a *App) agentCloseConnections(id, kw string) (any, error) {
	switch {
	case id != "":
		if err := a.ConnectionDelete(id); err != nil {
			return nil, err
		}
		return map[string]any{"message": "已断开 1 条连接"}, nil
	case kw != "":
		n := 0
		for _, c := range a.connViews() {
			if c.matches(kw) && a.ConnectionDelete(c.ID) == nil {
				n++
			}
		}
		return map[string]any{"message": fmt.Sprintf("已断开 %d 条匹配「%s」的连接", n, kw)}, nil
	default:
		if err := a.ConnectionsCloseAll(); err != nil {
			return nil, err
		}
		return map[string]any{"message": "已断开全部连接"}, nil
	}
}

func (a *App) agentLogs(limit int, level, kw string) map[string]any {
	logs := a.Logs()
	out := []LogEntry{}
	for i := len(logs) - 1; i >= 0 && len(out) < limit; i-- {
		e := logs[i]
		if level != "" && !strings.EqualFold(e.Type, level) {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(e.Payload), strings.ToLower(kw)) {
			continue
		}
		out = append(out, e)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return map[string]any{"count": len(out), "logs": out}
}

func (a *App) agentRuntimeRules(kw string, limit int) (any, error) {
	raw, err := a.Rules()
	if err != nil {
		return nil, err
	}
	list, _ := raw["rules"].([]any)
	out := []string{}
	for _, x := range list {
		m, _ := x.(map[string]any)
		line := fmt.Sprintf("%v,%v → %v", m["type"], m["payload"], m["proxy"])
		if kw != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(kw)) {
			continue
		}
		out = append(out, line)
		if len(out) >= limit {
			break
		}
	}
	return map[string]any{"total": len(list), "rules": out}, nil
}

// ---------------- 网络体检 ----------------

type probeResult struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Via    string `json:"via"`
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Ms     int64  `json:"ms,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (a *App) proxyClient(timeout time.Duration) *http.Client {
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", a.MixedPort())}
	return &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func directClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func probe(c *http.Client, name, u, via string) probeResult {
	r := probeResult{Name: name, URL: u, Via: via}
	start := time.Now()
	resp, err := c.Get(u)
	r.Ms = time.Since(start).Milliseconds()
	if err != nil {
		r.Error = simplifyNetErr(err)
		return r
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	r.Status = resp.StatusCode
	r.OK = resp.StatusCode > 0 && resp.StatusCode < 500
	return r
}

func simplifyNetErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "Client.Timeout") || strings.Contains(s, "deadline exceeded"):
		return "超时"
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝（代理端口未监听？）"
	case strings.Contains(s, "no such host"):
		return "域名解析失败"
	}
	return s
}

func (a *App) agentCheckNetwork(extra string) map[string]any {
	running := a.coreAvailable()
	pc := a.proxyClient(6 * time.Second)
	dc := directClient(5 * time.Second)
	type job struct {
		name, url, via string
		c              *http.Client
	}
	jobs := []job{{"本机网络（直连百度）", "https://www.baidu.com", "直连", dc}}
	if running {
		jobs = append(jobs,
			job{"代理出口", configgen.HealthURL, "代理", pc},
			job{"Google", "https://www.google.com/generate_204", "代理", pc},
			job{"GitHub", "https://github.com", "代理", pc},
			job{"ChatGPT", "https://chatgpt.com", "代理", pc},
		)
		if extra != "" {
			jobs = append(jobs, job{"自定义", normalizeURL(extra), "代理", pc})
		}
	}
	results := make([]probeResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			results[i] = probe(j.c, j.name, j.url, j.via)
		}(i, j)
	}
	ip := ""
	if running {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip, _ = a.PublicIP()
		}()
	}
	wg.Wait()

	st := a.CoreStatusNow()
	sysActual := sysproxy.Enabled()
	out := map[string]any{"checks": results, "systemProxySetting": st.SystemProxy, "systemProxyActual": sysActual}
	if ip != "" {
		out["exitIP"] = ip
	}
	okCount := 0
	for _, r := range results[1:] {
		if r.OK {
			okCount++
		}
	}
	var verdict, next string
	switch {
	case !results[0].OK && !running:
		verdict, next = "本机网络本身不通（直连百度失败），与代理无关", "检查 Wi-Fi/网线"
	case !running:
		verdict, next = "代理未连接，目前是直连状态", "调用 connect"
	case !results[0].OK && okCount == 0:
		verdict, next = "本机网络不通（直连与代理都失败）", "检查 Wi-Fi/网线后再 check_network"
	case !results[1].OK:
		verdict, next = "代理出口不通：当前节点可能失效", "调用 use_fastest_node 或 select_node node=auto；仍不行则 update_subscription"
	case okCount < len(results)-1:
		verdict, next = "代理可用，但部分网站不通", "用 which_route 看这些网站走向，或用 set_route_rule 为它们指定其他地区节点"
	default:
		verdict, next = "网络正常，代理工作良好", ""
	}
	if running && st.SystemProxy && !sysActual {
		verdict += "；注意：系统代理设置显示已开但实际未生效（可能被其它代理软件改写）"
		next = "调用 set_system_proxy on=true 重新写入"
	}
	out["verdict"] = verdict
	if next != "" {
		out["next"] = next
	}
	return out
}

// ---------------- 策略组 / 节点 ----------------

type proxyItem struct {
	Name  string
	Type  string
	Now   string
	All   []string
	Alive bool
	Delay int // -1 未测；0 超时
}

func (p proxyItem) delayJSON() any {
	if p.Delay < 0 {
		return nil
	}
	return p.Delay
}

func (a *App) proxyTable() (map[string]proxyItem, error) {
	raw, err := a.ProxiesAll()
	if err != nil {
		return nil, err
	}
	obj, _ := raw["proxies"].(map[string]any)
	out := map[string]proxyItem{}
	for k, v := range obj {
		m, _ := v.(map[string]any)
		if m == nil {
			continue
		}
		it := proxyItem{Name: k, Delay: -1}
		it.Type, _ = m["type"].(string)
		it.Now, _ = m["now"].(string)
		it.Alive, _ = m["alive"].(bool)
		if arr, ok := m["all"].([]any); ok {
			it.All = []string{}
			for _, x := range arr {
				if s, ok := x.(string); ok {
					it.All = append(it.All, s)
				}
			}
		}
		if h, ok := m["history"].([]any); ok && len(h) > 0 {
			if last, ok := h[len(h)-1].(map[string]any); ok {
				if d, ok := last["delay"].(float64); ok {
					it.Delay = int(d)
				}
			}
		}
		out[k] = it
	}
	return out, nil
}

func groupOrder(tbl map[string]proxyItem) []string {
	names := []string{}
	for n, it := range tbl {
		if it.All != nil && n != "GLOBAL" {
			names = append(names, n)
		}
	}
	rank := func(n string) int {
		switch n {
		case configgen.GroupSelect:
			return 0
		case configgen.GroupAuto:
			return 1
		}
		return 2
	}
	sort.Slice(names, func(i, j int) bool {
		if rank(names[i]) != rank(names[j]) {
			return rank(names[i]) < rank(names[j])
		}
		return names[i] < names[j]
	})
	return names
}

// resolveGroup 组名容错：空 = 主组；支持去掉 emoji 的写法（节点选择 / 自动选择）。
func resolveGroup(tbl map[string]proxyItem, g string) (proxyItem, error) {
	if g == "" {
		g = configgen.GroupSelect
	}
	if it, ok := tbl[g]; ok && it.All != nil {
		return it, nil
	}
	for _, n := range groupOrder(tbl) {
		if strings.Contains(strings.ToLower(n), strings.ToLower(g)) {
			return tbl[n], nil
		}
	}
	return proxyItem{}, fmt.Errorf("没有策略组「%s」；现有：%s", g, strings.Join(groupOrder(tbl), "、"))
}

func (a *App) agentGroups() (any, error) {
	tbl, err := a.proxyTable()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, n := range groupOrder(tbl) {
		it := tbl[n]
		role := "分流规则组"
		switch n {
		case configgen.GroupSelect:
			role = "主出口（除分流/直连外的流量都走这里）"
		case configgen.GroupAuto:
			role = "自动测速选最快"
		}
		out = append(out, map[string]any{"name": n, "type": it.Type, "now": it.Now, "nodeCount": len(it.All), "role": role})
	}
	return map[string]any{"groups": out}, nil
}

func (a *App) agentNodes(group, kw string) (any, error) {
	tbl, err := a.proxyTable()
	if err != nil {
		return nil, err
	}
	g, err := resolveGroup(tbl, group)
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	for _, n := range g.All {
		if kw != "" && !strings.Contains(strings.ToLower(n), strings.ToLower(kw)) {
			continue
		}
		it := tbl[n]
		list = append(list, map[string]any{"name": n, "type": it.Type, "delayMs": it.delayJSON(), "selected": n == g.Now})
	}
	return map[string]any{"group": g.Name, "now": g.Now, "count": len(list), "nodes": list}, nil
}

// delays 并发测一批节点（每个 3.5s 超时），返回 name → ms（失败为 0）。
func (a *App) delays(names []string) map[string]int {
	out := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, _ := a.core.ProxyDelay(n, configgen.HealthURL, 3500)
			mu.Lock()
			out[n] = d.Delay
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	return out
}

func fastest(m map[string]int) (string, int) {
	best, bd := "", 0
	for n, d := range m {
		if d > 0 && (bd == 0 || d < bd) {
			best, bd = n, d
		}
	}
	return best, bd
}

func (a *App) agentSelect(group, node string) (any, error) {
	tbl, err := a.proxyTable()
	if err != nil {
		return nil, err
	}
	g, err := resolveGroup(tbl, group)
	if err != nil {
		return nil, err
	}
	if g.Type == "URLTest" || g.Type == "Fallback" || g.Type == "LoadBalance" {
		return nil, fmt.Errorf("「%s」是自动测速组，不能手动指定节点；请对 🚀 节点选择 或手动分流组操作", g.Name)
	}
	target := ""
	switch strings.ToLower(node) {
	case "auto", "自动", "自动选择", "最快":
		node = configgen.GroupAuto
	case "direct", "直连":
		node = "DIRECT"
	}
	var hits []string
	for _, n := range g.All {
		if n == node {
			target = n
			break
		}
		if strings.Contains(strings.ToLower(n), strings.ToLower(node)) {
			hits = append(hits, n)
		}
	}
	note := ""
	if target == "" {
		switch len(hits) {
		case 0:
			return nil, fmt.Errorf("「%s」里没有名称含「%s」的节点；可用 list_nodes 查看", g.Name, node)
		case 1:
			target = hits[0]
		default:
			ds := a.delays(hits)
			best, d := fastest(ds)
			if best == "" {
				return nil, fmt.Errorf("匹配「%s」的 %d 个节点测速全部超时，暂不切换", node, len(hits))
			}
			target = best
			note = fmt.Sprintf("在 %d 个匹配节点中测速，选了最快的（%d ms）", len(hits), d)
		}
	}
	if err := a.ProxySelect(g.Name, target); err != nil {
		return nil, err
	}
	_ = a.ConnectionsCloseAll()
	res := map[string]any{"message": fmt.Sprintf("「%s」已切换到 %s", g.Name, target), "group": g.Name, "selected": target}
	if note != "" {
		res["note"] = note
	}
	return res, nil
}

func (a *App) agentFastest(group, kw string) (any, error) {
	tbl, err := a.proxyTable()
	if err != nil {
		return nil, err
	}
	g, err := resolveGroup(tbl, group)
	if err != nil {
		return nil, err
	}
	cands := []string{}
	for _, n := range g.All {
		if it, ok := tbl[n]; ok && it.All != nil {
			continue // 跳过子组
		}
		if n == "DIRECT" || n == "REJECT" {
			continue
		}
		if kw == "" || strings.Contains(strings.ToLower(n), strings.ToLower(kw)) {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("「%s」里没有符合条件的节点", g.Name)
	}
	ds := a.delays(cands)
	best, d := fastest(ds)
	if best == "" {
		return nil, fmt.Errorf("%d 个节点测速全部超时：网络可能有问题，先 check_network", len(cands))
	}
	if err := a.ProxySelect(g.Name, best); err != nil {
		return nil, err
	}
	_ = a.ConnectionsCloseAll()
	ok := 0
	for _, v := range ds {
		if v > 0 {
			ok++
		}
	}
	return map[string]any{
		"message":  fmt.Sprintf("已选中最快节点 %s（%d ms）", best, d),
		"group":    g.Name,
		"selected": best, "delayMs": d, "tested": len(cands), "reachable": ok,
	}, nil
}

func (a *App) agentTestDelay(group, node, kw string, limit int) (any, error) {
	if !a.coreAvailable() {
		return nil, errCoreStopped
	}
	if node != "" {
		d, err := a.ProxyDelay(node, "", 5000)
		if err != nil {
			return nil, err
		}
		return map[string]any{"node": node, "delayMs": d.Delay, "ok": d.Delay > 0}, nil
	}
	if group == "" {
		group = configgen.GroupAuto
	}
	tbl, err := a.proxyTable()
	if err != nil {
		return nil, err
	}
	g, err := resolveGroup(tbl, group)
	if err != nil {
		return nil, err
	}
	raw, err := a.ProxyGroupDelay(g.Name, "", 5000)
	if err != nil {
		return nil, err
	}
	type row struct {
		Name    string `json:"name"`
		DelayMs int    `json:"delayMs"`
	}
	rows := []row{}
	for n, v := range raw {
		if f, ok := v.(float64); ok && f > 0 {
			if kw == "" || strings.Contains(strings.ToLower(n), strings.ToLower(kw)) {
				rows = append(rows, row{n, int(f)})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].DelayMs < rows[j].DelayMs })
	total := len(rows)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return map[string]any{"group": g.Name, "reachable": total, "tested": len(g.All), "fastest": rows}, nil
}

// ---------------- 导入 ----------------

var nodePrefixes = []string{"vless://", "vmess://", "ss://", "ssr://", "trojan://", "hysteria2://", "hy2://", "hysteria://", "tuic://"}

func isNodeLink(s string) bool {
	l := strings.ToLower(s)
	for _, p := range nodePrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func isHTTPURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// classifyImport 把任意文本拆成订阅链接与节点链接；整段 base64 自动解码再拆一次。
func classifyImport(text string) (subs []string, nodes []string) {
	scan := func(t string) {
		for _, line := range strings.FieldsFunc(t, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' || r == '\t' }) {
			line = strings.Trim(strings.TrimSpace(line), "\"'`<>")
			switch {
			case line == "":
			case isNodeLink(line):
				nodes = append(nodes, line)
			case isHTTPURL(line):
				subs = append(subs, line)
			case strings.HasPrefix(strings.ToLower(line), "clash://") || strings.HasPrefix(strings.ToLower(line), "clash-meta://") ||
				strings.HasPrefix(strings.ToLower(line), "flclash://") || strings.HasPrefix(strings.ToLower(line), "mihomo://"):
				if u, err := url.Parse(line); err == nil {
					if v := u.Query().Get("url"); isHTTPURL(v) {
						subs = append(subs, v)
					}
				}
			}
		}
	}
	scan(text)
	if len(subs) == 0 && len(nodes) == 0 {
		compact := strings.Join(strings.Fields(text), "")
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(compact); err == nil {
				scan(string(b))
				break
			}
		}
	}
	return
}

func (a *App) agentSmartImport(text, name string, activate bool) (map[string]any, error) {
	subs, nodes := classifyImport(text)
	if len(subs) == 0 && len(nodes) == 0 {
		return nil, errors.New("没有识别到订阅链接（http/https）或节点分享链接（vless/vmess/ss/trojan/hysteria2）")
	}
	res := map[string]any{}
	errs := []string{}
	added := []map[string]any{}
	var first string
	for _, u := range subs {
		n := ""
		if len(subs) == 1 {
			n = name
		}
		p, err := a.ProfileAdd(u, n)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s：%v", truncate(u, 60), err))
			continue
		}
		if first == "" {
			first = p.ID
		}
		added = append(added, map[string]any{"id": p.ID, "name": p.Name, "nodeCount": p.NodeCount})
	}
	nodesAdded := 0
	if len(nodes) > 0 {
		n, err := a.NodesAddFromLinks(strings.Join(nodes, "\n"))
		if err != nil {
			errs = append(errs, "节点："+err.Error())
		}
		nodesAdded = n
		res["nodesAdded"] = n
	}
	res["subscriptionsAdded"] = added
	if len(errs) > 0 {
		res["errors"] = errs
	}
	if len(added) == 0 && nodesAdded == 0 {
		return nil, errors.New(strings.Join(errs, "；"))
	}
	if activate && first != "" {
		if err := a.activateAndRun(first); err != nil {
			res["activateError"] = err.Error()
		} else {
			res["activated"] = first
		}
	}
	parts := []string{}
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 个订阅", len(added)))
	}
	if nodesAdded > 0 {
		parts = append(parts, fmt.Sprintf("添加 %d 个节点", nodesAdded))
	}
	if res["activated"] != nil {
		parts = append(parts, "已启用")
	}
	res["message"] = strings.Join(parts, "，")
	return res, nil
}

// ---------------- 分流规则 ----------------

func findRule(rules []RouteRule, name string) int {
	for i, r := range rules {
		if r.Name == name {
			return i
		}
	}
	for i, r := range rules {
		if strings.EqualFold(r.Name, name) {
			return i
		}
	}
	return -1
}

func ruleNames(rules []RouteRule) string {
	if len(rules) == 0 {
		return "（无）"
	}
	ns := []string{}
	for _, r := range rules {
		ns = append(ns, r.Name)
	}
	return strings.Join(ns, "、")
}

func filterByKeywords(nodes, kws []string) []string {
	out := []string{}
	for _, n := range nodes {
		ln := strings.ToLower(n)
		for _, k := range kws {
			if strings.Contains(ln, strings.ToLower(k)) {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

func mergeUnique(base, add []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, base...), add...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (a *App) modeNote() string {
	if m := a.CoreStatusNow().Mode; m != "rule" {
		return fmt.Sprintf("注意：当前是 %s 模式，分流规则与直连白名单只在 rule（规则）模式生效——可 set_mode mode=rule", m)
	}
	return "规则模式，已生效"
}

func (a *App) agentSetRouteRule(x agentapi.Args) (any, error) {
	p, err := a.resolveProfile(x.Str("subscription"))
	if err != nil {
		return nil, err
	}
	name := x.Str("name")
	rules, err := a.RouteRulesList(p.ID)
	if err != nil {
		return nil, err
	}
	nodes := x.List("nodes")
	if kws := x.List("nodeKeyword"); len(kws) > 0 {
		all, err := a.RouteRuleNodes(p.ID)
		if err != nil {
			return nil, err
		}
		hit := filterByKeywords(all, kws)
		if len(hit) == 0 {
			return nil, fmt.Errorf("没有名称含 %s 的节点；用 list_rule_nodes 查看可选节点", strings.Join(kws, "/"))
		}
		nodes = mergeUnique(nodes, hit)
	}
	if len(nodes) > configgen.RouteNodeLimit {
		nodes = nodes[:configgen.RouteNodeLimit]
	}
	appendMode, err := x.Bool("append", false)
	if err != nil {
		return nil, err
	}
	domains := x.List("domains")
	idx := findRule(rules, name)
	created := idx < 0
	if created {
		if len(domains) == 0 || len(nodes) == 0 {
			return nil, errors.New("新建分流规则需要 domains 和 nodes（或 nodeKeyword）")
		}
		auto, err := x.Bool("auto", true)
		if err != nil {
			return nil, err
		}
		rules = append(rules, RouteRule{Name: name, Domains: domains, Nodes: nodes, Auto: auto})
	} else {
		r := rules[idx]
		if len(domains) > 0 {
			if appendMode {
				r.Domains = mergeUnique(r.Domains, domains)
			} else {
				r.Domains = domains
			}
		}
		if len(nodes) > 0 {
			if appendMode {
				r.Nodes = mergeUnique(r.Nodes, nodes)
			} else {
				r.Nodes = nodes
			}
		}
		if r.Auto, err = x.Bool("auto", r.Auto); err != nil {
			return nil, err
		}
		rules[idx] = r
	}
	if err := a.RouteRulesSave(p.ID, rules); err != nil {
		return nil, err
	}
	saved, _ := a.RouteRulesList(p.ID)
	var rule RouteRule
	if i := findRule(saved, name); i >= 0 {
		rule = saved[i]
	}
	verb := "已更新"
	if created {
		verb = "已创建"
	}
	return map[string]any{
		"message":   fmt.Sprintf("%s分流规则「%s」：%d 个域名 → %d 个节点（%s）", verb, rule.Name, len(rule.Domains), len(rule.Nodes), map[bool]string{true: "自动选最快", false: "手动选择"}[rule.Auto]),
		"rule":      rule,
		"effective": a.modeNote(),
	}, nil
}

func (a *App) agentEditRuleDomains(sub, name string, domains []string, add bool) (any, error) {
	p, err := a.resolveProfile(sub)
	if err != nil {
		return nil, err
	}
	rules, err := a.RouteRulesList(p.ID)
	if err != nil {
		return nil, err
	}
	idx := findRule(rules, name)
	if idx < 0 {
		return nil, fmt.Errorf("没有名为「%s」的分流规则；现有：%s（新建用 set_route_rule）", name, ruleNames(rules))
	}
	r := rules[idx]
	if add {
		r.Domains = mergeUnique(r.Domains, domains)
	} else {
		rm := map[string]bool{}
		for _, d := range domains {
			rm[normDomain(d)] = true
		}
		keep := []string{}
		for _, d := range r.Domains {
			if !rm[normDomain(d)] {
				keep = append(keep, d)
			}
		}
		r.Domains = keep
	}
	msg := "已保存"
	if len(r.Domains) == 0 {
		rules = append(rules[:idx], rules[idx+1:]...)
		msg = "域名已删空，整条规则已删除"
	} else {
		rules[idx] = r
	}
	if err := a.RouteRulesSave(p.ID, rules); err != nil {
		return nil, err
	}
	return map[string]any{"message": msg, "rule": r}, nil
}

// ---------------- which_route ----------------

func normDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "*.")
	s = strings.TrimPrefix(s, "+.")
	return s
}

func hostOf(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			return strings.ToLower(u.Hostname())
		}
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.ToLower(s)
}

// itemMatches 与 configgen.itemsToRules 的语义一致：
// example.com / *.example.com → 后缀匹配；≥2 个点（a.b.com）→ 精确匹配；CIDR 不参与域名判断。
func itemMatches(item, host string) bool {
	if strings.Contains(item, "/") {
		return false
	}
	wild := strings.HasPrefix(item, "*.") || strings.HasPrefix(item, "+.")
	d := normDomain(item)
	if !wild && strings.Count(d, ".") >= 2 {
		return host == d
	}
	return host == d || strings.HasSuffix(host, "."+d)
}

func (a *App) agentWhichRoute(domain string) (any, error) {
	host := hostOf(domain)
	if host == "" {
		return nil, errors.New("domain 不能为空")
	}
	st := a.CoreStatusNow()
	out := map[string]any{"domain": host, "mode": st.Mode}
	var route string
	switch st.Mode {
	case "direct":
		route = "全部直连模式：直连"
	case "global":
		route = "全局模式：走 " + configgen.GroupSelect
	default:
		if p, err := a.ProfileCurrent(); err == nil {
			rules, _ := a.RouteRulesList(p.ID)
			for _, r := range rules {
				for _, d := range r.Domains {
					if itemMatches(d, host) {
						route = fmt.Sprintf("命中分流规则「%s」（%s）：只走这 %d 个节点", r.Name, d, len(r.Nodes))
						out["ruleNodes"] = r.Nodes
						break
					}
				}
				if route != "" {
					break
				}
			}
		}
		if route == "" {
			for _, d := range a.WhitelistGet() {
				if itemMatches(d, host) {
					route = fmt.Sprintf("命中直连白名单（%s）：直连", d)
					break
				}
			}
		}
		if route == "" && (host == "cn" || strings.HasSuffix(host, ".cn")) {
			route = ".cn 域名：直连"
		}
		if route == "" {
			route = "按内置规则判断：国内站点（GEOSITE/GEOIP CN）直连，其余走 " + configgen.GroupSelect
		}
	}
	out["route"] = route
	live := []connView{}
	for _, c := range a.connViews() {
		if strings.Contains(strings.ToLower(c.Host), host) {
			live = append(live, c)
			if len(live) >= 5 {
				break
			}
		}
	}
	if len(live) > 0 {
		out["liveConnections"] = live
	} else {
		out["liveConnections"] = "当前没有访问该域名的连接（访问一次后再查可看到真实链路）"
	}
	return out, nil
}

// ---------------- 杂项 ----------------

func normalizeURL(u string) string {
	if u != "" && !strings.Contains(u, "://") {
		return "https://" + u
	}
	return u
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func humanBytes(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", b, units[i])
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

// portAvailable 改端口前探测：被其他程序占用则直接给出可操作的报错。
func portAvailable(port, current int) error {
	if port == current {
		return nil
	}
	if port < 1024 || port > 65535 {
		return fmt.Errorf("端口需在 1024–65535 之间，收到 %d", port)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("端口 %d 已被其他程序占用，换一个（如 %d）", port, port+1)
	}
	_ = ln.Close()
	return nil
}
