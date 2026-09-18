import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref, shallowReactive, shallowRef } from "vue";
import * as Vue from "vue";
import type { VNode } from "vue";
import { compile } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import type { CatalogEntry } from "./catalogOrganization.ts";
import { stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";

// 执行真实拖拽函数及位置索引 computed，不复制组件的落点计算实现。
const component = readFileSync(new URL("./CatalogBrowser.vue", import.meta.url), "utf8");
const source = component.split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
// 编译真实模板以覆盖末尾空白区域的事件绑定，避免函数测试漏掉无法触发的落点。
const renderCode = compile(parse(component).descriptor.template!.content, { mode: "function", expressionPlugins: ["typescript"] }).code;
const render = new Function("Vue", ts.transpileModule(renderCode, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText)(Vue);
const script = ts.createSourceFile("CatalogBrowser.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["startDrag", "endDrag", "dropLocation", "dragOver", "drop", "leaveDropTarget", "selectFolder", "selectDefinition", "inspectDefinition", "definitionMenu", "submitDialog"]);
const values = new Set(["placements", "isFiltered", "rows"]);
const workflow = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isVariableStatement(statement) && statement.declarationList.declarations.some(declaration => ts.isIdentifier(declaration.name) && values.has(declaration.name.text))),
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
function browser() {
  const project = blankProject();
  project.catalog = ["A", "B", "C"].map(id => ({ id, name: id, kind: "action", goName: id }));
  const index = new CatalogOrganizationIndex(project);
  index.addFolder("父目录", "", "parent");
  index.addFolder("子目录", "parent", "child");
  const emissions: { name: string; args: unknown[] }[] = [];
  const transactions = { attempts: 0, succeeded: 0, errors: [] as string[] };
  const props = shallowReactive({
    index, revision: 0, search: "", disabled: false,
    commit: (change: () => void) => {
      transactions.attempts++;
      try { change(); transactions.succeeded++; props.revision++; return true; }
      catch (cause) { transactions.errors.push(cause instanceof Error ? cause.message : String(cause)); return false; }
    },
  });
  const context = {
    computed, props, filters: ref<string[]>([]), dragged: shallowRef<CatalogEntry>(), dropHint: ref(""),
    selectedFolder: ref("parent"), selectedDefinition: ref(""), Node: class {},
    dialog: shallowRef<{ mode: string; id: string; parentId: string; entry: CatalogEntry }>(), failure: ref(""), nextTick: Vue.nextTick,
    expanded: ref(new Set<string>([""])), emit: (name: string, ...args: unknown[]) => emissions.push({ name, args }),
  };
  const actions = runInNewContext(`${js}\n({ startDrag, endDrag, dropLocation, dragOver, drop, leaveDropTarget, inspectDefinition, definitionMenu, submitDialog, rows });`, context) as {
    rows: { value: { entry: CatalogEntry; depth: number }[] };
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
  return { project, index, props, context, transactions, emissions, entry, ...actions };
}

// 扁平收集编译后的元素，事件回调仍来自组件原函数与原模板。
function elements(node: VNode): VNode[] {
  return [node, ...(Array.isArray(node.children) ? node.children.flatMap(child => child && typeof child === "object" ? elements(child as VNode) : []) : [])];
}

// 模拟实际库面板的渲染上下文，业务定义与目录取同一真实索引。
function renderBrowser(view: ReturnType<typeof browser>): VNode[] {
  return elements(render({
    ...view.props, ...view, rows: view.rows.value, expanded: view.context.expanded.value,
    selectedFolder: view.context.selectedFolder.value, selectedDefinition: view.context.selectedDefinition.value,
    dropHint: view.context.dropHint.value, isFiltered: Boolean(view.props.search), results: view.index.search(view.props.search),
    allTags: [], moreOpen: false, tagManager: false, folderMenu: undefined, dialog: undefined,
    endDrag: view.endDrag, emit: view.context.emit,
  }, []));
}

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
