import type { AgentCall, AgentInfo, AgentTool } from "./mygo";

/** 浏览器调试用：智能体页的模拟数据（形态与 AgentService.Info 一致），每 6s 模拟一次智能体调用。 */

const BIN = "/Applications/KunpengClash.app/Contents/MacOS/KunpengClash";
const BASE = "http://127.0.0.1:9096";

const TOOLS: AgentTool[] = [
  ["get_status", "查看整体状态", "状态与诊断", false, false],
  ["check_network", "网络体检", "状态与诊断", false, false],
  ["get_traffic", "实时流量", "状态与诊断", false, false],
  ["list_connections", "查看实时连接", "状态与诊断", false, false],
  ["close_connections", "断开连接", "状态与诊断", true, false],
  ["which_route", "某域名会怎么走", "状态与诊断", false, false],
  ["test_url", "经代理访问网址", "状态与诊断", false, false],
  ["get_logs", "内核日志", "状态与诊断", false, false],
  ["get_runtime_rules", "生效中的规则表", "状态与诊断", false, false],
  ["list_subscriptions", "订阅列表", "订阅与导入", false, false],
  ["smart_import", "智能导入（链接/文本）", "订阅与导入", true, false],
  ["add_subscription", "添加订阅链接", "订阅与导入", true, false],
  ["import_qrcode", "二维码导入", "订阅与导入", true, false],
  ["update_subscription", "更新订阅", "订阅与导入", true, false],
  ["edit_subscription", "修改订阅", "订阅与导入", true, false],
  ["activate_subscription", "切换订阅", "订阅与导入", true, false],
  ["remove_subscription", "删除订阅", "订阅与导入", true, true],
  ["list_proxy_groups", "策略组列表", "节点与切换", false, false],
  ["list_nodes", "节点列表", "节点与切换", false, false],
  ["select_node", "切换节点", "节点与切换", true, false],
  ["use_fastest_node", "选最快节点", "节点与切换", true, false],
  ["test_delay", "节点测速", "节点与切换", false, false],
  ["connect", "一键连接", "连接与端口", true, false],
  ["disconnect", "断开代理", "连接与端口", true, true],
  ["restart_core", "重启内核", "连接与端口", true, false],
  ["set_mode", "出站模式", "连接与端口", true, false],
  ["set_system_proxy", "系统代理开关", "连接与端口", true, false],
  ["set_port", "修改代理端口", "连接与端口", true, false],
  ["set_auto_connect", "启动时自动连接", "连接与端口", true, false],
  ["set_tun", "虚拟网卡（TUN）", "连接与端口", true, true],
  ["list_route_rules", "分流规则列表", "分流规则", false, false],
  ["list_rule_nodes", "可用于分流的节点", "分流规则", false, false],
  ["set_route_rule", "设置分流规则", "分流规则", true, false],
  ["add_rule_domains", "给分流规则加域名", "分流规则", true, false],
  ["remove_rule_domains", "从分流规则删域名", "分流规则", true, false],
  ["delete_route_rule", "删除分流规则", "分流规则", true, false],
  ["get_direct_list", "直连白名单", "直连白名单", false, false],
  ["add_direct_domains", "加入直连", "直连白名单", true, false],
  ["remove_direct_domains", "移出直连", "直连白名单", true, false],
  ["set_direct_list", "整体替换直连白名单", "直连白名单", true, false],
  ["add_nodes", "添加节点", "自定义节点", true, false],
  ["list_custom_nodes", "自定义节点列表", "自定义节点", false, false],
  ["remove_custom_node", "删除自定义节点", "自定义节点", true, false],
].map(([name, title, group, mutates, danger]) => ({
  name: name as string,
  title: title as string,
  group: group as string,
  description: `${title}（浏览器调试模式示例说明）`,
  mutates: mutates as boolean,
  danger: danger as boolean,
}));

function randomToken() {
  return "kp_" + Array.from({ length: 24 }, () => "0123456789abcdef"[Math.floor(Math.random() * 16)]).join("");
}

function build(token: string, enabled: boolean, activity: AgentCall[]): AgentInfo {
  const mcpConfig = JSON.stringify({ mcpServers: { "kunpeng-clash": { command: BIN, args: ["mcp"] } } }, null, 2);
  return {
    enabled,
    running: enabled,
    baseUrl: BASE,
    mcpUrl: `${BASE}/mcp`,
    token,
    cliPath: BIN,
    prompt: `请帮我接入并管理我电脑上的代理客户端「鲲鹏 Clash」。它在本机开放了智能体接口，你可以直接调用，无需我手动操作界面。

【接入信息】（仅本机 127.0.0.1 可访问）
- HTTP API：${BASE}   请求头：Authorization: Bearer ${token}
- MCP（Streamable HTTP）：${BASE}/mcp   （同一请求头）
- MCP（stdio）：命令 ${BIN}  参数 mcp   （自动读取令牌，无需配置）
- 命令行：${BIN} ctl <工具名> key=value

【请按顺序做】
1. 阅读完整教程：curl -s ${BASE}/v1/guide
2. 如果你支持 MCP，把下面配置加入你的 MCP 设置：
${mcpConfig}
3. 调用 get_status 确认连通，用一两句话告诉我当前代理状态。`,
    mcpConfig,
    mcpHttpConfig: JSON.stringify(
      { mcpServers: { "kunpeng-clash": { type: "http", url: `${BASE}/mcp`, headers: { Authorization: `Bearer ${token}` } } } },
      null,
      2,
    ),
    cliExample: `${BIN} ctl get_status\n${BIN} ctl select_node node=香港\n${BIN} ctl tools`,
    httpExample: `curl -s ${BASE}/v1/call/get_status \\\n  -H 'Authorization: Bearer ${token}' -d '{}'`,
    tools: TOOLS,
    activity,
    error: "",
  };
}

const SAMPLE: [string, string, string][] = [
  ["get_status", "查看整体状态", "mcp-stdio"],
  ["check_network", "网络体检", "mcp-stdio"],
  ["select_node", "切换节点", "mcp"],
  ["set_route_rule", "设置分流规则", "cli"],
  ["list_nodes", "节点列表", "http"],
];

class MockAgent {
  token = randomToken();
  enabled = true;
  activity: AgentCall[] = [];
  private listeners = new Set<(c: AgentCall) => void>();
  private i = 0;

  constructor() {
    setInterval(() => {
      if (!this.enabled) return;
      const [tool, title, source] = SAMPLE[this.i++ % SAMPLE.length];
      const t = TOOLS.find((x) => x.name === tool);
      const c: AgentCall = {
        tool,
        title,
        source,
        ok: Math.random() > 0.12,
        error: "",
        at: new Date().toISOString(),
        durationMs: Math.round(20 + Math.random() * 900),
        mutates: t?.mutates ?? false,
      };
      if (!c.ok) c.error = "匹配「香港」的 3 个节点测速全部超时，暂不切换";
      this.activity = [c, ...this.activity].slice(0, 60);
      this.listeners.forEach((fn) => fn(c));
    }, 6000);
  }

  info = async () => build(this.token, this.enabled, this.activity);
  setEnabled = async (on: boolean) => {
    this.enabled = on;
  };
  resetToken = async () => {
    this.token = randomToken();
    return this.info();
  };
  clear = async () => {
    this.activity = [];
  };
  on(fn: (c: AgentCall) => void) {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }
}

export const mockAgent = new MockAgent();
