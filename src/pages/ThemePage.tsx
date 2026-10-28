import React, { useState } from "react";
import {
  ACCENT_PRESETS,
  DEFAULT_ACCENT,
  effectiveTheme,
  getTheme,
  isHexColor,
  setTheme,
} from "../theme";
import type { PureBlackMode, ThemeMode } from "../theme";
import { IconCheck, IconChevronLeft, IconPencil } from "../components/Icons";

/** 主题设置子页（工具 → 主题）：模式 / 纯黑 / 主题色彩，参考 FlClash 主题页布局。 */

const MODES: { value: ThemeMode; label: string; desc: string }[] = [
  { value: "auto", label: "自动", desc: "跟随系统深浅色，实时切换" },
  { value: "light", label: "浅色", desc: "" },
  { value: "dark", label: "深色", desc: "" },
];

const BLACKS: { value: PureBlackMode; label: string; desc: string }[] = [
  { value: "standard", label: "标准", desc: "深灰背景（默认）" },
  { value: "black", label: "纯黑", desc: "纯黑背景 + 深灰卡片，仅深色主题下生效" },
];

function RadioDot({ selected }: { selected: boolean }) {
  return (
    <span className={`radio-dot${selected ? " selected" : ""}`}>
      {selected && <IconCheck size={13} />}
    </span>
  );
}

export default function ThemePage({ onBack }: { onBack: () => void }) {
  const [theme, setLocal] = useState(() => ({ ...getTheme() }));

  const update = (patch: Parameters<typeof setTheme>[0]) => {
    setTheme(patch);
    setLocal({ ...getTheme() });
  };

  const accentIsPreset = ACCENT_PRESETS.some((p) => p.value === theme.accent);
  const customColor = isHexColor(theme.accent) ? theme.accent : DEFAULT_ACCENT;
  const eff = effectiveTheme();

  return (
    <div style={{ maxWidth: 900 }}>
      <div className="page-header">
        <button className="icon-btn" title="返回工具" onClick={onBack}>
          <IconChevronLeft />
        </button>
        <h1 className="page-title">主题</h1>
      </div>

      {/* 主题模式 */}
      <div className="section-title">主题模式</div>
      <div className="settings-group">
        {MODES.map((m) => (
          <div
            key={m.value}
            className={`settings-row clickable${theme.mode === m.value ? " option-selected" : ""}`}
            onClick={() => update({ mode: m.value })}
          >
            <span className="settings-label">{m.label}</span>
            {m.desc && <span className="settings-desc">{m.desc}</span>}
            {m.value === "auto" && theme.mode === "auto" && (
              <span className="dim" style={{ fontSize: 12.5 }}>
                当前系统：{eff === "light" ? "浅色" : "深色"}
              </span>
            )}
            <span style={{ flex: 1 }} />
            <RadioDot selected={theme.mode === m.value} />
          </div>
        ))}
      </div>

      {/* 纯黑模式 */}
      <div className="section-title">纯黑模式</div>
      <div className="settings-group">
        {BLACKS.map((b) => (
          <div
            key={b.value}
            className={`settings-row clickable${theme.pureblack === b.value ? " option-selected" : ""}`}
            onClick={() => update({ pureblack: b.value })}
          >
            <span className="settings-label">{b.label}</span>
            <span className="settings-desc">{b.desc}</span>
            <span style={{ flex: 1 }} />
            <RadioDot selected={theme.pureblack === b.value} />
          </div>
        ))}
        {theme.mode === "light" && (
          <div className="settings-hint">当前为浅色主题，纯黑模式暂不生效。</div>
        )}
      </div>

      {/* 主题色彩 */}
      <div className="section-title">主题色彩</div>
      <div className="accent-card">
        {ACCENT_PRESETS.map((p) => (
          <button
            key={p.value}
            className={`accent-dot${theme.accent === p.value ? " selected" : ""}`}
            style={{ background: p.value }}
            title={p.label}
            onClick={() => update({ accent: p.value })}
          >
            {theme.accent === p.value && (
              <span className="accent-check">
                <IconCheck size={14} />
              </span>
            )}
          </button>
        ))}
        <label
          className={`accent-dot accent-custom${!accentIsPreset ? " selected" : ""}`}
          title="自定义颜色"
          style={!accentIsPreset ? { background: customColor } : undefined}
        >
          {accentIsPreset ? (
            <IconPencil size={15} />
          ) : (
            <span className="accent-check">
              <IconCheck size={14} />
            </span>
          )}
          <input
            type="color"
            value={customColor}
            onChange={(e) => update({ accent: e.target.value })}
          />
        </label>
        <span className="accent-label">自定义</span>
        <span style={{ flex: 1 }} />
        <span className="accent-label" style={{ fontVariantNumeric: "tabular-nums" }}>
          {theme.accent}
        </span>
        <button className="btn" onClick={() => update({ accent: DEFAULT_ACCENT })}>
          恢复默认
        </button>
      </div>
    </div>
  );
}
