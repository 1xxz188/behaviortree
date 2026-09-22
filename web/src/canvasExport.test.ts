import assert from "node:assert/strict";
import test from "node:test";
import { canvasExportOptions } from "./canvasExport.ts";

// 负坐标及视口外的节点仍落在导出留白以内，计算不依赖当前视口大小或缩放。
test("PNG 完整边界包含负坐标并保留留白", () => {
  const options = canvasExportOptions({ x: -450, y: -120, width: 1400, height: 900 });
  assert.equal(options.width, 1496);
  assert.equal(options.height, 996);
  assert.equal(options.style.transform, "translate(498px, 168px) scale(1)");
  assert.equal(options.backgroundColor, "#111c25");
});

// 极宽、极高和大面积画布缩小输出，避免创建超过浏览器安全预算的位图。
test("PNG 输出边长和总像素受到限制", () => {
  for (const [width, height] of [[100_000, 100], [100, 100_000], [100_000, 100_000]]) {
    const options = canvasExportOptions({ x: 0, y: 0, width: width!, height: height! });
    assert.ok(options.canvasWidth > 0 && options.canvasWidth <= 8192);
    assert.ok(options.canvasHeight > 0 && options.canvasHeight <= 8192);
    assert.ok(options.canvasWidth * options.canvasHeight <= 16_000_000);
  }
});

// 非有限边界不能传给浏览器图像编码器，防止静默产生空图片。
test("PNG 拒绝非法画布尺寸", () => {
  assert.throws(() => canvasExportOptions({ x: NaN, y: 0, width: 100, height: 100 }), /画布尺寸无效/);
  assert.throws(() => canvasExportOptions({ x: 0, y: 0, width: Infinity, height: 100 }), /画布尺寸无效/);
});

// PNG 只保留工程内容，节点气泡与临时框选、连线控件不能混入导出图像。
test("PNG 排除节点注释气泡而保留节点正文", () => {
  const options = canvasExportOptions({ x: 0, y: 0, width: 100, height: 100 });
  for (const name of ["node-comment-toggle", "vue-flow__nodesselection", "vue-flow__connectionline", "bt-node"]) {
    const element = { classList: { contains: (value: string) => value === name } } as HTMLElement;
    assert.equal(options.filter(element), name === "bt-node");
  }
});
