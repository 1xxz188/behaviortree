import test from "node:test";
import assert from "node:assert/strict";
import { reactive } from "vue";
import { clone, connect, emptyProject } from "./project.ts";
import type { BTNode, Project, Tree } from "./project.ts";
import { stringifyJSON } from "./json.ts";
import {
  captureSnapshot, restoreSnapshot, TreeIdentityIndex, validTreeID,
} from "./treeIdentity.ts";

// 创建最小树，测试仅关注身份与引用而不依赖示例节点。
function makeTree(id: string, nodes: BTNode[] = []): Tree {
  return { id, name: "重复展示名", root: nodes[0]?.id ?? "", nodes, layout: {} };
}

// 创建可独立修改的工程，保持其他元数据的合法默认值。
function makeProject(...trees: Tree[]): Project {
  return { ...emptyProject(), trees };
}

// 数字树 ID 按工程内最大值递增，手动改号推进游标，保存重载和历史恢复可重建。
test("树 ID 递增并纳入手动修改，恢复后继续分配", () => {
  const project = makeProject(makeTree("1"), makeTree("2"), makeTree("3"));
  const index = new TreeIdentityIndex(project);
  index.rename("2", "100");
  assert.equal(index.allocateID(), "101");
  assert.equal(index.allocateID(), "102");
  const restored = restoreSnapshot(captureSnapshot(project, "100", ""));
  assert.equal(restored.index.allocateID(), "101");
  index.removeTree(index.byID.get("100")!);
  assert.equal(index.allocateID(), "103");
  assert.equal(new TreeIdentityIndex(makeProject()).allocateID(), "1");
});

// 大整数和未完成的子树引用均参与游标计算；分配不扫描工程或树节点。
test("树 ID 避开悬挂引用且支持大整数常数次分配", () => {
  const ref: BTNode = { id: "1", type: "subtree", tree: "9007199254740992" };
  const tree = makeTree("1", [ref]);
  const project = makeProject(tree);
  const index = new TreeIdentityIndex(project);
  Object.defineProperty(project, "trees", { get() { throw new Error("不应扫描工程"); } });
  Object.defineProperty(tree, "nodes", { get() { throw new Error("不应扫描树节点"); } });
  assert.equal(index.allocateID(), "9007199254740993");
  index.setReference(ref, "9007199254741000");
  assert.equal(index.allocateID(), "9007199254741001");
});

// 自动序列也遵守后端 80 位上限，耗尽时不能分配不可保存的树身份。
test("树 ID 达到长度上限时明确拒绝自动创建", () => {
  const index = new TreeIdentityIndex(makeProject(makeTree("9".repeat(80))));
  assert.throws(() => index.allocateID(), /超过 80 位/);
  assert.equal(index.byID.size, 1);
});

// 格式严格对应 Go 规则，不自动清理空白、转换大小写或接受旧格式。
test("树 ID 严格校验格式与工程内唯一性，名称允许重复", () => {
  for (const id of ["", " ", "hello world", "树", "a-b", "a\n", "abc\n", "abc\r\n", "abc\u2028", "abc\u2029", null, 123]) {
    assert.equal(validTreeID(id), false, String(id));
    assert.throws(() => new TreeIdentityIndex(makeProject(makeTree(id as string))));
  }
  for (const id of ["a", "A", "0", "_", "12_tree_A"]) {
    assert.equal(validTreeID(id), true);
    assert.doesNotThrow(() => new TreeIdentityIndex(makeProject(makeTree(id))));
  }
  assert.throws(() => new TreeIdentityIndex(makeProject(makeTree("a"), makeTree("a"))), /已存在/);
  assert.throws(() => new TreeIdentityIndex(makeProject(makeTree("a"), makeTree("A"))), /已存在/);
  assert.equal(validTreeID("a".repeat(80)), true);
  assert.equal(validTreeID("a".repeat(81)), false);
});

