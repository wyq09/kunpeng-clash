import React, { useState } from "react";
import { api, isMock } from "../api";
import type { CoreStatus } from "../api";
import { getTheme } from "../theme";
import { Switch, SelectChip } from "../components/ui";
import logoUrl from "../assets/logo.png";
import {
  IconBrush,
  IconChevronRight,
  IconCloud,
  IconFolder,
  IconInfo,
  IconKeyboard,
  IconLanguage,
  IconSettings,
  IconWrench,
  IconZap,
} from "../components/Icons";
import { useToast } from "../components/Toast";

/** 工具：设置组列表（语言/主题/自动连接/备份/数据目录/混合端口）+ 关于与版本信息。 */

const MODE_LABEL: Record<string, string> = { auto: "自动", light: "浅色", dark: "深色" };

export default function Tools({ status, onOpenTheme }: { status: CoreStatus; onOpenTheme: () => void }) {
  const { showToast } = useToast();
  const [port, setPort] = useState(String(status.mixedPort));
  const [savingPort, setSavingPort] = useState(false);

  const theme = getTheme();
  const themeValue = `${MODE_LABEL[theme.mode] ?? "深色"}${
    theme.mode === "dark" && theme.pureblack === "black" ? " · 纯黑" : ""
  }`;

  const portDirty = Number(port) !== status.mixedPort;

  const savePort = async () => {
    const n = Number(port);
    if (!Number.isInteger(n) || n < 1024 || n > 65535) {
      showToast("端口必须是 1024–65535 的整数", "error");
      return;
    }
    setSavingPort(true);
    try {
      await api.setPort(n);
      showToast(`混合端口已设为 ${n}`, "success");
    } catch (e) {
      showToast(`设置端口失败：${e instanceof Error ? e.message : String(e)}`, "error");
    } finally {
      setSavingPort(false);
    }
  };

  const setAutoConnect = async (on: boolean) => {
    try {
      await api.setAutoConnect(on);
      showToast(on ? "已开启自动连接" : "已关闭自动连接", "success");
    } catch (e) {
      showToast(`设置失败：${e instanceof Error ? e.message : String(e)}`, "error");
    }
  };

  const openDataDir = async () => {
    try {
      await api.openDataDir();
    } catch (e) {
      showToast(e instanceof Error ? e.message : String(e), "error");
    }
  };

  return (
    <div style={{ maxWidth: 900 }}>
      <div className="page-header">
        <h1 className="page-title">工具</h1>
      </div>

      <div className="section-title">设置</div>
      <div className="settings-group">
        <div className="settings-row">
          <IconLanguage className="dim" />
          <span className="settings-label">语言</span>
          <span style={{ flex: 1 }} />
          <SelectChip value="默认" disabled />
        </div>
        <div className="settings-row clickable" onClick={onOpenTheme}>
          <IconBrush className="dim" />
          <span className="settings-label">主题</span>
          <span style={{ flex: 1 }} />
          <span className="settings-value">{themeValue}</span>
          <IconChevronRight size={16} className="dim" />
        </div>
        <div className="settings-row">
          <IconZap className="dim" />
          <span className="settings-label">自动连接</span>
          <span style={{ flex: 1 }} />
          <Switch on={status.autoConnect} onChange={setAutoConnect} />
        </div>
      </div>

      <div className="section-title">数据与备份</div>
      <div className="settings-group">
        <div
          className="settings-row clickable"
          onClick={() => showToast("备份与恢复：开发中", "info")}
        >
          <IconCloud className="dim" />
          <span className="settings-label">备份与恢复</span>
          <span style={{ flex: 1 }} />
          <span className="pill yellow">开发中</span>
          <IconChevronRight size={16} className="dim" />
        </div>
        <div className="settings-row clickable" onClick={openDataDir}>
          <IconFolder className="dim" />
          <span className="settings-label">数据目录</span>
          <span style={{ flex: 1 }} />
          <span className="settings-value">~/Library/Application Support/KunpengClash</span>
          <IconChevronRight size={16} className="dim" />
        </div>
        <div className="settings-row">
          <IconKeyboard className="dim" />
          <span className="settings-label">快捷键管理</span>
          <span style={{ flex: 1 }} />
          <span className="pill yellow">开发中</span>
        </div>
      </div>

      <div className="section-title">进阶</div>
      <div className="settings-group">
        <div className="settings-row">
          <IconWrench className="dim" />
          <span className="settings-label">混合端口（HTTP / SOCKS）</span>
          <span style={{ flex: 1 }} />
          <input
            value={port}
            onChange={(e) => setPort(e.target.value.replace(/[^\d]/g, ""))}
            style={{ width: 110, textAlign: "right", fontVariantNumeric: "tabular-nums" }}
            onKeyDown={(e) => e.key === "Enter" && portDirty && savePort()}
          />
          <button className="btn" onClick={savePort} disabled={!portDirty || savingPort}>
            {savingPort ? "保存中…" : "保存"}
          </button>
        </div>
        <div className="settings-row">
          <IconSettings className="dim" />
          <span className="settings-label">自定义配置资源</span>
          <span style={{ flex: 1 }} />
          <span className="pill yellow">开发中</span>
        </div>
      </div>

      <div className="section-title">关于</div>
      <div className="card about-card">
        <img className="about-logo" src={logoUrl} alt="鲲鹏 Clash" />
        <div>
          <div className="about-name">鲲鹏 Clash</div>
          <div className="about-sub">
            版本 v{__APP_VERSION__} · 内核 {status.version || "—"}
            {isMock ? " · 浏览器调试（Mock 数据）" : ""}
          </div>
        </div>
      </div>

      <div className="section-title">版本信息</div>
      <div className="settings-group">
        <div className="settings-row">
          <IconInfo className="dim" />
          <span className="settings-label">应用版本</span>
          <span style={{ flex: 1 }} />
          <span className="settings-value">KunpengClash v{__APP_VERSION__}</span>
        </div>
        <div className="settings-row">
          <IconInfo className="dim" />
          <span className="settings-label">内核版本</span>
          <span style={{ flex: 1 }} />
          <span className="settings-value">{status.version || "—"}</span>
        </div>
        <div className="settings-row">
          <IconInfo className="dim" />
          <span className="settings-label">运行环境</span>
          <span style={{ flex: 1 }} />
          <span className="settings-value">
            {isMock ? "浏览器调试（Mock 数据）" : "mygo + mihomo 内嵌"}
          </span>
        </div>
      </div>
    </div>
  );
}
