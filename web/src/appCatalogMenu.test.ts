import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref, shallowRef } from "vue";
import { blankProject } from "./project.ts";
import type { Definition } from "./project.ts";
import { synchronizeCatalog } from "./catalogSync.ts";
import { captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import type { EditorSnapshot } from "./treeIdentity.ts";
import { stringifyJSON } from "./json.ts";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import { EventRegistryIndex } from "./eventRegistry.ts";

// 抽取真实右键处理函数，验证目标身份和二次确认边界，避免复制实现。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["openCatalogMenu", "editCatalogDefinition", "requestDeleteDefinition", "confirmDeleteDefinition", "organizeCatalogDefinition"]);
const handlers = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isVariableStatement(statement) && statement.declarationList.declarations.some(item => ts.isIdentifier(item.name) && item.name.text === "catalogMenuReferences")),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 让被右击的定义与当前节点绑定不同，并保存真实工程快照检查撤销语义。
function session() {
  const project = ref(blankProject());
  project.value.catalog = [
    { id: "Idle", name: "待机", kind: "action", goName: "Idle" },
    { id: "MoveTo", name: "移动到目标", kind: "action", goName: "MoveTo", params: [{ name: "Target", type: "int64" }] },
  ];
  project.value.trees[0]!.nodes = [
    { id: "1", type: "action", codeName: "IdleNode", binding: "Idle" },
    { id: "2", type: "action", codeName: "MoveNode", binding: "MoveTo", params: { Target: { value: 42 } } },
  ];
  const treeID = ref(project.value.trees[0]!.id);
  const selected = ref("1");
  const definitionIndex = computed(() => new Map(project.value.catalog.map(item => [item.id, item])));
  const catalogMenu = shallowRef<{ definition: Definition; x: number; y: number; revision: number; initialMode?: string }>();
  const catalogDialog = ref<{ mode: string; definition?: Definition; kind?: string }>();
  const history: EditorSnapshot[] = [];
  const notices: string[] = [];
  const pending = ref(false); // 未应用表单必须阻止打开删除确认。
  const revision = ref(1); // 模拟确认打开期间发生的真实工程事务。
  const context = {
    guardPendingNavigation: () => !pending.value,
    project, treeID, selected, definitionIndex, catalogMenu, catalogDialog, pending, revision, computed,
    get editRevision() { return revision.value; }, // 实际处理函数读取最新修订，不冻结初始值。
    eventIndex: computed(() => new EventRegistryIndex(project.value)), // 与实际界面复用同一引用索引。
    catalogIndex: shallowRef(new CatalogOrganizationIndex(project.value)),
    catalogManager: shallowRef<{ editDefinition: (target: Definition) => void; openMoveDefinition: (id: string) => void; openDefinitionTags: (id: string) => void }>(),
    catalogBrowser: shallowRef<{ openMoveDefinition: (id: string) => void; openDefinitionTags: (id: string) => void }>(),
    workspaceChanging: ref(false), treeMenu: shallowRef(),
    notice: (text: string) => notices.push(text),
    applyCatalog: async (catalog: Definition[]) => {
      history.push(captureSnapshot(project.value, treeID.value, selected.value));
      synchronizeCatalog(project.value, catalog);
    },
  };
  const app = runInNewContext(`${js}\n({ openCatalogMenu, editCatalogDefinition, requestDeleteDefinition, confirmDeleteDefinition, organizeCatalogDefinition, catalogMenuReferences });`, context) as {
    organizeCatalogDefinition: (operation: "move" | "tags") => void; // 分类操作路由到当前可见窗口。
    openCatalogMenu: (event: unknown, target: Definition) => void; // 仅记录右击目标和位置。
    editCatalogDefinition: (target: Definition) => void; // 直接打开该定义的编辑页面。
    confirmDeleteDefinition: () => void | Promise<void>; // 二次确认后才移除定义。
    requestDeleteDefinition: (target: Definition, revision: number) => void; // 编辑页直接请求删除确认。
    catalogMenuReferences: { readonly value: { trees: number; nodes: number } }; // 删除确认使用真实引用数量。
  };
  return { ...context, app, history, notices };
}

// 模拟鼠标或键盘打开菜单时可用的真实元素接口。
function menuEvent() {
  return { clientX: 40, clientY: 80, currentTarget: {
    getBoundingClientRect: () => ({ left: 0, bottom: 30 }), focus: () => {},
  } };
}

// 从库中编辑其他定义不能误用当前节点绑定，也不能落入新建页面。
test("业务节点右键编辑指定定义，保持画布选择", () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  s.app.openCatalogMenu(menuEvent(), target);
  assert.equal(s.catalogMenu.value!.definition, target);
  assert.equal(s.selected.value, "1");
  s.app.editCatalogDefinition(target);
  assert.equal(s.catalogDialog.value!.mode, "edit");
  assert.equal(s.catalogDialog.value!.definition, target);
  assert.equal(s.catalogMenu.value, undefined);
  assert.equal(s.history.length, 0);
});

// 管理窗口已经打开时复用内部页面，防止只修改初始属性而未真正切换编辑目标。
test("管理列表菜单编辑和分类操作复用当前管理窗口", () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  const calls: string[] = [];
  s.catalogDialog.value = { mode: "manage" };
  s.catalogManager.value = {
    editDefinition: item => calls.push(`edit:${item.id}`),
    openMoveDefinition: id => calls.push(`move:${id}`),
    openDefinitionTags: id => calls.push(`tags:${id}`),
  };
  s.catalogBrowser.value = {
    openMoveDefinition: () => assert.fail("不应打开背景侧栏表单"),
    openDefinitionTags: () => assert.fail("不应打开背景侧栏表单"),
  };
  s.app.editCatalogDefinition(target);
  for (const operation of ["move", "tags"] as const) {
    s.app.openCatalogMenu(menuEvent(), target);
    s.app.organizeCatalogDefinition(operation);
  }
  assert.deepEqual(calls, ["edit:MoveTo", "move:MoveTo", "tags:MoveTo"]);
  assert.equal(s.catalogDialog.value.mode, "manage");
  assert.equal(s.history.length, 0);
});

