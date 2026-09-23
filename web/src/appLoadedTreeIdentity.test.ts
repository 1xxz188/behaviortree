import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, effect, reactive, ref, shallowRef, watch } from "vue";
import { blankProject, clone } from "./project.ts";
import type { Project } from "./project.ts";
import { NodeIdentityIndex } from "./nodeIdentity.ts";
import { TreeIdentityIndex, captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import type { EditorSnapshot } from "./treeIdentity.ts";
import { normalizeCodeNames } from "./codeNames.ts";
import { ProjectSaveState } from "./saveState.ts";
import { GenerationRequests, semanticSignature } from "./generation.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { pruneHighlightedEvents } from "./eventHighlight.ts";
import { EventRegistryIndex } from "./eventRegistry.ts";

// 提取真实加载、历史恢复及请求函数，避免预先构造响应式索引掩盖加载边界问题。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const functions = new Set([
  "applyTreeID", "cancelTreeID", "cancelNodeID", "installProject", "resetResults", "rebuildIDs",
  "checkpoint", "mutate", "invalidateCode", "restore", "saveCurrent", "currentProjectRequest",
  "ensureIdentityDraftsApplied", "generate", "cancelCodeName", "applyNodeID", "applyCodeName",
]);
const variables = new Set(["tree", "nodeIdentity", "nodeIndex", "node", "treeIDError", "identityDraftPending"]);
const handlers = script.statements.filter(statement => {
  if (ts.isFunctionDeclaration(statement)) return functions.has(statement.name?.text ?? "");
  if (ts.isVariableStatement(statement)) return statement.declarationList.declarations.some(
    declaration => ts.isIdentifier(declaration.name) && variables.has(declaration.name.text));
  if (!ts.isExpressionStatement(statement) || !ts.isCallExpression(statement.expression)) return false;
  const call = statement.expression;
  return call.expression.getText(script) === "watch"
    && ["tree", "treeID", "selected"].includes(call.arguments[0]?.getText(script) ?? "");
}).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 使用普通 JSON 工程模拟服务端响应，并观察侧栏依赖和实际保存、生成请求内容。
function session() {
  const project = ref(blankProject());
  const treeID = ref(project.value.trees[0]!.id);
  const saveState = reactive(new ProjectSaveState());
  const requests: { path: string; body: unknown }[] = [];
  const notices: { text: string; failed: boolean }[] = [];
  const context = {
    computed, effect, reactive, ref, shallowRef, watch, NodeIdentityIndex, TreeIdentityIndex,
    normalizeCodeNames, captureSnapshot, restoreSnapshot, semanticSignature, clone, parseJSON, stringifyJSON,
    project, treeID, treeIDDraft: ref(treeID.value), selected: ref(""), nodeIDDraft: ref(""),
    codeNameDraft: ref(""), codeNameError: ref(""),
    treeIdentity: shallowRef(new TreeIdentityIndex(project.value)), renamingTree: false,
    treeMenu: shallowRef(), catalogMenu: shallowRef(), catalogDialog: shallowRef(), canvasMenu: shallowRef(), editRevision: 0, occupiedIDs: new Set<string>(),
    eventManagerOpen: ref(false), highlightedEventIDs: shallowRef(new Set<string>()),
    eventIndex: computed(() => new EventRegistryIndex(project.value)), pruneHighlightedEvents,
    codeSnapshot: shallowRef(), scaffoldSnapshot: shallowRef(), semanticRevision: ref(0),
    generationRequests: new GenerationRequests(), diagnostics: ref([]),
    fileName: ref(""), suggestedName: ref(""), workspace: ref("E:/fixture"), files: ref<string[]>([]), allFiles: ref<string[]>([]),
    undoStack: ref<EditorSnapshot[]>([]), redoStack: ref<EditorSnapshot[]>([]),
    saveState, dirty: computed(() => saveState.dirty), projectReady: ref(false),
    workspaceError: ref(""), failedOpen: ref(""), inspectorOpen: ref(false),
    notice: (text: string, failed = false) => notices.push({ text, failed }),
    endPaletteDrag: () => {}, fitView: () => {}, closeTreeMenu: () => {},
    rememberProject: () => {}, recentStorage: () => undefined, updateOutputBounds: () => {},
    action: async (callback: () => Promise<void>) => callback(), acceptCodeSnapshot: () => {}, showOutput: () => {},
    setTimeout: (callback: () => void) => callback(), nextTick: async (callback: () => void) => callback(),
    request: async (path: string, body: unknown) => { requests.push({ path, body }); return { workspace: "E:/fixture", version: "fixture" }; },
  };
  const app = runInNewContext(`${js}\n({ tree, installProject, applyTreeID, applyNodeID, applyCodeName, restore, saveCurrent, currentProjectRequest, generate });`, context) as {
    tree: { readonly value: Project["trees"][number] }; // 当前树计算属性。
    installProject: (loaded: Project, name: string) => void; // 真实文件安装入口。
    applyTreeID: () => void; // 属性面板提交入口。
    applyNodeID: () => void; // 节点身份草稿提交入口。
    applyCodeName: () => void; // 节点代码名草稿提交入口。
    restore: (snapshot: EditorSnapshot) => void; // 撤销恢复入口。
    saveCurrent: () => Promise<boolean>; // 工程保存入口。
    currentProjectRequest: (path: string) => Promise<unknown>; // 生成请求快照入口。
    generate: (write?: boolean) => Promise<void>; // 顶部生成及预览按钮入口。
  };
  let sidebarIDs = "";
  effect(() => { sidebarIDs = project.value.trees.map(tree => tree.id).join(","); });
  const loaded = blankProject();
  loaded.trees[0]!.id = "npc_troop";
  const caller = clone(loaded.trees[0]!);
  caller.id = "caller";
  caller.nodes[0]!.tree = "npc_troop";
  loaded.trees.push(caller);
  app.installProject(parseJSON<Project>(stringifyJSON(loaded)), "loaded.json");
  return { ...context, app, requests, notices, sidebarIDs: () => sidebarIDs };
}

