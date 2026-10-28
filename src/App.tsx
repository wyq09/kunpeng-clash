import React, { useCallback, useEffect, useRef, useState } from "react";
import { api, AUTO_GROUP, MAIN_GROUP } from "./api";
import type { CoreStatus } from "./api";
import { isMock } from "./api";
import { initTheme } from "./theme";
import Dashboard from "./pages/Dashboard";
import Proxies from "./pages/Proxies";
import Profiles from "./pages/Profiles";
import Connections from "./pages/Connections";
import Tools from "./pages/Tools";
import ThemePage from "./pages/ThemePage";
import Agent from "./pages/Agent";
import logoUrl from "./assets/logo.png";
import {
  IconBot,
  IconConnections,
  IconDashboard,
  IconFile,
  IconGlobe,
  IconSidebar,
  IconTool,
} from "./components/Icons";
import { useToast } from "./components/Toast";

// 主题在任何渲染前先落到 <html> 属性与 CSS 变量上（避免首帧闪默认色）
initTheme();

export type PageId = "dashboard" | "proxies" | "profiles" | "connections" | "agent" | "tools";

const NAV: { id: PageId; label: string; icon: React.ReactNode }[] = [
  { id: "dashboard", label: "仪表盘", icon: <IconDashboard /> },
  { id: "proxies", label: "代理", icon: <IconGlobe /> },
  { id: "profiles", label: "配置", icon: <IconFile /> },
  { id: "connections", label: "实时连接", icon: <IconConnections /> },
  { id: "agent", label: "智能体", icon: <IconBot /> },
  { id: "tools", label: "工具", icon: <IconTool /> },
];

const SMART_MODE_KEY = "kp.smartMode";

/** 把内核的 StatusEvent.message 翻译成人话（英文原文不直接弹出）。 */
export function humanizeCoreMessage(msg: string): { text: string; type: "info" | "error" } {
  if (msg.includes("自动切换到")) {
    return { text: msg.replace("当前出口连续不可用", "当前节点连续失联"), type: "info" };
  }
  if (msg.includes("失去响应") || msg.includes("正在自动恢复")) {
    return { text: "网络出现波动，正在自动恢复连接…", type: "info" };
  }
  if (msg.includes("自动重启失败")) {
    return { text: "自动恢复失败，请点击重新连接", type: "error" };
  }
  if (msg.includes("自动重启") || msg.includes("自动恢复")) {
    return { text: "连接出现异常，正在自动恢复…", type: "info" };
  }
  // 没有中文 → 视为原始错误日志，不弹原文
  if (!/[\u4e00-\u9fff]/.test(msg)) {
    return { text: "连接出现异常，正在自动恢复…", type: "error" };
  }
  return { text: msg, type: "error" };
}

