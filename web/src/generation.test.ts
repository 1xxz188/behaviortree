import assert from "node:assert/strict";
import test from "node:test";
import { createGeneratedSourceIndex, selectGeneratedFile, sourceNodeKey } from "./generation.ts";
import type { GeneratedFile, SourceLocation } from "./generation.ts";

// 不同文件可使用相同行号，同一源节点的多次展开只能属于其定义树文件。
test("多文件源码索引隔离同名节点和行号，保留全部展开位置", () => {
  const files: GeneratedFile[] = [
    { name: "glue.gen.go", treeId: "", source: "package behavior\nfunc NewProgram() {\n}\n" },
    { name: "tree_a.gen.go", treeId: "a", source: "package behavior\nfunc btNodeA() {\n}\n" },
    { name: "tree_b.gen.go", treeId: "b", source: "package behavior\nfunc btNodeB() {\n}\nfunc btNodeBViaA() {\n}\n" },
  ];
  const locations: SourceLocation[] = [
    { file: "tree_a.gen.go", treeId: "a", nodeId: "shared", index: 0, line: 2, functionName: "btNodeA" },
    { file: "tree_b.gen.go", treeId: "b", nodeId: "shared", index: 1, line: 2, functionName: "btNodeB" },
    { file: "tree_b.gen.go", treeId: "b", nodeId: "shared", index: 2, line: 4, functionName: "btNodeBViaA" },
  ];
  const index = createGeneratedSourceIndex(files, locations);
  assert.equal(index.filesByName.size, 3);
  assert.equal(index.fileByTreeID.size, 2);
  assert.equal(index.fileByTreeID.has(""), false);
  assert.deepEqual(index.locationsByFile.get("glue.gen.go"), []);
  assert.deepEqual(index.locationsByFile.get("tree_b.gen.go"), locations.slice(1));
  const a = index.sourceIndexes.get("tree_a.gen.go")!;
  const b = index.sourceIndexes.get("tree_b.gen.go")!;
  assert.equal(a.byLine.get(2), locations[0]);
  assert.equal(b.byLine.get(2), locations[1]);
  assert.equal(a.byNode.has(sourceNodeKey("b", "shared")), false);
  assert.deepEqual(b.byNode.get(sourceNodeKey("b", "shared")), locations.slice(1));
  assert.deepEqual(b.lines, files[2]!.source.split("\n"));
  assert.equal(b.longestLineLength, "func btNodeBViaA() {".length);
});

// 文件选择结果同时用于显示、复制和下载，节点联动必须选择定义树且复用已建索引。
test("文件切换和节点联动选择正确源码与下载名称", () => {
  const files: GeneratedFile[] = [
    { name: "glue.gen.go", treeId: "", source: "package behavior\n" },
    { name: "tree_中文_id.gen.go", treeId: "中文/树", source: "package behavior\nfunc btNode() {\n}\n" },
  ];
  const index = createGeneratedSourceIndex(files, []);
  const cachedIndex = index.sourceIndexes.get(files[1]!.name);
  assert.equal(selectGeneratedFile(index, ""), files[0]);
  assert.equal(selectGeneratedFile(index, "glue.gen.go", "中文/树"), files[1]);
  assert.equal(selectGeneratedFile(index, files[1]!.name)?.name, "tree_中文_id.gen.go");
  assert.equal(selectGeneratedFile(index, files[1]!.name)?.source, files[1]!.source);
  assert.equal(selectGeneratedFile(index, "glue.gen.go"), files[0]);
  assert.equal(selectGeneratedFile(index, "removed.gen.go", "removed-tree"), files[0]);
  assert.equal(index.sourceIndexes.get(files[1]!.name), cachedIndex);
  assert.equal(selectGeneratedFile(createGeneratedSourceIndex([], []), ""), undefined);
});

// 缺失文件、错误定义树与公共 glue 映射都不应把行号链接到无关节点。
test("多文件索引拒绝跨文件错误映射与重复文件身份", () => {
  const file: GeneratedFile = { name: "tree_a.gen.go", treeId: "a", source: "func btNode() {\n}\n" };
  const location: SourceLocation = { file: file.name, treeId: "a", nodeId: "node", index: 0, line: 1, functionName: "btNode" };
  const index = createGeneratedSourceIndex([file, { ...file, name: "glue.gen.go", treeId: "" }], [
    { ...location, file: "missing.gen.go" },
    { ...location, treeId: "b" },
    { ...location, file: "glue.gen.go" },
    { ...location, functionName: "wrong" },
  ]);
  assert.equal(index.sourceIndexes.get(file.name)!.byNode.size, 0);
  assert.equal(index.sourceIndexes.get("glue.gen.go")!.byLine.size, 0);
  assert.throws(() => createGeneratedSourceIndex([file, file], []), /生成文件名重复/);
  assert.throws(() => createGeneratedSourceIndex([file, { ...file, name: "other.gen.go" }], []), /定义树生成文件重复/);
});
