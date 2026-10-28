import type { ConnectionsSnapshot, CoreStatus, DelayResult, LogEntry, Profile } from "./mygo";

/**
 * 浏览器调试用 mock：模拟真实 mihomo IPC 的数据形态与节奏。
 * - 每秒 traffic 事件（随机游走曲线，偶发突发）
 * - 每秒 connections 快照（速率轻微变化）
 * - 每 5s memory 事件
 * - 13 个策略组 / 约 20 个节点 / 2 个订阅 / 延迟数据
 */

/* ---------------- mihomo /proxies 数据形态 ---------------- */

export interface MihomoDelayHistoryItem {
  time: string;
  delay: number;
}

export interface MihomoProxy {
  name: string;
  type: string;
  now?: string;
  all?: string[];
  history?: MihomoDelayHistoryItem[];
  udp?: boolean;
  alive?: boolean;
}

export interface MihomoConnectionsPayload {
  downloadTotal?: number;
  uploadTotal?: number;
  connections?: RawConnection[];
}

export interface RawConnection {
  id: string;
  upload: number;
  download: number;
  start: string;
  chains: string[];
  rule: string;
  rulePayload: string;
  metadata: {
    network?: string;
    type?: string;
    sourceIP?: string;
    destinationIP?: string;
    sourcePort?: string;
    destinationPort?: string;
    host?: string;
    process?: string;
    processPath?: string;
  };
}

/* ---------------- 节点与策略组 ---------------- */

interface NodeSeed {
  name: string;
  type: string;
  delay: number;
}

// 约 20 个节点，国旗 emoji 内嵌在名称里（与截图一致）
const NODE_SEEDS: NodeSeed[] = [
  { name: "🇭🇰 香港-Gemini-IEPL", type: "Shadowsocks", delay: 58 },
  { name: "🇭🇰 香港2-IEPL", type: "Shadowsocks", delay: 63 },
  { name: "🇭🇰 香港3-Gemini", type: "AnyTLS", delay: 71 },
  { name: "🇹🇼 台湾-IEPL", type: "Shadowsocks", delay: 92 },
  { name: "🇹🇼 台湾2-IEPL", type: "Shadowsocks", delay: 96 },
  { name: "🇹🇼 台湾3-Hinet", type: "Trojan", delay: 104 },
  { name: "🇸🇬 新加坡-Gemini-IEPL", type: "Shadowsocks", delay: 125 },
  { name: "🇸🇬 新加坡2-Gemini-IEPL", type: "Shadowsocks", delay: 131 },
  { name: "🇸🇬 新加坡3-Gemini", type: "AnyTLS", delay: 118 },
  { name: "🇯🇵 日本-IEPL-GPT", type: "Shadowsocks", delay: 148 },
  { name: "🇯🇵 日本2-IEPL-GPT", type: "Shadowsocks", delay: 152 },
  { name: "🇯🇵 日本3-Gemini", type: "AnyTLS", delay: 143 },
  { name: "🇯🇵 日本家宽-IEPL2", type: "Shadowsocks", delay: 167 },
  { name: "🇺🇸 美国-IEPL-GPT", type: "Shadowsocks", delay: 226 },
  { name: "🇺🇸 美国2-IEPL", type: "Hysteria2", delay: 231 },
  { name: "🇺🇸 美国3-Gemini", type: "Vmess", delay: 218 },
  { name: "🇰🇷 韩国-IEPL", type: "Shadowsocks", delay: 174 },
  { name: "🇬🇧 英国-IEPL", type: "Trojan", delay: 291 },
  { name: "🇩🇪 德国-IEPL", type: "Trojan", delay: 305 },
  { name: "🇲🇾 马来西亚-IEPL", type: "Shadowsocks", delay: 139 },
];

const NODE_NAMES = NODE_SEEDS.map((s) => s.name);

export const MAIN_GROUP = "🚀 节点选择";
export const AUTO_GROUP = "♻️ 自动选择";

