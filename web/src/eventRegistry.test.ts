import assert from "node:assert/strict";
import test from "node:test";
import { allocateEventID, EventRegistryIndex, filterEventRows, normalizeEventDescription, removeEvent, validateEventRegistry, validateNextEventID } from "./eventRegistry.ts";
import { validateProjectTypes } from "./enums.ts";
import { semanticSignature } from "./generation.ts";
import { blankProject } from "./project.ts";
import type { EventDefinition, Project } from "./project.ts";

const first: EventDefinition = { id: "1", name: "命令变化", codeName: "CommandChanged", description: "更新命令后通知" };
const second: EventDefinition = { id: "2", name: "战斗变化", codeName: "CombatChanged" };

// 持久游标从一递增，删除最大成员后仍不得回退或复用身份。
test("事件 ID 从一递增且删除后不复用", () => {
  const initial = allocateEventID("1");
  assert.deepEqual(initial, { id: "1", nextEventId: "2" });
  assert.deepEqual(allocateEventID(initial.nextEventId), { id: "2", nextEventId: "3" });
  const project = blankProject();
  project.events = [first, second];
  project.nextEventId = "3";
  const afterDelete = removeEvent(project, second.id);
  assert.equal(afterDelete.nextEventId, "3");
  assert.deepEqual(allocateEventID(afterDelete.nextEventId), { id: "3", nextEventId: "4" });
});

// uint64 上界按 BigInt 无损分配，最后一个 ID 后持久化耗尽哨兵。
test("事件 ID 支持 uint64 上界并拒绝耗尽游标", () => {
  const last = allocateEventID("18446744073709551615");
  assert.deepEqual(last, { id: "18446744073709551615", nextEventId: "18446744073709551616" });
  assert.throws(() => allocateEventID(last.nextEventId), /已耗尽/);
  assert.doesNotThrow(() => validateNextEventID(last.nextEventId, [{ ...first, id: last.id }]));
  assert.throws(() => validateNextEventID("2", [first, second]), /必须大于/);
});

// ID、成员结构和定义引用必须在前端输入边界严格检查。
test("事件注册表拒绝非法身份、重复成员和悬空依赖", () => {
  for (const id of [0, "0", "01", "18446744073709551616", "0xffffffffffffffff", null]) {
    assert.throws(() => validateEventRegistry([{ ...first, id }], []), /events\[0\].id/);
  }
  assert.throws(() => validateEventRegistry([first, { ...first }], []), /重复事件 ID/);
  assert.throws(() => validateEventRegistry([first, { ...second, codeName: "commandchanged" }], []), /codeName/);
  assert.throws(() => validateEventRegistry([first], [{ id: "A", eventIds: [first.id, first.id] }]), /重复事件 ID/);
  assert.throws(() => validateEventRegistry([first], [{ id: "A", eventIds: [second.id] }]), /未定义事件 ID/);
  assert.throws(() => validateEventRegistry([first], [{ id: "A", events: ["command"] }]), /旧字符串事件/);
  assert.throws(() => validateEventRegistry([first], [{ id: "A", eventIds: null }]), /eventIds: 应为数组/);
  assert.throws(() => validateProjectTypes({ ...blankProject(), schemaVersion: 2 }), /版本 4/);
  assert.throws(() => validateProjectTypes({ ...blankProject(), events: null }), /events/);
});

// 中文多行注释按 UTF-8 字节计数，保留段落而统一换行。
test("两层注释规范化、类型和上限使用同一规则", () => {
  assert.equal(normalizeEventDescription("  段落\r\n  第二行  \r", "description"), "  段落\n  第二行  \n");
  assert.equal(normalizeEventDescription(" \t\r\n ", "description"), "");
  assert.throws(() => normalizeEventDescription(null, "eventEnumDescription"), /eventEnumDescription/);
  assert.throws(() => normalizeEventDescription("a\0b", "description"), /NUL/);
  assert.throws(() => normalizeEventDescription("中".repeat(5462), "description"), /16 KiB/);
  assert.doesNotThrow(() => normalizeEventDescription("中".repeat(5461), "description"));
  assert.throws(() => validateEventRegistry([{ ...first, description: 1 }], []), /events\[0\].description/);
});

