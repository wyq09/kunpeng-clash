import React, { useCallback, useEffect, useMemo, useState } from "react";
import { api, isMock } from "../api";
import type { AgentCall, AgentInfo } from "../api";
import { ConfirmButton, Segmented, Switch } from "../components/ui";
import { IconBook, IconBot, IconCheck, IconChevronDown, IconCopy, IconEye, IconEyeOff, IconRefresh } from "../components/Icons";
import { useToast } from "../components/Toast";

/** 智能体接入：一键复制接入指令（发给任意 AI 智能体即可自助接入）+ 各接入方式配置 + 令牌 + 能力清单 + 调用记录。 */

type Tab = "prompt" | "mcp" | "cli" | "http" | "skill";

const TABS: { value: Tab; label: string }[] = [
  { value: "prompt", label: "接入指令" },
  { value: "mcp", label: "MCP" },
  { value: "cli", label: "命令行" },
  { value: "http", label: "HTTP" },
  { value: "skill", label: "技能" },
];

const SOURCE_LABEL: Record<string, string> = {
  mcp: "MCP",
  "mcp-stdio": "MCP",
  http: "HTTP",
  cli: "命令行",
};

export default function Agent() {
  const { showToast } = useToast();
  const [info, setInfo] = useState<AgentInfo | null>(null);
  const [tab, setTab] = useState<Tab>("prompt");
  const [showToken, setShowToken] = useState(false);
  const [busy, setBusy] = useState(false);
  const [toolsOpen, setToolsOpen] = useState(false);
  const [freshAt, setFreshAt] = useState(0);
  const now = useNow(10_000);

  const load = useCallback(async () => {
    try {
      setInfo(await api.agentInfo());
    } catch (e) {
      showToast(`读取智能体信息失败：${errText(e)}`, "error");
    }
  }, [showToast]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(
    () =>
      api.onAgent((c) => {
        setFreshAt(Date.now());
        setInfo((prev) => (prev ? { ...prev, activity: [c, ...prev.activity].slice(0, 60) } : prev));
      }),
    [],
  );

  const copy = useCallback(
    async (text: string, what: string) => {
      try {
        await api.copyText(text);
        showToast(`已复制${what}`, "success");
        return true;
      } catch (e) {
        showToast(`复制失败：${errText(e)}`, "error");
        return false;
      }
    },
    [showToast],
  );

  const toggle = async (on: boolean) => {
    setBusy(true);
    try {
      await api.agentSetEnabled(on);
      await load();
      showToast(on ? "智能体接入已开启" : "智能体接入已关闭，所有智能体已断开", on ? "success" : "info");
    } catch (e) {
      showToast(`操作失败：${errText(e)}`, "error");
    } finally {
      setBusy(false);
    }
  };

  const resetToken = async () => {
    try {
      setInfo(await api.agentResetToken());
      showToast("令牌已重置：使用 HTTP 接入的智能体需重新发送接入指令", "success");
    } catch (e) {
      showToast(`重置失败：${errText(e)}`, "error");
    }
  };

  const exportSkill = async () => {
    try {
      const p = await api.agentExportSkill();
      showToast(`已导出到 ${p.replace(/^\/Users\/[^/]+/, "~")}`, "success");
    } catch (e) {
      showToast(errText(e), "error");
    }
  };

  const groups = useMemo(() => {
    const m = new Map<string, AgentInfo["tools"]>();
    for (const t of info?.tools ?? []) m.set(t.group, [...(m.get(t.group) ?? []), t]);
    return [...m.entries()];
  }, [info?.tools]);

  if (!info) {
    return (
      <div className="page">
        <div className="page-header">
          <h1 className="page-title">智能体</h1>
        </div>
        <div className="card skeleton" style={{ height: 168 }} />
        <div className="card skeleton" style={{ height: 260, marginTop: 16 }} />
      </div>
    );
  }

  const on = info.enabled && info.running;
  const live = Date.now() - freshAt < 4000;
  const last = info.activity[0];

  return (
    <div className="page" style={{ maxWidth: 1080 }}>
      <div className="page-header">
        <h1 className="page-title">智能体</h1>
        <div className="spacer" />
        <Switch on={info.enabled} disabled={busy} onChange={toggle} title={info.enabled ? "关闭智能体接入" : "开启智能体接入"} />
      </div>

      {/* 主卡：一键复制接入指令 */}
      <div className={`card agent-hero stagger${on ? " on" : ""}`}>
        <span className={`agent-orb${on ? " on" : ""}${live ? " live" : ""}`}>
          <IconBot size={28} />
        </span>
        <div className="agent-hero-text">
          <div className="agent-hero-title">让 AI 智能体帮你管理代理</div>
          <div className="agent-hero-meta">
            {on ? (
              <>
                <span className="dot ok" />
                <span>已就绪</span>
                <span className="sep" />
                <span className="mono">{info.baseUrl.replace("http://", "")}</span>
                <span className="sep" />
                <span>{info.tools.length} 项能力</span>
                {last && (
                  <>
                    <span className="sep" />
                    <span>最近调用 {relTime(last.at, now)}</span>
                  </>
                )}
              </>
            ) : info.error ? (
              <>
                <span className="dot bad" />
                <span>{info.error}</span>
              </>
            ) : (
              <>
                <span className="dot" />
                <span>接入已关闭，打开右上角开关后智能体才能连接</span>
              </>
            )}
          </div>
          <div className="dim agent-hero-hint">复制后发给 Claude、Cursor、Codex 等任意智能体，它会自己读教程、完成接入。</div>
        </div>
        <CopyButton
          className="btn primary agent-copy"
          disabled={!on}
          onCopy={() => copy(info.prompt, "接入指令，去发给你的智能体吧")}
          label="复制接入指令"
        />
      </div>

      {/* 接入方式 */}
      <div className="card stagger" style={{ marginTop: 16 }}>
        <div className="agent-tabs">
          <Segmented options={TABS} value={tab} onChange={setTab} />
        </div>
        <div key={tab} className="tab-panel">
          {tab === "prompt" && <CodeBlock text={info.prompt} onCopy={(t) => copy(t, "接入指令")} tall />}
          {tab === "mcp" && (
            <div className="agent-cols">
              <div>
                <div className="block-label">
                  stdio <span className="pill blue">推荐</span>
                </div>
                <CodeBlock text={info.mcpConfig} onCopy={(t) => copy(t, " MCP 配置")} />
                <CodeBlock text={`claude mcp add kunpeng-clash -- ${shq(info.cliPath)} mcp`} onCopy={(t) => copy(t, "命令")} compact />
              </div>
              <div>
                <div className="block-label">Streamable HTTP</div>
                <CodeBlock text={info.mcpHttpConfig} onCopy={(t) => copy(t, " MCP 配置")} />
                <CodeBlock
                  text={`claude mcp add --transport http kunpeng-clash ${info.mcpUrl} --header "Authorization: Bearer ${info.token}"`}
                  onCopy={(t) => copy(t, "命令")}
                  compact
                />
              </div>
            </div>
          )}
          {tab === "cli" && <CodeBlock text={info.cliExample} onCopy={(t) => copy(t, "命令")} />}
          {tab === "http" && (
            <>
              <CodeBlock text={info.httpExample} onCopy={(t) => copy(t, "命令")} />
              <CodeBlock text={`curl -s ${info.baseUrl}/v1/guide   # 完整教程（工具清单 / 参数 / 场景示例）`} onCopy={(t) => copy(t.split("   #")[0], "命令")} compact />
            </>
          )}
          {tab === "skill" && (
            <div className="agent-skill">
              <IconBook size={22} className="dim" />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600 }}>SKILL.md</div>
                <div className="dim mono" style={{ fontSize: 12.5, marginTop: 3 }}>
                  curl -s {info.baseUrl}/v1/skill
                </div>
              </div>
              <button className="btn" onClick={() => copy(`curl -s ${info.baseUrl}/v1/skill`, "命令")}>
                复制命令
              </button>
              <button className="btn primary" onClick={exportSkill} disabled={isMock}>
                导出文件
              </button>
            </div>
          )}
        </div>
      </div>

      <div className="agent-grid">
        {/* 令牌 */}
        <div className="card stagger">
          <div className="card-title-row">
            <span>令牌</span>
          </div>
          <div className="token-row">
            <span className="mono token-text">{showToken ? info.token : maskToken(info.token)}</span>
            <button className="row-btn" title={showToken ? "隐藏" : "显示"} onClick={() => setShowToken((v) => !v)}>
              {showToken ? <IconEyeOff size={17} /> : <IconEye size={17} />}
            </button>
            <button className="row-btn" title="复制令牌" onClick={() => copy(info.token, "令牌")}>
              <IconCopy size={17} />
            </button>
          </div>
          <ConfirmButton className="btn token-reset" confirmText="确认重置？" onConfirm={resetToken}>
            <IconRefresh size={15} />
            重置令牌
          </ConfirmButton>
        </div>

        {/* 调用记录 */}
        <div className="card stagger agent-activity">
          <div className="card-title-row">
            <span>调用记录</span>
            {info.activity.length > 0 && <span className="pill">{info.activity.length}</span>}
            <div className="spacer" />
            {info.activity.length > 0 && (
              <button
                className="link-btn"
                onClick={async () => {
                  await api.agentClearActivity();
                  setInfo((p) => (p ? { ...p, activity: [] } : p));
                }}
              >
                清空
              </button>
            )}
          </div>
          <div className="activity-list">
            {info.activity.length === 0 && <div className="empty-hint">智能体接入后，它的每一步操作都会实时出现在这里</div>}
            {info.activity.slice(0, 30).map((c) => (
              <ActivityRow key={c.at + c.tool + c.durationMs} c={c} now={now} />
            ))}
          </div>
        </div>
      </div>

      {/* 能力清单 */}
      <div className="card stagger" style={{ marginTop: 16 }}>
        <button className="collapse-head" onClick={() => setToolsOpen((v) => !v)}>
          <span>能力清单</span>
          <span className="pill">{info.tools.length}</span>
          <div className="spacer" />
          <IconChevronDown size={18} className={`chev${toolsOpen ? " up" : ""}`} />
        </button>
        <div className={`collapse-body${toolsOpen ? " open" : ""}`}>
          <div className="collapse-inner">
            {groups.map(([g, tools]) => (
              <div key={g} className="tool-group">
                <div className="tool-group-name">{g}</div>
                <div className="tool-chips">
                  {tools.map((t) => (
                    <span key={t.name} className={`tool-chip${t.danger ? " danger" : ""}`} title={`${t.name}\n${t.description}`}>
                      {t.title}
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function ActivityRow({ c, now }: { c: AgentCall; now: number }) {
  return (
    <div className="activity-row" title={c.error || c.tool}>
      <span className={`dot ${c.ok ? "ok" : "bad"}`} />
      <span className="activity-title">{c.title}</span>
      {!c.ok && <span className="activity-err">{c.error}</span>}
      <span className="spacer" />
      <span className="pill">{SOURCE_LABEL[c.source] ?? c.source}</span>
      <span className="dim activity-time">{relTime(c.at, now)}</span>
    </div>
  );
}

/** 代码块：等宽、可选中，右上角复制（成功后图标短暂变勾）。 */
function CodeBlock({ text, onCopy, tall, compact }: { text: string; onCopy: (t: string) => Promise<boolean>; tall?: boolean; compact?: boolean }) {
  return (
    <div className={`code-block${tall ? " tall" : ""}${compact ? " compact" : ""}`}>
      <pre>{text}</pre>
      <CopyButton className="code-copy" onCopy={() => onCopy(text)} />
    </div>
  );
}

function CopyButton({ onCopy, className, label, disabled }: { onCopy: () => Promise<boolean>; className: string; label?: string; disabled?: boolean }) {
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!done) return;
    const t = setTimeout(() => setDone(false), 1600);
    return () => clearTimeout(t);
  }, [done]);
  return (
    <button
      className={`${className}${done ? " done" : ""}`}
      disabled={disabled}
      title={label ? undefined : "复制"}
      onClick={async () => setDone(await onCopy())}
    >
      <span className="swap-icon">
        <span className={done ? "out" : "in"}>
          <IconCopy size={label ? 18 : 15} />
        </span>
        <span className={done ? "in" : "out"}>
          <IconCheck size={label ? 18 : 15} />
        </span>
      </span>
      {label && <span>{done ? "已复制" : label}</span>}
    </button>
  );
}

function useNow(interval: number) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), interval);
    return () => clearInterval(t);
  }, [interval]);
  return now;
}

function relTime(iso: string, now: number): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 10) return "刚刚";
  if (s < 60) return `${s} 秒前`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return new Date(iso).toLocaleDateString("zh-CN");
}

function maskToken(t: string) {
  return t.length > 10 ? `${t.slice(0, 5)}${"•".repeat(14)}${t.slice(-4)}` : "•".repeat(t.length);
}

function shq(s: string) {
  return /[\s'"$`\\]/.test(s) ? `'${s.replace(/'/g, `'\\''`)}'` : s;
}

function errText(e: unknown) {
  return e instanceof Error ? e.message : String(e);
}
