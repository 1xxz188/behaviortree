import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { compile, createSSRApp, ref } from "vue";
import { renderToString } from "vue/server-renderer";
import { validateProjectTypes } from "./enums.ts";
import { semanticSignature } from "./generation.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject, clone, kinds } from "./project.ts";
import type { BTNode, Project } from "./project.ts";

// 执行真实开关处理函数并渲染真实模板，验证属性变更能进入生成语义和画布。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8");
const script = ts.createSourceFile("App.ts", source.split('<script setup lang="ts">')[1]!.split("</script>")[0]!, ts.ScriptTarget.Latest, true);
const handler = script.statements.find(statement => ts.isFunctionDeclaration(statement) && statement.name?.text === "changePriorityCompletion")!.getText(script);
const js = ts.transpileModule(handler, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const canvasRender = compile(source.split('<template #node-behavior="{ data }">')[1]!.split("</template>")[0]!);
const inspectorRender = compile(source.split('<template v-if="node.type === \'priority\'">')[1]!.split("</template>")[0]!);

// 会话保留工程、语义和历史边界，以确认关闭开关恢复旧工程格式。
function session(type: BTNode["type"] = "priority") {
  const project = ref(blankProject());
  project.value.trees[0]!.nodes[0]!.type = type;
  const node = ref(project.value.trees[0]!.nodes[0]!);
  let mutations = 0;
  const change = runInNewContext(`${js}\nchangePriorityCompletion;`, {
    node, mutate: (fn: () => void) => { fn(); mutations++; },
  }) as (event: unknown) => void;
  const html = () => renderToString(createSSRApp({
    render: canvasRender, components: { Handle: { render: () => null } },
    setup: () => ({ data: { node: node.value, kind: kinds[type] }, selected: "", Position: { Left: "left", Right: "right" } }),
  }));
  const inspector = () => renderToString(createSSRApp({ render: inspectorRender, setup: () => ({ node: node.value, changePriorityCompletion: change }) }));
  return { project, node, change, html, inspector, mutations: () => mutations };
}

// 开关必须改变生成语义、显示选中状态和画布提示，重复输入不产生冗余历史。
test("完成后重选开关进入历史、工程快照和画布", async () => {
  const s = session();
  const original = semanticSignature(s.project.value);
  assert.doesNotMatch(await s.html(), /完成后重选/);
  s.change({ target: { checked: true } });
  assert.equal(s.node.value.reselectOnCompletion, true);
  assert.notEqual(semanticSignature(s.project.value), original);
  assert.match(await s.html(), /完成后重选/);
  assert.match(await s.inspector(), /type="checkbox"[^>]*checked/);
  assert.equal(clone(parseJSON<Project>(stringifyJSON(s.project.value))).trees[0]!.nodes[0]!.reselectOnCompletion, true);
  s.change({ target: { checked: true } });
  assert.equal(s.mutations(), 1);
  s.change({ target: { checked: false } });
  assert.equal(Object.hasOwn(s.node.value, "reselectOnCompletion"), false);
  assert.equal(semanticSignature(s.project.value), original);
  assert.doesNotMatch(await s.html(), /完成后重选/);
  assert.equal(s.mutations(), 2);
});

// 非 Priority 不可通过处理函数开启该配置，导入边界也拒绝错误类型和错误节点。
test("完成后重选只接受 Priority 的布尔开关", () => {
  const other = session("sequence");
  other.change({ target: { checked: true } });
  assert.equal(other.mutations(), 0);
  for (const value of ["true", 1, null, [], {}]) {
    const project = blankProject();
    const candidate = { ...project.trees[0]!.nodes[0]!, type: "priority", reselectOnCompletion: value };
    const invalid = { ...project, trees: [{ ...project.trees[0]!, nodes: [candidate] }] };
    assert.throws(() => validateProjectTypes(invalid), /reselectOnCompletion/);
  }
  other.node.value.reselectOnCompletion = true;
  assert.throws(() => validateProjectTypes(other.project.value), /仅适用于 priority/);
  const priority = session();
  priority.change({ target: { checked: true } });
  assert.doesNotThrow(() => validateProjectTypes(priority.project.value));
});