// 多树及非子树类型中的引用一起改号，并保持节点身份、布局和顺序。
test("改号同步全部引用，失败和相同 ID 提交不修改工程", () => {
  const self: BTNode = { id: "self", type: "subtree", tree: "a" };
  const first: BTNode = { id: "first", type: "subtree", tree: "a" };
  const second: BTNode = { id: "second", type: "wait", tree: "a" };
  const target = makeTree("a", [self]);
  target.layout = { self: { x: 20, y: 30 } };
  const project = makeProject(target, makeTree("b", [first, second]));
  const index = new TreeIdentityIndex(project);
  const before = stringifyJSON(project);
  for (const id of ["", "a b", "a-b", "b"]) {
    assert.ok(index.validateRename("a", id));
    assert.throws(() => index.rename("a", id));
    assert.equal(stringifyJSON(project), before);
  }
  assert.ok(index.validateRename("missing", "valid"));
  assert.equal(index.rename("a", "a"), 0);
  assert.equal(stringifyJSON(project), before);
  assert.equal(index.rename("a", "New_ID"), 3);
  assert.equal(index.byID.get("New_ID"), target);
  assert.equal(index.byID.has("a"), false);
  assert.deepEqual([self.tree, first.tree, second.tree], ["New_ID", "New_ID", "New_ID"]);
  assert.equal(target.nodes[0], self);
  assert.equal(target.root, "self");
  assert.deepEqual(target.layout, { self: { x: 20, y: 30 } });
  assert.equal(index.rename("New_ID", "again"), 3);
  assert.equal(index.rename("b", "no_refs"), 0);
});

// 新增、复制、改引用、删节点、删树及重新添加目标都通过增量索引保持一致。
test("增删节点和树后连续改号不会遗漏或复活已删除引用", () => {
  const target = makeTree("a");
  const holder = makeTree("b");
  const index = new TreeIdentityIndex(makeProject(target, holder));
  const original: BTNode = { id: "original", type: "subtree", tree: "a" };
  holder.nodes.push(original);
  index.addNode(original);
  const copied = clone(original);
  copied.id = "copied";
  holder.nodes.push(copied);
  index.addNode(copied);
  index.addNode(copied);
  index.setReference(copied, "other");
  assert.equal(index.rename("a", "next"), 1);
  assert.equal(copied.tree, "other");
  index.setReference(copied, "next");
  index.removeNode(original);
  holder.nodes.shift();
  assert.equal(index.rename("next", "later"), 1);
  assert.equal(original.tree, "next");
  assert.equal(copied.tree, "later");
  const copyTree = clone(holder);
  copyTree.id = "copy";
  index.addTree(copyTree);
  assert.equal(index.rename("later", "final"), 2);
  index.removeTree(holder);
  assert.equal(index.rename("final", "end"), 1);
  assert.equal(copied.tree, "final");
  index.setReference(copyTree.nodes[0]!, undefined);
  assert.equal(index.rename("end", "empty"), 0);
  assert.equal(copyTree.nodes[0]!.tree, undefined);
});

// 合并原有悬挂引用桶；删除自引用树不会清除其他树中的悬挂引用。
test("新 ID 已有悬挂引用与删除自引用树均正确维护桶", () => {
  const self: BTNode = { id: "self", type: "subtree", tree: "a" };
  const dangling: BTNode = { id: "dangling", type: "subtree", tree: "next" };
  const incoming: BTNode = { id: "incoming", type: "subtree", tree: "a" };
  const target = makeTree("a", [self]);
  const index = new TreeIdentityIndex(makeProject(target, makeTree("b", [dangling, incoming])));
  assert.equal(index.rename("a", "next"), 2);
  assert.equal(index.rename("next", "last"), 3);
  index.removeTree(target);
  index.addTree(makeTree("last"));
  assert.equal(index.rename("last", "replacement"), 2);
  assert.equal(self.tree, "last");
  assert.equal(dangling.tree, "replacement");
  assert.equal(incoming.tree, "replacement");
});

