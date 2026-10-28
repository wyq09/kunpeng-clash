import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { CoreStatus, MihomoConnectionsPayload } from "../api";
import type { RawConnection } from "../mock";
import { formatBytes } from "../format";
import { IconClose, IconSearch, IconTrashCircle } from "../components/Icons";
import { useToast } from "../components/Toast";

/** 实时连接：连接表格（进程/目标/规则/出口/速率/总量）+ 搜索 + 关闭。 */

const MAX_ROWS = 200;

interface ConnRow extends RawConnection {
  rateUp: number;
  rateDown: number;
}

export default function Connections({ status }: { status: CoreStatus }) {
  const { showToast } = useToast();
  const [rows, setRows] = useState<ConnRow[]>([]);
  const [totals, setTotals] = useState({ up: 0, down: 0 });
  const [search, setSearch] = useState("");
  const [searchOpen, setSearchOpen] = useState(false);
  const prevRef = useRef<Map<string, { up: number; down: number }>>(new Map());

  useEffect(() => {
    const un = api.onConnections((snap) => {
      const payload = api.parseConnections(snap);
      const prev = prevRef.current;
      const next = new Map<string, { up: number; down: number }>();
      const list: ConnRow[] = (payload.connections ?? []).map((c) => {
        const p = prev.get(c.id);
        next.set(c.id, { up: c.upload, down: c.download });
        return {
          ...c,
          rateUp: p ? Math.max(0, c.upload - p.up) : 0,
          rateDown: p ? Math.max(0, c.download - p.down) : 0,
        };
      });
      prevRef.current = next;
      list.sort((a, b) => (b.download + b.upload) - (a.download + a.upload));
      setRows(list);
      setTotals({ up: payload.uploadTotal ?? 0, down: payload.downloadTotal ?? 0 });
    });
    return un;
  }, []);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((c) =>
      [
        c.metadata.host,
        c.metadata.destinationIP,
        c.metadata.process,
        c.rule,
        c.rulePayload,
        c.chains.join(" "),
      ]
        .join(" ")
        .toLowerCase()
        .includes(q),
    );
  }, [rows, search]);

  const truncated = filtered.length > MAX_ROWS;
  const view = truncated ? filtered.slice(0, MAX_ROWS) : filtered;

  const targetText = useCallback((c: ConnRow) => {
    const port = c.metadata.destinationPort || "";
    if (c.metadata.host) return `${c.metadata.host}:${port}`;
    return `${c.metadata.destinationIP || "?"}:${port}`;
  }, []);

  const exitText = useCallback((c: ConnRow) => {
    if (c.chains.length === 0) return "—";
    const last = c.chains[c.chains.length - 1];
    return last || "—";
  }, []);

  const ruleText = useCallback((c: ConnRow) => {
    if (c.rulePayload) return `${c.rule} ${c.rulePayload}`;
    return c.rule || "—";
  }, []);

  const closeConn = useCallback(
    async (c: ConnRow) => {
      try {
        await api.deleteConnection(c.id);
        setRows((prev) => prev.filter((r) => r.id !== c.id));
        showToast("已关闭该连接", "success");
      } catch (e) {
        showToast(`关闭失败：${e instanceof Error ? e.message : String(e)}`, "error");
      }
    },
    [showToast],
  );

  const closeAll = useCallback(async () => {
    try {
      await api.closeAllConnections();
      setRows([]);
      showToast("已关闭全部连接", "success");
    } catch (e) {
      showToast(`关闭失败：${e instanceof Error ? e.message : String(e)}`, "error");
    }
  }, [showToast]);

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">实时连接</h1>
        <div className="spacer" />
        <div className="header-actions">
          {searchOpen && (
            <input
              autoFocus
              className="header-search"
              placeholder="搜索 host / 进程 / 规则…"
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
          <button className="icon-btn danger" title="关闭全部连接" onClick={closeAll}>
            <IconTrashCircle />
          </button>
        </div>
      </div>

      <div className="card" style={{ padding: "8px 14px 12px" }}>
        <div className="dim" style={{ fontSize: 12, padding: "8px 6px", display: "flex", gap: 18 }}>
          <span>连接 {rows.length}</span>
          <span>总下载 {formatBytes(totals.down)}</span>
          <span>总上传 {formatBytes(totals.up)}</span>
          {status.running ? null : <span style={{ color: "var(--red)" }}>内核未运行</span>}
        </div>
        <div style={{ overflowX: "auto" }}>
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: "10%" }}>进程</th>
                <th style={{ width: "20%" }}>目标</th>
                <th style={{ width: "15%" }}>规则</th>
                <th style={{ width: "13%" }}>出口</th>
                <th className="num" style={{ width: "8%" }}>
                  下载
                </th>
                <th className="num" style={{ width: "8%" }}>
                  上传
                </th>
                <th className="num" style={{ width: "9%" }}>
                  下载总量
                </th>
                <th className="num" style={{ width: "9%" }}>
                  上传总量
                </th>
                <th style={{ width: "4%" }} />
              </tr>
            </thead>
            <tbody>
              {view.length === 0 && (
                <tr>
                  <td colSpan={9} className="dim" style={{ textAlign: "center", padding: "36px 0" }}>
                    {status.running ? "暂无连接" : "内核未运行"}
                  </td>
                </tr>
              )}
              {view.map((c) => (
                <tr key={c.id}>
                  <td className="dim" title={c.metadata.processPath || c.metadata.process}>
                    {c.metadata.process || "—"}
                  </td>
                  <td title={targetText(c)}>{targetText(c)}</td>
                  <td title={ruleText(c)}>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", maxWidth: 180, display: "inline-block" }}>
                      {ruleText(c)}
                    </span>
                  </td>
                  <td>
                    <span className="pill" style={{ maxWidth: 150 }}>
                      <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{exitText(c)}</span>
                    </span>
                  </td>
                  <td className="num dim">{formatBytes(c.rateDown)}/s</td>
                  <td className="num dim">{formatBytes(c.rateUp)}/s</td>
                  <td className="num">{formatBytes(c.download)}</td>
                  <td className="num">{formatBytes(c.upload)}</td>
                  <td>
                    <button className="row-btn" title="关闭该连接" onClick={() => closeConn(c)}>
                      <IconClose size={15} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {truncated && (
          <div className="dim" style={{ fontSize: 12, padding: "10px 6px 0" }}>
            连接过多，已截断显示前 {MAX_ROWS} 条（共 {filtered.length} 条），可用搜索缩小范围。
          </div>
        )}
      </div>
    </div>
  );
}