export default function App() {
  const { showToast } = useToast();
  const [collapsed, setCollapsed] = useState(false);
  const [page, setPage] = useState<PageId>("dashboard");
  const [toolsSubpage, setToolsSubpage] = useState<"theme" | null>(null);
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [initialGroup, setInitialGroup] = useState<string | undefined>(undefined);
  const [smartMode, setSmartMode] = useState<boolean>(() => {
    try {
      const v = localStorage.getItem(SMART_MODE_KEY);
      return v === null ? true : v === "1";
    } catch {
      return true;
    }
  });
  const lastMessageRef = useRef<string>("");
  const contentRef = useRef<HTMLElement>(null);

  useEffect(() => {
    contentRef.current?.scrollTo({ top: 0 });
  }, [page, toolsSubpage]);

  const changeSmartMode = useCallback((on: boolean) => {
    setSmartMode(on);
    try {
      localStorage.setItem(SMART_MODE_KEY, on ? "1" : "0");
    } catch {
      /* 忽略存储失败 */
    }
  }, []);

  useEffect(() => {
    let alive = true;
    api
      .getStatus()
      .then((s) => {
        if (!alive) return;
        setStatus(s);
        lastMessageRef.current = s.message; // 初始 message 是旧状态，不弹
      })
      .catch((e) => alive && setLoadError(e instanceof Error ? e.message : String(e)));
    const un = api.onStatus((s) => {
      setStatus(s);
      if (s.message && s.message !== lastMessageRef.current) {
        const { text, type } = humanizeCoreMessage(s.message);
        showToast(text, type);
      }
      lastMessageRef.current = s.message;
    });
    return () => {
      alive = false;
      un();
    };
  }, [showToast]);

  // 智能模式：开启且内核运行时，确保 🚀 节点选择 选中 ♻️ 自动选择（测速最优 + 故障自动切换）
  useEffect(() => {
    if (!status?.running || !smartMode) return;
    const assert = async () => {
      try {
        const { all } = await api.getProxies();
        const main = all[MAIN_GROUP];
        if (!main) return;
        if (main.now !== AUTO_GROUP) {
          await api.select(MAIN_GROUP, AUTO_GROUP);
          showToast("智能模式：已切换为自动选择最快节点", "success");
        }
      } catch {
        /* 组未加载（内核刚启动）时静默 */
      }
    };
    assert();
    // 内核刚启动时组列表可能还没就绪，稍后补一次（已选中则无副作用）
    const t = setTimeout(assert, 3000);
    return () => clearTimeout(t);
  }, [status?.running, smartMode, showToast]);

  // 智能体改了配置：提示用户 + 让订阅/代理页重新拉数据；侧栏「智能体」亮起活动点
  const [dataRev, setDataRev] = useState(0);
  const [agentLiveAt, setAgentLiveAt] = useState(0);
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined;
    const un = api.onAgent((c) => {
      setAgentLiveAt(Date.now());
      if (!c.mutates || !c.ok) return;
      showToast(`智能体 · ${c.title}`, "success");
      clearTimeout(t);
      t = setTimeout(() => setDataRev((v) => v + 1), 400);
    });
    return () => {
      clearTimeout(t);
      un();
    };
  }, [showToast]);
  const [, setLiveTick] = useState(0);
  useEffect(() => {
    if (!agentLiveAt) return;
    const t = setTimeout(() => setLiveTick((x) => x + 1), 4000);
    return () => clearTimeout(t);
  }, [agentLiveAt]);
  const agentLive = Date.now() - agentLiveAt < 4000;

  const goProxies = useCallback((group?: string) => {
    setInitialGroup(group);
    setPage("proxies");
    setToolsSubpage(null);
  }, []);

  const changePage = useCallback((id: PageId) => {
    setPage(id);
    setToolsSubpage(null);
  }, []);

  if (status === null) {
    return (
      <div
        style={{
          height: "100vh",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--text-2)",
          flexDirection: "column",
          gap: 12,
        }}
      >
        <img className={loadError ? undefined : "splash-logo"} src={logoUrl} alt="logo" width={56} height={56} style={{ borderRadius: 12 }} />
        <div style={{ fontSize: 22, fontWeight: 700, color: "var(--text)" }}>鲲鹏 Clash</div>
        {loadError ? (
          <div style={{ color: "var(--red)" }}>初始化失败：{loadError}</div>
        ) : (
          <div className="dim splash-text">正在连接内核…</div>
        )}
        {isMock && <div className="dim" style={{ fontSize: 12 }}>（浏览器调试模式，使用模拟数据）</div>}
      </div>
    );
  }

  return (
    <div className="app-shell">
      <aside className={`sidebar${collapsed ? " collapsed" : ""}`}>
        <div className="sidebar-head">
          <img className="brand-logo" src={logoUrl} alt="鲲鹏 Clash" />
          <span className="brand-name">鲲鹏 Clash</span>
          <span style={{ flex: 1 }} />
          <button
            className="sidebar-toggle"
            title={collapsed ? "展开侧栏" : "收起侧栏"}
            onClick={() => setCollapsed((v) => !v)}
          >
            <IconSidebar />
          </button>
        </div>
        <nav className="sidebar-nav" style={{ "--nav-index": NAV.findIndex((n) => n.id === page) } as React.CSSProperties}>
          <span className="nav-indicator" aria-hidden />
          {NAV.map((n) => (
            <button
              key={n.id}
              className={`nav-item${page === n.id ? " active" : ""}`}
              onClick={() => changePage(n.id)}
              title={n.label}
            >
              {n.icon}
              <span className="nav-label">{n.label}</span>
              {n.id === "agent" && agentLive && <span className="nav-live" />}
            </button>
          ))}
        </nav>
        <div style={{ flex: 1 }} />
        <div className={`sidebar-status${collapsed ? " nav-hidden" : ""}`}>
          <span className={`dot${status.running ? " ok" : ""}`} />
          {isMock ? "浏览器调试 · Mock" : status.running ? "运行中" : "已停止"}
        </div>
      </aside>

      <main className="content" ref={contentRef}>
        <div key={`${page}-${toolsSubpage ?? ""}-${page === "profiles" || page === "proxies" ? dataRev : 0}`} className="page-anim">
          {page === "dashboard" && (
            <Dashboard status={status} goProxies={goProxies} smartMode={smartMode} onSmartModeChange={changeSmartMode} />
          )}
          {page === "proxies" && <Proxies status={status} initialGroup={initialGroup} />}
          {page === "profiles" && <Profiles />}
          {page === "connections" && <Connections status={status} />}
          {page === "agent" && <Agent />}
          {page === "tools" &&
            (toolsSubpage === "theme" ? (
              <ThemePage onBack={() => setToolsSubpage(null)} />
            ) : (
              <Tools status={status} onOpenTheme={() => setToolsSubpage("theme")} />
            ))}
        </div>
      </main>
    </div>
  );
}
