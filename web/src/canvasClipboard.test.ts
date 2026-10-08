import assert from "node:assert/strict";
import test from "node:test";
import { decodeCanvasSelection, encodeCanvasSelection } from "./canvasClipboard.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import type { CanvasClipboardEntry } from "./canvasClipboard.ts";
import type { BTNode } from "./project.ts";

// 框选复制仅保留组内连线，节点位置相对选区左上角归一，输入仍保持原样。
test("批量复制保留相对位置和选区内部连线", () => {
  const entries: CanvasClipboardEntry[] = [
    { node: { id: "parent", type: "sequence", children: ["inside", "outside"] }, position: { x: 210, y: -30 } },
    { node: { id: "inside", type: "action", binding: "attack" }, position: { x: 110, y: 70 } },
  ];
  const text = encodeCanvasSelection(entries, "parent");
  const payload = decodeCanvasSelection(text!);
  assert.deepEqual(payload?.nodes.map(entry => entry.position), [{ x: 100, y: 0 }, { x: 0, y: 100 }]);
  assert.deepEqual(payload?.nodes[0]?.node.children, ["inside"]);
  assert.equal(payload?.root, "parent");
  assert.deepEqual(entries[0]!.node.children, ["inside", "outside"]);
  assert.equal(decodeCanvasSelection(encodeCanvasSelection(entries, "outside")!)?.root, undefined);
});

// 单节点复制切断外部连线，解码后的快照不与输入节点共享对象。
test("单节点复制生成独立快照", () => {
  const entry: CanvasClipboardEntry = {
    node: { id: "one", type: "sequence", children: ["other"], params: { Count: { value: 3 } } },
    position: { x: -25, y: 40 },
  };
  const payload = decodeCanvasSelection(encodeCanvasSelection([entry])!);
  assert.deepEqual(payload?.nodes[0]?.position, { x: 0, y: 0 });
  assert.deepEqual(payload?.nodes[0]?.node.children, []);
  payload!.nodes[0]!.node.params!.Count!.value = 4;
  assert.equal(entry.node.params!.Count!.value, 3);
});

// 模式配置需要随复制粘贴完整保留，外部载荷不能给其他节点开启或伪造开关类型。
test("复制粘贴保留并校验优先级完成后重选", () => {
  const node: BTNode = { id: "one", type: "priority", reselectOnCompletion: true };
  const payload = decodeCanvasSelection(encodeCanvasSelection([{ node, position: { x: 0, y: 0 } }])!);
  assert.equal(payload?.nodes[0]?.node.reselectOnCompletion, true);
  for (const candidate of [{ ...node, type: "sequence" }, { ...node, reselectOnCompletion: "true" }]) {
    assert.equal(decodeCanvasSelection(stringifyJSON({ kind: "behaviortree/nodes", version: 1,
      nodes: [{ node: candidate, position: { x: 0, y: 0 } }],
    })), undefined);
  }
});

// 无损 JSON 往返必须保留参数里的 uint64，不能经原生 JSON 转为不精确的 number。
test("复制粘贴保留 64 位整数参数", () => {
  const large = parseJSON("18446744073709551615");
  const text = encodeCanvasSelection([{
    node: { id: "one", type: "action", params: { Target: { value: large } } },
    position: { x: 5, y: 9 },
  }]);
  const payload = decodeCanvasSelection(text!);
  assert.equal(stringifyJSON(payload?.nodes[0]?.node.params?.Target?.value), "18446744073709551615");
});

// repeat/retry 的 count 可以是超过 JS 安全整数范围的 Go int64，剪贴板往返不丢精度。
test("复制粘贴保留超大循环次数", () => {
  const node = parseJSON<BTNode>('{"id":"loop","type":"repeat","count":9007199254740993}');
  const text = encodeCanvasSelection([{ node, position: { x: 0, y: 0 } }]);
  const payload = decodeCanvasSelection(text!);
  assert.equal(stringifyJSON(payload?.nodes[0]?.node.count), "9007199254740993");
  assert.equal(decodeCanvasSelection(stringifyJSON({
    kind: "behaviortree/nodes", version: 1,
    nodes: [{ node: { id: "loop", type: "retry", count: parseJSON("9223372036854775808") }, position: { x: 0, y: 0 } }],
  })), undefined);
});

// 无效标记、重复 ID、非法坐标和外部连线均不能修改画布状态。
test("拒绝无效或非本功能的剪贴板文本", () => {
  const valid = { kind: "behaviortree/nodes", version: 1, nodes: [
    { node: { id: "one", type: "sequence" }, position: { x: 0, y: 0 } },
  ] };
  assert.equal(decodeCanvasSelection("普通文本"), undefined);
  assert.equal(decodeCanvasSelection("{"), undefined);
  for (const candidate of [
    { ...valid, kind: "other" },
    { ...valid, version: 2 },
    { ...valid, nodes: [] },
    { ...valid, nodes: [...valid.nodes, valid.nodes[0]] },
    { ...valid, nodes: [{ node: { id: "one", type: "unknown" }, position: { x: 0, y: 0 } }] },
    { ...valid, nodes: [{ node: { id: "one", type: "sequence" }, position: { x: "0", y: 0 } }] },
    { ...valid, nodes: [{ node: { id: "one", type: "sequence", children: [0] }, position: { x: 0, y: 0 } }] },
    { ...valid, nodes: [{ node: { id: "one", type: "sequence", children: ["outside"] }, position: { x: 0, y: 0 } }] },
    { ...valid, root: "outside" },
  ]) assert.equal(decodeCanvasSelection(stringifyJSON(candidate)), undefined);
  assert.equal(encodeCanvasSelection([]), undefined);
});

// 外部 JSON 即使标记正确，也不能通过伪造代码名、文本字段或参数对象进入批量变更。
test("拒绝伪造的节点字段", () => {
  for (const node of [
    { id: "one", type: "action", codeName: "1Invalid" },
    { id: "one", type: "action", codeName: { nested: true } },
    { id: "one", type: "action", binding: { nested: true } },
    { id: "one", type: "action", params: [] },
    { id: "one", type: "action", params: { Target: null } },
    { id: "one", type: "action", params: { Target: { field: 42 } } },
    { id: "one", type: "repeat", count: 1.5 },
    { id: "one", type: "repeat", count: { isLosslessNumber: true, value: "9007199254740993" } },
    { id: "one", type: "wait", durationMs: 1.5 },
  ]) {
    assert.equal(decodeCanvasSelection(stringifyJSON({
      kind: "behaviortree/nodes", version: 1,
      nodes: [{ node, position: { x: 0, y: 0 } }],
    })), undefined);
  }
});
