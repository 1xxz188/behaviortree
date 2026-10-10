import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, reactive, ref, shallowReactive, shallowRef } from "vue";
import * as Vue from "vue";
import type { VNode } from "vue";
import { compile } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import type { CatalogEntry } from "./catalogOrganization.ts";
import { CatalogUsageIndex } from "./catalogVisibility.ts";
import type { CatalogTreeScope, CatalogNodeStatus } from "./catalogVisibility.ts";
import { EventRegistryIndex } from "./eventRegistry.ts";
import type { DefinitionKind } from "./enums.ts";
import { stringifyJSON } from "./json.ts";
import { blankProject, clone } from "./project.ts";

// 执行真实拖拽函数及位置索引 computed，不复制组件的落点计算实现。
const component = readFileSync(new URL("./CatalogBrowser.vue", import.meta.url), "utf8");
const source = component.split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
// 编译真实模板以覆盖末尾空白区域的事件绑定，避免函数测试漏掉无法触发的落点。
const renderCode = compile(parse(component).descriptor.template!.content, { mode: "function", expressionPlugins: ["typescript"] }).code;
const render = new Function("Vue", ts.transpileModule(renderCode, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText)(Vue);
const script = ts.createSourceFile("CatalogBrowser.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["startDrag", "endDrag", "dropLocation", "dragOver", "drop", "leaveDropTarget", "selectFolder", "selectDefinition", "inspectDefinition", "definitionMenu", "submitDialog", "clearFilters", "toggleFolder"]);
const values = new Set(["placements", "isFiltered", "rows", "results", "treeScope", "nodeStatus", "isViewFiltered", "hasMatchingDefinitions", "isManager", "isManagementFiltered", "matchingTree", "filteredExpanded", "visibleExpanded", "initialFolderSynced"]);
// 展开和当前目录测试执行组件原 watch，避免在夹具中重写自动展开逻辑。
function isBrowserViewWatch(statement: ts.Statement): boolean {
  return ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)
    && statement.expression.expression.getText(script) === "watch"
    && /matchingTree|isManagementFiltered|props\.initialFolder/.test(statement.expression.arguments[0]?.getText(script) ?? "");
}
const workflow = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isVariableStatement(statement) && statement.declarationList.declarations.some(declaration => ts.isIdentifier(declaration.name) && values.has(declaration.name.text)))
  || isBrowserViewWatch(statement),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(workflow, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 构造浏览器拖拽边界，记录默认行为和冒泡处理，不依赖实际 DOM 或定时器。
function dragEvent(ratio = .5) {
  const data = new Map<string, string>();
  const state = { prevented: false, stopped: 0 };
  const transfer = {
    effectAllowed: "none", dropEffect: "none",
    setData: (format: string, value: string) => { data.set(format, value); },
  };
  const event = {
    dataTransfer: transfer, clientY: 100 + 100 * ratio,
    currentTarget: { getBoundingClientRect: () => ({ top: 100, height: 100 }) },
    preventDefault: () => { state.prevented = true; },
    stopPropagation: () => { state.stopped++; },
  } as unknown as DragEvent;
  return { event, state, data, transfer };
}

// 使用真实分类索引和事务回调，断言实际工程变化及画布事件，而非源代码文本。
function browser(ids = ["A", "B", "C"], initialFolder = "parent") {
  const project = reactive(blankProject());
  project.catalog = ids.map(id => ({ id, name: id, kind: "action", goName: id }));
  const index = new CatalogOrganizationIndex(project);
  const eventIndex = computed(() => new EventRegistryIndex(project)); // 与 App 共用工程引用索引，绑定变化自动失效。
  const usage = new CatalogUsageIndex(() => project.trees, () => eventIndex.value.definitionToNodes);
  const currentTree = shallowRef(project.trees[0]!); // 模拟父级切换当前树，使用真实响应式可达缓存。
  index.addFolder("父目录", "", "parent");
  index.addFolder("子目录", "parent", "child");
  const emissions: { name: string; args: unknown[] }[] = [];
  const transactions = { attempts: 0, succeeded: 0, errors: [] as string[] };
  const props = shallowReactive({
    index, revision: 0, search: "", disabled: false, collapsed: false, presentation: "sidebar" as "sidebar" | "directories" | "tags",
    treeScope: "all" as CatalogTreeScope, nodeStatus: "all" as CatalogNodeStatus,
    kindFilter: "" as "" | DefinitionKind, initialFolder: initialFolder as string | undefined,
    get matchesDefinition(): ((id: string) => boolean) | undefined { return usageMatcher.value; }, // 与 App 共用真实组合匹配入口。
    commit: (change: () => void) => {
      transactions.attempts++;
      try { change(); transactions.succeeded++; props.revision++; return true; }
      catch (cause) { transactions.errors.push(cause instanceof Error ? cause.message : String(cause)); return false; }
    },
  });
  const usageMatcher = computed<((id: string) => boolean) | undefined>(() => usage.matcher(currentTree.value, props.treeScope, props.nodeStatus));
  const context = {
    computed, ref, watch: Vue.watch, props, filters: ref<string[]>([]), filtersOpen: ref(false), dragged: shallowRef<CatalogEntry>(), dropHint: ref(""),
    selectedFolder: ref(initialFolder), selectedDefinition: ref(""), Node: class {},
    dialog: shallowRef<{ mode: string; id: string; parentId: string; entry: CatalogEntry }>(), failure: ref(""), nextTick: Vue.nextTick,
    expanded: ref(new Set<string>([""])), emit: (name: string, ...args: unknown[]) => {
      emissions.push({ name, args });
      // 模拟父级 v-model 与清空搜索，筛选事件不能进入工程事务。
      if (name === "update:treeScope") props.treeScope = args[0] as CatalogTreeScope;
      if (name === "update:nodeStatus") props.nodeStatus = args[0] as CatalogNodeStatus;
      if (name === "clearSearch") { props.search = ""; props.kindFilter = ""; }
      // 管理页把目录选择回传为 initialFolder，覆盖真实父子组件更新后的选择稳定性。
      if (name === "select" && props.presentation === "directories") props.initialFolder = args[0] as string;
    },
  };
  const actions = runInNewContext(`${js}\n({ startDrag, endDrag, dropLocation, dragOver, drop, leaveDropTarget, inspectDefinition, definitionMenu, submitDialog, toggleFolder, rows, results, isFiltered, treeScope, nodeStatus, clearFilters, isViewFiltered, hasMatchingDefinitions, isManager, isManagementFiltered, matchingTree, filteredExpanded, visibleExpanded });`, context) as {
    rows: { value: { entry: CatalogEntry; depth: number }[] };
    results: Vue.ComputedRef<typeof project.catalog>; // 真实搜索、标签和组合筛选结果。
    isFiltered: Vue.ComputedRef<boolean>; // 搜索结果与目录树的实际模板分支。
    treeScope: Vue.WritableComputedRef<CatalogTreeScope>; // 实际选择框的范围 model。
    nodeStatus: Vue.WritableComputedRef<CatalogNodeStatus>; // 实际选择框的状态 model。
    isViewFiltered: Vue.ComputedRef<boolean>; // 范围或状态是否限制显示。
    hasMatchingDefinitions: Vue.ComputedRef<boolean>; // 跨折叠目录检查真实匹配结果。
    isManager: Vue.ComputedRef<boolean>; // 管理页保留目录层级，侧栏仍采用原有搜索视图。
    isManagementFiltered: Vue.ComputedRef<boolean>; // 管理页任一筛选生效时暂停分类拖排。
    matchingTree: Vue.ComputedRef<{ definitions: Set<string>; folders: Set<string> } | undefined>; // 未筛选时无需建立匹配树缓存。
    filteredExpanded: Vue.Ref<Set<string>>; // 筛选期间的展开状态与普通视图隔离。
    visibleExpanded: Vue.ComputedRef<Set<string>>; // 当前模板及目录切换实际使用的展开集合。
    clearFilters(): void; // 恢复两个共享条件和标签，并请求父级清空搜索与种类。
    toggleFolder(id: string): void; // 执行真实目录手势，覆盖筛选期间的手动折叠。
    inspectDefinition(definition: typeof project.catalog[number]): void; // 选中定义并更新当前目录。
    startDrag(event: DragEvent, entry: CatalogEntry): void;
    endDrag(): void;
    dropLocation(event: DragEvent, entry?: CatalogEntry): { folder: string; before?: CatalogEntry; hint: string };
    dragOver(event: DragEvent, entry?: CatalogEntry): void;
    drop(event: DragEvent, entry?: CatalogEntry): void;
    leaveDropTarget(event: DragEvent): void;
    submitDialog(): Promise<void>;
  };
  // 条目引用直接取真实索引，模拟模板向事件函数传入的可见行。
  function entry(id: string): CatalogEntry {
    for (const entries of index.children.values()) {
      const value = entries.find(item => item.id === id);
      if (value) return value;
    }
    throw new Error(`测试条目不存在：${id}`);
  }
  return { project, index, props, currentTree, usage, context, transactions, emissions, entry, ...actions };
}

// 扁平收集编译后的元素，事件回调仍来自组件原函数与原模板。
function elements(node: VNode): VNode[] {
  return [node, ...(Array.isArray(node.children) ? node.children.flatMap(child => child && typeof child === "object" ? elements(child as VNode) : []) : [])];
}

// 模拟实际库面板的渲染上下文，业务定义与目录取同一真实索引。
function renderBrowser(view: ReturnType<typeof browser>): VNode[] {
  return elements(render(Vue.proxyRefs({
    ...view.props, ...view, rows: view.rows.value, expanded: view.context.expanded.value, visibleExpanded: view.visibleExpanded.value,
    selectedFolder: view.context.selectedFolder.value, selectedDefinition: view.context.selectedDefinition.value,
    dropHint: view.context.dropHint.value, isFiltered: view.isFiltered.value, results: view.results.value,
    treeScope: view.treeScope, nodeStatus: view.nodeStatus,
    allTags: [...view.index.tags.values()], filters: view.context.filters.value, filtersOpen: view.context.filtersOpen, folderMenu: undefined, dialog: undefined,
    endDrag: view.endDrag, emit: view.context.emit,
  }), []));
}

// 从真实选择框触发 model 更新，覆盖模板绑定与父级状态同步，而非直接改匹配结果。
function chooseFilter(view: ReturnType<typeof browser>, label: string, value: string) {
  const select = renderBrowser(view).find(node => node.type === "select" && node.props?.["aria-label"] === label)!;
  assert.equal(typeof select.props?.["onUpdate:modelValue"], "function");
  select.props!["onUpdate:modelValue"](value);
}

// 收集实际显示的业务定义，目录仍保留，不将其作为节点计入。
function visibleIDs(view: ReturnType<typeof browser>) {
  return Array.from(view.rows.value).filter(row => row.entry.kind === "definition").map(row => row.entry.id);
}

// 汇总真实模板的可见文本，断言图标与定义详情而不依赖样式选择器。
function nodeText(node: VNode): string {
  if (typeof node.children === "string") return node.children;
  if (!Array.isArray(node.children)) return "";
  return node.children.map(child => typeof child === "string" || typeof child === "number" ? String(child)
    : child && typeof child === "object" ? nodeText(child as VNode) : "").join("");
}

// 管理定义主按钮使用独立 ID，确保测试不会误命中侧栏的同名定义。
function managedDefinition(view: ReturnType<typeof browser>, id: string): VNode {
  const button = renderBrowser(view).find(node => node.type === "button" && node.props?.id === `catalog-managed-definition-${id}`);
  assert.ok(button, `管理树应显示定义 ${id}`);
  return button;
}

// 两棵树复用节点 ID；同一定义可仅用于草稿、跨树有效或同时被有效与草稿引用。
function mixedBrowser() {
  const view = browser(["A", "B", "C", "D", "E", "F", "G"]);
  const current = view.project.trees[0]!;
  current.root = "root";
  current.nodes = [
    { id: "root", type: "sequence", children: ["a", "f"] },
    { id: "a", type: "action", binding: "A" },
    { id: "b", type: "action", binding: "B" },
    { id: "d", type: "condition", binding: "D" },
    { id: "f", type: "action", binding: "F" },
    { id: "f-draft", type: "action", binding: "F" },
  ];
  view.project.trees.push({ id: "other", name: "其他树", root: "root", nodes: [
    { id: "root", type: "sequence", children: ["a", "d"] },
    { id: "a", type: "action", binding: "C" },
    { id: "d", type: "condition", binding: "D" },
    { id: "g", type: "action", binding: "G" },
  ] });
  return view;
}

// 复现根上条件实例尚未选择业务实现函数：目录定义必须仍能在当前树无效范围中找到。
test("当前树无效包含尚未接入业务实现函数的定义", () => {
  const view = browser(["IsTest"]);
  view.project.catalog[0]!.name = "测试";
  view.project.catalog[0]!.kind = "condition";
  const tree = view.project.trees[0]!;
  tree.root = "root";
  tree.nodes = [
    { id: "root", type: "sequence", children: ["unbound"] },
    { id: "unbound", type: "condition", codeName: "ConditionTest" },
  ];
  chooseFilter(view, "业务节点树范围", "current");
  chooseFilter(view, "业务节点状态", "invalid");
  assert.deepEqual(visibleIDs(view), ["IsTest"], "未选择业务实现函数不能把目录定义排除在当前树无效范围外");
  assert.ok(renderBrowser(view).some(node => node.props?.id === "catalog-definition-IsTest"));
  chooseFilter(view, "业务节点状态", "valid");
  assert.deepEqual(visibleIDs(view), [], "根连接本身不能代替业务实现函数绑定");
  chooseFilter(view, "业务节点状态", "all");
  assert.deepEqual(visibleIDs(view), ["IsTest"]);
  chooseFilter(view, "业务节点状态", "valid");
  tree.nodes[1]!.binding = "IsTest";
  assert.deepEqual(visibleIDs(view), ["IsTest"], "选择实现函数后立即进入有效范围");
  chooseFilter(view, "业务节点状态", "invalid");
  assert.deepEqual(visibleIDs(view), []);
  delete tree.nodes[1]!.binding;
  assert.deepEqual(visibleIDs(view), ["IsTest"], "清空实现函数后立即恢复无效范围");
  assert.equal(view.transactions.attempts, 0);
});

// 管理窗口在既有目录创建未绑定定义后，当前树筛选无需重载即可展示该公共候选。
test("当前树无效立即显示目录中新建但未绑定的定义", () => {
  const view = mixedBrowser();
  chooseFilter(view, "业务节点树范围", "current");
  chooseFilter(view, "业务节点状态", "invalid");
  view.project.catalog.push({ id: "IsNew", name: "新条件", kind: "condition", goName: "IsNew" });
  view.index.assignNewDefinitions(["IsNew"], "parent");
  view.props.revision++;
  view.context.expanded.value.add("parent");
  assert.deepEqual(visibleIDs(view), ["B", "D", "E", "IsNew"]);
  chooseFilter(view, "业务节点状态", "all");
  assert.deepEqual(visibleIDs(view), ["A", "B", "D", "E", "F", "IsNew"]);
});

// 验证两个真实下拉框的全部六种组合、跨树有效优先和未使用定义，并保证不改工程。
test("范围和状态可组合筛选且默认全部树全部状态", () => {
  const view = mixedBrowser();
  const before = stringifyJSON(view.project);
  assert.equal(view.treeScope.value, "all");
  assert.equal(view.nodeStatus.value, "all");
  const cases: [CatalogTreeScope, CatalogNodeStatus, string[]][] = [
    ["all", "all", ["A", "B", "C", "D", "E", "F", "G"]],
    ["all", "valid", ["A", "C", "D", "F"]],
    ["all", "invalid", ["B", "E", "G"]],
    ["current", "all", ["A", "B", "D", "E", "F"]],
    ["current", "valid", ["A", "F"]],
    ["current", "invalid", ["B", "D", "E"]],
  ];
  for (const [scope, status, expected] of cases) {
    chooseFilter(view, "业务节点树范围", scope);
    chooseFilter(view, "业务节点状态", status);
    assert.deepEqual(visibleIDs(view), expected, `${scope} + ${status}`);
    assert.equal(view.isFiltered.value, false, "仅范围和状态筛选仍应保留目录树");
  }
  assert.equal(view.transactions.attempts, 0);
  assert.equal(stringifyJSON(view.project), before);
});

// 搜索与标签继续按 AND 叠加两个条件；清除入口恢复默认全部，目录结构仍保持。
test("组合筛选与标签搜索叠加并能一次清除", () => {
  const view = mixedBrowser();
  const tag = view.index.addTag("筛选标签");
  for (const id of ["A", "D", "F"]) view.index.setTags(id, [tag.id]);
  view.context.filters.value = [tag.id];
  chooseFilter(view, "业务节点树范围", "current");
  chooseFilter(view, "业务节点状态", "invalid");
  assert.deepEqual(Array.from(view.results.value, item => item.id), ["D"]);
  view.props.search = "F";
  assert.deepEqual(Array.from(view.results.value), []);
  chooseFilter(view, "业务节点状态", "valid");
  assert.deepEqual(Array.from(view.results.value, item => item.id), ["F"]);
  const clear = renderBrowser(view).find(node => node.type === "button" && node.children === "清除筛选")!;
  clear.props!.onClick();
  assert.equal(view.treeScope.value, "all");
  assert.equal(view.nodeStatus.value, "all");
  assert.equal(view.context.filters.value.length, 0);
  assert.equal(view.props.search, "");
  assert.deepEqual(visibleIDs(view), ["A", "B", "C", "D", "E", "F", "G"]);
  assert.equal(view.transactions.attempts, 0);
});

// 切树、回接草稿、修改绑定及恢复树对象时即时更新，跨树相同节点 ID 不串用状态。
test("组合结果随当前树和根连接绑定变化即时更新", () => {
  const view = mixedBrowser();
  chooseFilter(view, "业务节点树范围", "current");
  chooseFilter(view, "业务节点状态", "invalid");
  view.currentTree.value = view.project.trees[1]!;
  assert.deepEqual(visibleIDs(view), ["E", "G"]);
  view.currentTree.value.nodes[0]!.children!.push("g");
  assert.deepEqual(visibleIDs(view), ["E"]);
  view.currentTree.value.nodes[0]!.children = ["a", "d"];
  view.currentTree.value.nodes[3]!.binding = "E";
  assert.deepEqual(visibleIDs(view), ["E", "G"]);
  const replacement = reactive(clone(view.project.trees[0]!));
  view.project.trees[0] = replacement;
  view.currentTree.value = replacement;
  assert.deepEqual(visibleIDs(view), ["B", "D", "G"]);
  replacement.root = "missing";
  chooseFilter(view, "业务节点状态", "valid");
  assert.deepEqual(visibleIDs(view), []);
  assert.ok(renderBrowser(view).some(node => node.type === "p" && node.children === "没有符合条件的业务定义。"), "保留目录时仍需提示没有匹配定义");
});

// 筛选、布局和注释不触发重新遍历，改线只更新一棵树；环与重复悬挂边仍有界结束。
test("业务可达缓存按树复用且草稿环不会重复展开", () => {
  const view = mixedBrowser();
  const current = view.project.trees[0]!;
  const other = view.project.trees[1]!;
  const before = view.usage.forTree(current).value;
  const otherBefore = view.usage.forTree(other).value;
  current.name = "只改显示名称";
  current.layout = { a: { x: 200, y: 100 } };
  current.nodes[1]!.comment = "只改注释";
  current.nodes[1]!.params = { Speed: { value: 1 } };
  chooseFilter(view, "业务节点树范围", "current");
  chooseFilter(view, "业务节点状态", "valid");
  view.props.search = "A";
  assert.equal(view.usage.forTree(current).value, before);
  current.nodes[0]!.children = ["root", "a", "a", "missing"];
  assert.deepEqual(visibleIDs(view), ["A"]);
  assert.notEqual(view.usage.forTree(current).value, before);
  assert.equal(view.usage.forTree(other).value, otherBefore);
  chooseFilter(view, "业务节点树范围", "all");
  view.props.search = "";
  assert.deepEqual(visibleIDs(view), ["A", "C", "D"]);
  view.project.trees.pop();
  assert.deepEqual(visibleIDs(view), ["A"]);
});

// 隐藏同级条目仍参与真实排序锚点，筛选时拖放不能破坏未显示定义的相对顺序。
test("组合筛选保留目录排序的完整后继", () => {
  const view = mixedBrowser();
  chooseFilter(view, "业务节点树范围", "current");
  chooseFilter(view, "业务节点状态", "valid");
  view.startDrag(dragEvent().event, view.entry("F"));
  view.drop(dragEvent(.9).event, view.entry("A"));
  assert.deepEqual(view.index.entries().map(item => item.id), ["A", "F", "B", "C", "D", "E", "G", "parent"]);
  assert.deepEqual(visibleIDs(view), ["A", "F"]);
  assert.equal(view.transactions.succeeded, 1);
});

// 复现截图中的末尾空白松手：从末行边缘继续向下时仍须接受移动并追加根目录末尾。
test("拖到目录树末尾空白处仍能完成排序", () => {
  const view = browser();
  view.startDrag(dragEvent().event, view.entry("A"));
  view.dragOver(dragEvent(.9).event, view.entry("parent"));
  const tree = renderBrowser(view).find(node => node.props?.["aria-label"] === "业务目录树")!;
  assert.equal(typeof tree.props?.onDragover, "function", "末尾空白区域必须接受拖拽");
  const over = dragEvent();
  tree.props!.onDragover(over.event);
  assert.equal(over.state.prevented, true);
  tree.props!.onDrop(dragEvent().event);
  assert.deepEqual(view.index.entries().map(item => item.id), ["B", "C", "parent", "A"]);
  assert.equal(view.transactions.succeeded, 1);
});

// 浏览器快速跨行时可能先进入落点就松手，不能依赖下一次 dragover 才允许放置。
test("快速进入末尾或目录后立即松手也能移动", () => {
  for (const folder of [false, true]) {
    const view = browser();
    view.startDrag(dragEvent().event, view.entry("A"));
    const nodes = renderBrowser(view);
    const target = folder
      ? nodes.find(node => node.props?.class?.includes("catalog-row") && elements(node).some(child => child.props?.class?.includes("catalog-folder")))!
      : nodes.find(node => node.props?.["aria-label"] === "业务目录树")!;
    const enter = dragEvent();
    target.props!.onDragenter(enter.event);
    assert.equal(enter.state.prevented, true);
    target.props!.onDrop(dragEvent().event);
    assert.equal(view.index.assignments.get("A")!.folderId, folder ? "parent" : "");
    assert.equal(view.index.entries(folder ? "parent" : "").at(-1)!.id, "A");
    assert.equal(view.transactions.succeeded, 1);
  }
});

// 定义落在目录主体时仅提交一次移动，停止冒泡并清理拖拽和画布临时状态。
test("定义拖入目录只移动一次并追加目录末尾", (t) => {
  const view = browser();
  const move = t.mock.method(view.index, "move");
  const start = dragEvent();
  view.startDrag(start.event, view.entry("A"));
  assert.equal(start.transfer.effectAllowed, "copyMove");
  assert.equal(start.data.get("application/x-behaviortree-catalog"), "definition:A");
  assert.equal(view.emissions.find(item => item.name === "drag")!.args[1], view.project.catalog[0]);
  const over = dragEvent();
  view.dragOver(over.event, view.entry("parent"));
  assert.equal(over.state.prevented, true);
  assert.equal(view.context.dropHint.value, "in:parent");
  const drop = dragEvent();
  view.drop(drop.event, view.entry("parent"));
  assert.equal(drop.state.stopped, 1);
  assert.equal(move.mock.callCount(), 1);
  assert.equal(view.transactions.succeeded, 1);
  assert.equal(view.index.assignments.get("A")!.folderId, "parent");
  assert.deepEqual(view.index.entries("parent").map(item => item.id), ["child", "A"]);
  assert.equal(view.context.dragged.value, undefined);
  assert.equal(view.context.dropHint.value, "");
  assert.equal(view.emissions.at(-1)!.name, "dragend");
});

// 仅悬停或取消不会写工程；真实 drop 才进入撤销事务。
test("取消拖拽不修改工程或产生事务", () => {
  const view = browser();
  const before = stringifyJSON(view.project);
  view.startDrag(dragEvent().event, view.entry("A"));
  view.dragOver(dragEvent().event, view.entry("parent"));
  view.endDrag();
  assert.equal(stringifyJSON(view.project), before);
  assert.equal(view.transactions.attempts, 0);
  assert.equal(view.context.dragged.value, undefined);
  assert.equal(view.context.dropHint.value, "");
});

// 目录拖拽清除画布创建状态，不能移入自身或后代形成目录环。
test("目录拖拽不创建画布节点且拒绝自身后代", () => {
  for (const target of ["parent", "child"]) {
    const view = browser();
    const before = stringifyJSON(view.project);
    const start = dragEvent();
    view.startDrag(start.event, view.entry("parent"));
    assert.equal(start.transfer.effectAllowed, "move");
    assert.equal(view.emissions.some(item => item.name === "drag" || item.name === "add"), false);
    assert.equal(view.emissions.at(-1)!.name, "dragend");
    const over = dragEvent();
    view.dragOver(over.event, view.entry(target));
    assert.equal(over.state.prevented, false);
    view.drop(dragEvent().event, view.entry(target));
    assert.equal(view.transactions.succeeded, 0);
    assert.equal(stringifyJSON(view.project), before);
    assert.equal(view.context.dragged.value, undefined);
    if (target === "child") assert.match(view.transactions.errors[0]!, /自身或后代/);
  }
});

// 搜索或标签过滤只禁止分类拖放，定义仍可通过相同手势拖到画布。
test("筛选时拒绝分类移动但继续发送定义画布拖拽事件", () => {
  for (const filter of ["search", "tag"]) {
    const view = browser();
    if (filter === "search") view.props.search = "A";
    else view.context.filters.value = ["selected-tag"];
    const before = stringifyJSON(view.project);
    view.startDrag(dragEvent().event, view.entry("A"));
    assert.equal(view.emissions.some(item => item.name === "drag"), true);
    const over = dragEvent();
    view.dragOver(over.event, view.entry("parent"));
    assert.equal(over.state.prevented, false);
    view.drop(dragEvent().event, view.entry("parent"));
    assert.equal(view.transactions.attempts, 0);
    assert.equal(stringifyJSON(view.project), before);
    view.endDrag();
  }
});

// 定义上下半区分别插到前后，末条后方和固定根落点正确追加。
test("定义前后落点使用真实同级后继并调整混合顺序", () => {
  const before = browser();
  before.startDrag(dragEvent().event, before.entry("C"));
  before.drop(dragEvent(.1).event, before.entry("A"));
  assert.deepEqual(before.index.entries().map(item => item.id), ["C", "A", "B", "parent"]);
  const after = browser();
  after.startDrag(dragEvent().event, after.entry("A"));
  after.drop(dragEvent(.9).event, after.entry("B"));
  assert.deepEqual(after.index.entries().map(item => item.id), ["B", "A", "C", "parent"]);
  const last = after.dropLocation(dragEvent(.9).event, after.entry("parent"));
  assert.equal(last.folder, "");
  assert.equal(last.before, undefined);
  after.startDrag(dragEvent().event, after.entry("A"));
  after.drop(dragEvent().event);
  assert.deepEqual(after.index.entries().map(item => item.id), ["B", "C", "parent", "A"]);
});

// 目录顶部与底部代表同级排序，中部代表移入，防止目录间拖拽落错层级。
test("目录边缘和主体落点分别表达排序与嵌套", () => {
  const view = browser();
  const folder = view.entry("parent");
  const top = view.dropLocation(dragEvent(.1).event, folder);
  assert.equal(top.folder, "");
  assert.equal(top.before, folder);
  assert.equal(top.hint, "before:folder:parent");
  const middle = view.dropLocation(dragEvent(.5).event, folder);
  assert.equal(middle.folder, "parent");
  assert.equal(middle.hint, "in:parent");
  const bottom = view.dropLocation(dragEvent(.9).event, folder);
  assert.equal(bottom.folder, "");
  assert.equal(bottom.before, undefined);
  assert.equal(bottom.hint, "after:folder:parent");
});

// 开始拖动根目录定义时，原目录立即取消高亮，成功排序后定义仍保持选中。
test("拖动定义会替换原目录选择且不打开定义窗口", () => {
  const view = browser();
  const captionCount = renderBrowser(view).filter(node => node.props?.class?.includes("catalog-current")).length;
  view.startDrag(dragEvent().event, view.entry("A"));
  assert.equal(renderBrowser(view).filter(node => node.props?.class?.includes("catalog-current")).length, captionCount, "拖拽开始不能移除目录说明行而改变落点布局");
  let buttons = renderBrowser(view).filter(node => node.type === "button");
  assert.equal(buttons.find(node => node.props?.id === "catalog-definition-A")!.props!.class.includes("selected"), true);
  assert.equal(buttons.filter(node => node.props?.class?.includes("selected")).length, 1);
  assert.equal(view.emissions.some(item => item.name === "inspect" || item.name === "add"), false);
  view.drop(dragEvent().event, view.entry("parent"));
  assert.equal(view.context.selectedFolder.value, "parent");
  assert.equal(view.context.selectedDefinition.value, "A");
  buttons = renderBrowser(view).filter(node => node.type === "button");
  assert.equal(buttons.filter(node => node.props?.class?.includes("selected")).length, 1);
});

// 原模板的普通与搜索结果单击都只发出查看事件，不产生工程变更或画布添加事件。
test("定义列表与搜索结果单击仅查看指定定义", () => {
  for (const search of ["", "A"]) {
    const view = browser();
    view.props.search = search;
    const before = stringifyJSON(view.project);
    const button = renderBrowser(view).find(node => node.type === "button" && node.props?.class?.includes("catalog-definition"))!;
    button.props!.onClick();
    assert.equal(view.emissions.find(item => item.name === "inspect")!.args[0], view.project.catalog[0]);
    assert.equal(view.emissions.some(item => item.name === "add" || item.name === "drag"), false);
    assert.equal(view.context.selectedDefinition.value, "A");
    assert.equal(view.transactions.attempts, 0);
    assert.equal(stringifyJSON(view.project), before);
  }
});

// 离开排序区域或进入非法目录时必须撤销旧提示，并阻止冒泡变成根目录落点。
test("离开落点及无效悬停清除旧排序提示", () => {
  const view = browser();
  view.startDrag(dragEvent().event, view.entry("parent"));
  view.dragOver(dragEvent(.1).event, view.entry("A"));
  assert.equal(view.context.dropHint.value, "before:definition:A");
  view.leaveDropTarget(dragEvent().event);
  assert.equal(view.context.dropHint.value, "");
  view.dragOver(dragEvent(.1).event, view.entry("A"));
  const invalid = dragEvent();
  view.dragOver(invalid.event, view.entry("child"));
  assert.equal(view.context.dropHint.value, "");
  assert.equal(invalid.state.stopped, 1);
  assert.equal(invalid.state.prevented, false);
});

// 右键移动与拖放保持一致：成功后新增位置使用目标目录，失败则保留原选择。
test("移动定义表单仅在成功后同步选中定义的目录", async () => {
  for (const destination of ["parent", "missing"]) {
    const view = browser();
    view.context.selectedFolder.value = "";
    view.context.selectedDefinition.value = "A";
    view.context.dialog.value = { mode: "move", id: "A", parentId: destination, entry: view.entry("A") };
    await view.submitDialog();
    assert.equal(view.context.selectedFolder.value, destination === "parent" ? "parent" : "");
    assert.equal(view.context.selectedDefinition.value, "A");
    assert.equal(view.context.dialog.value === undefined, destination === "parent");
  }
});

// 侧栏只提供浏览与管理入口，避免同层排列创建、导入和分类维护操作。
test("侧栏集中管理入口且标签筛选默认折叠", () => {
  const view = browser();
  view.index.addTag("战斗");
  const nodes = renderBrowser(view);
  const labels = nodes.filter(node => node.type === "button").map(node => typeof node.children === "string" ? node.children.trim() : "");
  assert.ok(labels.includes("管理"));
  assert.ok(labels.includes("▸ 标签筛选（0）"));
  for (const text of ["新建目录", "新建定义", "导入业务定义", "管理标签"]) assert.ok(!labels.includes(text));
  assert.equal(nodes.some(node => node.props?.["aria-label"] === "标签筛选（全部满足）"), false);
});

// 管理目录页复用原有排序，但不会触发向画布复制节点的事件。
test("目录管理页保留分类排序且阻止画布拖入事件", () => {
  const view = browser();
  view.props.presentation = "directories";
  const event = dragEvent();
  view.startDrag(event.event, view.entry("A"));
  assert.equal(event.transfer.effectAllowed, "move");
  assert.equal(view.emissions.some(item => item.name === "drag"), false);
  view.drop(dragEvent().event, view.entry("parent"));
  assert.equal(view.index.assignments.get("A")!.folderId, "parent");
  const nodes = renderBrowser(view);
  assert.ok(nodes.some(node => node.props?.["aria-label"] === "当前目录操作"));
});

// 根目录的工具栏仅允许新建子目录，防止无效改名、移动或删除。
test("目录管理根目录禁用自身修改按钮", () => {
  const view = browser();
  view.props.presentation = "directories";
  view.context.selectedFolder.value = "";
  const nodes = renderBrowser(view);
  for (const label of ["重命名", "移动到", "删除空目录"]) {
    const button = nodes.find(node => node.type === "button" && node.children === label)!;
    assert.equal(button.props!.disabled, true);
  }
});

// 标签页展示关联数量并隔离目录浏览，删除标签仍调用共享事务表单。
test("标签管理页展示关联定义数量且不混入目录树", () => {
  const view = browser();
  view.props.presentation = "tags";
  const tag = view.index.addTag("战斗");
  view.index.setTags("A", [tag.id]);
  const nodes = renderBrowser(view);
  assert.ok(nodes.some(node => node.type === "small" && node.children === "1 个关联定义"));
  assert.equal(nodes.some(node => node.props?.["aria-label"] === "业务目录树"), false);
});

// 管理搜索、种类与标签共同筛选定义，仅保留其祖先，不能把同目录未命中的节点一并显示。
test("管理筛选保留匹配定义和祖先层级且自动展开", async () => {
  const view = browser(["A", "B", "C", "D"]);
  view.props.presentation = "directories";
  view.context.expanded.value = new Set([""]);
  view.project.catalog[0]!.name = "匹配动作";
  view.project.catalog[1]!.name = "旁支动作";
  view.project.catalog[2]!.name = "匹配条件";
  view.project.catalog[2]!.kind = "condition";
  view.index.addFolder("无关目录", "", "unrelated");
  view.index.addFolder("空目录", "", "empty");
  view.index.move(view.entry("A"), "child");
  view.index.move(view.entry("B"), "child");
  view.index.move(view.entry("C"), "parent");
  view.index.move(view.entry("D"), "unrelated");
  const firstTag = view.index.addTag("命中标签");
  const secondTag = view.index.addTag("交集标签");
  view.index.setTags("A", [firstTag.id, secondTag.id]);
  view.index.setTags("C", [firstTag.id]);
  view.context.filters.value = [firstTag.id];
  view.props.search = "匹配";
  view.props.revision++;
  await Vue.nextTick();
  assert.deepEqual(Array.from(view.rows.value, row => [row.entry.id, row.depth]), [["parent", 0], ["child", 1], ["A", 2], ["C", 1]]);
  assert.deepEqual(Array.from(view.context.expanded.value), [""], "自动展开不能覆盖普通视图的展开偏好");
  assert.ok(view.visibleExpanded.value.has("parent") && view.visibleExpanded.value.has("child"));
  assert.equal(renderBrowser(view).some(node => node.props?.class?.includes("catalog-result")), false, "管理搜索不得切换为平铺结果");
  view.props.kindFilter = "action";
  await Vue.nextTick();
  assert.deepEqual(visibleIDs(view), ["A"]);
  view.props.kindFilter = "";
  view.context.filters.value = [firstTag.id, secondTag.id];
  await Vue.nextTick();
  assert.deepEqual(visibleIDs(view), ["A"], "多标签保持全部满足的交集语义");
  view.clearFilters();
  await Vue.nextTick();
  assert.ok(Array.from(view.rows.value).some(row => row.entry.id === "empty"), "清除后必须恢复空目录入口");
  assert.ok(Array.from(view.rows.value).some(row => row.entry.id === "unrelated"), "筛选隐藏的无关目录不能从普通树丢失");
  assert.equal(view.transactions.attempts, 0);
});

// 目录名称不参与关键词匹配；无定义命中时保留根入口，避免残留空目录造成伪命中。
test("管理空结果隐藏无关目录并保持当前目录归属", async () => {
  const view = browser();
  view.props.presentation = "directories";
  view.props.search = "父目录";
  await Vue.nextTick();
  assert.deepEqual(Array.from(view.rows.value), []);
  assert.equal(view.context.selectedFolder.value, "parent");
  const nodes = renderBrowser(view);
  assert.ok(nodes.some(node => node.type === "button" && node.props?.class?.includes("catalog-root")));
  assert.ok(nodes.some(node => node.type === "p" && nodeText(node) === "没有符合条件的业务定义。"));
  assert.ok(nodes.some(node => node.props?.class?.includes("catalog-current") && nodeText(node).includes("父目录")));
  assert.equal(view.transactions.attempts, 0);
});

// 范围与状态复用真实可达缓存，管理树对全部组合及种类筛选同时保留正确的祖先。
test("管理层级树叠加范围状态种类并随当前树更新", async () => {
  const view = mixedBrowser();
  view.props.presentation = "directories";
  view.context.expanded.value = new Set(["", "parent", "child"]);
  for (const definition of view.project.catalog) view.index.move(view.entry(definition.id), "child");
  view.project.catalog.find(definition => definition.id === "D")!.kind = "condition";
  view.props.revision++;
  const before = stringifyJSON(view.project);
  const cases: [CatalogTreeScope, CatalogNodeStatus, string[]][] = [
    ["all", "all", ["A", "B", "C", "D", "E", "F", "G"]],
    ["all", "valid", ["A", "C", "D", "F"]],
    ["all", "invalid", ["B", "E", "G"]],
    ["current", "all", ["A", "B", "D", "E", "F"]],
    ["current", "valid", ["A", "F"]],
    ["current", "invalid", ["B", "D", "E"]],
  ];
  for (const [scope, status, expected] of cases) {
    view.treeScope.value = scope;
    view.nodeStatus.value = status;
    await Vue.nextTick();
    assert.deepEqual(visibleIDs(view), expected, `${scope} + ${status}`);
    assert.deepEqual(Array.from(view.rows.value).filter(row => row.entry.kind === "folder").map(row => row.entry.id), ["parent", "child"]);
    assert.equal(view.isManagementFiltered.value, scope !== "all" || status !== "all");
  }
  view.props.kindFilter = "condition";
  await Vue.nextTick();
  assert.deepEqual(visibleIDs(view), ["D"]);
  view.currentTree.value = view.project.trees[1]!;
  await Vue.nextTick();
  assert.deepEqual(visibleIDs(view), [], "另一棵树中有效的条件不能留在当前树无效结果中");
  assert.equal(view.transactions.attempts, 0);
  assert.equal(stringifyJSON(view.project), before);
});

// 过滤期手动折叠只影响独立快照，定义选中和筛选切换均不得污染清除后的普通展开状态。
test("管理过滤可手动折叠且清除恢复普通展开和当前目录", async () => {
  const view = browser(["A", "B"]);
  view.props.presentation = "directories";
  view.index.move(view.entry("A"), "child");
  const tag = view.index.addTag("目标标签");
  view.index.setTags("A", [tag.id]);
  view.context.expanded.value = new Set([""]);
  view.context.filters.value = [tag.id];
  view.props.kindFilter = "action";
  view.props.search = "A";
  view.props.revision++;
  await Vue.nextTick();
  assert.deepEqual(visibleIDs(view), ["A"]);
  view.toggleFolder("child");
  await Vue.nextTick();
  assert.equal(view.visibleExpanded.value.has("child"), false);
  assert.deepEqual(visibleIDs(view), []);
  assert.equal(renderBrowser(view).some(node => node.type === "p" && nodeText(node) === "没有符合条件的业务定义。"), false, "手动折叠不能把仍有命中的树误报为空");
  view.toggleFolder("child");
  await Vue.nextTick();
  managedDefinition(view, "A").props!.onClick();
  await Vue.nextTick();
  assert.equal(view.context.selectedDefinition.value, "A");
  assert.equal(view.context.selectedFolder.value, "child");
  assert.deepEqual(Array.from(view.context.expanded.value), [""], "父级回传当前目录不能修改普通展开快照");
  view.props.search = "不存在";
  await Vue.nextTick();
  assert.equal(view.context.selectedFolder.value, "child", "筛选隐藏已选目录时仍保留新增和导入归属");
  const clear = renderBrowser(view).find(node => node.type === "button" && nodeText(node) === "清除全部筛选")!;
  clear.props!.onClick();
  await Vue.nextTick();
  assert.equal(view.props.search, "");
  assert.equal(view.props.kindFilter, "");
  assert.equal(view.context.filters.value.length, 0);
  assert.equal(view.treeScope.value, "all");
  assert.equal(view.nodeStatus.value, "all");
  assert.equal(view.context.selectedFolder.value, "child");
  assert.deepEqual(Array.from(view.visibleExpanded.value), [""]);
  assert.deepEqual(visibleIDs(view), ["B"]);
  assert.ok(Array.from(view.rows.value).some(row => row.entry.id === "parent"), "清除后恢复折叠目录入口");
  assert.equal(view.transactions.attempts, 0);
});

// 首轮初始化仍需展开深层当前目录的祖先，后续相同选择回显不能重新打开手动折叠的目录。
test("管理树首次继承深层目录并允许折叠当前目录", async () => {
  const view = browser(["A"], "child");
  view.props.presentation = "directories";
  view.index.move(view.entry("A"), "child");
  view.props.revision++;
  await Vue.nextTick();
  assert.equal(view.context.selectedFolder.value, "child");
  assert.ok(view.visibleExpanded.value.has("parent") && view.visibleExpanded.value.has("child"));
  assert.deepEqual(visibleIDs(view), ["A"]);
  view.toggleFolder("child");
  await Vue.nextTick();
  assert.equal(view.visibleExpanded.value.has("child"), false);
  assert.deepEqual(visibleIDs(view), []);
  assert.equal(view.context.selectedFolder.value, "child");
});

// 各类管理筛选都暂停分类拖排，真实事件入口必须拒绝提交；清除后原有排序仍可使用。
test("管理五类筛选都禁用分类拖排且清除后恢复", async () => {
  for (const filter of ["search", "kind", "tag", "scope", "status"]) {
    const view = browser();
    view.props.presentation = "directories";
    view.startDrag(dragEvent().event, view.entry("A"));
    view.dragOver(dragEvent().event, view.entry("parent"));
    assert.equal(view.context.dropHint.value, "in:parent");
    if (filter === "search") view.props.search = "A";
    if (filter === "kind") view.props.kindFilter = "action";
    if (filter === "tag") {
      const tag = view.index.addTag("拖排筛选");
      view.index.setTags("A", [tag.id]);
      view.context.filters.value = [tag.id];
    }
    if (filter === "scope") view.treeScope.value = "current";
    if (filter === "status") view.nodeStatus.value = "invalid";
    view.props.revision++;
    await Vue.nextTick();
    assert.equal(view.context.dragged.value, undefined, `${filter} 生效时应结束旧拖拽`);
    assert.equal(view.context.dropHint.value, "", `${filter} 生效时应清除旧落点提示`);
    const before = stringifyJSON(view.project);
    assert.equal(managedDefinition(view, "A").props!.draggable, false, `${filter} 应关闭行的拖动手势`);
    view.startDrag(dragEvent().event, view.entry("A"));
    const over = dragEvent();
    view.dragOver(over.event, view.entry("parent"));
    view.drop(dragEvent().event, view.entry("parent"));
    assert.equal(over.state.prevented, false);
    assert.equal(view.transactions.attempts, 0);
    assert.equal(view.emissions.some(item => item.name === "drag"), false);
    assert.equal(stringifyJSON(view.project), before);
    view.clearFilters();
    await Vue.nextTick();
    assert.equal(managedDefinition(view, "A").props!.draggable, true);
    view.startDrag(dragEvent().event, view.entry("A"));
    view.drop(dragEvent().event, view.entry("parent"));
    assert.equal(view.index.assignments.get("A")!.folderId, "parent");
    assert.equal(view.transactions.succeeded, 1);
  }
});

// 主内容、编辑和更多是兄弟按钮；两种图标与完整详情在普通树和筛选树中采用相同模板。
test("管理定义图标详情及兄弟操作按钮保持目标一致", async () => {
  for (const search of ["", "节点"]) {
    const view = browser(["A", "B"]);
    view.props.presentation = "directories";
    view.props.search = search;
    view.project.catalog[0]!.name = "动作节点";
    view.project.catalog[0]!.goName = "ActionDo";
    view.project.catalog[0]!.params = [{ name: "Target", type: "string" }];
    view.project.catalog[1]!.name = "条件节点";
    view.project.catalog[1]!.kind = "condition";
    view.project.catalog[1]!.goName = "ConditionReady";
    view.props.revision++;
    await Vue.nextTick();
    const nodes = renderBrowser(view);
    for (const [id, icon, goName, count] of [["A", "▶", "ActionDo", 1], ["B", "?", "ConditionReady", 0]] as const) {
      const main = managedDefinition(view, id);
      assert.ok(elements(main).some(node => node.type === "span" && nodeText(node) === icon));
      assert.match(nodeText(main), new RegExp(`ID[：:]\\s*${id}`));
      assert.match(nodeText(main), new RegExp(`Go\\s*函数[：:]\\s*${goName}`));
      assert.match(nodeText(main), new RegExp(`参数[：:]\\s*${count}`));
      const definition = view.project.catalog.find(item => item.id === id)!;
      // 选择同时包含主按钮和对应更多入口的最小容器，避免外层树误充当节点行。
      const row = nodes.filter(node => node.type === "div" && elements(node).some(child => child.props?.id === main.props!.id)
        && elements(node).some(child => child.props?.["aria-label"] === `${definition.name}的更多操作`))
        .sort((left, right) => elements(left).length - elements(right).length)[0]!;
      assert.ok(row);
      const rowButtons = elements(row).filter(node => node.type === "button");
      for (const button of rowButtons) assert.equal(elements(button).slice(1).some(child => child.type === "button"), false, "不能嵌套交互按钮");
      main.props!.onClick();
      const edit = rowButtons.find(node => nodeText(node) === "编辑")!;
      edit.props!.onClick(dragEvent().event);
      assert.deepEqual(view.emissions.filter(item => item.name === "inspect").slice(-2).map(item => item.args[0]), [definition, definition]);
      const more = rowButtons.find(node => node.props?.["aria-label"] === `${definition.name}的更多操作`)!;
      const event = dragEvent();
      more.props!.onClick(event.event);
      assert.equal(view.emissions.slice().reverse().find(item => item.name === "menu")!.args[1], definition);
      const rightClick = dragEvent();
      main.props!.onContextmenu(rightClick.event);
      assert.equal(view.emissions.slice().reverse().find(item => item.name === "menu")!.args[1], definition);
      const keyboard = Object.assign(dragEvent().event, { key: "F10", shiftKey: true });
      main.props!.onKeydown(keyboard);
      assert.equal(view.emissions.slice().reverse().find(item => item.name === "menu")!.args[1], definition);
      assert.equal(view.context.selectedDefinition.value, id);
    }
    assert.equal(view.emissions.some(item => item.name === "add" || item.name === "drag"), false);
    assert.equal(view.transactions.attempts, 0);
  }
});

// 管理页只在顶部由 Manager 提供范围和状态；Browser 开放标签折叠入口并避免重复下拉框。
test("管理树提供标签筛选且不重复渲染范围状态控件", async () => {
  const view = browser();
  view.props.presentation = "directories";
  const tag = view.index.addTag("筛选标签");
  view.props.revision++;
  await Vue.nextTick();
  let nodes = renderBrowser(view);
  assert.ok(nodes.some(node => node.type === "button" && nodeText(node) === "▸ 标签筛选（0）"));
  assert.equal(nodes.some(node => node.type === "select" && ["业务节点树范围", "业务节点状态"].includes(node.props?.["aria-label"])), false);
  view.context.filtersOpen.value = true;
  nodes = renderBrowser(view);
  assert.ok(nodes.some(node => node.type === "input" && node.props?.type === "checkbox" && node.props?.value === tag.id));
});

// 多个命中共享深层目录时，每个祖先只查一次父级；读取已缓存树及折叠状态不重新构造父链。
test("管理匹配父链按共享祖先去重并缓存结果", async (t) => {
  const depth = 64;
  const view = browser(Array.from({ length: 32 }, (_, index) => `Matched${index}`));
  view.props.presentation = "directories";
  let parent = "";
  for (let index = 0; index < depth; index++) {
    const id = `depth-${index}`;
    view.index.addFolder(`层级 ${index}`, parent, id);
    parent = id;
  }
  for (const definition of view.project.catalog) view.index.move(view.entry(definition.id), parent);
  view.props.search = "不存在";
  view.props.revision++;
  await Vue.nextTick();
  const reads = t.mock.method(view.index.folders, "get");
  view.props.search = "Matched";
  await Vue.nextTick();
  const matching = view.matchingTree.value;
  assert.ok(matching);
  assert.equal(matching.definitions.size, 32);
  assert.equal(Array.from(matching.folders).filter(id => id !== "").length, depth);
  assert.ok(reads.mock.callCount() <= depth + 1, "共享父链不能按每个定义重复扫描全部目录深度");
  const before = reads.mock.callCount();
  assert.equal(view.matchingTree.value, matching);
  assert.deepEqual(visibleIDs(view), view.project.catalog.map(definition => definition.id));
  assert.equal(view.matchingTree.value, matching);
  assert.equal(reads.mock.callCount(), before, "读取已缓存匹配集合和可见行不得重复爬祖先");
  view.filteredExpanded.value.delete("depth-0");
  assert.deepEqual(visibleIDs(view), []);
  assert.equal(view.matchingTree.value, matching);
  assert.equal(reads.mock.callCount(), before);
});
