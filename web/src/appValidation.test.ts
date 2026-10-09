import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { compile, computed, createSSRApp, ref } from "vue";
import { renderToString } from "@vue/server-renderer";
import { blankProject, clone, kinds } from "./project.ts";
import type { Diagnostic, Project } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { GenerationRequests } from "./generation.ts";
import { CatalogUsageIndex } from "./catalogVisibility.ts";
import { EventRegistryIndex } from "./eventRegistry.ts";

// 执行真实校验请求链，并渲染真实诊断模板，避免仅验证底部消息已被赋值。
const app = readFileSync(new URL("./App.vue", import.meta.url), "utf8");
const source = app.split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["request", "action", "notice", "validate", "currentProjectRequest", "showOutput", "focusDiagnostic"]);
const code = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError")
  || (ts.isVariableStatement(statement) && statement.declarationList.declarations.some(declaration =>
    ts.isIdentifier(declaration.name) && ["nodeDiagnosticLevels", "graphNodes"].includes(declaration.name.text))),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const outputStart = app.indexOf('<div v-if="bottomTab === \'diagnostics\'" class="output-content">');
const outputEnd = app.indexOf("<SourceViewer", outputStart);
const renderOutput = compile(app.slice(outputStart, outputEnd).trim());
const renderCanvasNode = compile(app.split('<template #node-behavior="{ data }">')[1]!.split("</template>")[0]!);

// 使用真实响应式状态记录请求、面板切换及摘要，网络边界支持可控失败。
function session() {
  let failure = false;
  let returnedDiagnostics: Diagnostic[] = [];
  const calls: string[] = [];
  const project = ref(blankProject());
  const treeID = ref("1");
  const trees = computed(() => new Map(project.value.trees.map(tree => [tree.id, tree])));
  const context = {
    noticeRevision: { value: 0 }, // 实际发布操作结果的版本。
    Error, clone, stringifyJSON, parseJSON, computed, kinds, catalogUsage: new CatalogUsageIndex(() => project.value.trees, () => new EventRegistryIndex(project.value).definitionToNodes),
    project, workspace: ref("E:/workspace"), tree: computed(() => trees.value.get(treeID.value) ?? project.value.trees[0]!),
    treeID, selected: ref(""), eventMatchedNodes: ref(new Set<string>()),
    semanticRevision: ref(1), generationRequests: new GenerationRequests(),
    diagnostics: ref<Diagnostic[]>([]), validationResult: ref<{ revision: number; count: number; errorCount: number; warningCount: number }>(),
    busy: ref(false), message: ref(""), error: ref(false), bottomTab: ref("source"),
    outputHeight: ref<number>(), outputCollapsed: ref(true), // 校验必须重新打开已最小化的输出区。
    ensureIdentityDraftsApplied: () => true,
    guardPendingNavigation: () => true, setCenter: () => {},
    nextTick: (callback: () => void) => callback(), fitView: () => {},
    setOutputHeight: (height: number) => { context.outputHeight.value = height; },
    fetch: async (path: string) => {
      calls.push(path);
      if (failure) throw new Error("网络连接失败");
      return { ok: true, status: 200, text: async () => stringifyJSON({ diagnostics: returnedDiagnostics }) };
    },
  };
  const workflow = runInNewContext(`${js}\n({ validate, focusDiagnostic, graphNodes });`, context) as {
    validate: () => Promise<void>; // 执行 App 中的真实校验入口。
    focusDiagnostic: (diagnostic: Diagnostic) => void; // 警告与错误共用真实定位入口。
    graphNodes: { readonly value: { id: string; data: unknown }[] }; // 真实画布投影用于验证节点级别。
  };
  return {
    ...workflow, context, calls,
    fail: () => { failure = true; },
    respond: (items: Diagnostic[]) => { returnedDiagnostics = items; },
    render: () => renderToString(createSSRApp({ setup: () => ({ ...context, focusDiagnostic: workflow.focusDiagnostic }), render: renderOutput })),
    renderNode: (id: string) => {
      // 浏览器先计算画布再渲染；在 SSR setup 外取数据，避免把懒缓存误建成 SSR 专用 computed。
      const data = workflow.graphNodes.value.find(node => node.id === id)!.data;
      return renderToString(createSSRApp({
        render: renderCanvasNode,
        components: { Handle: { render: () => null } },
        setup: () => ({ data, selected: "", Position: { Left: "left", Right: "right" } }),
      }));
    },
  };
}