// 不同树中相同 node ID 独立统计，未连接的草稿节点也在引用集合中。
test("引用索引匹配当前树直接节点并保留全工程直接引用计数", () => {
  const project = blankProject();
  project.events = [first, second];
  project.catalog = [{ id: "Check", name: "检查", kind: "condition", goName: "Check", eventIds: [first.id] },
    { id: "Unused", name: "未用", kind: "action", goName: "Unused", eventIds: [first.id] }];
  project.trees[0]!.nodes.push({ id: "same", type: "condition", binding: "Check" }, { id: "other", type: "action", binding: "Unused" });
  project.trees.push({ id: "second", name: "第二棵树", root: "", nodes: [{ id: "same", type: "condition", binding: "Check" }, { id: "parent", type: "priority" }] });
  const index = new EventRegistryIndex(project);
  assert.deepEqual([...index.nodes(first.id, project.trees[0]!.id)].sort(), ["other", "same"]);
  assert.deepEqual([...index.nodes(first.id, "second")], ["same"]);
  assert.equal(index.nodes(second.id, "second").size, 0);
  assert.deepEqual([...index.eventsInTree(project.trees[0]!.id)], [first.id]);
  assert.deepEqual([...index.eventsInTree("second")], [first.id]);
  assert.deepEqual(index.counts(first.id), { definitions: 2, trees: 2, nodes: 3 });
});

// 嵌套子树事件只列一次，却命中当前画布所有通向事件的第一层引用节点。
test("当前树包含递归子树事件并高亮重复引用和未连接草稿节点", () => {
  const project = blankProject();
  const third: EventDefinition = { id: "3", name: "交互变化", codeName: "InteractionChanged" };
  const fourth: EventDefinition = { id: "4", name: "巡逻变化", codeName: "PatrolChanged" };
  project.events = [first, second, third, fourth];
  project.catalog = [
    { id: "Direct", name: "直接", kind: "action", goName: "Direct", eventIds: [first.id, second.id] },
    { id: "Middle", name: "中层", kind: "condition", goName: "Middle", eventIds: [second.id] },
    { id: "Deep", name: "深层", kind: "action", goName: "Deep", eventIds: [third.id] },
    { id: "Other", name: "无关", kind: "action", goName: "Other", eventIds: [fourth.id] },
  ];
  const main = project.trees[0]!;
  main.nodes.push(
    { id: "draft", type: "action", binding: "Direct" },
    { id: "call-1", type: "subtree", tree: "middle" },
    { id: "call-2", type: "subtree", tree: "middle" },
  );
  project.trees.push(
    { id: "middle", name: "中层树", root: "root", nodes: [
      { id: "middle-direct", type: "condition", binding: "Middle" },
      { id: "call-deep", type: "subtree", tree: "deep" },
    ] },
    { id: "deep", name: "深层树", root: "leaf", nodes: [{ id: "leaf", type: "action", binding: "Deep" }] },
    { id: "unrelated", name: "无关树", root: "leaf", nodes: [{ id: "leaf", type: "action", binding: "Other" }] },
  );
  const index = new EventRegistryIndex(project);
  assert.deepEqual([...index.eventsInTree(main.id)].sort(), [first.id, second.id, third.id]);
  assert.deepEqual([...index.nodes(first.id, main.id)], ["draft"]);
  assert.deepEqual([...index.nodes(second.id, main.id)].sort(), ["call-1", "call-2", "draft"]);
  assert.deepEqual([...index.nodes(third.id, main.id)].sort(), ["call-1", "call-2"]);
  assert.deepEqual([...index.nodes(third.id, "middle")], ["call-deep"]);
  assert.deepEqual([...index.nodes(third.id, "deep")], ["leaf"]);
  assert.equal(index.nodes(fourth.id, main.id).size, 0);
  assert.deepEqual(filterEventRows(project.events, index.eventsInTree(main.id), "tree", "").map(item => item.id), ["1", "2", "3"]);
  assert.deepEqual(filterEventRows(project.events, index.eventsInTree(main.id), "tree", "interaction").map(item => item.id), ["3"]);
  assert.deepEqual(filterEventRows(project.events, index.eventsInTree(main.id), "all", "").map(item => item.id), ["1", "2", "3", "4"]);
  assert.deepEqual(index.counts(third.id), { definitions: 1, trees: 1, nodes: 1 });
});