// 13 个策略组
const GROUP_SEEDS: { name: string; icon: string; type: "Selector" | "URLTest"; now?: string }[] = [
  { name: MAIN_GROUP, icon: "🚀", type: "Selector", now: AUTO_GROUP },
  { name: AUTO_GROUP, icon: "♻️", type: "URLTest", now: "🇸🇬 新加坡-Gemini-IEPL" },
  { name: "ChatGPT", icon: "C", type: "Selector", now: "🇯🇵 日本2-IEPL-GPT" },
  { name: "Gemini", icon: "G", type: "Selector", now: NODE_NAMES[0] },
  { name: "Netflix", icon: "N", type: "Selector", now: "🇸🇬 新加坡-Gemini-IEPL" },
  { name: "哔哩哔哩", icon: "哔", type: "Selector", now: "DIRECT" },
  { name: "TikTok", icon: "T", type: "Selector", now: "🇹🇼 台湾-IEPL" },
  { name: "Twitter", icon: "T", type: "Selector", now: AUTO_GROUP },
  { name: "Copilot", icon: "C", type: "Selector", now: "🇺🇸 美国-IEPL-GPT" },
  { name: "微软服务", icon: "微", type: "Selector", now: "DIRECT" },
  { name: "谷歌服务", icon: "谷", type: "Selector", now: AUTO_GROUP },
  { name: "电报消息", icon: "T", type: "Selector", now: AUTO_GROUP },
  { name: "国内媒体", icon: "国", type: "Selector", now: "DIRECT" },
];

/* ---------------- 订阅 ---------------- */

const MOCK_PROFILES: Profile[] = [
  {
    id: "mock-profile-1",
    name: "飞鸟云",
    url: "https://example-sub1.example.com/api/v1/client/subscribe?token=mock1",
    active: false,
    uploadBytes: 38.4 * 1024 ** 3,
    downloadBytes: (532 - 38.4) * 1024 ** 3,
    totalBytes: 600 * 1024 ** 3,
    expire: "2026-12-31T23:59:59+08:00",
    updatedAt: new Date(Date.now() - 3600 * 1000).toISOString(),
    nodeCount: NODE_NAMES.length,
    autoUpdate: true,
    updateIntervalMin: 1440,
  },
  {
    id: "mock-profile-2",
    name: "八戒",
    url: "https://example-sub2.example.com/api/v1/client/subscribe?token=mock2",
    active: true,
    uploadBytes: 9.1 * 1024 ** 3,
    downloadBytes: (91.8 - 9.1) * 1024 ** 3,
    totalBytes: 100 * 1024 ** 3,
    expire: "",
    updatedAt: new Date(Date.now() - 7 * 3600 * 1000).toISOString(),
    nodeCount: NODE_NAMES.length,
    autoUpdate: false,
    updateIntervalMin: 1440,
  },
];

/* ---------------- 连接 ---------------- */

interface ConnSeed {
  host: string;
  rule: string;
  rulePayload: string;
  chain: string[];
  process?: string;
  down: number;
  up: number;
}

