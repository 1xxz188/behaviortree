import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";

// 执行真实视口适应函数，只模拟 Vue 更新、节点测量和 Vue Flow 视口接口。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const handlers = script.statements.filter(statement => ts.isFunctionDeclaration(statement)
  && ["fitCanvasWhenReady", "requestCanvasFit"].includes(statement.name?.text ?? ""))
  .map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 会话允许逐步推进节点尺寸与视口状态，重现加载时的异步顺序。
function session() {
  const projectReady = { value: true };
  const tree = { value: { nodes: [{ id: "root" }, { id: "child" }] } };
  const nodeIndex = { value: new Map([["root", true], ["child", true]]) };
  const getNodes = { value: [
    { id: "root", dimensions: { width: 0, height: 0 } },
    { id: "child", dimensions: { width: 0, height: 0 } },
  ] };
  const ticks: (() => void)[] = [];
  const fits: { padding: number }[] = [];
  let fitResult = true;
  const context = {
    projectReady, tree, nodeIndex, getNodes,
    nextTick: (callback: () => void) => { ticks.push(callback); },
    fitView: async (options: { padding: number }) => { fits.push(options); return fitResult; },
  };
  const app = runInNewContext(`let pendingCanvasFit = 0;\n${js}\n({ fitCanvasWhenReady, requestCanvasFit });`, context) as {
    fitCanvasWhenReady: () => void; // 模拟节点或画布就绪事件。
    requestCanvasFit: () => void; // 模拟打开工程或切换树。
  };
  return { app, projectReady, tree, nodeIndex, getNodes, fits, ticks, setFitResult: (value: boolean) => { fitResult = value; } };
}

// 打开工程后应等整棵树的节点完成测量，随后只执行一次适应。
test("打开工程等待全部节点尺寸就绪后适应整棵树", async () => {
  const s = session();
  s.app.requestCanvasFit();
  s.ticks.shift()!();
  assert.equal(s.fits.length, 0);
  s.getNodes.value[0]!.dimensions = { width: 188, height: 90 };
  s.app.fitCanvasWhenReady();
  assert.equal(s.fits.length, 0);
  s.getNodes.value[1]!.dimensions = { width: 188, height: 90 };
  s.app.fitCanvasWhenReady();
  await Promise.resolve();
  s.app.fitCanvasWhenReady();
  assert.equal(s.fits.length, 1);
  assert.equal(s.fits[0]!.padding, 0.18);
});

// 同 ID 工程可能不会再触发节点初始化事件，下一次视图更新也要重新适应。
test("切换同 ID 工程仍重新适应，失败的早期尝试可在就绪后重试", async () => {
  const s = session();
  for (const node of s.getNodes.value) node.dimensions = { width: 188, height: 90 };
  s.setFitResult(false);
  s.app.requestCanvasFit();
  s.ticks.shift()!();
  await Promise.resolve();
  assert.equal(s.fits.length, 1);
  s.setFitResult(true);
  s.app.fitCanvasWhenReady();
  await Promise.resolve();
  s.app.requestCanvasFit();
  s.ticks.shift()!();
  await Promise.resolve();
  assert.equal(s.fits.length, 3);
});

// 空树和仍属于旧工程的节点不应消耗适应请求或改变当前视口。
test("空树跳过适应，旧节点不能提前完成新树的适应", async () => {
  const s = session();
  s.tree.value.nodes = [];
  s.app.requestCanvasFit();
  s.ticks.shift()!();
  assert.equal(s.fits.length, 0);
  s.tree.value.nodes = [{ id: "new-root" }];
  s.nodeIndex.value = new Map([["new-root", true]]);
  s.app.requestCanvasFit();
  s.getNodes.value = [{ id: "root", dimensions: { width: 188, height: 90 } }];
  s.ticks.shift()!();
  assert.equal(s.fits.length, 0);
  s.getNodes.value = [{ id: "new-root", dimensions: { width: 188, height: 90 } }];
  s.app.fitCanvasWhenReady();
  await Promise.resolve();
  assert.equal(s.fits.length, 1);
});
