import assert from "node:assert/strict";
import test from "node:test";
import { blankProject } from "./project.ts";
import { sourceNodeKey } from "./generation.ts";
import { createScaffoldSourceIndex, selectedScaffoldCode } from "./scaffoldNavigation.ts";

// 动作与条件按业务绑定定位，共享函数保留每个树节点的独立入口和相同高亮标识。
test("骨架索引支持动作、条件、共享绑定和跨树同名节点", () => {
  const project = blankProject();
  project.catalog = [
    { id: "run", name: "动作", kind: "action", goName: "Run" },
    { id: "ready", name: "条件", kind: "condition", goName: "Ready" },
  ];
  project.trees = [
    { id: "a", name: "树甲", root: "one", nodes: [
      { id: "one", type: "action", binding: "run" },
      { id: "two", type: "action", binding: "run" },
      { id: "check", type: "condition", binding: "ready" },
    ] },
    { id: "b", name: "树乙", root: "one", nodes: [{ id: "one", type: "condition", binding: "ready" }] },
  ];
  const source = "package behavior\n\nfunc Run() {\n\tswitch phase {\n\t}\n}\n\nfunc Ready() bool {\n\treturn false\n}\n";
  const index = createScaffoldSourceIndex(source, project);
  const first = index.byNode.get(sourceNodeKey("a", "one"))!;
  const shared = index.byNode.get(sourceNodeKey("a", "two"))!;
  const condition = index.byNode.get(sourceNodeKey("b", "one"))!;
  assert.equal(index.byNode.size, 4);
  assert.equal(first[0]!.line, 3);
  assert.equal(shared[0]!.index, first[0]!.index);
  assert.notEqual(shared, first);
  assert.equal(condition[0]!.line, 8);
  assert.equal(condition[0]!.functionName, "Ready");
  assert.equal(index.byLine.get(5)?.index, shared[0]!.index);
  assert.equal(index.byLine.get(6)?.nodeId, "one");
  assert.equal(index.byLine.get(7), undefined);
  assert.equal(index.byLine.get(9)?.index, condition[0]!.index);
  // 复制共用函数必须包含嵌套代码块及结束括号，不混入后续条件函数。
  assert.equal(selectedScaffoldCode(index, shared[0]!), "func Run() {\n\tswitch phase {\n\t}\n}");
  assert.equal(selectedScaffoldCode(index, condition[0]!), "func Ready() bool {\n\treturn false\n}");
});

// 无业务函数的节点不应误跳到辅助函数、注释中的伪声明或其他类型的业务函数。
test("骨架索引忽略组合节点、缺失绑定、类型不匹配及缺失函数", () => {
  const project = blankProject();
  project.catalog = [{ id: "run", name: "动作", kind: "action", goName: "Run" }];
  project.trees = [{ id: "a", name: "树", root: "root", nodes: [
    { id: "root", type: "sequence", binding: "run" },
    { id: "missing", type: "action", binding: "missing" },
    { id: "wrong", type: "condition", binding: "run" },
    { id: "run", type: "action", binding: "run" },
  ] }];
  const index = createScaffoldSourceIndex("// func Run() {\nfunc Other() {\n}\n", project);
  assert.equal(index.byNode.size, 0);
  assert.equal(index.byLine.size, 0);
});
