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

// 抽取真实右键处理函数，验证目标身份和二次确认边界，避免复制实现。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["openCatalogMenu", "editCatalogDefinition", "confirmDeleteDefinition"]);
const handlers = script.statements.filter(statement =>
  ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""),
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
  const catalogMenu = shallowRef<{ definition: Definition; x: number; y: number }>();
  const catalogDialog = ref<{ mode: string; definition?: Definition; kind?: string }>();
  const history: EditorSnapshot[] = [];
  const notices: string[] = [];
  const context = {
    project, treeID, selected, definitionIndex, catalogMenu, catalogDialog,
    workspaceChanging: ref(false), treeMenu: shallowRef(),
    notice: (text: string) => notices.push(text),
    applyCatalog: async (catalog: Definition[]) => {
      history.push(captureSnapshot(project.value, treeID.value, selected.value));
      synchronizeCatalog(project.value, catalog);
    },
  };
  const app = runInNewContext(`${js}\n({ openCatalogMenu, editCatalogDefinition, confirmDeleteDefinition });`, context) as {
    openCatalogMenu: (event: unknown, target: Definition) => void; // 仅记录右击目标和位置。
    editCatalogDefinition: (target: Definition) => void; // 直接打开该定义的编辑页面。
    confirmDeleteDefinition: () => void | Promise<void>; // 二次确认后才移除定义。
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
  s.catalogMenu.value = { definition: detached, x: 0, y: 0 };
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
  s.catalogMenu.value = { definition: target, x: 0, y: 0 };
  await s.app.confirmDeleteDefinition();
  assert.equal(s.project.value.catalog.length, 2);
  assert.equal(s.history.length, 0);
});
