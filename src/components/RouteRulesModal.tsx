import React, { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { Profile, RouteRule } from "../api";
import { Modal } from "./ui";
import { IconPlus, IconSearch, IconTrash } from "./Icons";
import { useToast } from "./Toast";

/** 分流规则编辑器：域名组 → 指定节点组（挂在订阅下，跟着配置走）。 */

/** 与后端 configgen.MaxRouteGroups 一致。 */
const MAX_GROUPS = 20;

function RuleCard({
  rule,
  nodes,
  onChange,
  onRemove,
}: {
  rule: RouteRule;
  nodes: string[];
  onChange: (next: RouteRule) => void;
  onRemove: () => void;
}) {
  const [search, setSearch] = useState("");
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return nodes;
    return nodes.filter((n) => n.toLowerCase().includes(q));
  }, [nodes, search]);

  const toggleNode = (n: string) => {
    onChange(
      rule.nodes.includes(n)
        ? { ...rule, nodes: rule.nodes.filter((x) => x !== n) }
        : { ...rule, nodes: [...rule.nodes, n] },
    );
  };

  return (
    <div className="rule-card">
      <div className="rule-card-head">
        <input
          value={rule.name}
          placeholder="组名，如 AI 专线"
          maxLength={32}
          onChange={(e) => onChange({ ...rule, name: e.target.value })}
          style={{ width: 180 }}
        />
        <div className="segmented">
          <button type="button" className={!rule.auto ? "active" : ""} onClick={() => onChange({ ...rule, auto: false })}>
            手动
          </button>
          <button type="button" className={rule.auto ? "active" : ""} onClick={() => onChange({ ...rule, auto: true })}>
            自动
          </button>
        </div>
        <span className="dim" style={{ fontSize: 12 }}>
          {rule.auto ? "组内测速选最快" : "在代理页手动选节点"}
        </span>
        <span style={{ flex: 1 }} />
        <button className="row-btn" title="删除该组" onClick={onRemove}>
          <IconTrash size={15} />
        </button>
      </div>
      <div>
        <div className="rule-label">域名（每行一个，匹配含子域；支持 1.2.3.0/24）</div>
        <textarea
          className="modal-textarea"
          rows={3}
          placeholder={"*.chatgpt.com\nclaude.ai"}
          value={rule.domains.join("\n")}
          onChange={(e) => onChange({ ...rule, domains: e.target.value.split("\n") })}
          spellCheck={false}
        />
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span className="rule-label">
            可用节点（已选 {rule.nodes.length} / 共 {nodes.length}）
          </span>
          <span style={{ flex: 1 }} />
          <div className="rule-search">
            <IconSearch size={13} />
            <input
              value={search}
              placeholder="搜节点"
              onChange={(e) => setSearch(e.target.value)}
              spellCheck={false}
            />
          </div>
        </div>
        <div className="rule-node-grid">
          {filtered.map((n) => (
            <button
              key={n}
              type="button"
              className={`rule-node-chip${rule.nodes.includes(n) ? " on" : ""}`}
              onClick={() => toggleNode(n)}
            >
              {n}
            </button>
          ))}
          {filtered.length === 0 && (
            <span className="dim" style={{ fontSize: 12.5, padding: "8px 0" }}>
              无匹配节点
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

export function RouteRulesModal({ profile, onClose }: { profile: Profile | null; onClose: () => void }) {
  const { showToast } = useToast();
  const [rules, setRules] = useState<RouteRule[]>([]);
  const [nodes, setNodes] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);

  const pid = profile?.id ?? "";
  useEffect(() => {
    if (!pid) return;
    let alive = true;
    setLoaded(false);
    Promise.all([api.listRouteRules(pid), api.routeRuleNodes(pid)])
      .then(([r, n]) => {
        if (!alive) return;
        setRules(r);
        setNodes(n);
      })
      .catch((e) => showToast(`加载分流规则失败：${e instanceof Error ? e.message : String(e)}`, "error"))
      .finally(() => {
        if (alive) setLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [pid, showToast]);

  const updateRule = (index: number, next: RouteRule) =>
    setRules((prev) => prev.map((r, i) => (i === index ? next : r)));
  const removeRule = (index: number) => setRules((prev) => prev.filter((_, i) => i !== index));

  const addGroup = () => {
    if (rules.length >= MAX_GROUPS) {
      showToast(`最多 ${MAX_GROUPS} 组`, "error");
      return;
    }
    setRules((prev) => {
      const names = new Set(prev.map((r) => r.name.trim()));
      let n = 1;
      while (names.has(`分流组${n}`)) n++;
      return [...prev, { id: "", name: `分流组${n}`, domains: [], nodes: [], auto: false }];
    });
  };

  const save = async () => {
    // 客户端结构校验（与服务端 NormalizeRouteGroups 同口径），具体域名合法性由服务端把关
    const seen = new Set<string>();
    for (let i = 0; i < rules.length; i++) {
      const r = rules[i];
      const name = r.name.trim();
      if (!name) {
        showToast(`第 ${i + 1} 组没有填写组名`, "error");
        return;
      }
      if (name.includes(",") || name.includes("/")) {
        showToast(`组名「${name}」不能包含逗号或斜杠`, "error");
        return;
      }
      if (seen.has(name)) {
        showToast(`组名重复：「${name}」`, "error");
        return;
      }
      seen.add(name);
      if (!r.domains.some((d) => d.trim())) {
        showToast(`「${name}」没有填写域名`, "error");
        return;
      }
      if (r.nodes.length === 0) {
        showToast(`「${name}」没有选择节点`, "error");
        return;
      }
    }
    setSaving(true);
    try {
      await api.saveRouteRules(
        pid,
        rules.map((r) => ({
          ...r,
          name: r.name.trim(),
          domains: r.domains.map((d) => d.trim()).filter(Boolean),
        })),
      );
      showToast("分流规则已保存并生效", "success");
      onClose();
    } catch (e) {
      showToast(`保存失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={!!profile} title={`分流规则 · ${profile?.name ?? ""}`} onClose={() => !saving && onClose()} wide>
      <div className="dim" style={{ fontSize: 12.5, lineHeight: 1.7 }}>
        这些域名只会从所选节点访问（组内不含 DIRECT、不回落到其他节点）。规则跟着当前订阅走，切换订阅后互不影响；仅规则模式生效。
      </div>
      <div className="rule-scroll">
        {rules.map((r, i) => (
          <RuleCard key={r.id || `draft-${i}`} rule={r} nodes={nodes} onChange={(next) => updateRule(i, next)} onRemove={() => removeRule(i)} />
        ))}
        {loaded && rules.length === 0 && (
          <div className="dim" style={{ textAlign: "center", padding: "28px 0" }}>
            还没有分流规则。适合「某些站点只能用特定地区节点访问」的场景。
          </div>
        )}
      </div>
      <div className="modal-actions" style={{ justifyContent: "space-between" }}>
        <button className="btn" onClick={addGroup} disabled={saving}>
          <span style={{ display: "inline-flex", verticalAlign: "-3px", marginRight: 6 }}>
            <IconPlus size={15} />
          </span>
          添加规则组
        </button>
        <div style={{ display: "flex", gap: 10 }}>
          <button className="btn" onClick={onClose} disabled={saving}>
            取消
          </button>
          <button className="btn primary" onClick={save} disabled={saving}>
            {saving ? "保存中…" : "保存并生效"}
          </button>
        </div>
      </div>
    </Modal>
  );
}
