import jsQR from "jsqr";

/**
 * 二维码解码工具：纯 JS（jsqr），从图片文件 / 剪贴板图片解出文本。
 * 大图会等比缩到 1600px 内再解码，避免超大截图拖慢主线程。
 */

/** 支持的节点分享链接协议前缀。 */
export const NODE_LINK_PREFIXES = ["vless://", "vmess://", "ss://", "hysteria2://", "hysteria://", "trojan://"];

export type LinkKind = "node" | "subscribe" | null;

/** 智能判断一段文本是节点分享链接、订阅链接还是未知内容。 */
export function classifyLink(text: string): LinkKind {
  const t = text.trim();
  if (!t) return null;
  const lower = t.toLowerCase();
  if (NODE_LINK_PREFIXES.some((p) => lower.startsWith(p))) return "node";
  if (/^https?:\/\//i.test(t)) return "subscribe";
  return null;
}

function loadImage(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error("图片加载失败"));
    img.src = src;
  });
}

/** 解码已加载的图片元素；未识别到二维码返回 null。 */
export function decodeQrFromImage(img: HTMLImageElement): string | null {
  const maxSide = 1600;
  const scale = Math.min(1, maxSide / Math.max(img.naturalWidth, img.naturalHeight));
  const w = Math.max(1, Math.round(img.naturalWidth * scale));
  const h = Math.max(1, Math.round(img.naturalHeight * scale));
  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  if (!ctx) return null;
  ctx.drawImage(img, 0, 0, w, h);
  const data = ctx.getImageData(0, 0, w, h);
  const result = jsQR(data.data, data.width, data.height, { inversionAttempts: "attemptBoth" });
  return result?.data?.trim() ? result.data.trim() : null;
}

/** 从图片文件（本地选择 / 剪贴板拖来的 Blob）解码二维码；失败返回 null。 */
export async function decodeQrFromFile(file: File | Blob): Promise<string | null> {
  const url = URL.createObjectURL(file);
  try {
    const img = await loadImage(url);
    return decodeQrFromImage(img);
  } finally {
    URL.revokeObjectURL(url);
  }
}
