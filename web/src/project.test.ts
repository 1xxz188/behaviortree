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