// 构造可运行的根分支及互不连接的动作、条件和完整草稿分支，不依赖接口返回诊断。
function draftCanvasProject(): Project {
  const project = blankProject();
  project.trees[0]!.nodes = [
    { id: "1", type: "sequence", codeName: "Root", children: ["2"] },
    { id: "2", type: "wait", codeName: "ConnectedWait", durationMs: 10 },
    { id: "3", type: "action", codeName: "IsolatedAction" },
    { id: "4", type: "condition", codeName: "IsolatedCondition" },
    { id: "5", type: "sequence", codeName: "DraftSequence", children: ["6", "7"] },
    { id: "6", type: "condition", codeName: "DraftCondition" },
    { id: "7", type: "action", codeName: "DraftAction" },
  ];
  return project;
}

// 检查真实画布节点模板上的警告类，黄色标记应与当前根可达性同步且草稿不得显示旧错误红框。
async function assertDraftWarnings(s: ReturnType<typeof session>, ids: string[], expected: boolean): Promise<void> {
  for (const id of ids) {
    const html = await s.renderNode(id);
    if (expected) {
      assert.match(html, /class="bt-node[^\"]*\bwarning\b/, `根不可达节点 ${id} 缺少黄色警告外框`);
      assert.doesNotMatch(html, /class="bt-node[^\"]*\binvalid\b/, `草稿节点 ${id} 不应被旧业务错误染成红色`);
    } else {
      assert.doesNotMatch(html, /class="bt-node[^\"]*\bwarning\b/, `根可达节点 ${id} 不应保留草稿黄色外框`);
    }
  }
}

// 最小重载场景只有一个合法等待根及一个孤立动作，随后覆盖孤立条件和整段分支，均不手动校验。
test("草稿外框：重载工程的孤立动作条件和整段分支无需校验即全黄", async () => {
  const s = session();
  const minimal = blankProject();
  minimal.trees[0]!.nodes = [
    { id: "1", type: "wait", codeName: "RootWait", durationMs: 10 },
    { id: "2", type: "action", codeName: "DraftAction" },
  ];
  s.context.project.value = minimal;
  assert.deepEqual(s.context.diagnostics.value, []);
  await assertDraftWarnings(s, ["2"], true);
  await assertDraftWarnings(s, ["1"], false);

  s.context.project.value = draftCanvasProject();
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["3", "4", "5", "6", "7"], true);
  await assertDraftWarnings(s, ["1", "2"], false);
  assert.deepEqual(s.calls, []);
});

// 连线事务清空服务端诊断后仍应立即标记断开的整个后代，接回根则立即取消所有草稿黄色外框。
test("草稿外框：清空诊断后的断线和回接即时更新整段后代", async () => {
  const s = session();
  s.context.project.value = draftCanvasProject();
  s.context.tree.value.nodes[0]!.children!.push("5");
  await assertDraftWarnings(s, ["5", "6", "7"], false);

  s.context.tree.value.nodes[0]!.children = ["2"];
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["5", "6", "7"], true);
  await assertDraftWarnings(s, ["1", "2"], false);

  s.context.tree.value.nodes[0]!.children!.push("5");
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["5", "6", "7"], false);
  s.context.tree.value.nodes[4]!.children = ["6"];
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["5", "6"], false);
  await assertDraftWarnings(s, ["7"], true);
  assert.deepEqual(s.calls, []);
});