// 使用不可遍历的无关树检测索引操作，确保改号不扫描整个工程。
test("建索引后改号及局部增删不遍历无关树", () => {
  const incoming: BTNode = { id: "incoming", type: "subtree", tree: "a" };
  const target = makeTree("a");
  const unrelated = makeTree("unrelated", [incoming]);
  const project = makeProject(target, unrelated);
  const index = new TreeIdentityIndex(project);
  Object.defineProperty(project, "trees", { get() { throw new Error("不应扫描工程"); } });
  Object.defineProperty(unrelated, "nodes", { get() { throw new Error("不应扫描无关树"); } });
  assert.equal(index.rename("a", "next"), 1);
  const local = makeTree("local", [{ id: "ref", type: "subtree", tree: "next" }]);
  index.addTree(local);
  assert.equal(index.rename("next", "last"), 2);
  index.removeTree(local);
  assert.equal(index.rename("last", "end"), 1);
});

// 连线复用节点对象，历史恢复重建索引且保存树与节点选择。
test("连线后改号和撤销重建保持引用及选择", () => {
  const holder = makeTree("holder", [
    { id: "root", type: "sequence" },
    { id: "ref", type: "subtree", tree: "a" },
  ]);
  const project = makeProject(makeTree("a"), holder);
  const index = new TreeIdentityIndex(project);
  assert.equal(connect(holder, "root", "ref"), null);
  const before = captureSnapshot(project, "a", "ref");
  assert.equal(index.rename("a", "new"), 1);
  const after = captureSnapshot(project, "new", "ref");
  const undo = restoreSnapshot(before);
  assert.equal(before.treeID, "a");
  assert.equal(before.selected, "ref");
  assert.equal(undo.project.trees[1]!.nodes[1]!.tree, "a");
  assert.equal(undo.index.rename("a", "restored"), 1);
  const redo = restoreSnapshot(after);
  assert.equal(after.treeID, "new");
  assert.equal(redo.project.trees[1]!.nodes[1]!.tree, "new");
  assert.equal(redo.index.rename("new", "redone"), 1);
  assert.deepEqual(redo.project.trees[1]!.nodes[0]!.children, ["ref"]);
});

// 历史入口拒绝非法 ID 和枚举，不要求草稿根节点或引用已经完成。
test("快照恢复复用枚举和身份输入校验", () => {
  const project = makeProject(makeTree("valid"));
  assert.doesNotThrow(() => restoreSnapshot(captureSnapshot(project, "valid", "")));
  project.trees[0]!.id = "bad-id";
  assert.throws(() => restoreSnapshot(captureSnapshot(project, "bad-id", "")), /行为树 ID/);
  project.trees[0]!.id = "valid";
  const snapshot = captureSnapshot(project, "valid", "");
  snapshot.project = snapshot.project.replace('"nodes":[]', '"nodes":[{"id":"n","type":"unknown"}]');
  assert.throws(() => restoreSnapshot(snapshot), /类型/);
});

// 恢复时先包装 Vue 代理再建索引，后续删除节点必须能匹配原桶成员。
test("快照先包装响应式对象再建立身份索引", () => {
  const project = makeProject(makeTree("a"), makeTree("holder", [
    { id: "ref", type: "subtree", tree: "a" },
  ]));
  const restored = restoreSnapshot(captureSnapshot(project, "a", ""), reactive);
  assert.equal(restored.index.byID.get("a"), restored.project.trees[0]);
  const node = restored.project.trees[1]!.nodes[0]!;
  restored.index.removeNode(node);
  assert.equal(restored.index.rename("a", "new"), 0);
  assert.equal(node.tree, "a");
  restored.index.addNode(node);
  assert.equal(restored.index.rename("new", "a"), 0);
  assert.equal(restored.index.rename("a", "next"), 1);
  assert.equal(node.tree, "next");
});
