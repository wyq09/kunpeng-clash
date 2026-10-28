import { AgentService, AppService, CoreService, NodesService, ProfilesService, ProxyService, RulesService, events } from "./mygo";
import type { AgentCall, AgentInfo, AgentTool, ConnectionsSnapshot, CoreStatus, DelayResult, LogEntry, Profile, RouteRule } from "./mygo";
import { mockEngine } from "./mock";
import { mockAgent } from "./mockAgent";
import type { MihomoConnectionsPayload, MihomoProxy } from "./mock";

/**
 * API 适配层：页面组件只与这里交互，不直接 import mygo.ts。
 * - 在 mygo 窗口内（globalThis.mygo 存在）：全部转发到真实 IPC；
 * - 在纯浏览器（vite dev）里：自动切换到 mock 数据引擎，
 *   事件节奏与数据形态与真实 mihomo 保持一致。
 */

export type { AgentCall, AgentInfo, AgentTool, ConnectionsSnapshot, CoreStatus, DelayResult, LogEntry, Profile, RouteRule };
export type { MihomoConnectionsPayload, MihomoProxy } from "./mock";

export const isMock = (globalThis as { mygo?: unknown }).mygo === undefined;

export type Unsubscribe = () => void;

/** 配置生成器（internal/configgen）约定的固定组名。 */
export const MAIN_GROUP = "🚀 节点选择";
export const AUTO_GROUP = "♻️ 自动选择";

/** 单个代理/组的最新延迟：取 history 最后一项，无记录返回 undefined。 */
export function latestDelay(p: MihomoProxy | undefined): number | undefined {
  const h = p?.history;
  if (!h || h.length === 0) return undefined;
  const delay = h[h.length - 1]?.delay;
  return typeof delay === "number" && Number.isFinite(delay) ? delay : undefined;
}

/** 策略组排序：主组在前，其余按名称。 */
function sortGroupNames(names: string[]): string[] {
  const priority = ["🚀 节点选择", "♻️ 自动选择"];
  return [...names].sort((a, b) => {
    const pa = priority.indexOf(a);
    const pb = priority.indexOf(b);
    if (pa !== -1 || pb !== -1) return (pa === -1 ? 99 : pa) - (pb === -1 ? 99 : pb);
    return a.localeCompare(b, "zh-Hans-CN");
  });
}

/** 从 ProxyService.All() 的原始返回中解析出干净的代理表。 */
function parseAllProxies(raw: Record<string, unknown>): Record<string, MihomoProxy> {
  const obj = (raw?.proxies ?? raw) as Record<string, unknown> | undefined;
  const out: Record<string, MihomoProxy> = {};
  if (!obj || typeof obj !== "object") return out;
  for (const [key, value] of Object.entries(obj)) {
    if (!value || typeof value !== "object") continue;
    const v = value as Record<string, unknown>;
    const historyRaw = Array.isArray(v.history) ? v.history : [];
    const history = historyRaw
      .filter((h): h is Record<string, unknown> => !!h && typeof h === "object")
      .map((h) => ({
        time: typeof h.time === "string" ? h.time : "",
        delay: typeof h.delay === "number" ? h.delay : 0,
      }));
    out[key] = {
      name: typeof v.name === "string" ? v.name : key,
      type: typeof v.type === "string" ? v.type : "Unknown",
      now: typeof v.now === "string" ? v.now : undefined,
      all: Array.isArray(v.all) ? v.all.filter((x): x is string => typeof x === "string") : undefined,
      history,
      udp: v.udp === true,
      alive: v.alive === true,
    };
  }
  return out;
}

/** 从 /connections 原生 JSON 中解析连接数组（容忍字段缺失）。 */
function parseConnections(snapshot: ConnectionsSnapshot | undefined): MihomoConnectionsPayload {
  const data = snapshot?.data as Record<string, unknown> | undefined;
  if (!data) return { connections: [] };
  const connsRaw = Array.isArray(data.connections) ? data.connections : [];
  const connections = connsRaw
    .filter((c): c is Record<string, unknown> => !!c && typeof c === "object")
    .map((c, i) => {
      const meta = (c.metadata ?? {}) as Record<string, unknown>;
      const str = (x: unknown) => (typeof x === "string" ? x : "");
      return {
        id: str(c.id) || `conn-${i}`,
        upload: typeof c.upload === "number" ? c.upload : 0,
        download: typeof c.download === "number" ? c.download : 0,
        start: str(c.start),
        chains: Array.isArray(c.chains) ? c.chains.filter((x): x is string => typeof x === "string") : [],
        rule: str(c.rule),
        rulePayload: str(c.rulePayload),
        metadata: {
          network: str(meta.network),
          type: str(meta.type),
          sourceIP: str(meta.sourceIP),
          destinationIP: str(meta.destinationIP),
          sourcePort: str(meta.sourcePort),
          destinationPort: str(meta.destinationPort),
          host: str(meta.host),
          process: str(meta.process),
          processPath: str(meta.processPath),
        },
      };
    });
  return {
    downloadTotal: typeof data.downloadTotal === "number" ? data.downloadTotal : 0,
    uploadTotal: typeof data.uploadTotal === "number" ? data.uploadTotal : 0,
    connections,
  };
}

