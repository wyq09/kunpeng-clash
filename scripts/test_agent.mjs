#!/usr/bin/env node
// 智能体网关端到端回归：HTTP / MCP(HTTP) / MCP(stdio) / CLI 四条通道。
// 隔离运行：独立 --data-dir、--port 7892、--controller 127.0.0.1:19097、--no-sysproxy，
// 不联网拉订阅、不碰系统代理（结束时校验系统代理与开始一致）。
// 用法：node scripts/test_agent.mjs
import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

for (const k of ["http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"]) delete process.env[k];
process.env.GOPROXY = "https://goproxy.cn,direct";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DATA = "/tmp/kunpeng-agent-test-data";
const BIN = "/tmp/kunpeng-agent-test-bin";
const PORT = 7892;

let failures = 0;
const pass = (m) => console.log(`[PASS] ${m}`);
const fail = (m) => {
  console.log(`[FAIL] ${m}`);
  failures++;
};
const check = (cond, m, extra) => (cond ? pass(m) : fail(m + (extra ? ` —— ${JSON.stringify(extra).slice(0, 400)}` : "")));

const sysProxy = () => {
  try {
    return execFileSync("networksetup", ["-getwebproxy", "Wi-Fi"]).toString();
  } catch {
    return "";
  }
};
const sysBefore = sysProxy();

console.log("== 编译 ==");
execFileSync("go", ["build", "-o", BIN, "."], { cwd: root, stdio: "inherit" });

console.log("== 准备隔离数据目录 ==");
fs.rmSync(DATA, { recursive: true, force: true });
fs.mkdirSync(path.join(DATA, "profiles"), { recursive: true });
fs.writeFileSync(
  path.join(DATA, "profiles", "agt001.yaml"),
  `proxies:
  - { name: "🇭🇰 香港 01", type: ss, server: hk1.example.com, port: 8388, cipher: aes-128-gcm, password: p }
  - { name: "🇭🇰 香港 02", type: ss, server: hk2.example.com, port: 8388, cipher: aes-128-gcm, password: p }
  - { name: "🇺🇸 美国 01", type: ss, server: us1.example.com, port: 8388, cipher: aes-128-gcm, password: p }
  - { name: "🇯🇵 日本 01", type: ss, server: jp1.example.com, port: 8388, cipher: aes-128-gcm, password: p }
`,
);
fs.writeFileSync(
  path.join(DATA, "profiles.yaml"),
  `profiles:\n  - id: agt001\n    name: 测试机场\n    url: https://unused.invalid/sub\n    updatedAt: "2026-01-01T00:00:00Z"\n    nodeCount: 4\n`,
);
fs.writeFileSync(
  path.join(DATA, "state.json"),
  JSON.stringify({ mode: "rule", mixedPort: PORT, autoConnect: false, activeProfileId: "agt001", secret: "agentsecret" }),
);

const proc = spawn(BIN, ["--headless", "--port", String(PORT), "--no-sysproxy", "--data-dir", DATA, "--controller", "127.0.0.1:19097"], {
  stdio: ["ignore", "pipe", "pipe"],
});
let logs = "";
proc.stdout.on("data", (d) => (logs += d));
proc.stderr.on("data", (d) => (logs += d));

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let ep;
for (let i = 0; i < 60 && !ep; i++) {
  await sleep(250);
  try {
    ep = JSON.parse(fs.readFileSync(path.join(DATA, "agent.json"), "utf8"));
  } catch {}
}
if (!ep) {
  console.log(logs);
  fail("agent.json 未生成");
  proc.kill("SIGINT");
  process.exit(1);
}
pass(`网关启动 ${ep.baseUrl}`);
await sleep(1500); // 等内核就绪

const H = { Authorization: `Bearer ${ep.token}`, "Content-Type": "application/json" };
const call = async (tool, args = {}) => {
  const r = await fetch(`${ep.baseUrl}/v1/call/${tool}`, { method: "POST", headers: H, body: JSON.stringify(args) });
  return r.json();
};

