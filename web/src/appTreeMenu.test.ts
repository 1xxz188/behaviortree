import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, reactive, ref, shallowRef } from "vue";
import { blankProject } from "./project.ts";
import type { Tree } from "./project.ts";
import { TreeIdentityIndex, captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import type { EditorSnapshot } from "./treeIdentity.ts";
import { stringifyJSON } from "./json.ts";

// 使用真实应用处理函数验证菜单目标和确认边界，不复制重命名、删除实现。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["openTreeMenu", "renameTree", "deleteTree", "confirmDeleteTree"]);
const handlers = script.statements.filter(statement =>
  ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 会话包含当前树、被右击树和引用节点，用真实快照检查撤销恢复。
function session() {
  const project = ref(reactive(blankProject()));
  project.value.trees.push({ id: "2", name: "NPC", root: "1", nodes: [{ id: "1", type: "wait" }] });
  project.value.trees[0]!.nodes.push({ id: "2", type: "subtree", tree: "2" });
  const treeIdentity = shallowRef(new TreeIdentityIndex(project.value));
  const treeID = ref("1");
  const selected = ref("1");
  const tree = computed(() => treeIdentity.value.byID.get(treeID.value)!);
  const treeMenu = shallowRef<{ tree: Tree; x: number; y: number; initialMode?: "menu" | "delete" }>();
  const history: EditorSnapshot[] = [];
  const semantic: boolean[] = [];
  const notices: string[] = [];
  const context = {
    project, treeIdentity, treeID, selected, tree, treeMenu,
    workspaceChanging: { value: false },
    notice: (text: string) => notices.push(text),
    mutate: (fn: () => void, changesBehavior = true) => {
      history.push(captureSnapshot(project.value, treeID.value, selected.value));
      semantic.push(changesBehavior);
      fn();
    },
  };
  const app = runInNewContext(`${js}\n({ openTreeMenu, renameTree, deleteTree, confirmDeleteTree });`, context) as {
    openTreeMenu: (event: unknown, target: Tree) => void; // 捕获鼠标菜单目标。
    renameTree: (name: string) => void; // 确认名称后执行。
    deleteTree: () => void; // 属性面板仅打开确认框。
    confirmDeleteTree: () => void; // 二次确认的实际删除入口。
  };
  return { ...context, history, semantic, notices, app };
}

// 右击未选中的树不切换画布；重命名只改展示名，不改身份和引用且可撤销。
test("右键重命名操作指定树并保持当前选择与ID", () => {
  const s = session();
  const target = s.project.value.trees[1]!;
  s.app.openTreeMenu({ clientX: 40, clientY: 80, currentTarget: {
    getBoundingClientRect: () => ({ left: 0, bottom: 30 }), focus: () => {},
  } }, target);
  assert.equal(s.treeMenu.value!.tree, target);
  assert.equal(s.treeID.value, "1");
  s.app.renameTree("  新NPC  ");
  assert.equal(target.name, "新NPC");
  assert.equal(target.id, "2");
  assert.equal(s.project.value.trees[0]!.nodes[1]!.tree, "2");
  assert.equal(s.selected.value, "1");
  assert.deepEqual(s.semantic, [false]);
  const restored = restoreSnapshot(s.history[0]!);
  assert.equal(restored.project.trees[1]!.name, "NPC");
  assert.equal(s.treeMenu.value, undefined);
});

// 打开删除框及取消均不产生历史，确认后仅删除右击的树并保留外部引用以便诊断。
test("删除必须确认，非当前树删除不改变画布选择且支持撤销", () => {
  const s = session();
  const before = stringifyJSON(s.project.value);
  s.app.deleteTree();
  assert.equal(s.treeMenu.value!.initialMode, "delete");
  assert.equal(s.history.length, 0);
  s.treeMenu.value = undefined;
  assert.equal(stringifyJSON(s.project.value), before);
  s.treeMenu.value = { tree: s.project.value.trees[1]!, x: 0, y: 0 };
  s.app.confirmDeleteTree();
  assert.equal(s.project.value.trees.length, 1);
  assert.equal(s.treeID.value, "1");
  assert.equal(s.selected.value, "1");
  assert.equal(s.treeIdentity.value.byID.has("2"), false);
  assert.equal(s.project.value.trees[0]!.nodes[1]!.tree, "2");
  assert.deepEqual(s.semantic, [true]);
  assert.equal(restoreSnapshot(s.history[0]!).project.trees.length, 2);
});

// 当前树删除后选择剩余树，最后一棵树即使绕过禁用按钮也不能被删除。
test("删除当前树重置选择且拒绝删除最后一棵树", () => {
  const s = session();
  s.app.deleteTree();
  s.app.confirmDeleteTree();
  assert.equal(s.treeID.value, "2");
  assert.equal(s.selected.value, "");
  const count = s.history.length;
  s.app.deleteTree();
  assert.equal(s.treeMenu.value, undefined);
  s.treeMenu.value = { tree: s.project.value.trees[0]!, x: 0, y: 0 };
  s.app.confirmDeleteTree();
  assert.equal(s.project.value.trees.length, 1);
  assert.equal(s.history.length, count);
  assert.match(s.notices.at(-1)!, /至少保留一棵树/);
});

// 对话框目标失效时不能误操作新工程中恰好同 ID 的树；空名和原名不产生历史。
test("过期菜单和无效重命名不能修改工程", () => {
  const s = session();
  const target = s.project.value.trees[1]!;
  for (const name of ["", "  ", "NPC"]) {
    s.treeMenu.value = { tree: target, x: 0, y: 0 };
    s.app.renameTree(name);
  }
  const detached = { ...target };
  s.treeMenu.value = { tree: detached, x: 0, y: 0 };
  s.app.renameTree("错误名称");
  s.treeMenu.value = { tree: detached, x: 0, y: 0 };
  s.app.confirmDeleteTree();
  assert.equal(target.name, "NPC");
  assert.equal(s.project.value.trees.length, 2);
  assert.equal(s.history.length, 0);
});
