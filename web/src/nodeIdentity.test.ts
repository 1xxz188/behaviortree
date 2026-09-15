import test from "node:test";
import assert from "node:assert/strict";
import { computed, reactive } from "vue";
import { allocateID, blankProject, clone } from "./project.ts";
import type { Tree } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import { semanticSignature } from "./generation.ts";
import { NodeIdentityIndex, validNodeID } from "./nodeIdentity.ts";

// 构造含重复入边的草稿，验证即使拓扑尚未合法也不会遗漏引用。
function makeTree(): Tree {
  return { id: "combat", name: "战斗", root: "root", nodes: [
    { id: "root", type: "sequence", children: ["attack", "pause", "attack"] },
    { id: "attack", type: "action", name: "攻击目标", binding: "attack" },
    { id: "pause", type: "wait", durationMs: 1 },
  ], layout: { root: { x: 1, y: 2 }, attack: { x: 3, y: 4 } } };
}

// 身份不裁剪字符、不转成索引，重复限制仅作用于当前树。
test("Node ID 非空且树内唯一，保留特殊字符和跨树同 ID", () => {
  const tree = makeTree();
  const index = new NodeIdentityIndex(tree);
  const before = stringifyJSON(tree);
  assert.equal(validNodeID(""), false);
  assert.equal(validNodeID(null), false);
  assert.equal(validNodeID(37), false);
  for (const id of ["攻击 目标", "a-b", "37", " ", "a\n", "__proto__"])
    assert.equal(validNodeID(id), true);
  for (const id of ["", "pause"]) {
    assert.throws(() => index.rename("attack", id));
    assert.equal(stringifyJSON(tree), before);
  }
  assert.ok(index.validateRename("missing", "new"));
  assert.equal(index.rename("attack", "attack"), 0);
  assert.equal(stringifyJSON(tree), before);
  const second = new NodeIdentityIndex(makeTree());
  index.rename("attack", "攻击 目标");
  assert.equal(second.byID.has("attack"), true);
  assert.equal(second.validateRename("attack", "攻击 目标"), null);
});

// 根和非根改号保持节点顺序、对象、绑定及布局，同步所有入边。
test("改号原子同步根、父子引用及布局并保持稳定业务属性", () => {
  const tree = makeTree();
  const index = new NodeIdentityIndex(tree);
  const attack = tree.nodes[1]!;
  assert.equal(index.rename("attack", "attack_target"), 2);
  assert.deepEqual(tree.nodes[0]!.children, ["attack_target", "pause", "attack_target"]);
  assert.equal(tree.nodes[1], attack);
  assert.equal(attack.name, "攻击目标");
  assert.equal(attack.binding, "attack");
  assert.deepEqual(tree.layout!.attack_target, { x: 3, y: 4 });
  assert.equal(Object.hasOwn(tree.layout!, "attack"), false);
  assert.equal(index.rename("root", "combat_root"), 0);
  assert.equal(tree.root, "combat_root");
  assert.deepEqual(tree.nodes.map((node) => node.id), ["combat_root", "attack_target", "pause"]);
  assert.equal(index.rename("attack_target", "again"), 2);
});

// 连线、排序、断线及删除都增量维护实际入边位置。
test("拓扑修改后改号仍精确定位引用，删除清理相关边", () => {
  const tree = makeTree();
  const index = new NodeIdentityIndex(tree);
  const root = tree.nodes[0]!;
  index.setChildren(root, ["pause", "attack"]);
  index.rename("attack", "target");
  assert.deepEqual(root.children, ["pause", "target"]);
  index.setChildren(root, ["pause"]);
  assert.equal(index.rename("target", "detached"), 0);
  index.setChildren(root, ["detached", "pause", "detached"]);
  index.removeNode(index.byID.get("detached")!);
  assert.deepEqual(root.children, ["pause"]);
  assert.equal(index.byID.has("detached"), false);
  assert.equal(tree.nodes.length, 2);
  assert.equal(Object.hasOwn(tree.layout!, "detached"), false);
  index.removeNode(root);
  assert.equal(tree.root, "");
  assert.equal(index.rename("pause", "last"), 0);
});