// 可达节点的真实错误保留红框，断开草稿只依据当前拓扑报警告，旧绑定错误及已接回节点的旧草稿警告失效。
test("草稿外框：旧业务错误不覆盖草稿而可达节点仍保留真实错误", async () => {
  const s = session();
  s.context.project.value = draftCanvasProject();
  s.context.diagnostics.value = [
    { treeId: "1", nodeId: "2", field: "durationMs", message: "等待时长无效" },
    { treeId: "1", nodeId: "3", field: "binding", message: "旧校验：业务动作未绑定" },
    { severity: "warning", treeId: "1", nodeId: "2", message: "旧校验：节点未接入根" },
    { severity: "warning", treeId: "other", nodeId: "1", message: "其他树的草稿警告" },
  ];
  const rootError = await s.renderNode("2");
  assert.match(rootError, /class="bt-node[^\"]*\binvalid\b/);
  await assertDraftWarnings(s, ["1", "2"], false);
  await assertDraftWarnings(s, ["3", "4", "5", "6", "7"], true);

  s.context.diagnostics.value = [{ severity: "warning", treeId: "1", nodeId: "2", message: "旧校验：节点未接入根" }];
  await assertDraftWarnings(s, ["2"], false);
  assert.doesNotMatch(await s.renderNode("2"), /class="bt-node[^\"]*\binvalid\b/);
  assert.deepEqual(s.calls, []);
});

// 选择不能清除黄色标记，切树的同名节点按各自根判断，撤销快照及同 ID 工程替换也须重新计算。
test("草稿外框：选择切树和工程快照替换均保持当前拓扑标记", async () => {
  const s = session();
  const loaded = draftCanvasProject();
  const other = clone(loaded.trees[0]!);
  other.id = "other";
  other.nodes[0]!.children = ["2", "3", "4", "5"];
  loaded.trees.push(other);
  s.context.project.value = loaded;
  await assertDraftWarnings(s, ["3", "4", "5", "6", "7"], true);
  const currentProjection = s.graphNodes.value;
  s.context.selected.value = "5";
  assert.equal(s.graphNodes.value, currentProjection, "仅改变选择不应重建拓扑投影");
  await assertDraftWarnings(s, ["5", "6", "7"], true);

  s.context.treeID.value = "other";
  await assertDraftWarnings(s, ["1", "2", "3", "4", "5", "6", "7"], false);
  s.context.treeID.value = "1";
  await assertDraftWarnings(s, ["3", "4", "5", "6", "7"], true);

  const previous = clone(s.context.project.value);
  s.context.tree.value.nodes[0]!.children!.push("5");
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["5", "6", "7"], false);
  s.context.project.value = previous;
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["5", "6", "7"], true);

  const replaced = draftCanvasProject();
  replaced.trees[0]!.nodes[0]!.children = ["2", "3", "4", "5"];
  s.context.project.value = replaced;
  s.context.diagnostics.value = [];
  await assertDraftWarnings(s, ["1", "2", "3", "4", "5", "6", "7"], false);
  assert.deepEqual(s.calls, []);
});

// SSR 不计算 CSS，因此补充检查选中草稿的黄色光晕规则，避免选中后的绿色外框掩盖警告级别。
test("草稿外框：选中节点使用黄色警告光晕", () => {
  const stylesheet = readFileSync(new URL("./style.css", import.meta.url), "utf8");
  const selectedWarning = /\.vue-flow__node\.selected\s+\.bt-node\.warning\s*\{([^}]+)\}/.exec(stylesheet);
  assert.ok(selectedWarning, "选中的警告节点必须有独立黄色外框规则");
  assert.match(selectedWarning[1]!, /box-shadow\s*:[\s\S]*#e8c584(?:[0-9a-f]{2})?\b/i);
});

// 空诊断是明确的成功结果，必须替换未校验占位提示且展开结果面板。
test("校验通过后显示明确结果而不是点击校验占位提示", async () => {
  const s = session();
  await s.validate();
  assert.deepEqual(s.calls, ["/api/validate"]);
  assert.equal(s.context.bottomTab.value, "diagnostics");
  assert.equal(s.context.outputCollapsed.value, false);
  const html = await s.render();
  assert.match(html, /校验通过/);
  assert.doesNotMatch(html, /点击「校验」/);
  assert.equal(s.context.validationResult.value?.count, 0);
  assert.equal(s.context.validationResult.value?.revision, 1);
});

