import assert from "node:assert/strict";
import test from "node:test";
import { blankProject } from "./project.ts";
import { EventRegistryIndex } from "./eventRegistry.ts";
import { matchedEventNodes, pruneHighlightedEvents, toggleHighlightedEvent } from "./eventHighlight.ts";

// 多选取节点并集，两个事件同时引用同一节点时只计一次。
test("事件多选高亮合并当前树的节点且可逐项取消", () => {
  const project = blankProject();
  project.events = [{ id: "1", name: "移动", codeName: "Move" }, { id: "2", name: "攻击", codeName: "Attack" }, { id: "3", name: "空事件", codeName: "Empty" }];
  project.catalog = [
    { id: "A", name: "A", kind: "action", goName: "A", eventIds: ["1", "2"] },
    { id: "B", name: "B", kind: "condition", goName: "B", eventIds: ["2"] },
  ];
  const tree = project.trees[0]!;
  tree.nodes.push({ id: "shared", type: "action", binding: "A" }, { id: "only-second", type: "condition", binding: "B" });
  project.trees.push({ id: "other", name: "其他", root: "", nodes: [{ id: "other-node", type: "action", binding: "A" }] });
  const index = new EventRegistryIndex(project);
  const first = toggleHighlightedEvent(new Set<string>(), "1");
  assert.deepEqual([...matchedEventNodes(first, index, tree.id)], ["shared"]);
  const both = toggleHighlightedEvent(first, "2");
  assert.deepEqual([...matchedEventNodes(both, index, tree.id)].sort(), ["only-second", "shared"]);
  assert.deepEqual([...matchedEventNodes(both, index, "other")], ["other-node"]);
  assert.deepEqual([...matchedEventNodes(toggleHighlightedEvent(both, "1"), index, tree.id)].sort(), ["only-second", "shared"]);
  assert.equal(matchedEventNodes(new Set(["3"]), index, tree.id).size, 0);
  assert.equal(first.has("2"), false);
});

// 删除、撤销或替换事件表时仅移除失效勾选，保留仍存在的事件。
test("事件表变化只清理失效高亮", () => {
  const selected = new Set(["1", "2", "3"]);
  const available = new Map<string, unknown>([["1", {}], ["3", {}]]);
  assert.deepEqual([...pruneHighlightedEvents(selected, available)], ["1", "3"]);
  assert.deepEqual([...selected], ["1", "2", "3"]);
});
