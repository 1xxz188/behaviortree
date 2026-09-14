import test from "node:test";
import assert from "node:assert/strict";
import {
  autoLayout,
  clone,
  connect,
  emptyProject,
  removeNodes,
} from "./project.ts";
import { parseInput, parseJSON, stringifyJSON } from "./json.ts";
import {
  nodeTypes, valueTypes, definitionKinds, parseNodeType, parseValueType,
  parseDefinitionKind, validateProjectTypes, validateCatalogTypes,
} from "./enums.ts";

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