// 新 ID 的悬挂引用与旧入边合并，删除或连续改号不会留下失效桶。
test("改号合并悬挂引用且支持自引用草稿", () => {
  const tree = makeTree();
  tree.nodes[0]!.children = ["root", "future", "attack"];
  const index = new NodeIdentityIndex(tree);
  assert.equal(index.rename("attack", "future"), 1);
  assert.equal(index.rename("future", "next"), 2);
  assert.deepEqual(tree.nodes[0]!.children, ["root", "next", "next"]);
  assert.equal(index.rename("root", "self"), 1);
  index.removeNode(tree.nodes[0]!);
  assert.equal(index.rename("next", "detached"), 0);
});

// 特殊属性名作为布局键必须保持普通数据，不改变对象原型。
test("特殊 ID 布局键安全保存并可再次改号", () => {
  const tree = makeTree();
  const prototype = Object.getPrototypeOf(tree.layout);
  const index = new NodeIdentityIndex(tree);
  index.rename("attack", "__proto__");
  assert.equal(Object.getPrototypeOf(tree.layout), prototype);
  assert.equal(Object.hasOwn(tree.layout!, "__proto__"), true);
  assert.deepEqual(tree.layout!["__proto__"], { x: 3, y: 4 });
  index.rename("__proto__", "constructor");
  assert.deepEqual(tree.layout!.constructor, { x: 3, y: 4 });
  const loaded = parseJSON<Tree>(stringifyJSON(tree));
  assert.deepEqual(loaded.layout!.constructor, { x: 3, y: 4 });
});

// 历史记录同时保存选择身份，撤销、重做和文件重载恢复同一组引用。
test("节点改号快照支持撤销重做、选择恢复及保存重载", () => {
  const project = { ...blankProject(), trees: [makeTree()] };
  const before = captureSnapshot(project, "combat", "attack");
  const signature = semanticSignature(project);
  new NodeIdentityIndex(project.trees[0]!).rename("attack", "attack_target");
  assert.notEqual(semanticSignature(project), signature);
  const after = captureSnapshot(project, "combat", "attack_target");
  for (const snapshot of [before, after]) {
    const restored = restoreSnapshot(snapshot, reactive);
    const tree = restored.project.trees[0]!;
    const index = new NodeIdentityIndex(tree);
    assert.ok(index.byID.has(snapshot.selected));
    assert.equal(tree.nodes[0]!.children![0], snapshot.selected);
    assert.deepEqual(tree.layout![snapshot.selected], { x: 3, y: 4 });
    assert.equal(index.rename(snapshot.selected, "reloaded"), 2);
  }
});

// Vue 代理持有同一批节点和边对象，重复操作后响应式查找仍可更新。
test("响应式索引支持连续改号、复制和删除", () => {
  const tree = reactive(makeTree());
  const index = reactive(new NodeIdentityIndex(tree));
  const ids = computed(() => [...index.byID.keys()]);
  assert.ok(ids.value.includes("attack"));
  index.rename("attack", "custom");
  assert.ok(ids.value.includes("custom"));
  const occupied = new Set(index.byID.keys());
  const copy = clone(index.byID.get("custom")!);
  copy.id = allocateID(occupied, "node", () => "copy-uuid");
  copy.children = [];
  tree.nodes.push(copy);
  index.addNode(tree.nodes[tree.nodes.length - 1]!);
  assert.notEqual(copy.id, "custom");
  assert.ok(index.validateRename("custom", copy.id));
  index.setChildren(tree.nodes[0]!, [copy.id, "custom"]);
  index.rename(copy.id, "copied");
  index.removeNode(index.byID.get("custom")!);
  assert.deepEqual(tree.nodes[0]!.children, ["copied"]);
  assert.equal(index.rename("copied", "final"), 1);
});

// 建索引后的查重和改号不读取节点集合，也不遍历父节点的整个孩子列表。
test("改号只访问已索引入边，不扫描树或无关节点", () => {
  const tree = makeTree();
  const parent = tree.nodes[0]!;
  const unrelated = tree.nodes[2]!;
  const index = new NodeIdentityIndex(tree);
  Object.defineProperty(tree, "nodes", { get() { throw new Error("不应读取节点集合"); } });
  Object.defineProperty(unrelated, "children", { get() { throw new Error("不应读取无关节点"); } });
  Object.defineProperty(parent.children!, Symbol.iterator, {
    value() { throw new Error("不应遍历孩子列表"); },
  });
  assert.equal(index.validateRename("attack", "target"), null);
  assert.equal(index.rename("attack", "target"), 2);
  assert.equal(parent.children![0], "target");
  assert.equal(parent.children![2], "target");
});