const CONN_SEEDS: ConnSeed[] = [
  { host: "chatgpt.com:443", rule: "DomainSuffix", rulePayload: "chatgpt.com", chain: [MAIN_GROUP, "ChatGPT", "🇯🇵 日本2-IEPL-GPT"], process: "mihomo", down: 207360, up: 95232 },
  { host: "1.12.12.12:443", rule: "GeoIP", rulePayload: "cn", chain: ["DIRECT"], down: 32358, up: 9421 },
  { host: "ab.chatgpt.com:443", rule: "DomainSuffix", rulePayload: "chatgpt.com", chain: [MAIN_GROUP, "ChatGPT", "🇯🇵 日本2-IEPL-GPT"], down: 6963, up: 27955 },
  { host: "dns.cloudflare.com:443", rule: "DomainSuffix", rulePayload: "cloudflare.com", chain: [MAIN_GROUP, AUTO_GROUP, "🇺🇸 美国2-IEPL"], down: 16077, up: 7885 },
  { host: "ab.chatgpt.com:443", rule: "DomainSuffix", rulePayload: "chatgpt.com", chain: [MAIN_GROUP, "ChatGPT", "🇯🇵 日本2-IEPL-GPT"], down: 5325, up: 16896 },
  { host: "ab.chatgpt.com:443", rule: "DomainSuffix", rulePayload: "chatgpt.com", chain: [MAIN_GROUP, "ChatGPT", "🇯🇵 日本3-Gemini"], down: 5325, up: 16896 },
  { host: "39.185.254.163:443", rule: "GeoIP", rulePayload: "cn", chain: ["DIRECT"], down: 7578, up: 3891 },
  { host: "8.8.4.4:853", rule: "Match", rulePayload: "", chain: [MAIN_GROUP, AUTO_GROUP, "🇺🇸 美国2-IEPL"], down: 7270, up: 2150 },
  { host: "a.nel.cloudflare.com:443", rule: "Match", rulePayload: "", chain: [MAIN_GROUP, AUTO_GROUP, "🇸🇬 新加坡-Gemini-IEPL"], down: 40, up: 7475 },
  { host: "a.nel.cloudflare.com:443", rule: "Match", rulePayload: "", chain: [MAIN_GROUP, AUTO_GROUP, "🇸🇬 新加坡-Gemini-IEPL"], down: 0, up: 7475 },
  { host: "dns.twnic.tw:443", rule: "GeoIP", rulePayload: "cn", chain: ["DIRECT"], down: 1536, up: 1536 },
  { host: "www.google.com:443", rule: "DomainSuffix", rulePayload: "google.com", chain: [MAIN_GROUP, "谷歌服务", "🇸🇬 新加坡-Gemini-IEPL"], process: "mihomo", down: 0, up: 1536 },
  { host: "30-courier.push.apple.com:5223", rule: "DomainSuffix", rulePayload: "apple.com", chain: ["DIRECT"], down: 5325, up: 10547 },
  { host: "gateway.icloud.com:443", rule: "DomainSuffix", rulePayload: "icloud.com", chain: ["DIRECT"], down: 21504, up: 10240 },
  { host: "ocsp.apple.com:443", rule: "GeoIP", rulePayload: "cn", chain: ["DIRECT"], down: 3072, up: 2048 },
  { host: "api.twitter.com:443", rule: "DomainSuffix", rulePayload: "twitter.com", chain: [MAIN_GROUP, "Twitter", "🇹🇼 台湾2-IEPL"], down: 18432, up: 9216 },
  { host: "vod3.wenicdn.com:443", rule: "GeoIP", rulePayload: "cn", chain: ["DIRECT"], down: 524288, up: 16384 },
  { host: "static.bilibili.com:443", rule: "DomainSuffix", rulePayload: "bilibili.com", chain: ["DIRECT"], down: 15872, up: 4301 },
];

/* ---------------- 引擎 ---------------- */

type Listener<T> = (payload: T) => void;

class MockEngine {
  status: CoreStatus = {
    running: true,
    version: "mihomo v1.19.33 (mock)",
    mode: "rule",
    mixedPort: 7890,
    systemProxy: true,
    tun: false,
    autoConnect: true,
    profileId: "mock-profile-2",
    message: "",
  };

  profiles: Profile[] = MOCK_PROFILES.map((p) => ({ ...p }));
  proxies: Record<string, MihomoProxy> = {};
  whitelist: string[] = ["apple.com", "steamcommunity.com", "192.168.0.0/16"];
  customNodes: string[] = [];
  private trafficListeners = new Set<Listener<{ up: number; down: number }>>();
  private connListeners = new Set<Listener<ConnectionsSnapshot>>();
  private memListeners = new Set<Listener<{ inuse: number; osLimit: number }>>();
  private statusListeners = new Set<Listener<CoreStatus>>();
  private upRate = 45 * 1024;
  private downRate = 1100 * 1024;
  private conns: RawConnection[] = [];
  private timers: ReturnType<typeof setInterval>[] = [];
  private started = false;

  constructor() {
    // 构建节点
    for (const seed of NODE_SEEDS) {
      this.proxies[seed.name] = {
        name: seed.name,
        type: seed.type,
        udp: true,
        alive: seed.delay > 0,
        history: [
          { time: new Date(Date.now() - 300_000).toISOString(), delay: seed.delay },
        ],
      };
    }
    this.proxies["DIRECT"] = { name: "DIRECT", type: "Direct" };
    this.proxies["REJECT"] = { name: "REJECT", type: "Reject" };

    // 构建策略组
    for (const g of GROUP_SEEDS) {
      const all =
        g.type === "URLTest"
          ? [...NODE_NAMES]
          : [AUTO_GROUP, "DIRECT", ...NODE_NAMES];
      this.proxies[g.name] = {
        name: g.name,
        type: g.type,
        now: g.now ?? all[0],
        all,
        udp: true,
        history: [{ time: new Date().toISOString(), delay: 120 }],
      };
    }

    // 构建连接
    this.conns = CONN_SEEDS.map((s, i) => this.makeConn(s, i));
  }

