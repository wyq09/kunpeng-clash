import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, latestDelay } from "../api";
import type { CoreStatus, MihomoProxy } from "../api";
import { DelayBadge } from "../components/ui";
import {
  IconCheck,
  IconChevronDown,
  IconClose,
  IconLocation,
  IconSearch,
  IconZap,
} from "../components/Icons";
import { useToast } from "../components/Toast";

/** 代理：左列展开组的节点网格，右列全部策略组列表。 */

function nameAvatar(name: string): string {
  const ch = Array.from(name.trim())[0];
  return ch && /[a-zA-Z0-9]/.test(ch) ? ch.toUpperCase() : (ch ?? "?");
}

function groupTypeLabel(g: MihomoProxy): string {
  if (g.type === "URLTest") return "URLTest";
  if (g.type === "Fallback") return "Fallback";
  if (g.type === "LoadBalance") return "LoadBalance";
  return "Selector";
}

export default function Proxies({ status, initialGroup }: { status: CoreStatus; initialGroup?: string }) {
  const { showToast } = useToast();
  const [table, setTable] = useState<Record<string, MihomoProxy>>({});
  const [groups, setGroups] = useState<MihomoProxy[]>([]);
  const [expanded, setExpanded] = useState<string>("");
  const [searchOpen, setSearchOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [testingNodes, setTestingNodes] = useState<Set<string>>(new Set());
  /** 正在整组测速的组名（null = 无）。按组跟踪：测 A 组时切到 B 组，B 组不应被遮罩。 */
  const [testingGroup, setTestingGroup] = useState<string | null>(null);
  const testingGroupRef = useRef<string | null>(null);
  const [highlightNode, setHighlightNode] = useState<string | null>(null);
  const selectedRef = useRef<HTMLDivElement | null>(null);
  const [loaded, setLoaded] = useState(false);

  const refresh = useCallback(
    async (force = false) => {
      // 整组测速期间冻结表格刷新：mihomo 会随测试逐个改写节点 history（失败记 0），
      // 中途拉到的是「新结果 + 旧记录」混合态，一旦遮罩闪断徽章就会先落地成旧的超时。
      // 完成后由 testGroup 强制刷一次，拿全量新 history。
      if (!force && testingGroupRef.current) return;
      try {
        const { all, groups: gs } = await api.getProxies();
        setTable(all);
        setGroups(gs);
        setLoaded(true);
        setExpanded((prev) => (prev && all[prev] ? prev : gs[0]?.name ?? ""));
      } catch (e) {
        setLoaded(true);
        if (status.running) {
          showToast(`加载代理失败：${e instanceof Error ? e.message : String(e)}`, "error");
        }
      }
    },
    [showToast, status.running],
  );

  useEffect(() => {
    void refresh();
    const t = setInterval(() => void refresh(), 20000);
    return () => clearInterval(t);
  }, [refresh, status.running]);

  // 从仪表盘跳转过来时展开指定组
  useEffect(() => {
    if (initialGroup) {
      setExpanded(initialGroup);
      setSearch("");
    }
  }, [initialGroup]);

  const group = table[expanded];
  const nodeNames = useMemo(() => group?.all ?? [], [group]);

  const filteredNodes = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return nodeNames;
    return nodeNames.filter((n) => n.toLowerCase().includes(q));
  }, [nodeNames, search]);

  /** 本地写入延迟结果（服务端 history 会异步更新，随后 refresh 会覆盖）。 */
  const applyDelay = useCallback((name: string, ms: number) => {
    setTable((prev) => {
      const p = prev[name];
      if (!p) return prev;
      return {
        ...prev,
        [name]: {
          ...p,
          history: [...(p.history ?? []), { time: new Date().toISOString(), delay: ms > 0 ? ms : 0 }].slice(-8),
        },
      };
    });
  }, []);

  const testNode = useCallback(
    async (name: string) => {
      if (testingNodes.has(name) || testingGroupRef.current) return;
      setTestingNodes((prev) => new Set(prev).add(name));
      try {
        const r = await api.proxyDelay(name);
        applyDelay(name, r.delay);
        // 失败/超时只把徽章置灰为「超时」，不弹红色告警
        if (r.delay < 0) showToast(`${name}：测速超时`, "info");
      } catch (e) {
        showToast(`测延迟失败：${e instanceof Error ? e.message : String(e)}`, "error");
      } finally {
        setTestingNodes((prev) => {
          const next = new Set(prev);
          next.delete(name);
          return next;
        });
      }
    },
    [applyDelay, showToast, testingNodes],
  );

  const testGroup = useCallback(async () => {
    const groupName = group?.name;
    if (!groupName || testingGroupRef.current) return;
    // Promise 落地前整组徽章稳定显示「测速中…」：遮罩以 ref+state 双轨跟踪，
    // 只有本回调把它置空，refresh 冻结与 JSX 判断都读它，中间态不可能漏出。
    testingGroupRef.current = groupName;
    setTestingGroup(groupName);
    // 一次 groupDelay 会阻塞到整组完成（后端行为），给用户明确的过程提示
    showToast(`正在测速「${groupName}」…`, "info");
    try {
      const result = await api.groupDelay(groupName);
      const plainNodes = (group.all ?? []).filter((n) => {
        const t = table[n]?.type;
        return t !== "Selector" && t !== "URLTest" && t !== "Fallback" && t !== "LoadBalance";
      });
      setTable((prev) => {
        const next = { ...prev };
        for (const n of plainNodes) {
          const p = next[n];
          if (!p) continue;
          const ms = result[n];
          next[n] = {
            ...p,
            history: [
              ...(p.history ?? []),
              { time: new Date().toISOString(), delay: ms !== undefined ? ms : 0 },
            ].slice(-8),
          };
        }
        return next;
      });
      const ok = plainNodes.filter((n) => result[n] !== undefined).length;
      const fail = plainNodes.length - ok;
      showToast(
        `完成：${ok} 通 / ${fail} 超时`,
        fail === 0 ? "success" : ok === 0 ? "error" : "info",
      );
    } catch (e) {
      showToast(`组测延迟失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      testingGroupRef.current = null;
      setTestingGroup(null);
      // 完成后一次性用服务端最新 history 刷新（此刻所有节点的结果都已写回内核）
      void refresh(true);
    }
  }, [group, refresh, showToast, table]);

  const select = useCallback(
    async (g: MihomoProxy, name: string) => {
      if (g.type !== "Selector") return;
      try {
        await api.select(g.name, name);
        setTable((prev) => {
          const p = prev[g.name];
          if (!p) return prev;
          return { ...prev, [g.name]: { ...p, now: name } };
        });
        showToast(`已选择 ${name}`, "success");
      } catch (e) {
        showToast(`选择失败：${e instanceof Error ? e.message : String(e)}`, "error");
      }
    },
    [showToast],
  );

  const locateSelected = () => {
    if (group?.now) {
      setHighlightNode(group.now);
      selectedRef.current?.scrollIntoView({ block: "center", behavior: "smooth" });
      setTimeout(() => setHighlightNode(null), 1200);
    }
  };

  const notRunning = !status.running;

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">代理</h1>
        <div className="spacer" />
        <div className="header-actions">
          {searchOpen && (
            <input
              autoFocus
              className="header-search"
              placeholder="搜索节点名…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          )}
          <button
            className="icon-btn"
            title="搜索"
            onClick={() => {
              setSearchOpen((v) => !v);
              setSearch("");
            }}
          >
            {searchOpen ? <IconClose /> : <IconSearch />}
          </button>
        </div>
      </div>

      {notRunning && (
        <div className="card" style={{ marginBottom: 16, padding: "14px 20px", color: "var(--text-2)" }}>
          内核未运行——启动内核后才能加载策略组与节点。
        </div>
      )}

      <div className="proxies-layout">
        {/* 左：展开组 */}
        <div className="card proxy-expanded">
          {group ? (
            <>
              <div className="card-title-row" style={{ marginBottom: 16 }}>
                <span className="avatar" style={{ width: 42, height: 42, fontSize: 20 }}>
                  {nameAvatar(group.name)}
                </span>
                <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                  <span style={{ fontSize: 17, fontWeight: 700 }}>{group.name}</span>
                  <span className="dim" style={{ fontSize: 13, overflow: "hidden", textOverflow: "ellipsis" }}>
                    {group.now ?? "—"}
                  </span>
                </span>
                <div className="spacer" />
                <button className="icon-btn" title="定位当前节点" onClick={locateSelected}>
                  <IconLocation />
                </button>
                <button className="icon-btn" title="测整组延迟" onClick={testGroup} disabled={!!testingGroup || notRunning}>
                  <span className={testingGroup === group.name ? "spin" : ""} style={{ display: "inline-flex" }}>
                    <IconZap />
                  </span>
                </button>
                <button className="icon-btn" title="收起" onClick={() => setExpanded("")}>
                  <IconChevronDown style={{ transform: "rotate(180deg)" }} />
                </button>
              </div>

              <div className="node-grid">
                {filteredNodes.map((name) => {
                  const node = table[name] ?? { name, type: "Unknown" };
                  const isNow = group.now === name;
                  // 测速遮罩：本组整组测速中 → 全组「测速中…」；单点测速只遮自己
                  const testing = testingGroup === group.name || testingNodes.has(name);
                  const selectable = group.type === "Selector";
                  return (
                    <div
                      key={name}
                      ref={isNow ? selectedRef : undefined}
                      className={`node-card${isNow ? " selected" : ""}${highlightNode === name ? " flash" : ""}`}
                      title={selectable ? `点击选择 ${name}` : "URLTest 组自动选择节点"}
                      onClick={() => selectable && select(group, name)}
                      style={selectable ? undefined : { cursor: "default" }}
                    >
                      <div className="node-name-row">
                        <span className="node-name">{name}</span>
                        {isNow && selectable && (
                          <span className="node-check">
                            <IconCheck size={13} />
                          </span>
                        )}
                      </div>
                      <div className="node-foot">
                        <span className="dim">{node.type}</span>
                        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
                          <DelayBadge delay={latestDelay(node)} testing={testing} />
                          <button
                            className="row-btn"
                            title="测该节点延迟"
                            onClick={(e) => {
                              e.stopPropagation();
                              testNode(name);
                            }}
                            disabled={testing || notRunning}
                          >
                            <span className={testing ? "spin" : ""} style={{ display: "inline-flex" }}>
                              <IconZap size={15} />
                            </span>
                          </button>
                        </span>
                      </div>
                    </div>
                  );
                })}
                {filteredNodes.length === 0 && (
                  <div className="dim" style={{ gridColumn: "1 / -1", textAlign: "center", padding: "32px 0" }}>
                    {search ? "无匹配节点" : "组内没有节点"}
                  </div>
                )}
              </div>
            </>
          ) : (
            <div className="dim" style={{ textAlign: "center", padding: "60px 0" }}>
              {loaded ? "没有可展示的策略组" : "加载中…"}
            </div>
          )}
        </div>

        {/* 右：全部策略组 */}
        <div className="proxy-group-list">
          {groups.map((g) => (
            <button
              key={g.name}
              className={`proxy-group-item${g.name === expanded ? " active" : ""}`}
              onClick={() => {
                setExpanded(g.name);
                setSearch("");
              }}
            >
              <span className="avatar">{nameAvatar(g.name)}</span>
              <span className="pg-text">
                <span className="pg-name">{g.name}</span>
                <span className="dim pg-now">{g.now ?? "—"}</span>
              </span>
              <span className="pg-type dim">{groupTypeLabel(g)}</span>
              <IconChevronDown size={17} className={g.name === expanded ? "chev up" : "chev"} />
            </button>
          ))}
          {groups.length === 0 && (
            <div className="card dim" style={{ textAlign: "center", padding: "40px 0" }}>
              {loaded ? (notRunning ? "内核未运行" : "暂无策略组") : "加载中…"}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
