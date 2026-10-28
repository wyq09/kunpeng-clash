import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
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
  const { line, area, tip } = useMemo(() => {
    const pts = values.length >= 2 ? values.slice(-60) : [0, 0];
    const max = Math.max(...pts, 1) * 1.08;
    const pad = 3;
    const stepX = (width - pad * 2) / (pts.length - 1);
    const xy = pts.map((v, i) => [pad + i * stepX, height - pad - (v / max) * (height - pad * 2)] as const);
    // Catmull-Rom → 三次贝塞尔：曲线平滑但不越过相邻点的上下界（不出现负值假波峰）
    let linePath = `M${xy[0][0].toFixed(1)},${xy[0][1].toFixed(1)}`;
    for (let i = 0; i < xy.length - 1; i++) {
      const p0 = xy[Math.max(0, i - 1)];
      const p1 = xy[i];
      const p2 = xy[i + 1];
      const p3 = xy[Math.min(xy.length - 1, i + 2)];
      const lo = Math.min(p1[1], p2[1]);
      const hi = Math.max(p1[1], p2[1]);
      const c1y = Math.min(hi, Math.max(lo, p1[1] + (p2[1] - p0[1]) / 6));
      const c2y = Math.min(hi, Math.max(lo, p2[1] - (p3[1] - p1[1]) / 6));
      linePath += `C${(p1[0] + stepX / 3).toFixed(1)},${c1y.toFixed(1)} ${(p2[0] - stepX / 3).toFixed(1)},${c2y.toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`;
    }
    const lastPt = xy[xy.length - 1];
    const areaPath = linePath + `L${lastPt[0].toFixed(1)},${height - pad}L${pad},${height - pad}Z`;
    return { line: linePath, area: areaPath, tip: lastPt };
  }, [values, width, height]);

  // SVG 展示属性不支持 var()，颜色经 style 注入以跟随主题 accent
  return (
    <div className="sparkline">
      <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
        <path d={area} style={{ fill }} />
        <path d={line} style={{ stroke, fill: "none" }} strokeWidth={1.6} vectorEffect="non-scaling-stroke" />
      </svg>
      <span
        className="sparkline-tip"
        style={{ left: `${(tip[0] / width) * 100}%`, top: `${(tip[1] / height) * 100}%`, background: stroke }}
      />
    </div>
  );
}

/* ---------------- 数字补间（大数字变化时平滑滚动，而不是跳变） ---------------- */

export function useTweened(target: number, duration = 450): number {
  const [value, setValue] = useState(target);
  const fromRef = useRef(target);
  const valueRef = useRef(target);
  valueRef.current = value;
  useEffect(() => {
    if (!Number.isFinite(target)) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduce || Math.abs(target - valueRef.current) < 1e-6) {
      setValue(target);
      return;
    }
    fromRef.current = valueRef.current;
    const start = performance.now();
    let raf = 0;
    const step = (t: number) => {
      const k = Math.min(1, (t - start) / duration);
      const e = 1 - Math.pow(1 - k, 3);
      setValue(fromRef.current + (target - fromRef.current) * e);
      if (k < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [target, duration]);
  return value;
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
  // 选中滑块：测量当前按钮位置，用一块绝对定位的底色平滑滑过去
  const wrapRef = useRef<HTMLDivElement>(null);
  const [thumb, setThumb] = useState<{ x: number; w: number } | null>(null);
  const measure = useCallback(() => {
    const wrap = wrapRef.current;
    const btn = wrap?.querySelector<HTMLButtonElement>(`button[data-value="${CSS.escape(value)}"]`);
    if (wrap && btn) setThumb({ x: btn.offsetLeft, w: btn.offsetWidth });
  }, [value]);
  useLayoutEffect(measure, [measure, options.length]);
  useEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(wrap);
    return () => ro.disconnect();
  }, [measure]);

  return (
    <div className="segmented" ref={wrapRef}>
      {thumb && <span className="seg-thumb" style={{ transform: `translateX(${thumb.x}px)`, width: thumb.w }} />}
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          data-value={o.value}
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
  wide,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  /** 宽版（编辑器类弹窗，如分流规则） */
  wide?: boolean;
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
  return createPortal(
    <div className="modal-mask" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-label={title} style={wide ? { width: 720, maxWidth: "92vw" } : undefined}>
        <div style={{ display: "flex", alignItems: "center" }}>
          <h3 style={{ flex: 1 }}>{title}</h3>
          <button className="icon-btn" onClick={onClose} style={{ background: "transparent", border: "none" }}>
            <IconClose />
          </button>
        </div>
        {children}
      </div>
    </div>,
    document.body,
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
