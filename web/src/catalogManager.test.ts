import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref, shallowReactive } from "vue";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import { blankProject } from "./project.ts";
import type { Definition } from "./project.ts";

// 提取真实组件筛选逻辑，避免测试另写一份筛选实现。
const source = readFileSync(new URL("./CatalogManager.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("CatalogManager.ts", source, ts.ScriptTarget.Latest, true);
const declaration = script.statements.find(statement => ts.isVariableStatement(statement)
  && statement.declarationList.declarations.some(item => ts.isIdentifier(item.name) && item.name.text === "filteredDefinitions"))!;
const js = ts.transpileModule(declaration.getText(script), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 构建跨目录与不同种类的数据，以同一响应式上下文验证组合筛选和缓存失效。
function manager() {
  const project = blankProject();
  project.catalog = [
    { id: "walk", name: "移动到目标", kind: "action", goName: "MoveTo" },
    { id: "ready", name: "移动条件", kind: "condition", goName: "IsReady" },
    { id: "nested", name: "移动子目录", kind: "action", goName: "NestedMove" },
  ];
  const index = new CatalogOrganizationIndex(project);
  index.addFolder("子目录", "", "child");
  index.move({ kind: "definition", id: "nested" }, "child");
  const props = shallowReactive({ index, catalog: project.catalog, revision: 0 });
  const context = { props, computed, query: ref(""), kindFilter: ref(""), selectedFolder: ref("") };
  const result = runInNewContext(`${js}\nfilteredDefinitions`, context) as { value: Definition[] };
  return { ...context, result, ids: () => Array.from(result.value, item => item.id) };
}

// 根目录只包含直属定义，名称、ID、Go 函数与种类筛选可以组合。
test("管理定义列表按直属目录、关键词和种类组合筛选", () => {
  const state = manager();
  assert.deepEqual(state.ids(), ["walk", "ready"]);
  state.query.value = "移动";
  state.kindFilter.value = "condition";
  assert.deepEqual(state.ids(), ["ready"]);
  state.kindFilter.value = "";
  state.query.value = "  MOVETO  ";
  assert.deepEqual(state.ids(), ["walk"]);
  state.query.value = "ready";
  assert.deepEqual(state.ids(), ["ready"]);
  state.selectedFolder.value = "child";
  state.query.value = "";
  assert.deepEqual(state.ids(), ["nested"]);
});

// 未变更输入复用 computed 结果，分类事务修订后才重新计算；失效目录安全回退。
test("管理定义筛选复用缓存并响应移动和失效目录", () => {
  const state = manager();
  const previous = state.result.value;
  assert.equal(state.result.value, previous);
  state.props.index.move({ kind: "definition", id: "walk" }, "child");
  state.props.revision++;
  assert.deepEqual(state.ids(), ["ready"]);
  assert.notEqual(state.result.value, previous);
  state.selectedFolder.value = "deleted-folder";
  assert.deepEqual(state.ids(), ["ready"]);
  state.query.value = "没有结果";
  assert.deepEqual(state.ids(), []);
});

