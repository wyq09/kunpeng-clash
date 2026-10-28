/**
 * 主题系统：模式（自动/浅色/深色）+ 纯黑模式 + 主题色彩。
 * - App 根元素（<html>）由 data-theme / data-pureblack / data-accent 属性驱动；
 * - 主色及其衍生（hover/active/dim/边框/文字）由 JS 计算后写入 CSS 变量；
 * - 持久化 localStorage（kp.theme.*）；自动模式监听 prefers-color-scheme 实时切换。
 */

export type ThemeMode = "auto" | "light" | "dark";
export type PureBlackMode = "standard" | "black";

export const DEFAULT_ACCENT = "#4078dc";

export const ACCENT_PRESETS: { value: string; label: string }[] = [
  { value: "#4078dc", label: "蓝" },
  { value: "#8b5cf6", label: "紫" },
  { value: "#ef4444", label: "红" },
  { value: "#f97316", label: "橙" },
  { value: "#eab308", label: "黄" },
  { value: "#22c55e", label: "绿" },
  { value: "#06b6d4", label: "青" },
  { value: "#64748b", label: "灰" },
];

export interface ThemeState {
  mode: ThemeMode;
  pureblack: PureBlackMode;
  accent: string;
}

const LS_MODE = "kp.theme.mode";
const LS_BLACK = "kp.theme.pureblack";
const LS_ACCENT = "kp.theme.accent";

/* ---------------- 颜色工具 ---------------- */

interface Rgb {
  r: number;
  g: number;
  b: number;
}

export function isHexColor(value: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(value) || /^#[0-9a-fA-F]{3}$/.test(value);
}

function hexToRgb(hex: string): Rgb {
  let h = hex.replace("#", "");
  if (h.length === 3) h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2];
  const n = parseInt(h, 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}

function toHex({ r, g, b }: Rgb): string {
  const c = (x: number) => Math.round(Math.max(0, Math.min(255, x))).toString(16).padStart(2, "0");
  return `#${c(r)}${c(g)}${c(b)}`;
}

/** 向 target（#ffffff / #000000）线性混合 ratio 比例。 */
function mix(hex: string, target: string, ratio: number): string {
  const a = hexToRgb(hex);
  const b = hexToRgb(target);
  return toHex({
    r: a.r + (b.r - a.r) * ratio,
    g: a.g + (b.g - a.g) * ratio,
    b: a.b + (b.b - a.b) * ratio,
  });
}

function rgba({ r, g, b }: Rgb, alpha: number): string {
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

/** 相对亮度（sRGB 加权，0~1），用于决定主色上的文字用深还是浅。 */
function luminance({ r, g, b }: Rgb): number {
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
}

/* ---------------- 状态与持久化 ---------------- */

let state: ThemeState = {
  mode: "dark",
  pureblack: "standard",
  accent: DEFAULT_ACCENT,
};

function loadState() {
  try {
    const mode = localStorage.getItem(LS_MODE);
    const pureblack = localStorage.getItem(LS_BLACK);
    const accent = localStorage.getItem(LS_ACCENT);
    if (mode === "auto" || mode === "light" || mode === "dark") state.mode = mode;
    if (pureblack === "standard" || pureblack === "black") state.pureblack = pureblack;
    if (accent && isHexColor(accent)) state.accent = accent;
  } catch {
    /* 存储不可用时用默认值 */
  }
}

function saveState() {
  try {
    localStorage.setItem(LS_MODE, state.mode);
    localStorage.setItem(LS_BLACK, state.pureblack);
    localStorage.setItem(LS_ACCENT, state.accent);
  } catch {
    /* 忽略存储失败 */
  }
}

/* ---------------- 应用到 DOM ---------------- */

/** 系统当前深浅色（auto 模式的实际落点）。 */
export function systemTheme(): "light" | "dark" {
  try {
    return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  } catch {
    return "dark";
  }
}

/** 当前生效主题（auto 解析后）。 */
export function effectiveTheme(): "light" | "dark" {
  return state.mode === "auto" ? systemTheme() : state.mode;
}

/** 把当前主题状态写到 <html> 的属性与 CSS 变量上。 */
function apply() {
  const root = document.documentElement;
  const eff = effectiveTheme();
  root.dataset.theme = eff;
  // 纯黑仅在深色下生效（CSS 里按 data-theme 拦截，这里只透传用户选择）
  root.dataset.pureblack = state.pureblack === "black" ? "true" : "false";
  root.dataset.accent = state.accent;

  const rgb = hexToRgb(state.accent);
  root.style.setProperty("--accent", state.accent);
  root.style.setProperty("--accent-hover", mix(state.accent, "#ffffff", 0.1)); // 亮 10%
  root.style.setProperty("--accent-active", mix(state.accent, "#000000", 0.1)); // 暗 10%
  root.style.setProperty("--accent-dim", rgba(rgb, 0.16));
  root.style.setProperty("--accent-sel", rgba(rgb, 0.35));
  root.style.setProperty("--accent-border", rgba(rgb, 0.5));
  // 深色下强调色文字提亮保证可读；浅色场景直接用同 accent
  root.style.setProperty("--accent-text", eff === "dark" ? mix(state.accent, "#ffffff", 0.35) : state.accent);
  // 主色上的文字：亮色主色（黄/橙等）配深字，其余配白字
  root.style.setProperty("--on-accent", luminance(rgb) > 0.62 ? "#1a1c1e" : "#ffffff");
}

/* ---------------- 对外 API ---------------- */

export function getTheme(): Readonly<ThemeState> {
  return state;
}

export function setTheme(patch: Partial<ThemeState>) {
  state = { ...state, ...patch };
  saveState();
  apply();
}

let initialized = false;

/** 应用启动时调用一次：读持久化 + 首次应用 + 监听系统深浅色变化。 */
export function initTheme() {
  if (initialized) return;
  initialized = true;
  loadState();
  apply();
  try {
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const onChange = () => {
      if (state.mode === "auto") apply();
    };
    if (typeof mq.addEventListener === "function") {
      mq.addEventListener("change", onChange);
    } else if (typeof mq.addListener === "function") {
      mq.addListener(onChange); // 兼容旧 WebView
    }
  } catch {
    /* matchMedia 不可用时 auto 退化为当前判断 */
  }
}