  private makeConn(seed: ConnSeed, i: number): RawConnection {
    const destPort = seed.host.split(":")[1] ?? "443";
    return {
      id: `mock-conn-${i}-${Math.random().toString(36).slice(2, 8)}`,
      upload: seed.up,
      download: seed.down,
      start: new Date(Date.now() - (60 + i * 137) * 1000).toISOString(),
      chains: seed.chain,
      rule: seed.rule,
      rulePayload: seed.rulePayload,
      metadata: {
        network: destPort === "5223" ? "tcp" : "tcp",
        type: "HTTPS",
        sourceIP: "192.168.1.23",
        destinationIP: seed.host.includes(":") && /^\d/.test(seed.host) ? seed.host.split(":")[0] : "104.18.32.7",
        sourcePort: String(49152 + i * 7),
        destinationPort: destPort,
        host: seed.host.split(":")[0],
        process: seed.process ?? "",
        processPath: "",
      },
    };
  }

  start() {
    if (this.started) return;
    this.started = true;
    this.timers.push(
      setInterval(() => this.tickTraffic(), 1000),
      setInterval(() => this.tickConnections(), 1000),
      setInterval(() => this.tickMemory(), 5000),
    );
  }

  stop() {
    this.timers.forEach(clearInterval);
    this.timers = [];
    this.started = false;
  }

  private tickTraffic() {
    // 随机游走 + 偶发突发，看起来像真实流量曲线
    const walk = (v: number, noise = 0.5, floor = 8 * 1024) => {
      const next = v * (1 + (Math.random() - 0.5) * noise * 2);
      return Math.max(floor, Math.min(next, 12 * 1024 * 1024));
    };
    this.upRate = walk(this.upRate);
    this.downRate = walk(this.downRate);
    if (Math.random() < 0.12) this.downRate *= 1.6 + Math.random();
    if (Math.random() < 0.3) this.upRate *= 0.6 + Math.random() * 0.8;
    const payload = { up: Math.round(this.upRate), down: Math.round(this.downRate) };
    this.trafficListeners.forEach((fn) => fn(payload));
  }

  private tickConnections() {
    for (const c of this.conns) {
      const active = c.id.endsWith("a") || Math.random() < 0.85;
      if (active) {
        c.download += Math.round(Math.random() * 40 * 1024);
        c.upload += Math.round(Math.random() * 12 * 1024);
      }
    }
    // 偶尔新增/移除一条连接，保持列表有生命感（列表快空时补充更快）
    const addChance = this.conns.length < 4 ? 0.6 : 0.08;
    if (Math.random() < addChance && this.conns.length < 24) {
      const seed = CONN_SEEDS[Math.floor(Math.random() * CONN_SEEDS.length)];
      this.conns.unshift(this.makeConn(seed, this.conns.length));
    }
    if (this.conns.length > 24) this.conns.pop();

    let downloadTotal = 0;
    let uploadTotal = 0;
    for (const c of this.conns) {
      downloadTotal += c.download;
      uploadTotal += c.upload;
    }
    this.connListeners.forEach((fn) =>
      fn({
        data: {
          downloadTotal,
          uploadTotal,
          connections: JSON.parse(JSON.stringify(this.conns)) as unknown,
        },
      }),
    );
  }

  private tickMemory() {
    const inuse = 85 * 1024 * 1024 + Math.round((Math.random() - 0.5) * 6 * 1024 * 1024);
    this.memListeners.forEach((fn) => fn({ inuse, osLimit: 0 }));
  }

  pushStatus() {
    const snapshot = { ...this.status };
    this.statusListeners.forEach((fn) => fn(snapshot));
  }

  // ---- 模拟服务调用 ----

  async getStatus(): Promise<CoreStatus> {
    await delay(60);
    return { ...this.status };
  }

  async startCore() {
    await delay(500);
    this.status.running = true;
    this.status.message = "";
    this.pushStatus();
  }

  async stopCore() {
    await delay(300);
    this.status.running = false;
    this.status.systemProxy = false;
    this.pushStatus();
  }

  async setMode(mode: string) {
    await delay(150);
    this.status.mode = mode;
    this.pushStatus();
  }

  async setSystemProxy(on: boolean) {
    await delay(400);
    this.status.systemProxy = on;
    this.pushStatus();
  }

