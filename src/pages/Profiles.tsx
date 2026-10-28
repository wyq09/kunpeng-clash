import React, { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api";
import type { Profile } from "../api";
import { formatBytes, formatRelative } from "../format";
import { ConfirmButton, Modal, Switch } from "../components/ui";
import { RouteRulesModal } from "../components/RouteRulesModal";
import {
  IconCheck,
  IconImport,
  IconPencil,
  IconPlus,
  IconPlusCircle,
  IconQrCode,
  IconRefresh,
  IconRule,
  IconShield,
  IconSort,
  IconTrash,
} from "../components/Icons";
import { useToast } from "../components/Toast";
import { classifyLink, decodeQrFromFile } from "../qr";

/** 配置页：订阅卡片（编辑/删除/激活）+ 添加菜单（订阅/节点链接/二维码）+ 自定义节点 + 直连白名单。 */

/** 与后端 NodesService.AddFromLinks 一致的协议前缀（含 hysteria 兼容）。 */
const NODE_LINK_PREFIXES = ["vless://", "vmess://", "ss://", "hysteria2://", "hysteria://", "trojan://"];
const WHITELIST_LIMIT = 200;

/** 找出无法识别的行号（空行不算），用于导入结果的失败提示。 */
function invalidLineNumbers(raw: string): number[] {
  const out: number[] = [];
  raw.split(/\r?\n/).forEach((line, i) => {
    const t = line.trim();
    if (!t) return;
    const lower = t.toLowerCase();
    if (!NODE_LINK_PREFIXES.some((p) => lower.startsWith(p))) out.push(i + 1);
  });
  return out;
}

export default function Profiles() {
  const { showToast } = useToast();
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [activatingId, setActivatingId] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [sorted, setSorted] = useState(false);

  // ---- 添加菜单 / 三个弹窗 ----
  const [menuOpen, setMenuOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [addUrl, setAddUrl] = useState("");
  const [addName, setAddName] = useState("");
  const [adding, setAdding] = useState(false);

  const [importOpen, setImportOpen] = useState(false);
  const [importRaw, setImportRaw] = useState("");
  const [importing, setImporting] = useState(false);

  const [qrOpen, setQrOpen] = useState(false);
  const [qrBusy, setQrBusy] = useState(false);
  const qrFileRef = useRef<HTMLInputElement | null>(null);

  // ---- 编辑订阅 ----
  const [editing, setEditing] = useState<Profile | null>(null);
  const [editName, setEditName] = useState("");
  const [editUrl, setEditUrl] = useState("");
  const [editAutoUpdate, setEditAutoUpdate] = useState(false);
  const [editInterval, setEditInterval] = useState("1440");
  const [saving, setSaving] = useState(false);

  // ---- 分流规则（挂在订阅下） ----
  const [rulesProfile, setRulesProfile] = useState<Profile | null>(null);

  // ---- 自定义节点 ----
  const [nodes, setNodes] = useState<string[]>([]);
  const [removingNode, setRemovingNode] = useState<string | null>(null);

  // ---- 直连白名单 ----
  const [whitelist, setWhitelist] = useState<string[]>([]);
  const [wlLoaded, setWlLoaded] = useState(false);
  const [wlInput, setWlInput] = useState("");
  const [wlSaving, setWlSaving] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setProfiles(await api.listProfiles());
    } catch (e) {
      showToast(`加载订阅失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setLoading(false);
    }
  }, [showToast]);

  const refreshNodes = useCallback(async () => {
    try {
      setNodes(await api.listNodes());
    } catch {
      // 列表加载失败不打断页面
    }
  }, []);

  useEffect(() => {
    refresh();
    refreshNodes();
    api
      .getWhitelist()
      .then(setWhitelist)
      .catch((e) => showToast(`加载白名单失败：${e instanceof Error ? e.message : String(e)}`, "error"))
      .finally(() => setWlLoaded(true));
  }, [refresh, refreshNodes, showToast]);

  const displayed = sorted ? [...profiles].sort((a, b) => a.name.localeCompare(b.name, "zh-Hans-CN")) : profiles;

  const refreshAll = async () => {
    if (refreshingAll) return;
    setRefreshingAll(true);
    try {
      const results = await Promise.allSettled(profiles.map((p) => api.updateProfile(p.id)));
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const fail = results.length - ok;
      showToast(fail === 0 ? `已更新 ${ok} 个订阅` : `更新完成：${ok} 成功 / ${fail} 失败`, fail === 0 ? "success" : "error");
      await refresh();
    } finally {
      setRefreshingAll(false);
    }
  };

  const activate = async (p: Profile) => {
    if (activatingId || p.active) return;
    setActivatingId(p.id);
    try {
      await api.activateProfile(p.id);
      showToast(`已激活「${p.name}」`, "success");
      await refresh();
    } catch (e) {
      showToast(`激活失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setActivatingId(null);
    }
  };

  const remove = async (p: Profile) => {
    setRemovingId(p.id);
    try {
      await api.removeProfile(p.id);
      showToast(`已删除「${p.name}」`, "success");
      await refresh();
    } catch (e) {
      showToast(`删除失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setRemovingId(null);
    }
  };

  /* ---------------- 添加订阅 ---------------- */

  const submitAdd = async () => {
    const url = addUrl.trim();
    if (!url) return;
    setAdding(true);
    try {
      const p = await api.addProfile(url, addName.trim());
      showToast(`已添加订阅「${p.name}」`, "success");
      setAddOpen(false);
      setAddUrl("");
      setAddName("");
      await refresh();
    } catch (e) {
      showToast(`添加失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setAdding(false);
    }
  };

  /* ---------------- 编辑订阅 ---------------- */

  const openEdit = (p: Profile) => {
    setEditing(p);
    setEditName(p.name);
    setEditUrl(p.url);
    setEditAutoUpdate(p.autoUpdate);
    setEditInterval(String(p.updateIntervalMin > 0 ? p.updateIntervalMin : 1440));
  };

  const submitEdit = async () => {
    if (!editing) return;
    const url = editUrl.trim();
    if (!url) return;
    let interval = 1440;
    if (editAutoUpdate) {
      interval = Number(editInterval);
      if (!Number.isInteger(interval) || interval < 30 || interval > 10080) {
        showToast("自动更新间隔必须是 30–10080 的整数（分钟）", "error");
        return;
      }
    }
    setSaving(true);
    try {
      await api.editProfile(editing.id, editName.trim(), url, editAutoUpdate, interval);
      showToast("已保存并更新订阅", "success");
      setEditing(null);
      await refresh();
    } catch (e) {
      showToast(`保存失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setSaving(false);
    }
  };

  /* ---------------- 导入节点（分享链接） ---------------- */

  const submitImportNodes = async () => {
    const raw = importRaw;
    if (!raw.trim()) return;
    const badLines = invalidLineNumbers(raw);
    setImporting(true);
    try {
      const n = await api.addNodesFromLinks(raw);
      if (n > 0) {
        showToast(
          badLines.length
            ? `成功导入 ${n} 个节点；第 ${badLines.join("、")} 行无法识别`
            : `成功导入 ${n} 个节点`,
          "success",
        );
        setImportOpen(false);
        setImportRaw("");
      } else {
        showToast(
          badLines.length ? `未导入任何节点（第 ${badLines.join("、")} 行无法识别）` : "未识别到有效节点链接",
          "error",
        );
      }
      await refreshNodes();
    } catch (e) {
      showToast(`导入失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setImporting(false);
    }
  };

  /* ---------------- 导入二维码 ---------------- */

  /** 二维码文本的智能分发：节点链接 → 导入节点；http(s) → 添加订阅；其余提示。 */
  const handleQrText = useCallback(
    async (text: string) => {
      const kind = classifyLink(text);
      if (kind === "node") {
        try {
          const n = await api.addNodesFromLinks(text);
          if (n > 0) {
            showToast(`成功导入 ${n} 个节点`, "success");
            setQrOpen(false);
            await refreshNodes();
          } else {
            showToast("二维码里的链接无法识别为节点", "error");
          }
        } catch (e) {
          showToast(`导入失败：${e instanceof Error ? e.message : String(e)}`, "error");
        }
      } else if (kind === "subscribe") {
        try {
          const p = await api.addProfile(text.trim(), "");
          showToast(`已添加订阅「${p.name}」`, "success");
          setQrOpen(false);
          await refresh();
        } catch (e) {
          showToast(`添加订阅失败：${e instanceof Error ? e.message : String(e)}`, "error");
        }
      } else {
        showToast("二维码内容不是节点或订阅链接", "error");
      }
    },
    [refresh, refreshNodes, showToast],
  );

  const handleQrFile = useCallback(
    async (file: File | Blob) => {
      if (qrBusy) return;
      setQrBusy(true);
      try {
        const text = await decodeQrFromFile(file);
        if (!text) {
          showToast("未识别到二维码", "error");
          return;
        }
        await handleQrText(text);
      } catch {
        showToast("未识别到二维码", "error");
      } finally {
        setQrBusy(false);
      }
    },
    [handleQrText, qrBusy, showToast],
  );

  // 二维码弹窗打开时，支持直接 ⌘V / Ctrl+V 粘贴截图
  useEffect(() => {
    if (!qrOpen) return;
    const onPaste = (e: ClipboardEvent) => {
      const items = e.clipboardData?.items;
      if (!items) return;
      for (let i = 0; i < items.length; i++) {
        const item = items[i];
        if (item.type.startsWith("image/")) {
          const file = item.getAsFile();
          if (file) {
            e.preventDefault();
            void handleQrFile(file);
            return;
          }
        }
      }
    };
    window.addEventListener("paste", onPaste);
    return () => window.removeEventListener("paste", onPaste);
  }, [qrOpen, handleQrFile]);

  /* ---------------- 自定义节点删除 ---------------- */

  const removeNode = async (name: string) => {
    setRemovingNode(name);
    try {
      await api.removeNode(name);
      showToast(`已删除「${name}」`, "success");
      await refreshNodes();
    } catch (e) {
      showToast(`删除失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setRemovingNode(null);
    }
  };

  /* ---------------- 直连白名单 ---------------- */

  const saveWhitelist = useCallback(
    async (next: string[]) => {
      setWlSaving(true);
      try {
        await api.setWhitelist(next);
        setWhitelist(next);
        showToast("已生效，网络规则已更新", "success");
      } catch (e) {
        showToast(`保存失败：${e instanceof Error ? e.message : String(e)}`, "error");
        // 回读，保持与后端一致
        try {
          setWhitelist(await api.getWhitelist());
        } catch {
          /* 忽略 */
        }
      } finally {
        setWlSaving(false);
      }
    },
    [showToast],
  );

  const addWhitelist = () => {
    const v = wlInput.trim();
    if (!v) return; // 空串直接忽略
    if (whitelist.some((x) => x.toLowerCase() === v.toLowerCase())) {
      showToast("已存在相同条目", "info");
      return;
    }
    if (whitelist.length >= WHITELIST_LIMIT) {
      showToast(`白名单最多 ${WHITELIST_LIMIT} 条`, "error");
      return;
    }
    setWlInput("");
    void saveWhitelist([...whitelist, v]);
  };

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">配置</h1>
        <div className="spacer" />
        <div className="header-actions">
          <button className="icon-btn" title="刷新全部订阅" onClick={refreshAll} disabled={refreshingAll}>
            <span className={refreshingAll ? "spin" : ""} style={{ display: "inline-flex" }}>
              <IconRefresh />
            </span>
          </button>
          <button
            className="icon-btn"
            title={sorted ? "恢复默认排序" : "按名称排序"}
            onClick={() => setSorted((v) => !v)}
            style={sorted ? { color: "var(--accent)" } : undefined}
          >
            <IconSort />
          </button>
          <div style={{ position: "relative" }}>
            <button className="icon-btn" title="添加" onClick={() => setMenuOpen((v) => !v)}>
              <IconPlusCircle size={22} />
            </button>
            {menuOpen && (
              <>
                <div className="menu-backdrop" onClick={() => setMenuOpen(false)} />
                <div className="dropdown-menu">
                  <button
                    onClick={() => {
                      setMenuOpen(false);
                      setAddOpen(true);
                    }}
                  >
                    <IconPlus size={17} />
                    添加订阅
                  </button>
                  <button
                    onClick={() => {
                      setMenuOpen(false);
                      setImportOpen(true);
                    }}
                  >
                    <IconImport size={17} />
                    导入节点
                  </button>
                  <button
                    onClick={() => {
                      setMenuOpen(false);
                      setQrOpen(true);
                    }}
                  >
                    <IconQrCode size={17} />
                    导入二维码
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      {loading ? (
        <div className="dim" style={{ padding: 40, textAlign: "center" }}>
          加载中…
        </div>
      ) : displayed.length === 0 ? (
        <div className="card dim" style={{ textAlign: "center", padding: 48 }}>
          还没有订阅。点击右上角 + 添加你的第一个订阅。
        </div>
      ) : (
        <div className="profile-grid">
          {displayed.map((p) => {
            const used = p.uploadBytes + p.downloadBytes;
            const pct = p.totalBytes > 0 ? Math.min(100, (used / p.totalBytes) * 100) : 0;
            const busy = activatingId === p.id || removingId === p.id;
            return (
              <div
                key={p.id}
                className={`profile-card${p.active ? " active" : ""}`}
                onClick={() => activate(p)}
                title={p.active ? "当前使用中" : `点击激活「${p.name}」`}
                style={busy ? { opacity: 0.6 } : undefined}
              >
                <div className="profile-main">
                  <div className="profile-name-row">
                    <span className="profile-name">{p.name}</span>
                    {activatingId === p.id && (
                      <span className="dim" style={{ fontSize: 12 }}>
                        激活中…
                      </span>
                    )}
                  </div>
                  <div className="dim" style={{ fontSize: 13, marginTop: 4, fontVariantNumeric: "tabular-nums" }}>
                    {p.totalBytes > 0
                      ? `${formatBytes(used)} / ${formatBytes(p.totalBytes)}`
                      : `${formatBytes(used)}（不限量）`}
                    <span style={{ marginLeft: 10 }}>节点 {p.nodeCount}</span>
                  </div>
                  <div className="dim" style={{ fontSize: 12, marginTop: 3 }}>
                    {p.autoUpdate ? `自动更新 · 每 ${p.updateIntervalMin} 分钟` : "手动更新"}
                  </div>
                  {p.totalBytes > 0 && (
                    <div className="progress-track" style={{ marginTop: 10, maxWidth: 260 }}>
                      <div
                        className="progress-fill"
                        style={{ width: `${pct}%`, background: pct >= 90 ? "var(--red)" : undefined }}
                      />
                    </div>
                  )}
                </div>
                <div className="profile-right">
                  <span className="dim" style={{ fontSize: 13 }}>
                    {formatRelative(p.updatedAt)}
                  </span>
                  {p.active ? (
                    <span className="profile-check">
                      <IconCheck size={14} />
                    </span>
                  ) : (
                    <span className="profile-check ghost" title="未激活" />
                  )}
                  <button
                    className="row-btn accent"
                    title="分流规则：指定域名只走指定节点"
                    onClick={(e) => {
                      e.stopPropagation();
                      setRulesProfile(p);
                    }}
                  >
                    <IconRule size={15} />
                  </button>
                  <button
                    className="row-btn accent"
                    title="编辑订阅"
                    onClick={(e) => {
                      e.stopPropagation();
                      openEdit(p);
                    }}
                  >
                    <IconPencil size={15} />
                  </button>
                  <span onClick={(e) => e.stopPropagation()}>
                    <ConfirmButton className="row-btn" confirmText="确认?" onConfirm={() => remove(p)}>
                      <IconTrash size={16} />
                    </ConfirmButton>
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* ---------------- 自定义节点 ---------------- */}
      <div className="cfg-section" style={{ marginTop: 24 }}>
        <div className="cfg-section-head">
          <IconImport size={18} />
          <span className="cfg-section-title">自定义节点</span>
          {nodes.length > 0 && <span className="pill">{nodes.length}</span>}
          <span className="cfg-hint-inline">这些节点会与订阅节点一起出现在代理页</span>
        </div>
        <div className="card" style={{ padding: "6px 20px" }}>
          {nodes.length === 0 ? (
            <div className="dim" style={{ padding: "18px 0", textAlign: "center" }}>
              还没有自定义节点。用右上角「导入节点」粘贴 vless:// vmess:// ss:// 等分享链接。
            </div>
          ) : (
            <div className="cfg-list">
              {nodes.map((name) => (
                <div key={name} className="cfg-row" style={removingNode === name ? { opacity: 0.5 } : undefined}>
                  <span className="cfg-row-text" title={name}>
                    {name}
                  </span>
                  <ConfirmButton
                    className="row-btn"
                    confirmText="确认?"
                    onConfirm={() => removeNode(name)}
                    disabled={removingNode === name}
                  >
                    <IconTrash size={15} />
                  </ConfirmButton>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* ---------------- 直连白名单 ---------------- */}
      <div className="cfg-section" style={{ marginTop: 24 }}>
        <div className="cfg-section-head">
          <IconShield size={18} />
          <span className="cfg-section-title">直连白名单</span>
          <span className="pill">
            {whitelist.length} / {WHITELIST_LIMIT}
          </span>
        </div>
        <div className="cfg-subtitle">名单里的网址不走加速，始终直接连接。支持 example.com、*.example.com、1.2.3.0/24</div>
        <div className="card" style={{ padding: "6px 20px" }}>
          {whitelist.length === 0 ? (
            <div className="dim" style={{ padding: "18px 0", textAlign: "center" }}>
              白名单为空。在下方输入域名或 IP 段添加，例如 example.com。
            </div>
          ) : (
            <div className="cfg-list">
              {whitelist.map((item) => (
                <div key={item} className="cfg-row">
                  <span className="cfg-row-text" title={item}>
                    {item}
                  </span>
                  <ConfirmButton
                    className="row-btn"
                    confirmText="确认?"
                    onConfirm={() => saveWhitelist(whitelist.filter((x) => x !== item))}
                    disabled={wlSaving}
                  >
                    <IconTrash size={15} />
                  </ConfirmButton>
                </div>
              ))}
            </div>
          )}
          <div className="wl-add-row">
            <input
              style={{ flex: 1 }}
              placeholder="输入域名或 IP 段，如 example.com / 1.2.3.0/24"
              value={wlInput}
              maxLength={200}
              onChange={(e) => setWlInput(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && addWhitelist()}
            />
            <button className="btn primary" onClick={addWhitelist} disabled={wlSaving}>
              添加
            </button>
          </div>
        </div>
      </div>

      {/* ---------------- 弹窗 ---------------- */}

      <Modal open={addOpen} title="添加订阅" onClose={() => !adding && setAddOpen(false)}>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <input
            placeholder="订阅链接 https://…"
            value={addUrl}
            onChange={(e) => setAddUrl(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submitAdd()}
            style={{ width: "100%" }}
          />
          <input
            placeholder="名称（留空自动获取）"
            value={addName}
            onChange={(e) => setAddName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submitAdd()}
            style={{ width: "100%" }}
          />
        </div>
        <div className="modal-actions">
          <button className="btn" onClick={() => setAddOpen(false)} disabled={adding}>
            取消
          </button>
          <button className="btn primary" onClick={submitAdd} disabled={adding || !addUrl.trim()}>
            {adding ? "拉取中…" : "添加"}
          </button>
        </div>
      </Modal>

      <Modal open={!!editing} title="编辑订阅" onClose={() => !saving && setEditing(null)}>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <input
            placeholder="订阅名称"
            value={editName}
            onChange={(e) => setEditName(e.target.value)}
            style={{ width: "100%" }}
          />
          <input
            placeholder="订阅链接 https://…"
            value={editUrl}
            onChange={(e) => setEditUrl(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submitEdit()}
            style={{ width: "100%" }}
          />
          <div className="dim" style={{ fontSize: 12 }}>
            修改链接后会立即重新拉取订阅内容。
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 12, paddingTop: 2 }}>
            <span style={{ fontSize: 14 }}>自动更新</span>
            <span className="dim" style={{ fontSize: 12 }}>
              定时重新拉取订阅
            </span>
            <span style={{ flex: 1 }} />
            <Switch on={editAutoUpdate} onChange={setEditAutoUpdate} title="自动更新" />
          </div>
          {editAutoUpdate && (
            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <span style={{ fontSize: 14 }}>更新间隔（分钟）</span>
              <span style={{ flex: 1 }} />
              <input
                value={editInterval}
                onChange={(e) => setEditInterval(e.target.value.replace(/[^\d]/g, "").slice(0, 5))}
                style={{ width: 96, textAlign: "right", fontVariantNumeric: "tabular-nums" }}
                placeholder="1440"
              />
            </div>
          )}
          {editAutoUpdate && (
            <div className="dim" style={{ fontSize: 12, marginTop: -4 }}>
              范围 30–10080 分钟，默认 1440（每天一次）。
            </div>
          )}
        </div>
        <div className="modal-actions">
          <button className="btn" onClick={() => setEditing(null)} disabled={saving}>
            取消
          </button>
          <button className="btn primary" onClick={submitEdit} disabled={saving || !editUrl.trim()}>
            {saving ? "保存中…" : "保存"}
          </button>
        </div>
      </Modal>

      <Modal open={importOpen} title="导入节点" onClose={() => !importing && setImportOpen(false)}>
        <div className="dim" style={{ fontSize: 12.5 }}>
          每行一条分享链接，支持 vless:// vmess:// ss:// hysteria2:// trojan://
        </div>
        <textarea
          className="modal-textarea"
          rows={8}
          placeholder={"vless://…\nvmess://eyJhZGQ…\nss://YWVzLTI1Ni…"}
          value={importRaw}
          onChange={(e) => setImportRaw(e.target.value)}
          spellCheck={false}
        />
        <div className="modal-actions">
          <button className="btn" onClick={() => setImportOpen(false)} disabled={importing}>
            取消
          </button>
          <button className="btn primary" onClick={submitImportNodes} disabled={importing || !importRaw.trim()}>
            {importing ? "导入中…" : "导入"}
          </button>
        </div>
      </Modal>

      <Modal open={qrOpen} title="导入二维码" onClose={() => !qrBusy && setQrOpen(false)}>
        <div className="dim" style={{ fontSize: 12.5, lineHeight: 1.7 }}>
          选择一张含二维码的图片，或把截图直接 ⌘V / Ctrl+V 粘贴到窗口。
          <br />
          节点分享码会导入节点，订阅码会自动添加订阅。
        </div>
        <div className="qr-actions">
          <button className="btn primary" onClick={() => qrFileRef.current?.click()} disabled={qrBusy}>
            {qrBusy ? "识别中…" : "选择图片"}
          </button>
          <button className="btn" onClick={() => setQrOpen(false)} disabled={qrBusy}>
            关闭
          </button>
        </div>
        <input
          ref={qrFileRef}
          type="file"
          accept="image/*"
          style={{ display: "none" }}
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void handleQrFile(f);
            e.target.value = "";
          }}
        />
      </Modal>

      <RouteRulesModal profile={rulesProfile} onClose={() => setRulesProfile(null)} />
    </div>
  );
}
