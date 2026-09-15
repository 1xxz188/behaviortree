import test from "node:test";
import assert from "node:assert/strict";
import {
  autoLayout,
  clone,
  connect,
  emptyProject,
  removeNodes,
  allocateID,
  uid,
} from "./project.ts";
import {
  createSourceIndex, GenerationRequests, semanticSignature, sourceNodeKey,
} from "./generation.ts";
import { parseInput, parseJSON, stringifyJSON } from "./json.ts";
import {
  nodeTypes, valueTypes, definitionKinds, parseNodeType, parseValueType,
  parseDefinitionKind, validateProjectTypes, validateCatalogTypes,
} from "./enums.ts";

// 展示名可重复且不影响生成语义，改号仍必须淘汰旧版本与在途结果。
test("树展示名不改变签名或在途请求，ID变更仍使语义改变", () => {
  const project = emptyProject();
  const original = clone(project);
  const requests = new GenerationRequests();
  const token = requests.begin(project);
  project.trees[0]!.name = "新的展示名";
  assert.equal(semanticSignature(project), semanticSignature(original));
  assert.equal(requests.accepts(token, project), true);
  assert.equal(project.trees[0]!.name, "新的展示名");
  project.trees[0]!.id = "new_tree";
  assert.notEqual(semanticSignature(project), semanticSignature(original));
  assert.equal(requests.accepts(token, project), false);
  requests.invalidate();
  assert.equal(requests.acceptsRevision(token), false);
});

test("导入、撤销拷贝和导出保留完整64位整数", () => {
  const input =
    '{"value":18446744073709551615,"negative":-9223372036854775808,"x":12.5}';
  assert.equal(stringifyJSON(clone(parseJSON(input))), input);
  assert.equal(
    stringifyJSON(parseInput("18446744073709551615", "uint64")),
    "18446744073709551615",
  );
  assert.equal(parseInput("moving", "enum"), "moving");
});

test("自动布局只改变坐标，不改变根、ID或显式执行顺序", () => {
  const tree = emptyProject().trees[0]!;
  const nodes = stringifyJSON(tree.nodes),
    root = tree.root;
  tree.layout = {};
  autoLayout(tree);
  assert.equal(stringifyJSON(tree.nodes), nodes);
  assert.equal(tree.root, root);
  assert.equal(Object.keys(tree.layout).length, tree.nodes.length);
});

test("连线拒绝重复父节点、环和非法叶节点连接", () => {
  const tree = emptyProject().trees[0]!;
  assert.ok(connect(tree, "root", "walk"));
  assert.ok(connect(tree, "again", "root"));
  assert.ok(connect(tree, "walk", "root"));
  assert.ok(connect(tree, "root", "root"));
});

test("删除根及内部节点移除悬挂连接，撤销快照保持完整", () => {
  const tree = emptyProject().trees[0]!,
    previous = clone(tree);
  removeNodes(tree, ["root", "again"]);
  assert.equal(tree.root, "");
  assert.equal(tree.nodes.length, 2);
  assert.equal(previous.nodes.length, 4);
  assert.deepEqual(previous.nodes[0]!.children, ["pause", "again"]);
});

// 覆盖可读标签往返及实际 JSON 输入，不能仅依赖 TypeScript 的静态约束。
test("全部节点、目录和值类型保持可读名称", () => {
  for (const type of nodeTypes) assert.equal(parseNodeType(parseJSON(stringifyJSON(type))), type);
  for (const type of valueTypes) assert.equal(parseValueType(parseJSON(stringifyJSON(type))), type);
  for (const kind of definitionKinds) assert.equal(parseDefinitionKind(parseJSON(stringifyJSON(kind))), kind);
  assert.equal(nodeTypes.length, 14);
  assert.equal(valueTypes.length, 8);
});

// 各类非法标签均在解析边界拒绝；旧实体拼写也不提供兼容别名。
test("枚举解析拒绝缺失、空、null、数字及未知标签", () => {
  for (const value of [undefined, "", null, 0, 1, "unknown", "entityID", {}, []]) {
    assert.throws(() => parseNodeType(value), /type/);
    assert.throws(() => parseDefinitionKind(value), /kind/);
    assert.throws(() => parseValueType(value), /type/);
  }
  // 模拟越过静态类型检查的 JS 调用，未知类型不能回退为整数。
  // @ts-expect-error 验证运行时对非法输入的拒绝。
  assert.throws(() => parseInput("123", "mystery"), /未知/);
});

