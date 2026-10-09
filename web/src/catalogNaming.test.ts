import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { compile, computed, createSSRApp, nextTick, ref, shallowReactive, watch } from "vue";
import { renderToString } from "vue/server-renderer";
import { BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix } from "./businessNames.ts";
import { catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents } from "./catalog.ts";
import type { CatalogPackage } from "./catalog.ts";
import { normalizeEventDescription, validateEventRegistry } from "./eventRegistry.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";
import type { Definition } from "./project.ts";
import type { DefinitionKind } from "./enums.ts";

// 执行真实组件 setup、表单计算属性及提交入口，只替代生命周期和网络边界。
const source = readFileSync(new URL("./CatalogManager.vue", import.meta.url), "utf8");
const script = ts.createSourceFile("CatalogManager.ts", source.split('<script setup lang="ts">')[1]!.split("</script>")[0]!, ts.ScriptTarget.Latest, true);
const setup = script.statements.filter(statement => !ts.isImportDeclaration(statement)).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(setup, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const functionField = source.slice(source.indexOf("<label>业务实现函数"), source.indexOf("</label>", source.indexOf("<label>业务实现函数")) + "</label>".length);

// 每个测试使用独立工程与真实 Vue 响应式状态，捕获实际发出的目录提交。
function form(kind: DefinitionKind = "action", existing?: Definition) {
  const project = blankProject();
  if (existing) project.catalog = [existing];
  const props = shallowReactive({ project, catalog: project.catalog, projectRevision: 1,
    initialMode: existing ? "edit" : "create", initialKind: kind, initialDefinition: existing });
  const requests: CatalogPackage[] = [];
  const applied: Definition[][] = [];
  const context = {
    computed, nextTick, ref, watch, BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix,
    catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents,
    normalizeEventDescription, validateEventRegistry, parseJSON, stringifyJSON,
    defineProps: () => props, withDefaults: (value: unknown) => value, defineExpose: () => {},
    onMounted: () => {}, onUnmounted: () => {},
    defineEmits: () => (event: string, catalog: Definition[]) => { if (event === "apply") applied.push(catalog); },
    validateCatalog: async (value: CatalogPackage) => { requests.push(value); return value; },
  };
  const state = runInNewContext(`${js}\n({ draft, functionPrefix, functionSuffix, prefixedNaming, functionNameError, hasPending, busy, error, acknowledged, applyCatalog, changeMode, editDefinition });`, context) as {
    draft: { value: Definition }; // 表单独立草稿。
    functionPrefix: { readonly value: string }; // 固定前缀显示值。
    functionSuffix: { value: string }; // 实际输入框双向绑定值。
    prefixedNaming: { readonly value: boolean }; // 旧定义使用完整原名输入框。
    functionNameError: { readonly value: string | null }; // 规则错误提示。
    hasPending: { readonly value: boolean }; // 未应用草稿守卫。
    busy: { value: boolean }; // 表单提交状态。
    error: { value: string }; // 提交失败提示。
    acknowledged: { value: boolean }; // 既有定义更新确认。
    applyCatalog: () => Promise<void>; // 真实提交处理函数。
    changeMode: (mode: string, discard?: boolean) => boolean; // 真实工作页切换入口。
    editDefinition: (definition: Definition) => void; // 管理页的直接编辑入口。
  };
  const html = () => renderToString(createSSRApp({ render: compile(functionField), setup: () => state }));
  return { ...state, requests, applied, html };
}

// 默认前缀属于初始状态，动作和条件均由真实提交入口保存完整函数名与规则标记。
test("新建定义显示固定前缀并保存新规则", async () => {
  for (const [kind, prefix, suffix] of [["action", "Action", "MoveTo"], ["condition", "Is", "Ready"]] as const) {
    const state = form(kind);
    assert.equal(state.functionPrefix.value, prefix);
    assert.equal(state.draft.value.goName, prefix);
    assert.equal(state.functionSuffix.value, "");
    assert.equal(state.hasPending.value, false);
    assert.match(await state.html(), new RegExp(`aria-label="固定函数前缀">${prefix}</span>`));
    state.draft.value.id = "new_business";
    state.draft.value.name = "新业务";
    state.functionSuffix.value = suffix;
    await state.applyCatalog();
    assert.equal(state.requests.length, 1);
    assert.equal(state.applied[0]![0]!.goName, prefix + suffix);
    assert.equal(state.applied[0]![0]!.namingVersion, BUSINESS_NAMING_VERSION);
  }
});

// 切换种类保留后缀，取消后恢复默认前缀且不会把默认值误判为未应用草稿。
test("新定义切换种类只替换前缀，取消恢复基线", () => {
  const state = form();
  state.functionSuffix.value = "Ready";
  state.draft.value.kind = "condition";
  assert.equal(state.draft.value.goName, "IsReady");
  assert.equal(state.functionSuffix.value, "Ready");
  state.draft.value.kind = "action";
  assert.equal(state.draft.value.goName, "ActionReady");
  state.functionSuffix.value = "Actionable";
  assert.equal(state.draft.value.goName, "ActionActionable", "输入的后缀不能在键入 Action 时被吞掉");
  state.changeMode("create", true);
  assert.equal(state.draft.value.goName, "Action");
  assert.equal(state.hasPending.value, false);
});

// 仅填写前缀或直接伪造错误前缀都不能绕过提交处理函数，也不会发起重复校验 IO。
test("新定义未填后缀或前缀错误时阻止提交", async () => {
  const state = form();
  state.draft.value.id = "new_business";
  state.draft.value.name = "新业务";
  for (const name of ["Action", "MoveTo", "actionMoveTo", "IsReady"]) {
    state.draft.value.goName = name;
    await state.applyCatalog();
    assert.match(state.error.value, /Action/);
    assert.equal(state.requests.length, 0);
    assert.equal(state.applied.length, 0);
  }
});

// 旧定义的完整原名继续显示与保存，不加前缀标记，也不会因打开编辑窗口而产生改动。
test("历史业务定义编辑保存保持原名", async () => {
  const original: Definition = { id: "move", name: "移动", kind: "action", goName: "MoveTo" };
  const state = form("action", original);
  assert.equal(state.prefixedNaming.value, false);
  assert.equal(state.draft.value.goName, "MoveTo");
  assert.equal(state.hasPending.value, false);
  assert.match(await state.html(), /历史定义保留原函数名/);
  assert.doesNotMatch(await state.html(), /aria-label="固定函数前缀"/);
  state.acknowledged.value = true;
  await state.applyCatalog();
  assert.equal(state.applied[0]![0]!.goName, "MoveTo");
  assert.equal(state.applied[0]![0]!.namingVersion, undefined);
  assert.equal(original.goName, "MoveTo");
});

// 新规则定义重开编辑时仍锁定当前种类的前缀，后续改型也保存同一规则。
test("新规则条件定义重开后保持前缀约束", async () => {
  const original: Definition = { id: "ready", name: "就绪", kind: "condition", goName: "IsReady", namingVersion: 1 };
  const state = form("action", original);
  assert.equal(state.prefixedNaming.value, true);
  assert.equal(state.draft.value.goName, "IsReady");
  assert.equal(state.functionSuffix.value, "Ready");
  assert.equal(state.hasPending.value, false);
  state.functionSuffix.value = "Enabled";
  state.draft.value.kind = "action";
  state.acknowledged.value = true;
  await state.applyCatalog();
  assert.equal(state.applied[0]![0]!.goName, "ActionEnabled");
  assert.equal(state.applied[0]![0]!.namingVersion, 1);
  assert.equal(original.goName, "IsReady");
});
