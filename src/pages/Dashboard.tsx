import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, AUTO_GROUP, MAIN_GROUP, delayTestURL, latestDelay } from "../api";
import type { CoreStatus, MihomoConnectionsPayload, MihomoProxy } from "../api";
import { formatBytes, formatRate } from "../format";
import { Sparkline, Switch, Segmented } from "../components/ui";
import {
  IconCheckCircle,
  IconClock,
  IconConnections,
  IconDownload,
  IconPause,
  IconPlay,
  IconRule,
  IconShuffle,
  IconShield,
  IconTarget,
  IconGlobe,
  IconUpload,
  IconZap,
} from "../components/Icons";
import { useToast } from "../components/Toast";

/** 仪表盘：智能模式 + 一键连接（公网 IP/人话状态行）+ 速率曲线 + 分流模式 + 实时连接摘要 + 节点分组。 */

const MODES = [
  { value: "rule", label: "自动分流", icon: <IconRule size={16} /> },
  { value: "global", label: "全局加速", icon: <IconGlobe size={16} /> },
  { value: "direct", label: "全部直连", icon: <IconTarget size={16} /> },
] as const;

/** 取名称首字符（组名自带 emoji 时直接展示）。 */
function nameAvatar(name: string): string {
  const ch = Array.from(name.trim())[0];
  return ch && /[a-zA-Z0-9]/.test(ch) ? ch.toUpperCase() : (ch ?? "?");
}

function BigRate({ value }: { value: number }) {
  const [num, unit] = formatBytes(value, 1).split(" ");
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 10 }}>
      <span style={{ fontSize: 34, fontWeight: 700, fontVariantNumeric: "tabular-nums" }}>{num}</span>
      <span className="muted" style={{ fontSize: 14 }}>
        {unit}/s
      </span>
    </div>
  );
}

/** 内核自动重试类 message（此时用户视角是"连接失败，正在自动重试"）。 */
function isRetryingMessage(msg: string): boolean {
  return /失去响应|自动重启|自动恢复/.test(msg);
}

