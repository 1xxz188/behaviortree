import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import type { TestContext } from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import * as Vue from "vue";
import type { Ref, VNode } from "vue";
import { renderToString } from "vue/server-renderer";
import { parse as parseTemplate, compile, NodeTypes } from "@vue/compiler-dom";
import type { ElementNode, RootNode } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import { CatalogUsageIndex } from "./catalogVisibility.ts";
import type { CatalogTreeScope, CatalogNodeStatus } from "./catalogVisibility.ts";
import { BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix } from "./businessNames.ts";
import { catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents } from "./catalog.ts";
import { normalizeEventDescription, validateEventRegistry, EventRegistryIndex } from "./eventRegistry.ts";
import { valueTypes } from "./enums.ts";
import { PARAM_TYPE_META, parameterTypeTooltip } from "./parameterTypeMeta.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";
import type { Project } from "./project.ts";

// 执行完整真实 setup，避免继续测试已经移除的直属目录筛选表达式。
const managerDescriptor = parse(readFileSync(new URL("./CatalogManager.vue", import.meta.url), "utf8")).descriptor;
const managerScript = ts.createSourceFile("CatalogManager.ts", managerDescriptor.scriptSetup!.content, ts.ScriptTarget.Latest, true);
const managerSetup = managerScript.statements.filter(statement => !ts.isImportDeclaration(statement)).map(statement => statement.getText(managerScript)).join("\n");
const managerJS = ts.transpileModule(managerSetup, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const setupNames = managerScript.statements.flatMap(statement => ts.isVariableStatement(statement)
  ? statement.declarationList.declarations.flatMap(declaration => ts.isIdentifier(declaration.name) ? [declaration.name.text] : [])
  : ts.isFunctionDeclaration(statement) && statement.name ? [statement.name.text] : []);

// 子组件只替代渲染边界；真实树形筛选另由 CatalogBrowser 的功能用例验证。
const browserBoundary = Vue.defineComponent({ name: "CatalogBrowserBoundary", render: () => Vue.h("div") });
const managerBoundary = Vue.defineComponent({ name: "CatalogManagerBoundary", render: () => Vue.h("div") });
const eventBoundary = Vue.defineComponent({ name: "EventMultiSelectBoundary", render: () => Vue.h("div") });

// 编译真实模板生成 VNode，v-model 和事件均来自组件原绑定。
function templateRender(template: string): (context: unknown, cache: unknown[]) => VNode {
  const code = compile(template, { mode: "function", prefixIdentifiers: true, expressionPlugins: ["typescript"] }).code;
  return new Function("Vue", ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText)(Vue);
}
const managerRender = templateRender(managerDescriptor.template!.content);

// 在真实 Vue 渲染上下文执行指令，保留 v-show 和事件用于行为断言。
async function renderNodes(render: ReturnType<typeof templateRender>, context: Record<string, unknown>): Promise<VNode[]> {
  let rendered!: VNode;
  const app = Vue.createSSRApp({
    setup: () => context,
    render() { rendered = render(this, []); return rendered; },
  });
  app.component("CatalogBrowser", browserBoundary);
  app.component("CatalogManager", managerBoundary);
  app.component("EventMultiSelect", eventBoundary);
  await renderToString(app);
  return elements(rendered);
}

// 展开 VNode 子树，不复制子组件的业务逻辑。
function elements(node: VNode): VNode[] {
  return [node, ...(Array.isArray(node.children) ? node.children.flatMap(child => child && typeof child === "object" ? elements(child as VNode) : []) : [])];
}

// 获取实际控件文字，避免依赖源码排版。
function text(node: VNode): string {
  return typeof node.children === "string" ? node.children : Array.isArray(node.children)
    ? node.children.map(child => child && typeof child === "object" ? text(child as VNode) : String(child ?? "")).join("") : "";
}

// 兼容 Vue 对组件属性的驼峰和连字符写法。
function property(node: VNode, name: string): unknown {
  const kebab = name.replace(/[A-Z]/g, value => "-" + value.toLowerCase());
  return node.props?.[name] ?? node.props?.[kebab];
}

// 调用真实模板事件，不直接修改筛选 ref。
function trigger(node: VNode, name: string, ...args: unknown[]): void {
  const handler = node.props?.[name];
  assert.equal(typeof handler, "function", "缺少模板事件 " + name);
  handler(...args);
}

// 从 label 寻找真实输入，覆盖可见筛选和双向绑定。
function filterControl(nodes: VNode[], label: string): VNode {
  const row = nodes.find(node => node.type === "label" && text(node).startsWith(label));
  assert.ok(row, "缺少 " + label + " 筛选控件");
  const control = elements(row).find(node => node.type === "input" || node.type === "select");
  assert.ok(control, label + " 未连接输入控件");
  return control;
}

// 构建跨目录、不同种类和跨树引用的真实工程。
function projectFixture(): Project {
  const project = Vue.reactive(blankProject());
  project.catalog = [
    { id: "walk", name: "移动到目标", kind: "action", goName: "MoveTo" },
    { id: "ready", name: "移动条件", kind: "condition", goName: "IsReady" },
    { id: "nested", name: "移动子目录", kind: "action", goName: "NestedMove" },
  ];
  project.trees[0]!.root = "root";
  project.trees[0]!.nodes = [
    { id: "root", type: "sequence", children: ["ready"] },
    { id: "ready", type: "condition", binding: "ready" },
    { id: "walk", type: "action", binding: "walk" },
  ];
  project.trees.push({ id: "other", name: "其他树", root: "nested", nodes: [
    { id: "nested", type: "action", binding: "nested" },
  ] });
  return project;
}

// 管理页可交互状态，其余 setup 绑定仍完整提供给模板。
interface ManagerState extends Record<string, unknown> {
  query: Ref<string>; // 管理关键词独立于侧栏。
  kindFilter: Ref<string>; // 管理种类独立于侧栏。
  selectedFolder: Ref<string>; // 只保存新建和导入位置。
  activeTab: Ref<string>; // 节点或标签工作页。
  mode: Ref<string>; // 定义表单与管理页模式。
  treeScope: Ref<CatalogTreeScope>; // 直接桥接父级范围。
  nodeStatus: Ref<CatalogNodeStatus>; // 直接桥接父级状态。
  changeTab: (tab: string) => void; // 真实管理工作页切换入口。
}

// 只替代生命周期和外部输出，setup、watch 与操作函数使用真实代码。
function manager(t: TestContext, project = projectFixture(), onEmit?: (name: string, args: unknown[]) => void) {
  const index = new CatalogOrganizationIndex(project);
  index.addFolder("子目录", "", "child");
  index.move({ kind: "definition", id: "nested" }, "child");
  const props = Vue.shallowReactive({ project, catalog: project.catalog, projectRevision: 1, index, revision: 0,
    initialMode: "manage", initialFolder: "", initialKind: undefined, disabled: false, transferBusy: false, failureMessage: "",
    treeScope: "all" as CatalogTreeScope, nodeStatus: "all" as CatalogNodeStatus,
    matchesDefinition: undefined as ((id: string) => boolean) | undefined });
  const emissions: { name: string; args: unknown[] }[] = [];
  const context = {
    ...Vue, BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix,
    catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents,
    normalizeEventDescription, validateEventRegistry, valueTypes, PARAM_TYPE_META, parameterTypeTooltip, parseJSON, stringifyJSON,
    defineProps: () => props, withDefaults: (value: unknown) => value, defineExpose: () => {},
    onMounted: () => {}, onUnmounted: () => {}, validateCatalog: async (value: unknown) => value,
    defineEmits: () => (name: string, ...args: unknown[]) => { emissions.push({ name, args }); onEmit?.(name, args); },
  };
  const scope = Vue.effectScope();
  const state = scope.run(() => runInNewContext(managerJS + "\n({ " + setupNames.join(", ") + " });", context)) as ManagerState;
  t.after(() => scope.stop());
  const render = () => renderNodes(managerRender, { ...props, ...state });
  return { project, props, state, emissions, render };
}

// 标签维护组件与常驻组织浏览器分别定位。
function organization(nodes: VNode[]): VNode {
  const browser = nodes.find(node => node.type === browserBoundary && property(node, "presentation") === "directories");
  assert.ok(browser, "统一节点页应保留目录浏览器");
  return browser;
}

// 在表单或标签页检验外层隐藏指令，而非只检验 setup 状态值。
function organizationPanel(nodes: VNode[]): VNode {
  const panel = nodes.find(node => node.type === "div" && String(node.props?.class ?? "").split(" ").includes("catalog-existing"));
  assert.ok(panel, "缺少管理节点页容器");
  return panel;
}

// 提取 App 的真实偏好定义和组件模板，不另写一份父级同步逻辑。
const appDescriptor = parse(readFileSync(new URL("./App.vue", import.meta.url), "utf8")).descriptor;
const appScript = ts.createSourceFile("App.ts", appDescriptor.scriptSetup!.content, ts.ScriptTarget.Latest, true);
const appFilterNames = new Set(["catalogTreeScope", "catalogNodeStatus", "catalogMatchesDefinition"]);
const appFilters = appScript.statements.filter(statement => ts.isVariableStatement(statement)
  && statement.declarationList.declarations.some(declaration => ts.isIdentifier(declaration.name) && appFilterNames.has(declaration.name.text)))
  .map(statement => statement.getText(appScript)).join("\n");
const appJS = ts.transpileModule(appFilters, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 沿模板 AST 查找组件，换行和属性顺序变化不会影响夹具。
function componentTemplate(node: RootNode | ElementNode, tag: string): string | undefined {
  for (const child of node.children) {
    if (child.type !== NodeTypes.ELEMENT) continue;
    if (child.tag === tag) return child.loc.source;
    const match = componentTemplate(child, tag);
    if (match) return match;
  }
}
const appTemplate = parseTemplate(appDescriptor.template!.content);
const appRender = templateRender(componentTemplate(appTemplate, "CatalogBrowser") + "\n" + componentTemplate(appTemplate, "CatalogManager"));

// 连接真实 App 与 Manager 模板事件，并模拟父级更新后 props 回流。
async function connected(t: TestContext) {
  const project = projectFixture();
  const eventIndex = Vue.computed(() => new EventRegistryIndex(project));
  const catalogUsage = new CatalogUsageIndex(() => project.trees, () => eventIndex.value.definitionToNodes);
  const tree = Vue.shallowRef(project.trees[0]!);
  const state = runInNewContext(appJS + "\n({ catalogTreeScope, catalogNodeStatus, catalogMatchesDefinition });", { ...Vue, catalogUsage, tree }) as {
    catalogTreeScope: Ref<CatalogTreeScope>; // App 的真实范围 ref。
    catalogNodeStatus: Ref<CatalogNodeStatus>; // App 的真实状态 ref。
    catalogMatchesDefinition: Ref<((id: string) => boolean) | undefined>; // 复用可达缓存的真实匹配器。
  };
  const sidebarSearch = Vue.ref("侧栏关键词");
  const noop = () => {};
  const context = {
    ...state, project: Vue.ref(project), catalogIndex: Vue.shallowRef(new CatalogOrganizationIndex(project)), catalogRevision: Vue.ref(0),
    catalogDialog: Vue.ref({ mode: "manage", folderId: "" }), editRevision: 1, catalogCollapsed: Vue.ref(false),
    workspaceChanging: Vue.ref(false), search: sidebarSearch, error: Vue.ref(false), message: Vue.ref(""), catalogFolder: Vue.ref(""),
    catalogTransferBusy: Vue.ref(false), commitCatalogOrganization: noop, manageCatalog: noop, editCatalogDefinition: noop,
    openCatalogMenu: noop, startPaletteDrag: noop, endPaletteDrag: noop, transferCatalog: noop, applyCatalog: noop, requestDeleteDefinition: noop,
  };
  let nodes = await renderNodes(appRender, context);
  const parentNode = () => nodes.find(node => node.type === managerBoundary)!;
  const sidebar = () => nodes.find(node => node.type === browserBoundary)!;
  const view = manager(t, project, (name, args) => {
    const key = "on" + name[0]!.toUpperCase() + name.slice(1);
    if (parentNode().props?.[key]) trigger(parentNode(), key, ...args);
  });
  const refresh = async () => {
    nodes = await renderNodes(appRender, context);
    view.props.treeScope = property(parentNode(), "treeScope") as CatalogTreeScope;
    view.props.nodeStatus = property(parentNode(), "nodeStatus") as CatalogNodeStatus;
    view.props.matchesDefinition = property(parentNode(), "matchesDefinition") as ((id: string) => boolean) | undefined;
  };
  await refresh();
  return { view, state, sidebar, sidebarSearch, refresh, tree };
}

// 统一节点页提供四个顶层筛选，不再保留重复目录页与直属目录下拉。
test("管理统一节点页显示搜索种类范围状态并移除直属目录筛选", async t => {
  const view = manager(t);
  const nodes = await view.render();
  for (const label of ["搜索定义", "种类", "范围", "状态"]) filterControl(nodes, label);
  assert.equal(nodes.some(node => node.type === "label" && text(node).includes("直属定义")), false);
  assert.equal(nodes.some(node => node.type === "button" && text(node) === "目录"), false);
  assert.equal(property(organization(nodes), "index"), view.props.index);
  assert.equal(property(organization(nodes), "initialFolder"), "");
});

// 管理输入通过真实模型传给 Browser，选中目录只更新新增归属。
test("管理搜索和种类连到目录浏览器且选目录不裁剪候选", async t => {
  const view = manager(t);
  const nodes = await view.render();
  trigger(filterControl(nodes, "搜索定义"), "onUpdate:modelValue", "移动");
  trigger(filterControl(nodes, "种类"), "onUpdate:modelValue", "condition");
  trigger(organization(nodes), "onSelect", "child");
  const browser = organization(await view.render());
  assert.equal(property(browser, "search"), "移动");
  assert.equal(property(browser, "kindFilter"), "condition");
  assert.equal(property(browser, "initialFolder"), "child");
  assert.equal(view.props.index.definitions.size, 3);
  assert.equal(view.emissions.at(-1)!.name, "select");
  assert.equal(view.emissions.at(-1)!.args[0], "child");
});

// 清空通知清管理本地条件，不自行改变共享范围状态或新增目录。
test("管理清除搜索事件恢复本地条件并保留共享偏好和目录", async t => {
  const view = manager(t);
  view.props.treeScope = "current";
  view.props.nodeStatus = "invalid";
  const nodes = await view.render();
  trigger(filterControl(nodes, "搜索定义"), "onUpdate:modelValue", "移动");
  trigger(filterControl(nodes, "种类"), "onUpdate:modelValue", "action");
  trigger(organization(nodes), "onSelect", "child");
  trigger(organization(await view.render()), "onClearSearch");
  const browser = organization(await view.render());
  assert.equal(property(browser, "search"), "");
  assert.equal(property(browser, "kindFilter"), "");
  assert.equal(view.state.selectedFolder.value, "child");
  assert.equal(view.state.treeScope.value, "current");
  assert.equal(view.state.nodeStatus.value, "invalid");
});

// 表单和标签页只隐藏组织组件，返回节点页恢复本地筛选和目录。
test("管理切换表单和标签页保留筛选且组织浏览器持续挂载", async t => {
  const view = manager(t);
  const nodes = await view.render();
  const nodesTab = view.state.activeTab.value;
  trigger(filterControl(nodes, "搜索定义"), "onUpdate:modelValue", "移动");
  trigger(filterControl(nodes, "种类"), "onUpdate:modelValue", "action");
  trigger(organization(nodes), "onSelect", "child");
  trigger(nodes.find(node => node.type === "button" && text(node) === "新建定义")!, "onClick");
  const form = await view.render();
  assert.equal(view.state.mode.value, "create");
  organization(form);
  assert.ok(organizationPanel(form).dirs?.some(binding => binding.dir === Vue.vShow && binding.value === false));
  trigger(form.find(node => node.type === "button" && text(node).startsWith("← 返回"))!, "onClick");
  await Vue.nextTick();
  view.state.changeTab("tags");
  const tags = await view.render();
  organization(tags);
  assert.ok(organizationPanel(tags).dirs?.some(binding => binding.dir === Vue.vShow && binding.value === false));
  view.state.changeTab(nodesTab);
  const restored = await view.render();
  const browser = organization(restored);
  assert.equal(property(browser, "search"), "移动");
  assert.equal(property(browser, "kindFilter"), "action");
  assert.equal(property(browser, "initialFolder"), "child");
  assert.ok(organizationPanel(restored).dirs?.some(binding => binding.dir === Vue.vShow && binding.value === true));
});

// 管理选择框经 Manager 更新 App，侧栏获得相同偏好及同一个匹配函数。
test("管理范围状态经真实父子事件同步侧栏且共用可达匹配器", async t => {
  const app = await connected(t);
  const before = stringifyJSON(app.view.project);
  const nodes = await app.view.render();
  trigger(filterControl(nodes, "范围"), "onUpdate:modelValue", "current");
  trigger(filterControl(nodes, "状态"), "onUpdate:modelValue", "valid");
  await app.refresh();
  const browser = organization(await app.view.render());
  assert.equal(property(app.sidebar(), "treeScope"), "current");
  assert.equal(property(app.sidebar(), "nodeStatus"), "valid");
  assert.equal(property(browser, "matchesDefinition"), property(app.sidebar(), "matchesDefinition"));
  assert.equal(app.view.props.matchesDefinition!("ready"), true);
  assert.equal(app.view.props.matchesDefinition!("walk"), false);
  assert.equal(app.view.props.matchesDefinition!("nested"), false);
  assert.equal(stringifyJSON(app.view.project), before, "筛选不得写入工程");
  assert.equal(app.view.props.projectRevision, 1);
});

// 侧栏反向更新管理页，管理清空通知只影响管理关键词与种类。
test("侧栏范围状态回流管理页且两处关键词相互独立", async t => {
  const app = await connected(t);
  trigger(app.sidebar(), "onUpdate:treeScope", "current");
  trigger(app.sidebar(), "onUpdate:nodeStatus", "invalid");
  await app.refresh();
  let nodes = await app.view.render();
  assert.equal(app.view.state.treeScope.value, "current");
  assert.equal(app.view.state.nodeStatus.value, "invalid");
  trigger(filterControl(nodes, "搜索定义"), "onUpdate:modelValue", "管理关键词");
  trigger(filterControl(nodes, "种类"), "onUpdate:modelValue", "condition");
  assert.equal(app.sidebarSearch.value, "侧栏关键词");
  nodes = await app.view.render();
  trigger(organization(nodes), "onUpdate:treeScope", "all");
  trigger(organization(nodes), "onUpdate:nodeStatus", "all");
  trigger(organization(nodes), "onClearSearch");
  await app.refresh();
  assert.equal(property(app.sidebar(), "treeScope"), "all");
  assert.equal(property(app.sidebar(), "nodeStatus"), "all");
  assert.equal(app.sidebarSearch.value, "侧栏关键词");
  assert.equal(app.view.state.query.value, "");
  assert.equal(app.view.state.kindFilter.value, "");
});

// 当前树和绑定变化沿父级缓存刷新，管理页不另建行为树引用索引。
test("管理复用父级匹配器并随当前树和根绑定即时更新", async t => {
  const app = await connected(t);
  trigger(app.sidebar(), "onUpdate:treeScope", "current");
  trigger(app.sidebar(), "onUpdate:nodeStatus", "valid");
  await app.refresh();
  assert.equal(app.view.props.matchesDefinition!("ready"), true);
  app.tree.value = app.view.project.trees[1]!;
  await app.refresh();
  assert.equal(app.view.props.matchesDefinition!("nested"), true);
  assert.equal(app.view.props.matchesDefinition!("ready"), false);
  app.tree.value.nodes[0]!.binding = "walk";
  await app.refresh();
  assert.equal(app.view.props.matchesDefinition!("nested"), false);
  assert.equal(app.view.props.matchesDefinition!("walk"), true);
  assert.equal(property(organization(await app.view.render()), "matchesDefinition"), property(app.sidebar(), "matchesDefinition"));
});