/* ---------------- API ---------------- */

export const api = {
  isMock,

  // ---- 内核 ----
  getStatus(): Promise<CoreStatus> {
    if (isMock) return mockEngine.getStatus();
    return CoreService.status();
  },
  startCore(): Promise<void> {
    if (isMock) return mockEngine.startCore();
    return CoreService.start();
  },
  stopCore(): Promise<void> {
    if (isMock) return mockEngine.stopCore();
    return CoreService.stop();
  },
  restartCore(): Promise<void> {
    if (isMock) return mockEngine.startCore();
    return CoreService.restart();
  },
  setMode(mode: string): Promise<void> {
    if (isMock) return mockEngine.setMode(mode);
    return CoreService.setMode(mode);
  },
  setSystemProxy(on: boolean): Promise<void> {
    if (isMock) return mockEngine.setSystemProxy(on);
    return CoreService.setSystemProxy(on);
  },
  setPort(port: number): Promise<void> {
    if (isMock) return mockEngine.setPort(port);
    return CoreService.setPort(port);
  },
  setAutoConnect(on: boolean): Promise<void> {
    if (isMock) return mockEngine.setAutoConnect(on);
    return CoreService.setAutoConnect(on);
  },
  /** 开关虚拟网卡（TUN）。开启需管理员授权；状态变化经 StatusEvent 推送。 */
  setTun(on: boolean): Promise<void> {
    if (isMock) return mockEngine.setTun(on);
    return CoreService.setTun(on);
  },
  getWhitelist(): Promise<string[]> {
    if (isMock) return mockEngine.getWhitelist();
    return CoreService.getWhitelist();
  },
  setWhitelist(domains: string[]): Promise<void> {
    if (isMock) return mockEngine.setWhitelist(domains);
    return CoreService.setWhitelist(domains);
  },

  // ---- 代理 ----
  /** 返回全部代理（含组），并把「组」单独抽出（已过滤 GLOBAL、已排序）。 */
  async getProxies(): Promise<{
    all: Record<string, MihomoProxy>;
    groups: MihomoProxy[];
  }> {
    let table: Record<string, MihomoProxy>;
    if (isMock) {
      // mock 引擎返回的是同一个可变对象；克隆一份保证 React 状态引用变化能触发重渲染
      const src = mockEngine.getAllProxies();
      table = {};
      for (const [k, v] of Object.entries(src)) table[k] = { ...v };
    } else {
      table = parseAllProxies(await ProxyService.all());
    }
    const groupNames = sortGroupNames(
      Object.values(table)
        .filter((p) => Array.isArray(p.all) && p.name !== "GLOBAL")
        .map((p) => p.name),
    );
    const groups = groupNames
      .map((n) => table[n])
      .filter((p): p is MihomoProxy => !!p);
    return { all: table, groups };
  },
  async select(group: string, name: string): Promise<void> {
    if (isMock) return mockEngine.select(group, name);
    return ProxyService.select(group, name);
  },
  /** 组批量延迟：返回 name → ms（失败的节点不在结果里）。 */
  async groupDelay(group: string): Promise<Record<string, number>> {
    if (isMock) return mockEngine.groupDelay(group);
    const raw = await ProxyService.groupDelay(group, "https://www.gstatic.com/generate_204", 5000);
    const out: Record<string, number> = {};
    for (const [k, v] of Object.entries(raw ?? {})) {
      if (typeof v === "number") out[k] = v;
    }
    return out;
  },
  /** 单节点延迟测试。 */
  async proxyDelay(name: string): Promise<DelayResult> {
    if (isMock) return mockEngine.proxyDelay(name);
    return ProxyService.proxyDelay(name, "https://www.gstatic.com/generate_204", 5000);
  },

  /** 关闭单个连接（id 来自连接快照）。 */
  async deleteConnection(id: string): Promise<void> {
    if (isMock) return mockEngine.deleteConnection(id);
    return ProxyService.deleteConnection(id);
  },

  /** 关闭全部连接。 */
  async closeAllConnections(): Promise<void> {
    if (isMock) return mockEngine.closeAllConnections();
    return ProxyService.closeAllConnections();
  },

  // ---- 订阅 ----
  listProfiles(): Promise<Profile[]> {
    if (isMock) return mockEngine.listProfiles();
    return ProfilesService.list();
  },
  addProfile(url: string, name: string): Promise<Profile> {
    if (isMock) return mockEngine.addProfile(url, name);
    return ProfilesService.add(url, name);
  },
  updateProfile(id: string): Promise<Profile> {
    if (isMock) return mockEngine.updateProfile(id);
    return ProfilesService.update(id);
  },
  removeProfile(id: string): Promise<void> {
    if (isMock) return mockEngine.removeProfile(id);
    return ProfilesService.remove(id);
  },
  activateProfile(id: string): Promise<void> {
    if (isMock) return mockEngine.activateProfile(id);
    return ProfilesService.activate(id);
  },
  currentProfile(): Promise<Profile> {
    if (isMock) return mockEngine.currentProfile();
    return ProfilesService.current();
  },
  /** 修改订阅的名称、URL 与自动更新设置（URL 变更后后端会立即重新拉取）。 */
  editProfile(id: string, name: string, url: string, autoUpdate: boolean, updateIntervalMin: number): Promise<Profile> {
    if (isMock) return mockEngine.editProfile(id, name, url, autoUpdate, updateIntervalMin);
    return ProfilesService.edit(id, name, url, autoUpdate, updateIntervalMin);
  },

  // ---- 自定义节点 ----
  /** 全部自定义节点名称。 */
  listNodes(): Promise<string[]> {
    if (isMock) return mockEngine.listNodes();
    return NodesService.list();
  },
  /** 批量导入节点分享链接，返回成功条数。 */
  addNodesFromLinks(raw: string): Promise<number> {
    if (isMock) return mockEngine.addNodesFromLinks(raw);
    return NodesService.addFromLinks(raw);
  },
  /** 删除单个自定义节点。 */
  removeNode(name: string): Promise<void> {
    if (isMock) return mockEngine.removeNode(name);
    return NodesService.remove(name);
  },

  // ---- 分流规则（挂在具体订阅下） ----
  /** 某订阅的全部分流规则（域名组 → 指定节点组）。 */
  listRouteRules(profileId: string): Promise<RouteRule[]> {
    if (isMock) return mockEngine.listRouteRules(profileId);
    return RulesService.list(profileId);
  },
  /** 全量保存某订阅的分流规则并热应用（仅规则模式生效）。 */
  saveRouteRules(profileId: string, rules: RouteRule[]): Promise<void> {
    if (isMock) return mockEngine.saveRouteRules(profileId, rules);
    return RulesService.save(profileId, rules);
  },
  /** 分流规则编辑器可选的节点名（该订阅真实节点 + 自定义节点）。 */
  routeRuleNodes(profileId: string): Promise<string[]> {
    if (isMock) return mockEngine.routeRuleNodes(profileId);
    return RulesService.nodeNames(profileId);
  },

  // ---- 杂项 ----
  testURL(url: string): Promise<number> {
    if (isMock) return mockEngine.testURL();
    return AppService.testURL(url);
  },
  /** 公网出口 IP；失败返回空串（页面显示占位）。 */
  async publicIP(): Promise<string> {
    if (isMock) return mockEngine.publicIP();
    try {
      const ip = await AppService.publicIP();
      return typeof ip === "string" ? ip.trim() : "";
    } catch {
      return "";
    }
  },
  openDataDir(): Promise<void> {
    if (isMock) {
      return Promise.reject(new Error("浏览器调试模式：数据目录仅在应用内可用"));
    }
    return AppService.openDataDir();
  },
  /** 复制到剪贴板：应用内走系统 pbcopy，浏览器调试走 navigator.clipboard。 */
  async copyText(text: string): Promise<void> {
    if (isMock) return navigator.clipboard.writeText(text);
    return AppService.copyText(text);
  },
  getLogs(): Promise<LogEntry[]> {
    if (isMock) return Promise.resolve([]);
    return AppService.getLogs();
  },

  // ---- 智能体接入 ----
  agentInfo(): Promise<AgentInfo> {
    if (isMock) return mockAgent.info();
    return AgentService.info();
  },
  agentSetEnabled(on: boolean): Promise<void> {
    if (isMock) return mockAgent.setEnabled(on);
    return AgentService.setEnabled(on);
  },
  agentResetToken(): Promise<AgentInfo> {
    if (isMock) return mockAgent.resetToken();
    return AgentService.resetToken();
  },
  agentExportSkill(): Promise<string> {
    if (isMock) return Promise.reject(new Error("浏览器调试模式：导出仅在应用内可用"));
    return AgentService.exportSkill();
  },
  agentClearActivity(): Promise<void> {
    if (isMock) return mockAgent.clear();
    return AgentService.clearActivity();
  },
  onAgent(fn: (c: AgentCall) => void): Unsubscribe {
    if (isMock) return mockAgent.on(fn);
    return events.agent.on(fn);
  },

  // ---- 事件 ----
  onTraffic(fn: (t: { up: number; down: number }) => void): Unsubscribe {
    if (isMock) return mockEngine.onTraffic(fn);
    return events.traffic.on(fn);
  },
  onConnections(fn: (c: ConnectionsSnapshot) => void): Unsubscribe {
    if (isMock) return mockEngine.onConnections(fn);
    return events.connections.on(fn);
  },
  onStatus(fn: (s: CoreStatus) => void): Unsubscribe {
    if (isMock) return mockEngine.onStatus(fn);
    return events.status.on(fn);
  },
  onMemory(fn: (m: { inuse: number; osLimit: number }) => void): Unsubscribe {
    if (isMock) return mockEngine.onMemory(fn);
    return events.memory.on(fn);
  },
  /** 解析连接快照为干净结构。 */
  parseConnections,
};

export const delayTestURL = "https://www.gstatic.com/generate_204";