export default function Dashboard({
  status,
  goProxies,
  smartMode,
  onSmartModeChange,
}: {
  status: CoreStatus;
  goProxies: (group?: string) => void;
  smartMode: boolean;
  onSmartModeChange: (on: boolean) => void;
}) {
  const { showToast } = useToast();

  // ---- 流量曲线 ----
  const [upHist, setUpHist] = useState<number[]>([]);
  const [downHist, setDownHist] = useState<number[]>([]);
  const [totals, setTotals] = useState({ up: 0, down: 0 });
  const peaks = useRef({ up: 0, down: 0 });
  const [, setPeakTick] = useState(0);

  useEffect(() => {
    const un = api.onTraffic((t) => {
      peaks.current.up = Math.max(peaks.current.up, t.up);
      peaks.current.down = Math.max(peaks.current.down, t.down);
      setTotals((prev) => ({ up: prev.up + t.up, down: prev.down + t.down }));
      setUpHist((prev) => [...prev.slice(-59), t.up]);
      setDownHist((prev) => [...prev.slice(-59), t.down]);
      setPeakTick((x) => x + 1);
    });
    return un;
  }, []);

  // ---- 节点表（推算当前实际出口） ----
  const [table, setTable] = useState<Record<string, MihomoProxy>>({});
  const [groups, setGroups] = useState<MihomoProxy[]>([]);

  const refreshGroups = useCallback(async () => {
    try {
      const { all, groups: gs } = await api.getProxies();
      setTable(all);
      setGroups(gs);
    } catch {
      /* 内核未运行时静默 */
    }
  }, []);

  useEffect(() => {
    refreshGroups();
    const t = setInterval(refreshGroups, 8000);
    return () => clearInterval(t);
  }, [refreshGroups, status.running]);

  // 智能模式刚切换时，稍等选中生效后立即刷新出口展示
  useEffect(() => {
    const t = setTimeout(refreshGroups, 700);
    return () => clearTimeout(t);
  }, [smartMode, refreshGroups]);

  /** 实际出口节点：主组选中自动组时看自动组的当前节点。 */
  const exitInfo = useMemo(() => {
    const main = table[MAIN_GROUP];
    if (!main) return null;
    const isAuto = main.now === AUTO_GROUP;
    const exitName = isAuto ? table[AUTO_GROUP]?.now : main.now;
    const node = exitName ? table[exitName] : undefined;
    const delay = node ? latestDelay(node) : undefined;
    return { exitName: exitName ?? "", delay: delay ?? undefined, isAuto };
  }, [table]);

  // ---- 公网出口 IP ----
  const [pubIP, setPubIP] = useState("");
  useEffect(() => {
    if (!status.running) {
      setPubIP("");
      return;
    }
    let alive = true;
    const fetchIP = async () => {
      const ip = await api.publicIP();
      if (alive) setPubIP(ip);
    };
    fetchIP();
    const t = setInterval(fetchIP, 60000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [status.running]);

  // ---- 运行时长 ----
  const [uptime, setUptime] = useState(0);
  useEffect(() => {
    if (!status.running) {
      setUptime(0);
      return;
    }
    const startedAt = Date.now();
    setUptime(0);
    const t = setInterval(() => setUptime(Math.floor((Date.now() - startedAt) / 1000)), 1000);
    return () => clearInterval(t);
  }, [status.running]);

  // ---- 连通性检测（AppService.testURL，顺带得到体验质量） ----
  const [checking, setChecking] = useState(false);
  const [latency, setLatency] = useState<number | null>(null);
  const [reachOk, setReachOk] = useState<boolean | null>(null);
  const runCheck = useCallback(async () => {
    if (checking) return;
    setChecking(true);
    const t0 = Date.now();
    try {
      const code = await api.testURL(delayTestURL);
      const ms = Date.now() - t0;
      setLatency(ms);
      setReachOk(code >= 200 && code < 400);
    } catch {
      setLatency(null);
      setReachOk(false);
    } finally {
      setChecking(false);
    }
  }, [checking]);

  useEffect(() => {
    if (status.running) runCheck();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status.running]);

  // ---- 智能模式开关 ----
  const handleSmartToggle = (on: boolean) => {
    onSmartModeChange(on);
    if (!on) {
      showToast("已关闭智能模式，可在「代理」页手动选择节点", "info");
    }
    // 开启时的自动选中由 App 统一断言并提示
  };

  // ---- 一键连接 / 断开 ----
  const [coreBusy, setCoreBusy] = useState(false);
  const toggleCore = async () => {
    if (coreBusy) return;
    setCoreBusy(true);
    try {
      if (status.running) {
        await api.stopCore();
        showToast("已断开连接，网络恢复直连", "success");
      } else {
        await api.startCore();
        // 一键连接：内核起来后顺手接管系统网络（小白一步到位）
        if (!status.systemProxy) {
          try {
            await api.setSystemProxy(true);
          } catch {
            showToast("已连接，但系统加速开启失败，可在仪表盘手动打开「系统加速」", "error");
          }
        }
        showToast("已连接 · 网络已加速", "success");
      }
    } catch (e) {
      showToast(`连接失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setCoreBusy(false);
    }
  };

  // ---- 分流模式 / 系统加速 ----
  const setMode = async (mode: string) => {
    try {
      await api.setMode(mode);
    } catch (e) {
      showToast(`切换模式失败：${e instanceof Error ? e.message : String(e)}`, "error");
    }
  };
  const setSysProxy = async (on: boolean) => {
    try {
      await api.setSystemProxy(on);
      showToast(on ? "已开启网络加速" : "已关闭网络加速", "success");
    } catch (e) {
      showToast(`设置失败：${e instanceof Error ? e.message : String(e)}`, "error");
    }
  };

  // ---- 虚拟网卡（TUN）：开启需管理员授权，状态经 StatusEvent 回推 ----
  const [tunBusy, setTunBusy] = useState(false);
  const setTun = async (on: boolean) => {
    if (tunBusy) return;
    setTunBusy(true);
    try {
      await api.setTun(on);
      showToast(on ? "虚拟网卡已开启" : "虚拟网卡已关闭", "success");
    } catch (e) {
      showToast(`虚拟网卡设置失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setTunBusy(false);
    }
  };

  // ---- 实时连接摘要 ----
  const [conns, setConns] = useState<MihomoConnectionsPayload>({ connections: [] });
  useEffect(() => {
    const un = api.onConnections((snap) => {
      setConns(api.parseConnections(snap));
    });
    return un;
  }, []);

  const connRows = useMemo(() => (conns.connections ?? []).slice(0, 6), [conns]);

  // ---- 人话状态行 ----
  const retrying = !status.running && isRetryingMessage(status.message);
  let stateLine: { text: string; cls: string };
  if (coreBusy) {
    stateLine = { text: "正在连接…", cls: "busy" };
  } else if (retrying) {
    stateLine = { text: "连接失败，正在自动重试…", cls: "bad" };
  } else if (status.running) {
    const exit = exitInfo?.exitName;
    const delay = exitInfo?.delay;
    stateLine = {
      text:
        exit && delay && delay > 0
          ? `已连接 · 走 ${exit} 节点 · 延迟 ${delay}ms`
          : exit
            ? `已连接 · 走 ${exit} 节点`
            : "已连接",
      cls: "ok",
    };
  } else {
    stateLine = { text: "未连接 · 点击右侧按钮一键连接", cls: "idle" };
  }

  const quality =
    reachOk === null ? null : reachOk ? (latency !== null && latency < 1500 ? "优" : "良") : null;

  const modeText = status.mode === "global" ? "全局加速" : status.mode === "direct" ? "全部直连" : "自动分流";

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">仪表盘</h1>
        <div className="spacer" />
        {status.running && reachOk !== null && (
          <span className={`pill ${reachOk ? "green" : "red"}`} style={{ fontSize: 13 }}>
            <IconCheckCircle size={15} />
            {reachOk ? `连通 ${latency ?? "?"} ms` : "出口不可达"}
          </span>
        )}
      </div>

      <div className="dash-grid">
        {/* 智能模式（醒目置顶） */}
        <div className={`card smart-card${smartMode ? " on" : ""}`}>
          <span className="smart-icon">
            <IconZap size={22} />
          </span>
          <div className="smart-text">
            <div className="smart-title">
              智能模式
              <span className={`pill ${smartMode ? "green" : ""}`}>{smartMode ? "已开启" : "已关闭"}</span>
            </div>
            <div className="dim" style={{ fontSize: 13, marginTop: 2 }}>
              自动为你选择最快的节点，断线自动切换
            </div>
          </div>
          <div style={{ flex: 1 }} />
          <Switch on={smartMode} onChange={handleSmartToggle} title="智能模式" />
        </div>

        {/* 状态卡 */}
        <div className="card dash-status">
          <div className="card-title-row">
            <span className="pill" style={{ background: "var(--pill-bg)" }}>
              状态
            </span>
            <span className={`pill ${status.running ? "green" : ""}`}>
              {status.running ? "运行中" : "已停止"}
            </span>
            {quality && status.running && <span className="pill green">体验 {quality}</span>}
            <span className="pill">端口 {status.mixedPort}</span>
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 24, marginTop: 16 }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div className="dim" style={{ fontSize: 12 }}>
                公网出口
              </div>
              <div
                style={{
                  fontSize: 30,
                  fontWeight: 700,
                  letterSpacing: 0.5,
                  fontVariantNumeric: "tabular-nums",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  lineHeight: 1.25,
                }}
              >
                {status.running ? (pubIP || "检测中…") : "—"}
              </div>
              <div className={`state-line ${stateLine.cls}`}>{stateLine.text}</div>
            </div>
            <button
              className="big-round-btn"
              onClick={toggleCore}
              disabled={coreBusy}
              title={status.running ? "一键断开" : "一键连接"}
            >
              {coreBusy ? (
                <span className="spin" style={{ display: "inline-flex" }}>
                  <IconClock size={26} />
                </span>
              ) : status.running ? (
                <IconPause size={26} />
              ) : (
                <IconPlay size={26} />
              )}
            </button>
          </div>

          <div style={{ display: "flex", gap: 48, marginTop: 18 }}>
            <div>
              <div className="dim" style={{ fontSize: 12 }}>
                运行时长
              </div>
              <div style={{ fontSize: 18, fontWeight: 600, fontVariantNumeric: "tabular-nums", marginTop: 4 }}>
                {formatDurationText(uptime)}
              </div>
            </div>
            <div>
              <div className="dim" style={{ fontSize: 12 }}>
                模式
              </div>
              <div style={{ fontSize: 18, fontWeight: 600, marginTop: 4 }}>{modeText}</div>
            </div>
          </div>
        </div>

        {/* 上传速率 */}
        <div className="card dash-traffic">
          <div className="card-title-row">
            <IconUpload size={16} />
            <span>上传</span>
            <div className="spacer" />
            <span className="dim">峰值 {formatRate(peaks.current.up)}</span>
          </div>
          <BigRate value={upHist[upHist.length - 1] ?? 0} />
          <div className="dim" style={{ fontSize: 12, margin: "6px 0 4px" }}>
            累计 {formatBytes(totals.up)}
          </div>
          <Sparkline values={upHist} />
        </div>

        {/* 下载速率 */}
        <div className="card dash-traffic">
          <div className="card-title-row">
            <IconDownload size={16} />
            <span>下载</span>
            <div className="spacer" />
            <span className="dim">峰值 {formatRate(peaks.current.down)}</span>
          </div>
          <BigRate value={downHist[downHist.length - 1] ?? 0} />
          <div className="dim" style={{ fontSize: 12, margin: "6px 0 4px" }}>
            累计 {formatBytes(totals.down)}
          </div>
          <Sparkline values={downHist} />
        </div>

        {/* 分流模式 */}
        <div className="card dash-mode">
          <div className="card-title-row">
            <IconRule size={16} />
            <span>模式</span>
          </div>
          <div style={{ marginTop: 14 }}>
            <Segmented
              options={MODES.map((m) => ({
                value: m.value,
                label: (
                  <>
                    {m.icon}
                    {m.label}
                  </>
                ),
              }))}
              value={(status.mode as "rule" | "global" | "direct") ?? "rule"}
              onChange={(v) => setMode(v)}
            />
          </div>
        </div>

        {/* 系统加速 */}
        <div className="card dash-switch-card">
          <div className="card-title-row">
            <IconShuffle size={16} />
            <span>系统加速</span>
            <div className="spacer" />
            <Switch
              on={status.systemProxy}
              disabled={!status.running}
              title={status.running ? undefined : "连接后自动开启"}
              onChange={setSysProxy}
            />
          </div>
          <div className="dim" style={{ fontSize: 12, marginTop: 8 }}>
            {status.running
              ? status.systemProxy
                ? "已接管浏览器和常用软件的网络"
                : "未开启，网页流量不走加速"
              : "一键连接后自动开启"}
          </div>
        </div>

        {/* 虚拟网卡（TUN，需管理员授权） */}
        <div className="card dash-switch-card">
          <div className="card-title-row">
            <IconShield size={16} />
            <span>虚拟网卡</span>
            <div className="spacer" />
            <Switch
              on={status.tun}
              disabled={!status.running || tunBusy}
              title={!status.running ? "需要管理员授权" : tunBusy ? "正在授权…" : undefined}
              onChange={setTun}
            />
          </div>
          <div className="dim" style={{ fontSize: 12, marginTop: 8 }}>
            {!status.running
              ? "需要管理员授权"
              : tunBusy
                ? "正在授权，内核将切换为特权进程…"
                : status.tun
                  ? "已接管全局流量（TUN 模式）"
                  : "开启需管理员授权，内核切换为特权进程"}
          </div>
        </div>

        {/* 实时连接摘要 */}
        <div className="card dash-conn">
          <div className="card-title-row" style={{ marginBottom: 12 }}>
            <span className="pill blue" style={{ fontSize: 13, padding: "5px 14px" }}>
              <IconConnections size={14} />
              {(conns.connections ?? []).length} 实时连接
            </span>
          </div>
          <div style={{ overflow: "hidden" }}>
            <table className="table">
              <thead>
                <tr>
                  <th style={{ width: "18%" }}>来源</th>
                  <th style={{ width: "34%" }}>目标</th>
                  <th style={{ width: "22%" }}>出口</th>
                  <th className="num">下载</th>
                  <th className="num">上传</th>
                </tr>
              </thead>
              <tbody>
                {connRows.length === 0 && (
                  <tr>
                    <td colSpan={5} className="dim" style={{ textAlign: "center", padding: "18px 0" }}>
                      {status.running ? "暂无连接" : "未连接"}
                    </td>
                  </tr>
                )}
                {connRows.map((c) => (
                  <tr key={c.id}>
                    <td className="dim">{c.metadata.process || c.metadata.sourceIP || "—"}</td>
                    <td title={targetOf(c)}>
                      {targetOf(c)}
                    </td>
                    <td>
                      <span className="pill" style={{ maxWidth: "100%" }}>
                        <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{exitOf(c)}</span>
                      </span>
                    </td>
                    <td className="num">{formatBytes(c.download)}</td>
                    <td className="num">{formatBytes(c.upload)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        {/* 节点分组 */}
        <div className="card dash-groups">
          <div className="card-title-row" style={{ marginBottom: 12 }}>
            <span>节点分组</span>
            <span className="pill">{groups.length}</span>
            <div className="spacer" />
          </div>
          <div className="group-mini-grid">
            {groups.map((g) => (
              <button key={g.name} className="group-mini" onClick={() => goProxies(g.name)} title={g.name}>
                <span className="avatar">{nameAvatar(g.name)}</span>
                <span className="group-mini-text">
                  <span className="group-mini-name">{g.name}</span>
                  <span className="dim" style={{ fontSize: 12, overflow: "hidden", textOverflow: "ellipsis" }}>
                    {g.now ?? "—"}
                  </span>
                </span>
              </button>
            ))}
            {groups.length === 0 && (
              <div className="dim" style={{ fontSize: 13, padding: "8px 2px" }}>
                {status.running ? "加载中…" : "未连接"}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function targetOf(c: MihomoConnectionsPayload["connections"] extends (infer T)[] | undefined ? T : never): string {
  const host = c.metadata.host;
  const port = c.metadata.destinationPort;
  if (host) return `${host}:${port}`;
  return `${c.metadata.destinationIP || "?"}:${port}`;
}

function exitOf(c: { chains: string[] }): string {
  if (c.chains.length === 0) return "—";
  const last = c.chains[c.chains.length - 1];
  return last === "DIRECT" ? "DIRECT" : last;
}

function formatDurationText(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const pad = (x: number) => String(x).padStart(2, "0");
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}