  async setPort(port: number) {
    await delay(200);
    this.status.mixedPort = port;
    this.pushStatus();
  }

  async setAutoConnect(on: boolean) {
    await delay(100);
    this.status.autoConnect = on;
    this.pushStatus();
  }

  /** 模拟 SetTun：需要管理员授权，仿真 2s 授权/切换延迟后生效。 */
  async setTun(on: boolean) {
    await delay(2000);
    this.status.tun = on;
    this.pushStatus();
  }

  async select(group: string, name: string) {
    await delay(120);
    const g = this.proxies[group];
    if (g && g.all) g.now = name;
  }

  async proxyDelay(name: string): Promise<DelayResult> {
    await delay(400 + Math.random() * 900);
    const base = NODE_SEEDS.find((s) => s.name === name)?.delay ?? 150;
    const jitter = Math.round((Math.random() - 0.35) * 60);
    const failed = Math.random() < 0.06;
    const delayMs = failed ? -1 : Math.max(20, base + jitter);
    this.recordDelay(name, delayMs);
    return {
      name,
      delay: delayMs,
      message: failed ? "An error occurred in the delay test" : "",
    };
  }

  async groupDelay(group: string): Promise<Record<string, number>> {
    // 慢速仿真（2.2s+）：给前端验证「组测速中间态不闪超时」留出观察窗口
    await delay(2200 + Math.random() * 800);
    const g = this.proxies[group];
    const result: Record<string, number> = {};
    if (!g?.all) return result;
    for (const name of g.all) {
      const seed = NODE_SEEDS.find((s) => s.name === name);
      if (!seed) continue; // DIRECT / 内部组跳过
      if (Math.random() < 0.06) {
        this.recordDelay(name, 0);
      } else {
        const d = Math.max(20, seed.delay + Math.round((Math.random() - 0.3) * 70));
        this.recordDelay(name, d);
        result[name] = d;
      }
    }
    return result;
  }

  private recordDelay(name: string, delayMs: number) {
    const p = this.proxies[name];
    if (!p) return;
    p.history = [
      ...(p.history ?? []),
      { time: new Date().toISOString(), delay: delayMs > 0 ? delayMs : 0 },
    ].slice(-8);
  }

  async listProfiles(): Promise<Profile[]> {
    await delay(80);
    return this.profiles.map((p) => ({ ...p }));
  }