// 报错必须包含实际字段路径；语义不完整的合法类型草稿仍能编辑。
test("工程与目录解析定位非法类型并允许未连完草稿", () => {
  const draft = emptyProject();
  draft.trees[0]!.root = "";
  draft.trees[0]!.nodes[0]!.children = [];
  assert.doesNotThrow(() => validateProjectTypes(parseJSON(stringifyJSON(draft))));
  assert.throws(() => validateProjectTypes({ trees: [{ nodes: [{ type: "bogus" }] }] }), /trees\[0\].nodes\[0\].type.*bogus/);
  assert.throws(() => validateProjectTypes({ trees: [{ nodes: [{}] }] }), /trees\[0\].nodes\[0\].type.*undefined/);
  assert.throws(() => validateProjectTypes({ blackboard: [{ type: null }] }), /blackboard\[0\].type.*null/);
  assert.throws(() => validateCatalogTypes([{ kind: "sequence" }]), /catalog\[0\].kind.*sequence/);
  assert.throws(() => validateCatalogTypes([{ kind: "action", params: [{ type: 1 }] }]), /catalog\[0\].params\[0\].type.*1/);
  assert.throws(() => validateProjectTypes({ trees: {} }), /trees.*数组/);
});

// 枚举标签收紧不改变用户业务枚举、实体 ID 及 duration 的无损值表示。
test("八种值输入保留原有语义及大整数精度", () => {
  const cases = [
    ["bool", "true", "true"], ["int64", "-9223372036854775808", "-9223372036854775808"],
    ["uint64", "18446744073709551615", "18446744073709551615"],
    ["float64", "1.25", "1.25"], ["string", "hello", '"hello"'],
    ["enum", "moving", '"moving"'], ["entity", "18446744073709551615", "18446744073709551615"],
    ["duration", "9223372036854775807", "9223372036854775807"],
  ] as const;
  for (const [type, input, output] of cases)
    assert.equal(stringifyJSON(parseInput(input, type)), output);
});

// 使用可控随机序列制造碰撞，验证重试后立即保留新 ID，并保留完整 UUID。
test("节点ID分配保留完整UUID并在集合中查重重试", () => {
  const first = "00000000-0000-4000-8000-000000000001";
  const second = "00000000-0000-4000-8000-000000000002";
  const occupied = new Set([`node_${first.replaceAll("-", "")}`]);
  const random = [first, first, second];
  let calls = 0;
  const id = allocateID(occupied, "node", () => random[calls++]!);
  assert.equal(calls, 3);
  assert.equal(id, `node_${second.replaceAll("-", "")}`);
  assert.ok(occupied.has(id));
  assert.match(uid(), /^node_[0-9a-f]{32}$/);
  assert.match(allocateID(occupied, "tree"), /^tree_[0-9a-f]{32}$/);
});

// 坐标、对象键顺序及无语义集合顺序不改变签名，执行顺序和无损整数仍参与比较。
test("源码语义签名忽略布局且保留业务变化与64位精度", () => {
  const project = emptyProject();
  project.blackboard.push({ id: "precise", name: "Precise", type: "uint64", default: parseJSON("18446744073709551615") });
  const before = stringifyJSON(project);
  const signature = semanticSignature(project);
  const changed = clone(project);
  changed.trees[0]!.layout = { root: { x: 900, y: -100 } };
  changed.trees[0]!.nodes.reverse();
  changed.blackboard.reverse();
  assert.equal(semanticSignature(changed), signature);
  assert.equal(stringifyJSON(project), before);
  changed.trees[0]!.nodes.find((node) => node.id === "root")!.children!.reverse();
  assert.notEqual(semanticSignature(changed), signature);
  const precise = clone(project);
  precise.blackboard.find((field) => field.id === "precise")!.default = parseJSON("18446744073709551614");
  assert.notEqual(semanticSignature(precise), signature);
});

// 模拟 Go omitempty 保存后的空属性省略，重新打开同一工程不会误报过期。
test("源码语义签名统一JSON保存后的可选空属性", () => {
  const project = emptyProject();
  const node = project.trees[0]!.nodes[0]!;
  node.params = {};
  node.binding = "";
  node.count = 0;
  const reopened = clone(project);
  delete reopened.trees[0]!.nodes[0]!.params;
  delete reopened.trees[0]!.nodes[0]!.binding;
  delete reopened.trees[0]!.nodes[0]!.count;
  assert.equal(semanticSignature(project), semanticSignature(reopened));
});

