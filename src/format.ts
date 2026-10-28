/** 数字/时间格式化工具。 */

const UNIT = ["B", "KB", "MB", "GB", "TB", "PB"];

/** 字节数 → "532 GB"（可带 1 位小数）。 */
export function formatBytes(n: number, digits?: number): string {
  if (!Number.isFinite(n) || n < 0) return "0 B";
  let i = 0;
  let v = n;
  while (v >= 1024 && i < UNIT.length - 1) {
    v /= 1024;
    i++;
  }
  const d = digits ?? (i === 0 ? 0 : v >= 100 ? 1 : v >= 10 ? 1 : 2);
  return `${v.toFixed(d)} ${UNIT[i]}`;
}

/** 速率（字节/秒）→ "1.1 KB/s"。 */
export function formatRate(n: number): string {
  return `${formatBytes(n, n >= 1024 * 1024 ? 1 : n >= 10240 ? 1 : 2)}/s`;
}

/** 秒 → "07:29:37" 或 "12:05"。 */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (x: number) => String(x).padStart(2, "0");
  if (h > 0) return `${pad(h)}:${pad(m)}:${pad(sec)}`;
  return `${pad(m)}:${pad(sec)}`;
}

/** 相对时间："1 小时前"。 */
export function formatRelative(iso: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "—";
  const diff = Math.max(0, Date.now() - then);
  const min = Math.floor(diff / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} 天前`;
  return new Date(iso).toLocaleDateString("zh-CN");
}
