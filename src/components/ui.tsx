import React, { useEffect, useMemo, useState } from "react";
import { IconClose } from "./Icons";

/** 基础 UI 原子组件：开关 / 迷你折线图 / 分段控件 / 模态 / 延迟徽章。 */

/* ---------------- 开关 ---------------- */

export function Switch({
  on,
  onChange,
  disabled,
  title,
}: {
  on: boolean;
  onChange?: (next: boolean) => void;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      className={`switch${on ? " on" : ""}`}
      disabled={disabled}
      title={title}
      onClick={() => onChange?.(!on)}
    />
  );
}

/* ---------------- Sparkline（自绘 SVG 折线，不引图表库） ---------------- */

export function Sparkline({
  values,
  width = 240,
  height = 56,
  stroke = "var(--accent)",
  fill = "var(--accent-dim)",
}: {
  values: number[];
  width?: number;
  height?: number;
  stroke?: string;
  fill?: string;
}) {
  const { line, area } = useMemo(() => {
    const pts = values.length >= 2 ? values.slice(-60) : [0, 0];
    const max = Math.max(...pts, 1);
    const pad = 2;
    const stepX = (width - pad * 2) / (pts.length - 1);
    const y = (v: number) => height - pad - (v / max) * (height - pad * 2);
    let linePath = "";
    let areaPath = "";
    pts.forEach((v, i) => {
      const x = pad + i * stepX;
      linePath += `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y(v).toFixed(1)}`;
    });
    areaPath =
      linePath + `L${(pad + (pts.length - 1) * stepX).toFixed(1)},${height - pad}L${pad},${height - pad}Z`;
    return { line: linePath, area: areaPath };
  }, [values, width, height]);

  // SVG 展示属性不支持 var()，颜色经 style 注入以跟随主题 accent
  return (
    <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
      <path d={area} style={{ fill }} />
      <path d={line} style={{ stroke, fill: "none" }} strokeWidth={1.5} />
    </svg>
  );
}

/* ---------------- 分段控件 ---------------- */

export function Segmented<T extends string>({
  options,
  value,
  onChange,
}: {
  options: { value: T; label: React.ReactNode }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div className="segmented">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          className={value === o.value ? "active" : ""}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/* ---------------- 模态 ---------------- */

export function Modal({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div className="modal-mask" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal">
        <div style={{ display: "flex", alignItems: "center" }}>
          <h3 style={{ flex: 1 }}>{title}</h3>
          <button className="icon-btn" onClick={onClose} style={{ background: "transparent", border: "none" }}>
            <IconClose />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

/* ---------------- 延迟徽章 ---------------- */

/**
 * 延迟徽章：绿色 <300 / 黄色 <800 / 红色 ≥800；失败超时为灰色「超时」（不用红色制造恐慌）。
 * testing=true 时显示「测速中…」，作为节点级/组级测速的过程反馈。
 */
export function DelayBadge({
  delay,
  testing,
  size,
}: {
  delay: number | undefined;
  testing?: boolean;
  size?: number;
}) {
  let cls: string;
  let text: string;
  if (testing) {
    cls = "testing";
    text = "测速中…";
  } else if (delay === undefined || delay === null) {
    cls = "none";
    text = "未测";
  } else if (delay <= 0) {
    cls = "none";
    text = "超时";
  } else if (delay < 300) {
    cls = "ok";
    text = `${delay} ms`;
  } else if (delay < 800) {
    cls = "mid";
    text = `${delay} ms`;
  } else {
    cls = "bad";
    text = `${delay} ms`;
  }
  return (
    <span className={`delay-badge ${cls}`} style={size ? { fontSize: size } : undefined}>
      {text}
    </span>
  );
}

/* ---------------- 下拉选择（Tools 页只读展示用） ---------------- */

export function SelectChip({ value, disabled }: { value: string; disabled?: boolean }) {
  return (
    <span className="pill" style={{ opacity: disabled ? 0.6 : 1, fontSize: 13, padding: "5px 12px" }}>
      {value}
    </span>
  );
}

/** 行内两步确认删除按钮：第一次点变红确认态，再点执行。 */
export function ConfirmButton({
  onConfirm,
  children,
  confirmText = "确认删除?",
  className = "btn",
  disabled,
}: {
  onConfirm: () => void;
  children?: React.ReactNode;
  confirmText?: string;
  className?: string;
  disabled?: boolean;
}) {
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(t);
  }, [armed]);
  return (
    <button
      type="button"
      disabled={disabled}
      className={className}
      style={armed ? { background: "var(--red-dim)", color: "var(--red)" } : undefined}
      onClick={() => {
        if (armed) {
          setArmed(false);
          onConfirm();
        } else {
          setArmed(true);
        }
      }}
    >
      {armed ? confirmText : children}
    </button>
  );
}