  async addProfile(url: string, name: string): Promise<Profile> {
    await delay(1200);
    if (!/^https?:\/\//i.test(url)) throw new Error("订阅地址必须是 http(s):// 链接");
    const id = `mock-profile-${Date.now()}`;
    const p: Profile = {
      id,
      name: name || "新建订阅",
      url,
      active: false,
      uploadBytes: 1.2 * 1024 ** 3,
      downloadBytes: 24.8 * 1024 ** 3,
      totalBytes: 200 * 1024 ** 3,
      expire: "",
      updatedAt: new Date().toISOString(),
      nodeCount: NODE_NAMES.length,
      autoUpdate: false,
      updateIntervalMin: 1440,
    };
    this.profiles.push(p);
    return { ...p };
  }

  async updateProfile(id: string): Promise<Profile> {
    await delay(900);
    const p = this.profiles.find((x) => x.id === id);
    if (!p) throw new Error("订阅不存在");
    p.updatedAt = new Date().toISOString();
    return { ...p };
  }

  async removeProfile(id: string) {
    await delay(200);
    this.profiles = this.profiles.filter((x) => x.id !== id);
  }

  async activateProfile(id: string) {
    await delay(700);
    const p = this.profiles.find((x) => x.id === id);
    if (!p) throw new Error("订阅不存在");
    for (const x of this.profiles) x.active = x.id === id;
    this.status.profileId = id;
    this.pushStatus();
  }

  async editProfile(id: string, name: string, url: string, autoUpdate: boolean, updateIntervalMin: number): Promise<Profile> {
    await delay(900);
    const p = this.profiles.find((x) => x.id === id);
    if (!p) throw new Error("订阅不存在");
    if (!/^https?:\/\//i.test(url)) throw new Error("订阅地址必须是 http(s):// 链接");
    if (autoUpdate && (!Number.isInteger(updateIntervalMin) || updateIntervalMin < 30 || updateIntervalMin > 10080)) {
      throw new Error("自动更新间隔必须是 30–10080 的整数（分钟）");
    }
    if (name.trim()) p.name = name.trim();
    p.url = url.trim();
    p.autoUpdate = autoUpdate;
    p.updateIntervalMin = autoUpdate ? updateIntervalMin : 1440;
    p.updatedAt = new Date().toISOString();
    return { ...p };
  }

  // ---- 自定义节点 ----

  /** 从分享链接里取节点名：优先 URI fragment（#名称），否则生成占位名。 */
  private parseNodeName(link: string, index: number): string {
    const m = link.match(/#([^#?]+)/);
    if (m) {
      try {
        const decoded = decodeURIComponent(m[1]);
        if (decoded.trim()) return decoded.trim();
      } catch {
        if (m[1].trim()) return m[1].trim();
      }
    }
    return `自定义节点 ${index}`;
  }

  private guessNodeType(link: string): string {
    const lower = link.toLowerCase();
    if (lower.startsWith("vless://")) return "Vless";
    if (lower.startsWith("vmess://")) return "Vmess";
    if (lower.startsWith("ss://")) return "Shadowsocks";
    if (lower.startsWith("hysteria2://") || lower.startsWith("hysteria://")) return "Hysteria2";
    if (lower.startsWith("trojan://")) return "Trojan";
    return "Unknown";
  }

  async addNodesFromLinks(raw: string): Promise<number> {
    await delay(700);
    const lines = raw
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter(Boolean);
    let ok = 0;
    for (const line of lines) {
      const type = this.guessNodeType(line);
      if (type === "Unknown") continue; // 非法链接行：不计入成功数
      const name = this.parseNodeName(line, this.customNodes.length + 1);
      if (this.customNodes.includes(name)) continue; // 同名去重
      ok += 1;
      this.customNodes.push(name);
      // 同时并入代理表与全部策略组，模拟后端「与订阅节点合并进策略组」
      this.proxies[name] = { name, type, udp: true, alive: true, history: [] };
      for (const g of Object.values(this.proxies)) {
        if (Array.isArray(g.all) && !g.all.includes(name)) g.all.push(name);
      }
    }
    return ok;
  }

  async listNodes(): Promise<string[]> {
    await delay(60);
    return [...this.customNodes];
  }

  async removeNode(name: string) {
    await delay(150);
    this.customNodes = this.customNodes.filter((n) => n !== name);
    delete this.proxies[name];
    for (const g of Object.values(this.proxies)) {
      if (Array.isArray(g.all)) g.all = g.all.filter((n) => n !== name);
    }
  }

  // ---- 直连白名单 ----

  async getWhitelist(): Promise<string[]> {
    await delay(80);
    return [...this.whitelist];
  }

  async setWhitelist(domains: string[]) {
    await delay(250);
    this.whitelist = domains.map((d) => d.trim()).filter(Boolean);
  }

  async currentProfile(): Promise<Profile> {
    await delay(40);
    return { ...(this.profiles.find((x) => x.active) ?? this.profiles[0]) };
  }

  async testURL(): Promise<number> {
    await delay(300 + Math.random() * 700);
    return this.status.running ? 204 : 0;
  }

  async publicIP(): Promise<string> {
    await delay(300 + Math.random() * 600);
    if (!this.status.running) throw new Error("内核未运行");
    return "185.151.146.146";
  }

  async deleteConnection(id: string) {
    await delay(120);
    this.conns = this.conns.filter((c) => c.id !== id);
  }

  async closeAllConnections() {
    await delay(200);
    this.conns = [];
  }

  // ---- 事件订阅 ----

  onTraffic(fn: Listener<{ up: number; down: number }>) {
    this.start();
    this.trafficListeners.add(fn);
    return () => this.trafficListeners.delete(fn);
  }

  onConnections(fn: Listener<ConnectionsSnapshot>) {
    this.start();
    this.connListeners.add(fn);
    return () => this.connListeners.delete(fn);
  }

  onMemory(fn: Listener<{ inuse: number; osLimit: number }>) {
    this.start();
    this.memListeners.add(fn);
    return () => this.memListeners.delete(fn);
  }

  onStatus(fn: Listener<CoreStatus>) {
    this.start();
    this.statusListeners.add(fn);
    return () => this.statusListeners.delete(fn);
  }

  getAllProxies(): Record<string, MihomoProxy> {
    return this.proxies;
  }
}

function delay(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

export const mockEngine = new MockEngine();
