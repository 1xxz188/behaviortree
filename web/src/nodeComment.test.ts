import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref } from "vue";
import { nodeTypes, validateProjectTypes } from "./enums.ts";
import { GenerationRequests, semanticSignature } from "./generation.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { NodeIdentityIndex } from "./nodeIdentity.ts";
import { blankProject, clone, kinds } from "./project.ts";
import type { Project } from "./project.ts";
import { TreeIdentityIndex } from "./treeIdentity.ts";

// 全部节点种类都允许省略或填写注释，Schema 4 无需补写节点注释属性。
test("所有节点允许可选字符串注释", () => {
  const project = blankProject();
  project.trees[0]!.nodes = nodeTypes.map((type, index) => ({ id: `node_${index}`, type }));
  const before = stringifyJSON(project);
  assert.doesNotThrow(() => validateProjectTypes(parseJSON(before)));
  assert.equal(stringifyJSON(project), before);
  assert.equal(project.schemaVersion, 4);
  for (const node of project.trees[0]!.nodes) {
    assert.equal(Object.hasOwn(node, "comment"), false);
    for (const comment of ["", "中文说明\n保留内部换行", "<b>纯文本</b> & // /* */"]) {
      node.comment = comment;
      assert.doesNotThrow(() => validateProjectTypes(parseJSON(stringifyJSON(project))));
    }
  }
});

// 校验真实 JSON 输入边界，不能依靠静态类型或仅检查第一棵树的第一个节点。
test("非字符串节点注释被拒绝并指出完整字段位置", () => {
  for (const comment of [null, false, true, 0, 123, {}, [], ["说明"]]) {
    const input = { schemaVersion: 4, nextEventId: "1", events: [], catalog: [], trees: [
      { nodes: [{ id: "old", type: "wait" }] },
      { nodes: [{ id: "valid", type: "sequence" }, { id: "invalid", type: "action", comment }] },
    ] };
    assert.throws(() => validateProjectTypes(parseJSON(stringifyJSON(input))), /trees\[1\].nodes\[1\].comment.*字符串/);
  }
});

// 保存、导出及撤销使用同一无损 JSON 路径，注释不应破坏多行内容或大整数参数。
test("节点注释在克隆和工程 JSON 往返中无损保留", () => {
  const project = blankProject();
  const node = project.trees[0]!.nodes[0]!;
  node.comment = "中文注释\n\n  缩进与末尾空格  \n<script>alert(1)</script> & \"引号\" \\";
  node.params = { Target: { value: parseJSON("18446744073709551615") } };
  const before = stringifyJSON(project);
  const snapshot = clone(project);
  const reopened = parseJSON<Project>(stringifyJSON(snapshot));
  assert.doesNotThrow(() => validateProjectTypes(reopened));
  assert.equal(stringifyJSON(reopened), before);
  assert.equal(reopened.trees[0]!.nodes[0]!.comment, node.comment);
  assert.equal(stringifyJSON(reopened.trees[0]!.nodes[0]!.params!.Target!.value), "18446744073709551615");
  reopened.trees[0]!.nodes[0]!.comment = "已修改";
  assert.equal(stringifyJSON(snapshot), before);
  assert.equal(stringifyJSON(project), before);
});

// Go 保存会省略空注释；签名归一化不能反过来给原工程补写空属性。
test("缺省与空节点注释签名等价且不会修改工程", () => {
  const project = blankProject();
  const before = stringifyJSON(project);
  const baseline = semanticSignature(project);
  const explicitEmpty = clone(project);
  for (const node of explicitEmpty.trees[0]!.nodes) node.comment = "";
  assert.equal(semanticSignature(explicitEmpty), baseline);
  assert.equal(stringifyJSON(project), before);
  const requests = new GenerationRequests();
  const pending = requests.begin(explicitEmpty);
  assert.equal(requests.accepts(pending, project), true);
});

// 填写、修改及清空均改变生成结果，迟到响应不能覆盖这些编辑后的预览。
test("节点注释填写修改清空使原生成请求过期", () => {
  const project = blankProject();
  const node = project.trees[0]!.nodes[0]!;
  const requests = new GenerationRequests();
  const baseline = semanticSignature(project);
  for (const comment of ["首次注释", "修改后\n第二行", ""]) {
    const pending = requests.begin(project);
    node.comment = comment;
    assert.notEqual(semanticSignature(project), pending.signature);
    assert.equal(requests.accepts(pending, project), false);
    assert.equal(requests.accepts(requests.begin(project), project), true);
  }
  delete node.comment;
  assert.equal(semanticSignature(project), baseline);
});

// 调用实际复制入口及身份索引，验证副本保留注释且编辑副本不会改动源节点。
test("实际复制节点保留多行注释并保持独立数据", () => {
  const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
    .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
  const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
  const declaration = script.statements.find(statement => ts.isFunctionDeclaration(statement) && statement.name?.text === "duplicate");
  assert.ok(declaration, "应复用实际复制函数");
  const js = ts.transpileModule(declaration.getText(script), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const project = ref(blankProject());
  const tree = computed(() => project.value.trees[0]!);
  const nodeIdentity = { value: new NodeIdentityIndex(tree.value) };
  const treeIdentity = { value: new TreeIdentityIndex(project.value) };
  const original = tree.value.nodes[0]!;
  original.comment = "源节点说明\n复制后仍然保留";
  const selected = ref(original.id);
  const node = computed(() => nodeIdentity.value.byID.get(selected.value));
  let mutations = 0;
  const duplicate = runInNewContext(`${js}\nduplicate;`, {
    clone, kinds, node, tree, selected, nodeIdentity, treeIdentity,
    mutate: (change: () => void) => { mutations++; change(); },
  }) as () => void;
  duplicate();
  const copied = nodeIdentity.value.byID.get(selected.value)!;
  assert.equal(mutations, 1);
  assert.notEqual(copied.id, original.id);
  assert.notEqual(copied, original);
  assert.equal(copied.comment, original.comment);
  copied.comment = "副本说明";
  assert.equal(original.comment, "源节点说明\n复制后仍然保留");
});
