import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { compile, computed, createSSRApp, ref } from "vue";
import { renderToString } from "vue/server-renderer";
import { NodeIdentityIndex } from "./nodeIdentity.ts";
import { blankProject, kinds } from "./project.ts";
import type { NodeType } from "./enums.ts";

// 编译真实画布模板并执行真实应用入口，避免只验证数据已修改而遗漏画布显示错误。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8");
const template = source.split('<template #node-behavior="{ data }">')[1]!.split("</template>")[0]!;
const render = compile(template);
const script = ts.createSourceFile("App.ts", source.split('<script setup lang="ts">')[1]!.split("</script>")[0]!, ts.ScriptTarget.Latest, true);
const handlers = script.statements.filter(statement =>
  ts.isFunctionDeclaration(statement) && ["applyCodeName", "cancelCodeName"].includes(statement.name?.text ?? "")
  || ts.isVariableStatement(statement) && statement.declarationList.declarations.some(
    declaration => ts.isIdentifier(declaration.name) && declaration.name.text === "graphNodes"),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 单个节点使用实际响应式索引和画布数据投影，模拟属性面板草稿、应用和取消。
function session(type: NodeType = "action", binding = "move_to") {
  const project = ref(blankProject());
  const tree = computed(() => project.value.trees[0]!);
  tree.value.nodes = [{ id: "3", type, name: "移动到目标", codeName: "OldMove", binding, durationMs: 100 }];
  tree.value.root = "3";
  const nodeIdentity = ref(new NodeIdentityIndex(tree.value));
  const selected = ref("3");
  const node = computed(() => nodeIdentity.value.byID.get(selected.value));
  const codeNameDraft = ref("OldMove");
  const codeNameError = ref("");
  const app = runInNewContext(`${js}\n({ graphNodes, applyCodeName, cancelCodeName });`, {
    computed, tree, selected, node, nodeIdentity, codeNameDraft, codeNameError, kinds,
    invalidNodes: ref(new Set()), mutate: (fn: () => void) => fn(), notice: () => {},
  }) as {
    graphNodes: { readonly value: { data: unknown }[] }; // 真实画布节点投影。
    applyCodeName: () => void; // 应用属性面板中的代码名草稿。
    cancelCodeName: () => void; // 取消未提交草稿。
  };
  // 端点与本次文本显示无关，以空组件替代，其他模板完整渲染。
  const html = () => renderToString(createSSRApp({
    render,
    components: { Handle: { render: () => null } },
    setup: () => ({ data: app.graphNodes.value[0]!.data, selected: "", Position: { Left: "left", Right: "right" } }),
  }));
  return { ...app, node, codeNameDraft, codeNameError, html };
}

// 应用后的动作代码名必须立刻可见，显示名称和业务绑定仍保持各自含义。
test("应用节点代码名后画布显示新代码名", async () => {
  const s = session();
  s.codeNameDraft.value = "MoveTo";
  s.applyCodeName();
  assert.equal(s.node.value!.codeName, "MoveTo");
  assert.match(await s.html(), />MoveTo</);
  assert.doesNotMatch(await s.html(), />move_to</);
  assert.equal(s.node.value!.binding, "move_to");
  assert.equal(s.node.value!.name, "移动到目标");
});

// 输入、取消或非法提交不能改变画布上已生效的代码名。
test("未应用和非法代码名草稿不改变画布显示", async () => {
  const s = session();
  s.codeNameDraft.value = "PendingMove";
  assert.match(await s.html(), />OldMove</);
  assert.doesNotMatch(await s.html(), /PendingMove/);
  s.cancelCodeName();
  assert.equal(s.codeNameDraft.value, "OldMove");
  s.codeNameDraft.value = "for";
  s.applyCodeName();
  assert.ok(s.codeNameError.value);
  assert.match(await s.html(), />OldMove</);
});

// 条件、等待和未绑定业务节点也显示代码名，同时保留时长和未绑定提示。
test("不同节点类型均显示代码名并保留辅助信息", async () => {
  for (const type of ["condition", "wait", "action"] as const) {
    const s = session(type, "");
    s.codeNameDraft.value = "NewCode";
    s.applyCodeName();
    const html = await s.html();
    assert.match(html, />NewCode</);
    if (type === "wait") assert.match(html, /100 ms/);
    else assert.match(html, /未绑定业务定义/);
  }
});
