import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, reactive, ref, shallowRef, watch } from "vue";
import { blankProject, clone } from "./project.ts";
import { NodeIdentityIndex } from "./nodeIdentity.ts";
import { TreeIdentityIndex } from "./treeIdentity.ts";
import { stringifyJSON } from "./json.ts";

// 提取真实应用的身份提交和选择监听器，覆盖草稿到侧栏已生效身份的完整同步路径。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const functions = new Set(["applyTreeID", "cancelTreeID", "cancelNodeID"]);
const variables = new Set(["tree", "nodeIdentity", "nodeIndex", "node", "treeIDError"]);
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

// 创建包含两棵真实响应式树的会话，以检查切树、提交和取消是否误写其他树。
function session() {
  const initial = blankProject();
  initial.trees[0]!.id = "npc_troop";
  const other = clone(initial.trees[0]!);
  other.id = "enemy";
  initial.trees.push(other);
  const project = ref(reactive(initial));
  const treeID = ref("npc_troop");
  const treeIDDraft = ref(treeID.value);
  const selected = ref("");
  const nodeIDDraft = ref("");
  const history: string[] = [];
  const context = {
    computed, reactive, ref, shallowRef, watch, NodeIdentityIndex,
    project, treeID, treeIDDraft, selected, nodeIDDraft,
    treeIdentity: shallowRef(new TreeIdentityIndex(project.value)), renamingTree: false,
    notice: () => {}, endPaletteDrag: () => {}, fitView: () => {}, closeTreeMenu: () => {}, cancelCodeName: () => {},
    setTimeout: (callback: () => void) => callback(),
    mutate: (callback: () => void) => { history.push(stringifyJSON(project.value)); callback(); },
  };
  const app = runInNewContext(`${js}\n({ tree, node, treeIDError, applyTreeID, cancelTreeID });`, context) as {
    tree: { readonly value: typeof other }; // 侧栏和右侧设置共同定位的真实树。
    node: { readonly value: typeof other.nodes[number] | undefined }; // 右侧面板的实际分支条件。
    treeIDError: { readonly value: string }; // 输入框与提交入口共享的校验结果。
    applyTreeID: () => void; // 应用草稿并同步索引与选择。
    cancelTreeID: () => void; // 放弃草稿并恢复生效身份。
  };
  return { ...context, app, history };
}

// 输入框只保存草稿，侧栏始终显示已生效 ID；点击应用后两者必须立即统一。
test("树 ID 草稿允许暂时不同，应用后同步侧栏身份且只记录一次历史", () => {
  const s = session();
  s.treeIDDraft.value = "NpcTroop";
  assert.equal(s.project.value.trees[0]!.id, "npc_troop");
  assert.equal(s.treeIDDraft.value, "NpcTroop");
  assert.equal(s.app.treeIDError.value, "");
  assert.equal(s.history.length, 0);
  s.app.applyTreeID();
  assert.equal(s.project.value.trees[0]!.id, "NpcTroop");
  assert.equal(s.app.tree.value.id, "NpcTroop");
  assert.equal(s.treeIDDraft.value, "NpcTroop");
  assert.equal(s.treeID.value, "NpcTroop");
  assert.equal(s.history.length, 1);
});

// 未提交内容不能随切树带入另一棵树，返回原树也只能看到其已生效身份。
test("切树自动取消身份草稿且不改变任一树", () => {
  const s = session();
  s.treeIDDraft.value = "NpcTroop";
  s.treeID.value = "enemy";
  assert.equal(s.treeIDDraft.value, "enemy");
  assert.equal(s.app.tree.value.id, "enemy");
  s.treeID.value = "npc_troop";
  assert.equal(s.treeIDDraft.value, "npc_troop");
  assert.equal(s.history.length, 0);
});

// 根节点属于节点属性分支；合法改树 ID 不能清空当前节点，而真正切树应清空。
test("选择根节点显示节点属性，改树 ID 保留选择，切树清空选择", () => {
  const s = session();
  assert.equal(s.app.node.value, undefined);
  s.selected.value = s.app.tree.value.root;
  const root = s.app.tree.value.nodes[0]!;
  assert.equal(s.app.node.value, root);
  assert.equal(s.nodeIDDraft.value, root.id);
  s.treeIDDraft.value = "NpcTroop";
  s.app.applyTreeID();
  assert.equal(s.app.node.value, root);
  assert.equal(s.selected.value, root.id);
  s.treeID.value = "enemy";
  assert.equal(s.selected.value, "");
  assert.equal(s.app.node.value, undefined);
});

// 冲突输入及回车提交均不得改变侧栏真实 ID，取消只恢复草稿且不产生历史。
test("冲突草稿不能生效，取消恢复侧栏所示身份", () => {
  const s = session();
  s.treeIDDraft.value = "enemy";
  assert.match(s.app.treeIDError.value, /已存在/);
  s.app.applyTreeID();
  assert.equal(s.project.value.trees[0]!.id, "npc_troop");
  s.app.cancelTreeID();
  assert.equal(s.treeIDDraft.value, "npc_troop");
  assert.equal(s.history.length, 0);
});
