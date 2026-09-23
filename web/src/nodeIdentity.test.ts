import test from "node:test";
import assert from "node:assert/strict";
import { computed, reactive } from "vue";
import { blankProject, clone } from "./project.ts";
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
  const copy = clone(index.byID.get("custom")!);
  copy.id = index.allocateID();
  copy.codeName = index.codeNames.allocateCopy(copy.codeName!);
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

// 新增及复制共用树内序列，旧字符串 ID 保持原样，不受其他树的编号影响。
test("节点自动 ID 为树内递增数字字符串并兼容已有身份", () => {
  const tree = makeTree();
  tree.nodes.push({ id: "7", type: "wait" }, { id: "node_abcdef", type: "wait" });
  const index = new NodeIdentityIndex(tree);
  assert.equal(index.allocateID(), "8");
  assert.equal(index.allocateID(), "9");
  index.rename("attack", "20");
  assert.equal(index.allocateID(), "21");
  index.removeNode(index.byID.get("20")!);
  assert.equal(index.allocateID(), "22");
  assert.ok(index.byID.has("node_abcdef"));
  assert.equal(new NodeIdentityIndex(makeTree()).allocateID(), "1");
  const restored = new NodeIdentityIndex(parseJSON<Tree>(stringifyJSON(tree)));
  assert.equal(restored.allocateID(), "8");
});

// 数字字符串不经过浮点数转换，超过安全整数后仍能递增且不会产生相同 ID。
test("节点序列支持大整数且分配不扫描节点集合", () => {
  const tree = makeTree();
  tree.nodes.push({ id: "9007199254740992", type: "wait" });
  const index = reactive(new NodeIdentityIndex(tree));
  Object.defineProperty(tree, "nodes", { get() { throw new Error("分配不应扫描节点集合"); } });
  assert.equal(index.allocateID(), "9007199254740993");
  assert.equal(index.allocateID(), "9007199254740994");
});

// 自动分配不能意外接管草稿中尚未修复的根、连线或布局身份。
test("自动序列避开草稿悬挂引用及残留布局", () => {
  const tree = makeTree();
  tree.root = "10";
  tree.nodes[0]!.children!.push("11");
  tree.layout!["12"] = { x: 0, y: 0 };
  const index = new NodeIdentityIndex(tree);
  assert.equal(index.allocateID(), "13");
  index.setChildren(tree.nodes[0]!, ["50"]);
  assert.equal(index.allocateID(), "51");
});

// 独立分支覆盖重连位置、跨父自动转移及旧节点保留，不依赖节点数组的排列。
function reconnectTree(): Tree {
  return { id: "reconnect", name: "重连", root: "root", nodes: [
    { id: "root", type: "sequence", children: ["left", "right", "branch"] },
    { id: "left", type: "sequence", children: ["nested"] },
    { id: "right", type: "sequence", children: ["child"] },
    { id: "branch", type: "sequence", children: [] },
    { id: "nested", type: "sequence", children: [] },
    { id: "child", type: "action" },
    { id: "leaf", type: "wait" },
    { id: "decorator", type: "timeout", children: ["leaf"] },
  ] };
}

// 删除连线只移除该父节点的一个引用，保留两端节点、顺序及根节点。
test("断开连线保持节点与其他子节点并更新引用索引", () => {
  const tree = reconnectTree();
  const index = new NodeIdentityIndex(tree);
  const plan = index.prepareDisconnect("root", "right");
  if (typeof plan === "string") assert.fail(plan);
  assert.deepEqual(tree.nodes[0]!.children, ["left", "right", "branch"]);
  index.setChildren(plan.parent, plan.children);
  assert.deepEqual(tree.nodes[0]!.children, ["left", "branch"]);
  assert.equal(tree.root, "root");
  assert.ok(index.byID.has("right"));
  assert.equal(index.prepareDisconnect("root", "right"), "连线已失效，请重试");
});

// 不存在或重复的旧边在确认前拒绝删除，工程数据维持原值。
test("断开无效或重复连线不会修改树", () => {
  const tree = reconnectTree();
  tree.nodes[0]!.children!.push("left");
  const index = new NodeIdentityIndex(tree);
  const before = stringifyJSON(tree);
  assert.equal(index.prepareDisconnect("root", "left"), "连线存在重复引用，请先修复草稿");
  assert.equal(index.prepareDisconnect("missing", "right"), "连线已失效，请重试");
  assert.equal(stringifyJSON(tree), before);
});