// 有问题时同时展示数量及可定位节点的明细，不能误显示校验通过。
test("校验不通过时显示问题数量和诊断明细", async () => {
  const s = session();
  s.respond([{ treeId: "1", nodeId: "1", message: "入口节点不存在" }]);
  await s.validate();
  assert.equal(s.context.validationResult.value?.count, 1);
  assert.equal(s.context.error.value, true);
  const html = await s.render();
  assert.match(html, /1\s*个错误/);
  assert.match(html, /入口节点不存在/);
  assert.doesNotMatch(html, /校验通过/);
});

// 只有草稿警告仍明确校验通过，诊断和画布均显示黄色级别且能定位原草稿。
test("孤立草稿警告允许生成并显示可定位的警告标记", async () => {
  const s = session();
  s.context.tree.value.nodes.push({ id: "2", type: "action", codeName: "DraftAction", name: "草稿动作" });
  s.respond([{ severity: "warning", treeId: "1", nodeId: "2", message: "节点无法从根节点到达，生成时将跳过" }]);
  await s.validate();
  assert.equal(s.context.error.value, false);
  assert.equal(s.context.validationResult.value?.errorCount, 0);
  assert.equal(s.context.validationResult.value?.warningCount, 1);
  assert.match(s.context.message.value, /校验通过.*1 个草稿节点.*跳过/);
  const html = await s.render();
  assert.match(html, /validation-warning/);
  assert.match(html, /class="diagnostic warning"/);
  assert.match(html, />警告</);
  assert.doesNotMatch(html, /identity-error|>错误</);
  assert.match(await s.renderNode("2"), /class="bt-node[^\"]*\bwarning\b/);
  assert.doesNotMatch(await s.renderNode("2"), /class="bt-node[^\"]*\binvalid\b/);
  assert.doesNotMatch(await s.renderNode("1"), /class="bt-node[^\"]*\bwarning\b/);
  s.focusDiagnostic(s.context.diagnostics.value[0]!);
  assert.equal(s.context.selected.value, "2");
  assert.equal(s.context.treeID.value, "1");
});

// 混合诊断分别计数，缺省级别仍是错误，且同一节点总由错误优先而不受返回顺序影响。
test("错误和草稿警告分别展示且节点错误标记优先", async () => {
  const warning: Diagnostic = { severity: "warning", treeId: "1", nodeId: "1", message: "草稿警告" };
  const error: Diagnostic = { treeId: "1", nodeId: "1", message: "入口节点不存在" };
  for (const items of [[warning, error], [error, warning]]) {
    const s = session();
    s.respond(items);
    await s.validate();
    assert.equal(s.context.error.value, true);
    assert.equal(s.context.validationResult.value?.errorCount, 1);
    assert.equal(s.context.validationResult.value?.warningCount, 1);
    assert.match(s.context.message.value, /1 个错误，1 个警告/);
    const html = await s.render();
    assert.match(html, /1 个错误、1 个警告/);
    assert.match(html, /class="diagnostic warning"/);
    assert.match(html, />错误</);
    const node = await s.renderNode("1");
    assert.match(node, /class="bt-node[^\"]*\binvalid\b/);
    assert.doesNotMatch(node, /class="bt-node[^\"]*\bwarning\b/);
  }
});

// 再次校验网络失败时清除旧的成功摘要，由统一通知显示本次错误。
test("校验网络失败不会继续显示上次校验通过", async () => {
  const s = session();
  await s.validate();
  s.fail();
  await s.validate();
  assert.equal(s.context.validationResult.value, undefined);
  assert.equal(s.context.error.value, true);
  assert.equal(s.context.message.value, "网络连接失败");
  assert.equal(s.context.busy.value, false);
  assert.doesNotMatch(await s.render(), /校验通过/);
});

// 相同结果重复校验仍重新请求并展示本次结果，不被已有摘要短路。
test("重复校验保持明确结果且工程修改后旧摘要失效", async () => {
  const s = session();
  await s.validate();
  const previous = s.context.validationResult.value;
  await s.validate();
  assert.equal(s.calls.length, 2);
  assert.notEqual(s.context.validationResult.value, previous);
  assert.match(await s.render(), /校验通过/);
  s.context.semanticRevision.value++;
  assert.doesNotMatch(await s.render(), /校验通过/);
});