// 缺失目标与循环引用可以存在于编辑草稿，索引必须安全终止并保留直接事件。
test("子树缺失目标及循环草稿不会阻塞事件索引", () => {
  const project = blankProject();
  project.events = [first, second];
  project.catalog = [
    { id: "A", name: "A", kind: "action", goName: "A", eventIds: [first.id] },
    { id: "B", name: "B", kind: "action", goName: "B", eventIds: [second.id] },
  ];
  const main = project.trees[0]!;
  main.nodes.push({ id: "a", type: "action", binding: "A" }, { id: "to-b", type: "subtree", tree: "b" }, { id: "missing", type: "subtree", tree: "missing" });
  project.trees.push({ id: "b", name: "B", root: "b", nodes: [{ id: "b", type: "action", binding: "B" }, { id: "to-a", type: "subtree", tree: main.id }] });
  const index = new EventRegistryIndex(project);
  assert.deepEqual([...index.eventsInTree(main.id)].sort(), [first.id, second.id]);
  assert.deepEqual([...index.nodes(second.id, main.id)], ["to-b"]);
  assert.equal(index.nodes(first.id, main.id).has("missing"), false);
  assert.equal(index.eventsInTree("missing").size, 0);
  assert.deepEqual([...index.eventsInTree("b")].sort(), [first.id, second.id]);
  // 交换树数组顺序不得改变循环草稿的可见事件。
  project.trees.reverse();
  const reordered = new EventRegistryIndex(project);
  assert.deepEqual([...reordered.eventsInTree(main.id)].sort(), [first.id, second.id]);
  assert.deepEqual([...reordered.eventsInTree("b")].sort(), [first.id, second.id]);
});

// 万层子树链使用迭代引用遍历，打开工程时不因 JavaScript 调用栈深度失败。
test("深层子树链仍可查询末端事件且构造不展开所有闭包", () => {
  const project = blankProject();
  project.events = [first];
  project.catalog = [{ id: "A", name: "A", kind: "action", goName: "A", eventIds: [first.id] }];
  project.trees[0]!.id = "t0";
  project.trees[0]!.nodes.push({ id: "next", type: "subtree", tree: "t1" });
  for (let i = 1; i < 10000; i++) {
    project.trees.push({ id: `t${i}`, name: `第 ${i} 层`, root: "node", nodes: [
      i === 9999 ? { id: "node", type: "action", binding: "A" } : { id: "node", type: "subtree", tree: `t${i + 1}` },
    ] });
  }
  const index = new EventRegistryIndex(project);
  assert.deepEqual([...index.eventsInTree("t0")], [first.id]);
  assert.deepEqual([...index.nodes(first.id, "t0")], ["next"]);
  assert.deepEqual([...index.nodes(first.id, "t9999")], ["node"]);
});

// 删除候选一次清理所有定义引用并保留树、节点、布局及整体注释。
test("删除事件全工程原子清理且源工程保持不变", () => {
  const project = blankProject();
  project.events = [first, second];
  project.eventEnumDescription = "整体功能说明";
  project.catalog = [{ id: "A", name: "A", kind: "action", goName: "A", eventIds: [first.id, second.id] },
    { id: "B", name: "B", kind: "condition", goName: "B", eventIds: [first.id] }];
  const candidate = removeEvent(project, first.id);
  assert.deepEqual(candidate.events.map(item => item.id), [second.id]);
  assert.deepEqual(candidate.catalog.map(item => item.eventIds), [[second.id], []]);
  assert.equal(candidate.eventEnumDescription, "整体功能说明");
  assert.deepEqual(project.catalog[0]!.eventIds, [first.id, second.id]);
  assert.equal(project.trees[0]!.nodes.length, candidate.trees[0]!.nodes.length);
  assert.throws(() => removeEvent(candidate, first.id), /已不存在/);
});

// 注释改变产物签名但不改变身份，缺省和空串签名等价。
test("双层注释进入语义签名且空串等价缺省", () => {
  const project: Project = blankProject();
  project.events = [{ ...first, description: undefined }];
  const base = semanticSignature(project);
  project.nextEventId = "2";
  assert.equal(semanticSignature(project), base);
  project.events[0]!.description = "";
  project.eventEnumDescription = "";
  assert.equal(semanticSignature(project), base);
  project.eventEnumDescription = "整体说明";
  const overall = semanticSignature(project);
  assert.notEqual(overall, base);
  project.events[0]!.description = "成员说明";
  assert.notEqual(semanticSignature(project), overall);
  assert.equal(project.events[0]!.id, first.id);
});