// 节点可有多个展开位置，跨树同名不得合并；辅助函数和节点间的空白不属于节点。
test("源码映射支持节点多实例且导航仅限对应函数范围", () => {
  const source = [
    "package behavior",
    "func btNodeMainShared_Slot0(f *Frame) Status {",
    "\tif true {",
    "\t\treturn Success",
    "\t}",
    "\treturn Failure",
    "}",
    "",
    "func helper() {",
    "}",
    "func btNodeMainShared_Slot1(f *Frame) Status {",
    "\treturn Success",
    "}",
    "func btNodeOtherShared(f *Frame) Status {",
    "\treturn Success",
    "}",
  ].join("\n");
  const locations = [
    { treeId: "main", nodeId: "shared", index: 0, line: 2, functionName: "btNodeMainShared_Slot0" },
    { treeId: "main", nodeId: "shared", index: 1, line: 11, functionName: "btNodeMainShared_Slot1" },
    { treeId: "other", nodeId: "shared", index: 2, line: 14, functionName: "btNodeOtherShared" },
    { treeId: "main", nodeId: "wrong", index: 3, line: 9, functionName: "btNodeMainWrong" },
  ];
  const index = createSourceIndex(source, locations);
  assert.deepEqual(index.byNode.get(sourceNodeKey("main", "shared")), locations.slice(0, 2));
  assert.deepEqual(index.byNode.get(sourceNodeKey("other", "shared")), [locations[2]]);
  assert.equal(index.byLine.get(6)?.index, 0);
  assert.equal(index.byLine.get(7)?.index, 0);
  for (const line of [1, 8, 9, 10, 17]) assert.equal(index.byLine.has(line), false);
  assert.equal(index.byNode.has(sourceNodeKey("main", "wrong")), false);
  assert.notEqual(sourceNodeKey("a:b", "c"), sourceNodeKey("a", "b:c"));
});

// 映射按实际符号区分重复展开，错误名称和过期行号不得匹配。
test("语义函数名映射拒绝不匹配的函数", () => {
  const source = [
    "package behavior",
    "func btNodePatrolWalk(f *Frame) Status {",
    "\tconst node = nodePatrolWalk",
    "\treturn f.Exit(node, Success)",
    "}",
    "func btNodePatrolWalk_Slot7(f *Frame) Status {",
    "\treturn Success",
    "}",
    "func btNodeOtherWalk(f *Frame) Status {",
    "\treturn Success",
    "}",
    "func btNodeWrongName(f *Frame) Status {",
    "\treturn Success",
    "}",
    "func helper() {",
    "}",
  ].join("\n");
  const locations = [
    { treeId: "patrol", nodeId: "walk", index: 3, line: 2, functionName: "btNodePatrolWalk" },
    { treeId: "patrol", nodeId: "walk", index: 7, line: 6, functionName: "btNodePatrolWalk_Slot7" },
    { treeId: "other", nodeId: "walk", index: 8, line: 9, functionName: "btNodeOtherWalk" },
    { treeId: "wrong", nodeId: "name", index: 9, line: 12, functionName: "btNodeOther" },
    { treeId: "wrong", nodeId: "line", index: 10, line: 15, functionName: "btNodePatrolWalk" },
  ];
  const index = createSourceIndex(source, locations);
  assert.deepEqual(index.byNode.get(sourceNodeKey("patrol", "walk")), locations.slice(0, 2));
  assert.deepEqual(index.byNode.get(sourceNodeKey("other", "walk")), [locations[2]]);
  assert.equal(index.byLine.get(3)?.index, 3);
  assert.equal(index.byLine.get(7)?.index, 7);
  assert.equal(index.byLine.get(10)?.index, 8);
  for (const line of [1, 12, 13, 14, 15, 16]) assert.equal(index.byLine.has(line), false);
  assert.equal(index.byNode.has(sourceNodeKey("wrong", "name")), false);
  assert.equal(index.byNode.has(sourceNodeKey("wrong", "line")), false);
});

// 模拟不完整 JSON 响应，缺失或空函数名不得根据整数槽位推测源码身份。
test("源码映射必须明确提供函数名", () => {
  const source = "func btNode3(f *Frame) Status {\n\treturn Success\n}";
  for (const json of [
    '[{"treeId":"main","nodeId":"walk","index":3,"line":1}]',
    '[{"treeId":"main","nodeId":"walk","index":3,"line":1,"functionName":""}]',
  ]) {
    const index = createSourceIndex(source, JSON.parse(json));
    assert.equal(index.byNode.size, 0);
    assert.equal(index.byLine.size, 0);
  }
});

// 用迟到响应、连续请求和工程切换复现竞争；单独修改布局仍可接收正确源码。
test("生成请求淘汰旧响应且布局调整不使当前请求失效", () => {
  const requests = new GenerationRequests();
  const project = emptyProject();
  const first = requests.begin(project);
  project.trees[0]!.layout = {};
  assert.equal(requests.accepts(first, project), true);
  const second = requests.begin(project);
  assert.equal(requests.accepts(first, project), false);
  assert.equal(requests.acceptsRevision(first), false);
  assert.equal(requests.acceptsRevision(second), true);
  project.trees[0]!.nodes[0]!.name = "已编辑";
  assert.equal(requests.accepts(second, project), false);
  requests.invalidate();
  assert.equal(requests.acceptsRevision(second), false);
  const third = requests.begin(project);
  requests.invalidate();
  assert.equal(requests.accepts(third, project), false);
});