// 打开菜单和取消确认均不写入工程；确认后保留引用数据，允许诊断和完整撤销。
test("业务定义删除必须确认，并保留已有绑定和参数以便撤销", async () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  const before = stringifyJSON(s.project.value);
  s.app.openCatalogMenu(menuEvent(), target);
  assert.equal(s.history.length, 0);
  s.catalogMenu.value = undefined;
  await s.app.confirmDeleteDefinition();
  assert.equal(stringifyJSON(s.project.value), before);
  assert.equal(s.history.length, 0);
  s.app.openCatalogMenu(menuEvent(), target);
  await s.app.confirmDeleteDefinition();
  assert.deepEqual(s.project.value.catalog.map(item => item.id), ["Idle"]);
  assert.equal(s.project.value.trees[0]!.nodes[1]!.binding, "MoveTo");
  assert.equal(s.project.value.trees[0]!.nodes[1]!.params!.Target!.value, 42);
  assert.equal(s.selected.value, "1");
  assert.equal(s.history.length, 1);
  assert.equal(s.catalogMenu.value, undefined);
  assert.equal(stringifyJSON(restoreSnapshot(s.history[0]!).project), before);
});

// 工程替换后同 ID 的新对象不能被旧菜单操作，防止编辑或删除另一个工程的定义。
test("过期业务定义菜单不能编辑或删除同 ID 的新定义", async () => {
  const s = session();
  const detached = { ...s.project.value.catalog[1]! };
  s.app.editCatalogDefinition(detached);
  assert.equal(s.catalogDialog.value, undefined);
  s.catalogMenu.value = { definition: detached, x: 0, y: 0, revision: 1 };
  await s.app.confirmDeleteDefinition();
  assert.equal(s.project.value.catalog.length, 2);
  assert.equal(s.history.length, 0);
});

// 工作区切换期间所有入口保持冻结，避免对切换中的工程创建悬空编辑状态。
test("工作区切换期间拒绝打开业务定义菜单和执行变更", async () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  s.workspaceChanging.value = true;
  s.app.openCatalogMenu(menuEvent(), target);
  assert.equal(s.catalogMenu.value, undefined);
  s.app.editCatalogDefinition(target);
  assert.equal(s.catalogDialog.value, undefined);
  s.catalogMenu.value = { definition: target, x: 0, y: 0, revision: 1 };
  await s.app.confirmDeleteDefinition();
  assert.equal(s.project.value.catalog.length, 2);
  assert.equal(s.history.length, 0);
});

// 编辑页删除绕过菜单直接进入确认，取消保留编辑页；确认仍沿用同一撤销事务。
test("编辑页删除只打开确认，取消保留原工程和编辑页", async () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  s.catalogDialog.value = { mode: "edit", definition: target };
  const before = stringifyJSON(s.project.value);
  s.app.requestDeleteDefinition(target, 1);
  assert.equal(s.catalogMenu.value!.initialMode, "delete");
  assert.equal(s.catalogMenu.value!.definition, target);
  assert.equal(s.history.length, 0);
  s.catalogMenu.value = undefined;
  await s.app.confirmDeleteDefinition();
  assert.equal(stringifyJSON(s.project.value), before);
  assert.equal(s.catalogDialog.value.definition, target);
  s.app.requestDeleteDefinition(target, 1);
  await s.app.confirmDeleteDefinition();
  assert.equal(s.history.length, 1);
  assert.equal(s.project.value.trees[0]!.nodes[1]!.binding, target.id);
  assert.equal(s.project.value.trees[0]!.nodes[1]!.params!.Target!.value, 42);
  assert.equal(stringifyJSON(restoreSnapshot(s.history[0]!).project), before);
});

// 表单草稿、过期修订及确认期间的额外事务均不得执行删除，包括原对象未被替换的情况。
test("编辑页删除拒绝草稿和过期确认修订", async () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  s.pending.value = true;
  s.app.requestDeleteDefinition(target, 1);
  assert.equal(s.catalogMenu.value, undefined);
  s.pending.value = false;
  s.app.requestDeleteDefinition(target, 0);
  assert.equal(s.catalogMenu.value, undefined);
  s.app.requestDeleteDefinition(target, 1);
  s.revision.value++;
  await s.app.confirmDeleteDefinition();
  assert.equal(s.project.value.catalog.length, 2);
  assert.equal(s.history.length, 0);
});

// 引用统计覆盖跨树、重复节点 ID 和未接入根的草稿，且仅使用已构建的索引桶。
test("业务定义删除确认统计工程引用，包含草稿节点", () => {
  const s = session();
  const target = s.project.value.catalog[1]!;
  s.project.value.trees.push({ id: "2", name: "其他树", root: "root", nodes: [
    { id: "root", type: "sequence", children: ["2"] },
    { id: "2", type: "action", binding: target.id },
    { id: "draft", type: "action", binding: target.id },
  ] });
  s.app.requestDeleteDefinition(target, 1);
  const counts = s.app.catalogMenuReferences.value;
  assert.equal(counts.trees, 2);
  assert.equal(counts.nodes, 3);
  assert.equal(s.app.catalogMenuReferences.value, counts, "重复渲染复用汇总结果");
  s.catalogMenu.value = undefined;
  assert.equal(s.app.catalogMenuReferences.value.nodes, 0);
});
