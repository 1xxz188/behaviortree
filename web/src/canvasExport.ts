import { toBlob } from "html-to-image";

// 采用 Vue Flow 的实际测量边界，兼容负坐标、换行节点和视口外节点。
interface CanvasBounds {
  x: number; // 最左侧节点的画布坐标。
  y: number; // 最上侧节点的画布坐标。
  width: number; // 所有节点占用的宽度。
  height: number; // 所有节点占用的高度。
}

// 仅在导出时克隆一次画布；尺寸上限同时约束边长和总像素，避免巨图耗尽内存。
export function canvasExportOptions(bounds: CanvasBounds) {
  const padding = 48;
  const width = Math.ceil(bounds.width + padding * 2);
  const height = Math.ceil(bounds.height + padding * 2);
  if (![bounds.x, bounds.y, width, height].every(Number.isFinite) || width <= 0 || height <= 0) {
    throw new Error("画布尺寸无效，无法导出 PNG");
  }
  const scale = Math.min(2, 8192 / width, 8192 / height, Math.sqrt(16_000_000 / (width * height)));
  return {
    width,
    height,
    canvasWidth: Math.max(1, Math.floor(width * scale)),
    canvasHeight: Math.max(1, Math.floor(height * scale)),
    pixelRatio: 1,
    backgroundColor: "#111c25",
    // 页面只使用系统字体，不扫描样式表或请求外部字体。
    skipFonts: true,
    style: {
      transform: `translate(${padding - bounds.x}px, ${padding - bounds.y}px) scale(1)`,
      transformOrigin: "0 0",
    },
    // 排除临时连线与框选层，保留实际节点、连线和顺序标签。
    filter: (element: HTMLElement) => !element.classList?.contains("vue-flow__connectionline")
      && !element.classList?.contains("vue-flow__nodesselection"),
  };
}

// 只转换克隆后的变换层，不改变编辑器当前缩放、位置和选中状态。
export async function exportCanvasPNG(element: HTMLElement, bounds: CanvasBounds): Promise<Blob> {
  const blob = await toBlob(element, canvasExportOptions(bounds));
  if (!blob) throw new Error("浏览器未能生成 PNG，请重试");
  return blob;
}