// 文件加载路径必须立即刷新侧栏、反向引用，且保存和生成均使用应用后的身份。
test("加载 JSON 后应用树 ID 同步侧栏、保存及生成请求", async () => {
  const s = session();
  s.treeIDDraft.value = "NPCTroop";
  s.app.applyTreeID();
  assert.equal(s.sidebarIDs(), "NPCTroop,caller");
  assert.equal(s.app.tree.value, s.project.value.trees[0]);
  assert.equal(s.project.value.trees[1]!.nodes[0]!.tree, "NPCTroop");
  assert.equal(s.dirty.value, true);
  assert.equal(await s.app.saveCurrent(), true);
  await s.app.currentProjectRequest("/api/generate");
  const saved = s.requests.find(item => item.path === "/api/project")!.body as { project: Project };
  const generated = s.requests.find(item => item.path === "/api/generate")!.body as Project;
  assert.equal(saved.project.trees[0]!.id, "NPCTroop");
  assert.equal(generated.trees[0]!.id, "NPCTroop");
});

// 历史恢复后继续改号仍须写入同一响应式对象，避免索引指向解析前的独立对象。
test("加载工程改号并撤销后再次改号仍立即刷新侧栏", () => {
  const s = session();
  s.treeIDDraft.value = "NPCTroop";
  s.app.applyTreeID();
  s.app.restore(s.undoStack.value[0]!);
  assert.equal(s.sidebarIDs(), "npc_troop,caller");
  s.treeIDDraft.value = "NPCTroop2";
  s.app.applyTreeID();
  assert.equal(s.sidebarIDs(), "NPCTroop2,caller");
  assert.equal(s.project.value.trees[1]!.nodes[0]!.tree, "NPCTroop2");
});

// 三类身份草稿都不能被保存或生成静默忽略；显式应用后恢复正常保存生成。
for (const kind of ["treeID", "nodeID", "codeName"] as const) {
  test(`${kind} 未应用时保存和生成零请求并提示，应用后允许操作`, async () => {
    const s = session();
    if (kind === "treeID") s.treeIDDraft.value = "NPCTroop";
    else {
      s.selected.value = s.app.tree.value.root;
      if (kind === "nodeID") s.nodeIDDraft.value = "100";
      else s.codeNameDraft.value = "NPCRoot";
    }
    const before = stringifyJSON(s.project.value);
    assert.equal(await s.app.saveCurrent(), false);
    await s.app.generate(true);
    await s.app.generate(false);
    await s.app.currentProjectRequest("/api/validate");
    assert.equal(s.requests.length, 0);
    assert.equal(stringifyJSON(s.project.value), before);
    assert.ok(s.notices.length >= 4);
    assert.ok(s.notices.every(notice => notice.failed && /应用/.test(notice.text)));
    if (kind === "treeID") s.app.applyTreeID();
    else if (kind === "nodeID") s.app.applyNodeID();
    else s.app.applyCodeName();
    assert.equal(await s.app.saveCurrent(), true);
    await s.app.generate(true);
    assert.deepEqual(s.requests.map(request => request.path), ["/api/project", "/api/generate"]);
    const generated = s.requests[1]!.body as Project;
    if (kind === "treeID") assert.equal(generated.trees[0]!.id, "NPCTroop");
    else if (kind === "nodeID") assert.equal(generated.trees[0]!.nodes[0]!.id, "100");
    else assert.equal(generated.trees[0]!.nodes[0]!.codeName, "NPCRoot");
  });
}
