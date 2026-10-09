import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { compile, computed, createSSRApp, nextTick, ref, shallowReactive, shallowRef, watch } from "vue";
import { renderToString } from "vue/server-renderer";
import { BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix } from "./businessNames.ts";
import { catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents } from "./catalog.ts";
import type { CatalogPackage } from "./catalog.ts";
import { normalizeEventDescription, validateEventRegistry } from "./eventRegistry.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";
import type { Definition } from "./project.ts";
import type { DefinitionKind } from "./enums.ts";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";

// 执行真实组件 setup、表单计算属性及提交入口，只替代生命周期和网络边界。
const source = readFileSync(new URL("./CatalogManager.vue", import.meta.url), "utf8");
const script = ts.createSourceFile("CatalogManager.ts", source.split('<script setup lang="ts">')[1]!.split("</script>")[0]!, ts.ScriptTarget.Latest, true);
const setup = script.statements.filter(statement => !ts.isImportDeclaration(statement)).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(setup, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const functionField = source.slice(source.indexOf("<label>业务实现函数"), source.indexOf("</label>", source.indexOf("<label>业务实现函数")) + "</label>".length);
const editorControls = source.slice(source.indexOf("<header>"), source.indexOf("</header>") + "</header>".length)
  + source.slice(source.indexOf("<footer>"), source.indexOf("</footer>") + "</footer>".length); // 使用真实标题及底部按钮，覆盖入口显示条件。

// 每个测试使用独立工程与真实 Vue 响应式状态，捕获实际发出的目录提交。
function form(kind: DefinitionKind = "action", existing?: Definition) {
  const project = blankProject();
  if (existing) project.catalog = [existing];
  const failCommit = ref(false); // 模拟分类事务失败，确保不推进草稿修订。
  const props = shallowReactive({ project, catalog: project.catalog, projectRevision: 1,
    initialMode: existing ? "edit" : "create", initialKind: kind, initialDefinition: existing,
    index: new CatalogOrganizationIndex(project), disabled: false,
    commit: (change: () => void): boolean => {
      if (failCommit.value) return false;
      change(); props.projectRevision++; return true;
    } });
  const requests: CatalogPackage[] = [];
  const applied: Definition[][] = [];
  const emissions: { name: string; args: unknown[] }[] = []; // 捕获删除请求的原对象和修订，不代替实际工程删除。
  const context = {
    computed, nextTick, ref, shallowRef, watch, BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix,
    catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents,
    normalizeEventDescription, validateEventRegistry, parseJSON, stringifyJSON,
    defineProps: () => props, withDefaults: (value: unknown) => value, defineExpose: () => {},
    onMounted: () => {}, onUnmounted: () => {},
    defineEmits: () => (event: string, ...args: unknown[]) => {
      if (event === "apply") applied.push(args[0] as Definition[]);
      else emissions.push({ name: event, args });
    },
    validateCatalog: async (value: CatalogPackage) => { requests.push(value); return value; },
  };
  const state = runInNewContext(`${js}\n({ draft, functionPrefix, functionSuffix, prefixedNaming, functionNameError, hasPending, busy, error, acknowledged, applyCatalog, changeMode, editDefinition, mode, dialogTitle, draftRevision, organizationBrowser, pendingWarning, commitOrganization, setEditingTags, requestDeleteDefinition });`, context) as {
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
    mode: { value: string }; // 渲染实际编辑、新建和管理入口。
    dialogTitle: { readonly value: string }; // 编辑页标题。
    draftRevision: { readonly value: number }; // 草稿对应的工程修订。
    organizationBrowser: { value: { hasPending: boolean; openDefinitionTags: (id: string) => void; focusPending: () => void } | undefined }; // 共用标签弹窗宿主边界。
    pendingWarning: { readonly value: string }; // 草稿拦截后保留的提示。
    commitOrganization: (change: () => void) => boolean; // 真实分类事务桥接入口。
    setEditingTags: () => void; // 真实编辑页标签按钮。
    requestDeleteDefinition: () => void; // 只请求确认，不修改工程。
  };
  const html = () => renderToString(createSSRApp({ render: compile(functionField), setup: () => state }));
  const controlsHTML = () => renderToString(createSSRApp({ render: compile(editorControls), setup: () => ({ ...props, ...state }) }));
  return { ...state, props, failCommit, requests, applied, emissions, html, controlsHTML };
}

// 标签成功后允许继续修改定义；额外外部事务仍应使旧草稿失效，防止放宽并发保护。
test("编辑页标签事务只刷新自身一次修订并允许继续保存", async () => {
  const original: Definition = { id: "move", name: "移动", kind: "action", goName: "MoveTo" };
  const state = form("action", original);
  state.props.index.addTag("战斗", "combat");
  let opened = "";
  state.organizationBrowser.value = { hasPending: false, openDefinitionTags: id => { opened = id; }, focusPending: () => {} };
  state.setEditingTags();
  assert.equal(opened, "move");
  assert.equal(state.props.projectRevision, 1, "打开和取消标签弹窗不能写入工程");
  state.organizationBrowser.value.hasPending = true;
  assert.equal(state.commitOrganization(() => state.props.index.setTags("move", ["combat"])), true);
  state.organizationBrowser.value.hasPending = false;
  await nextTick();
  assert.equal(state.draftRevision.value, 2);
  assert.equal(state.hasPending.value, false);
  state.draft.value.name = "移动更新";
  state.acknowledged.value = true;
  await state.applyCatalog();
  assert.equal(state.applied[0]![0]!.name, "移动更新");
  assert.equal(state.error.value, "");
  state.props.projectRevision++;
  await nextTick();
  state.acknowledged.value = true;
  await state.applyCatalog();
  assert.match(state.error.value, /已变化/);
  assert.equal(state.applied.length, 1);
});

// 提交失败和同一更新周期内的额外事务都不允许刷新旧表单的修订基准。
test("标签失败或合并外部事务不能绕过业务定义草稿失效", async () => {
  const original: Definition = { id: "move", name: "移动", kind: "action", goName: "MoveTo" };
  const state = form("action", original);
  state.failCommit.value = true;
  assert.equal(state.commitOrganization(() => assert.fail("失败事务不得应用")), false);
  await nextTick();
  assert.equal(state.draftRevision.value, 1);
  state.failCommit.value = false;
  assert.equal(state.commitOrganization(() => {}), true);
  state.props.projectRevision++;
  await nextTick();
  assert.equal(state.draftRevision.value, 1);
  state.acknowledged.value = true;
  await state.applyCatalog();
  assert.match(state.error.value, /已变化/);
  assert.equal(state.requests.length, 0);
});

// 新入口针对当前切换的原定义，并保留未应用字段及子弹窗草稿；锁定或过期时不能继续。
test("编辑页标签与删除入口遵守真实目标和草稿保护", async () => {
  const original: Definition = { id: "move", name: "移动", kind: "action", goName: "MoveTo" };
  const state = form("action", original);
  const other: Definition = { id: "ready", name: "准备", kind: "condition", goName: "IsReady" };
  state.props.catalog.push(other);
  state.props.index = new CatalogOrganizationIndex(state.props.project);
  state.editDefinition(other);
  const tags: string[] = [];
  let focused = 0;
  state.organizationBrowser.value = { hasPending: false, openDefinitionTags: id => tags.push(id), focusPending: () => { focused++; } };
  state.setEditingTags();
  state.requestDeleteDefinition();
  assert.deepEqual(tags, ["ready"]);
  assert.equal(state.emissions[0]!.name, "delete");
  assert.equal(state.emissions[0]!.args[0], other);
  assert.equal(state.emissions[0]!.args[1], 1);
  state.draft.value.name = "未保存修改";
  state.setEditingTags();
  state.requestDeleteDefinition();
  assert.match(state.pendingWarning.value, /未应用/);
  assert.equal(state.draft.value.name, "未保存修改");
  assert.equal(tags.length, 1);
  assert.equal(state.emissions.length, 1);
  state.draft.value.name = other.name;
  state.organizationBrowser.value.hasPending = true;
  state.requestDeleteDefinition();
  assert.equal(focused, 1);
  state.organizationBrowser.value.hasPending = false;
  state.props.disabled = true;
  state.requestDeleteDefinition();
  state.props.disabled = false;
  state.busy.value = true;
  state.setEditingTags();
  state.busy.value = false;
  assert.equal(state.emissions.length, 1);
  assert.equal(tags.length, 1);
  state.props.catalog[1] = { ...other };
  state.props.index = new CatalogOrganizationIndex(state.props.project);
  state.requestDeleteDefinition();
  assert.match(state.error.value, /已变化/);
  assert.equal(state.emissions.length, 1);
  await nextTick();
});

// 编译真实标题和底部，保证新建页不提供已有定义操作，编辑页具有两项独立入口。
test("定义编辑页显示标签与删除入口，新建和管理页隐藏", async () => {
  const state = form("action", { id: "move", name: "移动", kind: "action", goName: "MoveTo" });
  const editing = await state.controlsHTML();
  assert.match(editing, /<header>[\s\S]*设置标签[\s\S]*<\/header>/);
  assert.match(editing, /<footer>[\s\S]*删除定义[\s\S]*取消[\s\S]*验证并保存修改/);
  for (const mode of ["create", "manage"]) {
    state.changeMode(mode, true);
    const html = await state.controlsHTML();
    assert.doesNotMatch(html, /设置标签|删除定义/);
  }
});

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
