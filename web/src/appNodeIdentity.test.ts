import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, reactive, ref, shallowRef, watch } from "vue";
import { blankProject, clone, kinds } from "./project.ts";
import { NodeIdentityIndex } from "./nodeIdentity.ts";
import { TreeIdentityIndex } from "./treeIdentity.ts";
import { stringifyJSON } from "./json.ts";
import type { NodeType } from "./enums.ts";

// 提取真实应用处理函数及响应式错误表达式，验证快捷键也无法绕过提交校验。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["applyNodeID", "cancelNodeID", "addNode", "duplicate", "addTree", "applyTreeID", "cancelTreeID"]);
const handlers = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isVariableStatement(statement) && statement.declarationList.declarations.some(
    declaration => ts.isIdentifier(declaration.name) && ["nodeIDError", "treeIDError"].includes(declaration.name.text))),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 测试会话保留实际响应式索引、历史边界及工程内容，避免复制业务实现。
function session() {
  const project = reactive(blankProject());
  const treeIdentity = shallowRef(new TreeIdentityIndex(project));
  const treeID = ref(project.trees[0]!.id);
  const treeIDDraft = ref(treeID.value);
  const tree = computed(() => treeIdentity.value.byID.get(treeID.value) ?? project.trees[0]!);
  const nodeIdentity = ref(new NodeIdentityIndex(tree.value));
  const selected = ref(tree.value.root);
  const nodeIDDraft = ref(selected.value);
  const node = computed(() => nodeIdentity.value.byID.get(selected.value));
  const history: string[] = [];
  const notices: string[] = [];
  const context = {
    computed, clone, kinds, tree, nodeIdentity, treeIdentity, selected, nodeIDDraft, node,
    project: ref(project), treeID, treeIDDraft, renamingTree: false,
    definitionIndex: { value: new Map() }, cancelCodeName: () => {},
    notice: (message: string) => notices.push(message),
    mutate: (fn: () => void) => { history.push(stringifyJSON(project)); fn(); },
  };
  const app = runInNewContext(`${js}\n({ applyNodeID, cancelNodeID, addNode, duplicate, nodeIDError, addTree, applyTreeID, cancelTreeID, treeIDError });`, context) as {
    applyNodeID: () => void; // 属性面板及回车共用的提交入口。
    cancelNodeID: () => void; // 放弃身份草稿的入口。
    addNode: (type: NodeType) => void; // 侧栏和拖拽共用的新增入口。
    duplicate: () => void; // 复制当前选中节点的入口。
    nodeIDError: { readonly value: string }; // 模板直接使用的即时校验结果。
    addTree: () => void; // 侧栏创建新树的真实入口。
    applyTreeID: () => void; // 树身份提交入口。
    cancelTreeID: () => void; // 取消树身份草稿。
    treeIDError: { readonly value: string }; // 工程内树身份校验结果。
  };
  // 与应用相同，在实际树对象切换时重建节点索引，验证不同树独立编号。
  watch(tree, current => { nodeIdentity.value = new NodeIdentityIndex(current); }, { flush: "sync" });
  return { ...context, project, history, notices, app };
}

// 冲突即时可见，强行调用提交入口也不改变节点、历史、选择或生成状态。
test("节点冲突草稿即时提示，提交无效且取消恢复原值", () => {
  const s = session();
  s.app.addNode("wait");
  const selected = s.selected.value;
  const before = stringifyJSON(s.project);
  const historyLength = s.history.length;
  s.nodeIDDraft.value = s.tree.value.root;
  assert.match(s.app.nodeIDError.value, /当前树内节点 ID 已存在/);
  s.app.applyNodeID();
  assert.equal(stringifyJSON(s.project), before);
  assert.equal(s.selected.value, selected);
  assert.equal(s.history.length, historyLength);
  assert.equal(s.notices.length, 0);
  s.nodeIDDraft.value = "";
  assert.match(s.app.nodeIDError.value, /不能为空/);
  s.app.applyNodeID();
  assert.equal(s.history.length, historyLength);
  s.app.cancelNodeID();
  assert.equal(s.nodeIDDraft.value, selected);
  assert.equal(s.app.nodeIDError.value, "");
});

// 新增、复制和显式改号必须共用分配游标，成功改号只产生一次历史记录。
test("真实应用新增复制使用递增 ID，合法改号推进后续序列", () => {
  const s = session();
  s.app.addNode("wait");
  assert.equal(s.selected.value, "2");
  s.app.duplicate();
  assert.equal(s.selected.value, "3");
  const codeName = s.node.value!.codeName;
  const before = s.history.length;
  s.nodeIDDraft.value = "100";
  assert.equal(s.app.nodeIDError.value, "");
  s.app.applyNodeID();
  assert.equal(s.selected.value, "100");
  assert.equal(s.node.value!.codeName, codeName);
  assert.equal(s.history.length, before + 1);
  assert.ok(s.tree.value.layout!["100"]);
  assert.equal(s.tree.value.layout!["3"], undefined);
  s.app.addNode("wait");
  assert.equal(s.selected.value, "101");
});

// 新建工程和新增树都使用数字身份；树与节点分开编号，树改号影响后续树分配。
test("树递增入口及改为100后的101分配，冲突不产生历史", () => {
  const s = session();
  assert.equal(s.tree.value.id, "1");
  assert.equal(s.tree.value.root, "1");
  s.app.addTree();
  assert.equal(s.treeID.value, "2");
  s.app.addNode("wait");
  assert.equal(s.selected.value, "1");
  s.treeIDDraft.value = "1";
  assert.match(s.app.treeIDError.value, /已存在/);
  const before = stringifyJSON(s.project);
  const count = s.history.length;
  s.app.applyTreeID();
  assert.equal(stringifyJSON(s.project), before);
  assert.equal(s.history.length, count);
  s.treeIDDraft.value = "100";
  s.app.applyTreeID();
  assert.equal(s.treeID.value, "100");
  s.app.addTree();
  assert.equal(s.treeID.value, "101");
});