// 拖动终点占用旧边的顺序位置，目标原有父边同次删除；保存往返保留拓扑。
test("重连终点保留顺序并自动转移已有父节点", () => {
  const tree = reconnectTree();
  const index = new NodeIdentityIndex(tree);
  const plan = index.prepareReconnect("root", "left", "root", "child");
  if (!plan || typeof plan === "string") assert.fail(`预期合法重连：${plan}`);
  index.applyReconnect(plan);
  assert.deepEqual(index.byID.get("root")!.children, ["child", "right", "branch"]);
  assert.deepEqual(index.byID.get("right")!.children, []);
  assert.ok(index.byID.has("left"));
  const loaded = parseJSON<Tree>(stringifyJSON(tree));
  assert.deepEqual(loaded.nodes.find(node => node.id === "root")!.children, ["child", "right", "branch"]);
  assert.deepEqual(loaded.nodes.find(node => node.id === "right")!.children, []);
});

// 拖动起点将旧子节点追加到新父末尾，并保持反向引用索引可继续改号。
test("重连起点追加到新父末尾并更新入边索引", () => {
  const tree = reconnectTree();
  const index = new NodeIdentityIndex(tree);
  const plan = index.prepareReconnect("root", "branch", "right", "branch");
  if (!plan || typeof plan === "string") assert.fail(`预期合法重连：${plan}`);
  index.applyReconnect(plan);
  assert.deepEqual(index.byID.get("root")!.children, ["left", "right"]);
  assert.deepEqual(index.byID.get("right")!.children, ["child", "branch"]);
  assert.equal(index.rename("branch", "moved"), 1);
  assert.deepEqual(index.byID.get("right")!.children, ["child", "moved"]);
});

// 当前根作为新子节点时沿用普通连接的入口规则，旧目标保持可编辑草稿。
test("重连到当前根时调整入口且不删除旧节点", () => {
  const tree = reconnectTree();
  tree.root = "leaf";
  const index = new NodeIdentityIndex(tree);
  const plan = index.prepareReconnect("root", "left", "root", "leaf");
  if (!plan || typeof plan === "string") assert.fail(`预期合法重连：${plan}`);
  index.applyReconnect(plan);
  assert.equal(tree.root, "root");
  assert.deepEqual(index.byID.get("root")!.children, ["leaf", "right", "branch"]);
  assert.ok(index.byID.has("left"));
});

// 所有失败及原地拖放均不得改变工程；叶节点、装饰容量和环必须被拒绝。
test("非法重连及无变化重连保持工程和索引不变", () => {
  const tree = reconnectTree();
  const index = new NodeIdentityIndex(tree);
  const before = stringifyJSON(tree);
  const rejected: [string, string, string, string][] = [
    ["root", "left", "root", "right"], // 同父已有连接。
    ["root", "left", "child", "left"], // 叶节点不能作为父节点。
    ["root", "right", "decorator", "right"], // 装饰节点已有孩子。
    ["root", "left", "nested", "left"], // 后代回连形成循环。
    ["root", "left", "missing", "left"], // 无效落点。
    ["missing", "left", "right", "left"], // 过期旧边。
    ["root", "left", "right", "branch"], // 一次修改两个端点。
  ];
  for (const args of rejected) {
    assert.equal(typeof index.prepareReconnect(...args), "string");
    assert.equal(stringifyJSON(tree), before);
  }
  assert.equal(index.prepareReconnect("root", "left", "root", "left"), null);
  assert.equal(stringifyJSON(tree), before);
  assert.equal(index.rename("left", "still_left"), 1);
});

// 建索引后的重连只读取相关父节点和目标子树，不扫描整棵树或无关分支。
test("重连不遍历节点数组和无关分支", () => {
  const tree = reconnectTree();
  const index = new NodeIdentityIndex(tree);
  const unrelated = index.byID.get("decorator")!;
  Object.defineProperty(tree, "nodes", { get() { throw new Error("不应扫描节点数组"); } });
  Object.defineProperty(unrelated, "children", { get() { throw new Error("不应读取无关分支"); } });
  const plan = index.prepareReconnect("root", "branch", "right", "branch");
  if (!plan || typeof plan === "string") assert.fail(`预期合法重连：${plan}`);
  index.applyReconnect(plan);
  assert.deepEqual(index.byID.get("right")!.children, ["child", "branch"]);
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