try {
  console.log("== 鉴权与安全 ==");
  check((await fetch(`${ep.baseUrl}/v1/ping`)).ok, "ping 免鉴权");
  const guide = await (await fetch(`${ep.baseUrl}/v1/guide`)).text();
  check(guide.includes("智能体接入教程") && guide.includes("set_route_rule") && !guide.includes(ep.token), "教程免鉴权且不含令牌");
  const skill = await (await fetch(`${ep.baseUrl}/v1/skill`)).text();
  check(skill.startsWith("---\nname: kunpeng-clash"), "SKILL.md frontmatter");
  check((await fetch(`${ep.baseUrl}/v1/tools`)).status === 401, "无令牌 401");
  check((await fetch(`${ep.baseUrl}/v1/tools`, { headers: { ...H, Origin: "https://evil.example" } })).status === 403, "网页 Origin 403");
  const tools = await (await fetch(`${ep.baseUrl}/v1/tools`, { headers: H })).json();
  check(tools.ok && tools.tools.length >= 40, `工具清单 ${tools.tools?.length} 项`);

  console.log("== HTTP 工具调用 ==");
  const st = await call("get_status");
  check(st.ok && st.result.connected === true && st.result.mixedPort === PORT, "get_status", st);
  check(st.result.subscription?.name === "测试机场", "get_status 当前订阅");

  const bad = await call("set_mode", { mode: "xx" });
  check(!bad.ok && bad.error.includes("rule/global/direct"), "枚举校验报错带可选值");
  const unk = await call("select_node", { nod: "x" });
  check(!unk.ok && unk.error.includes("可用参数"), "未知参数提示正确参数名");

  const groups = await call("list_proxy_groups");
  check(groups.ok && groups.result.groups[0].name === "🚀 节点选择", "list_proxy_groups 主组在前");
  const sel = await call("select_node", { node: "日本" });
  check(sel.ok && sel.result.selected === "🇯🇵 日本 01", "select_node 名称片段唯一匹配", sel);
  const nodes = await call("list_nodes", { keyword: "日本" });
  check(nodes.ok && nodes.result.nodes[0].selected === true, "list_nodes 选中标记");
  const auto = await call("select_node", { node: "auto" });
  check(auto.ok && auto.result.selected === "♻️ 自动选择", "select_node auto");

  const rr = await call("set_route_rule", { name: "AI", domains: ["chatgpt.com", "claude.ai"], nodeKeyword: "美国" });
  check(rr.ok && rr.result.rule.nodes.length === 1 && rr.result.rule.auto === true, "set_route_rule 关键字选节点", rr);
  const add = await call("add_rule_domains", { name: "AI", domains: "gemini.google.com" });
  check(add.ok && add.result.rule.domains.length === 3, "add_rule_domains（字符串形态数组）");
  const why = await call("which_route", { domain: "https://chat.chatgpt.com/c/1" });
  check(why.ok && why.result.route.includes("AI"), "which_route 命中分流规则", why);
  const rules = await call("get_runtime_rules", { keyword: "chatgpt" });
  check(rules.ok && rules.result.rules.some((l) => l.includes("chatgpt.com") && l.includes("AI")), "分流规则已热应用到内核", rules);
  const groups2 = await call("list_proxy_groups");
  check(groups2.result.groups.some((g) => g.name === "AI"), "分流组出现在策略组");

  const wl = await call("add_direct_domains", { domains: ["corp.example.com", "10.0.0.0/8"] });
  check(wl.ok && wl.result.items.length === 2, "add_direct_domains");
  const why2 = await call("which_route", { domain: "corp.example.com" });
  check(why2.result.route.includes("直连白名单"), "which_route 命中白名单");
  const wl2 = await call("remove_direct_domains", { domains: "10.0.0.0/8" });
  check(wl2.ok && wl2.result.items.length === 1, "remove_direct_domains");

  const m = await call("set_mode", { mode: "global" });
  check(m.ok && (await call("get_status")).result.mode === "global", "set_mode global");
  await call("set_mode", { mode: "rule" });

  const nl = await call("add_nodes", { links: "trojan://pw@tr.example.com:443?sni=tr.example.com#自建Trojan" });
  check(nl.ok && nl.result.nodes.includes("自建Trojan"), "add_nodes", nl);
  const si = await call("smart_import", { text: Buffer.from("ss://YWVzLTEyOC1nY206cHc@ss.example.com:8388#B64节点").toString("base64") });
  check(si.ok && si.result.nodesAdded === 1, "smart_import 识别 base64 节点", si);
  const rm = await call("remove_custom_node", { name: "B64节点" });
  check(rm.ok, "remove_custom_node");
  const qr = await call("import_qrcode", { path: "/tmp/__no_such_qr.png" });
  check(!qr.ok && qr.error.includes("读取图片失败"), "import_qrcode 错误人话");

  const pt = await call("set_port", { port: PORT + 1 });
  check(pt.ok && (await call("get_status")).result.mixedPort === PORT + 1, "set_port", pt);
  await call("set_port", { port: PORT });

  const subs = await call("list_subscriptions");
  check(subs.ok && subs.result.subscriptions[0].active, "list_subscriptions");
  const tr = await call("get_traffic");
  check(tr.ok && "up" in tr.result, "get_traffic");
  const cn = await call("list_connections");
  check(cn.ok && Array.isArray(cn.result.connections), "list_connections");
  const lg = await call("get_logs", { limit: 5 });
  check(lg.ok && lg.result.count <= 5, "get_logs");

  console.log("== MCP over HTTP ==");
  const rpc = async (body) => (await fetch(`${ep.baseUrl}/mcp`, { method: "POST", headers: { ...H, Accept: "application/json, text/event-stream" }, body: JSON.stringify(body) }));
  const init = await (await rpc({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "t", version: "1" } } })).json();
  check(init.result?.protocolVersion === "2025-06-18" && init.result.capabilities.tools, "initialize");
  check((await rpc({ jsonrpc: "2.0", method: "notifications/initialized" })).status === 202, "通知返回 202");
  const tl = await (await rpc({ jsonrpc: "2.0", id: 2, method: "tools/list" })).json();
  const setTun = tl.result.tools.find((t) => t.name === "set_tun");
  check(setTun?.annotations.destructiveHint === true && setTun.inputSchema.required[0] === "on", "tools/list 注解与 schema");
  const tc = await (await rpc({ jsonrpc: "2.0", id: 3, method: "tools/call", params: { name: "get_status", arguments: {} } })).json();
  check(tc.result?.isError === false && JSON.parse(tc.result.content[0].text).connected === true, "tools/call get_status");
  const te = await (await rpc({ jsonrpc: "2.0", id: 4, method: "tools/call", params: { name: "set_mode", arguments: {} } })).json();
  check(te.result?.isError === true && te.result.content[0].text.includes("缺少必填参数"), "tools/call 错误为 isError 结果");
  const batch = await (await rpc([{ jsonrpc: "2.0", id: 5, method: "ping" }, { jsonrpc: "2.0", id: 6, method: "tools/list" }])).json();
  check(Array.isArray(batch) && batch.length === 2, "批量请求");

  console.log("== MCP stdio ==");
  const stdio = spawn(BIN, ["mcp", "--data-dir", DATA], { stdio: ["pipe", "pipe", "inherit"] });
  const lines = [];
  let buf = "";
  stdio.stdout.on("data", (d) => {
    buf += d;
    let i;
    while ((i = buf.indexOf("\n")) >= 0) {
      lines.push(JSON.parse(buf.slice(0, i)));
      buf = buf.slice(i + 1);
    }
  });
  stdio.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2024-11-05" } }) + "\n");
  stdio.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
  stdio.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/call", params: { name: "list_subscriptions", arguments: {} } }) + "\n");
  for (let i = 0; i < 40 && lines.length < 2; i++) await sleep(100);
  stdio.stdin.end();
  check(lines.length === 2 && lines[0].result.protocolVersion === "2024-11-05", "stdio initialize（协议版本协商）", lines);
  check(lines[1]?.result?.content?.[0]?.text.includes("测试机场"), "stdio tools/call");

  console.log("== CLI ==");
  const cli = (...a) => {
    try {
      return { code: 0, out: execFileSync(BIN, ["ctl", "--data-dir", DATA, ...a], { stdio: "pipe" }).toString() };
    } catch (e) {
      return { code: e.status, out: String(e.stdout) + String(e.stderr) };
    }
  };
  const c1 = cli("get_status");
  check(c1.code === 0 && JSON.parse(c1.out).connected === true, "ctl get_status");
  const c2 = cli("select_node", "node=香港");
  check(c2.code <= 1 && c2.out.includes("香港"), "ctl select_node 多匹配（测速选最快，或测试节点全超时时如实报告）", c2);
  const c3 = cli("set_mode", "mode=bad");
  check(c3.code === 1 && c3.out.includes("错误"), "ctl 工具报错退出码 1");
  const c4 = cli("tools");
  check(c4.code === 0 && c4.out.includes("【分流规则】"), "ctl tools 分组列出");
  const c5 = (() => {
    try {
      execFileSync(BIN, ["ctl", "--data-dir", "/tmp/__kp_none", "get_status"], { stdio: "pipe" });
      return 0;
    } catch (e) {
      return e.status;
    }
  })();
  check(c5 === 2, "应用未运行时 ctl 退出码 2");
} catch (e) {
  fail("异常：" + (e?.stack || e));
} finally {
  proc.kill("SIGINT");
  await new Promise((r) => proc.on("exit", r));
}
check(!fs.existsSync(path.join(DATA, "agent.json")), "退出后 agent.json 已清理");
check(sysProxy() === sysBefore, "系统代理前后一致");
console.log(failures ? `\n${failures} 项失败` : "\n全部通过");
process.exit(failures ? 1 : 0);
