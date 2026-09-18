import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref, shallowReactive, shallowRef } from "vue";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import type { CatalogEntry } from "./catalogOrganization.ts";
import { stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";

// 执行真实拖拽函数及位置索引 computed，不复制组件的落点计算实现。
const source = readFileSync(new URL("./CatalogBrowser.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("CatalogBrowser.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["startDrag", "endDrag", "dropLocation", "dragOver", "drop"]);
const values = new Set(["placements", "isFiltered"]);
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
    expanded: ref(new Set<string>([""])), emit: (name: string, ...args: unknown[]) => emissions.push({ name, args }),
  };
  const actions = runInNewContext(`${js}\n({ startDrag, endDrag, dropLocation, dragOver, drop });`, context) as {
    startDrag(event: DragEvent, entry: CatalogEntry): void;
    endDrag(): void;
    dropLocation(event: DragEvent, entry?: CatalogEntry): { folder: string; before?: CatalogEntry; hint: string };
    dragOver(event: DragEvent, entry?: CatalogEntry): void;
    drop(event: DragEvent, entry?: CatalogEntry): void;
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

// 定义落在目录主体时仅提交一次移动，停止冒泡并清理拖拽和画布临时状态。
test("定义拖入目录只移动一次并追加目录末尾", (t) => {
  const view = browser();
  const move = t.mock.method(view.index, "move");
  const start = dragEvent();
  view.startDrag(start.event, view.entry("A"));
  assert.equal(start.transfer.effectAllowed, "copyMove");
  assert.equal(start.data.get("application/x-behaviortree-catalog"), "definition:A");
  assert.equal(view.emissions[0]!.name, "drag");
  assert.equal(view.emissions[0]!.args[1], view.project.catalog[0]);
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
    assert.equal(view.emissions[0]!.name, "dragend");
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
    assert.equal(view.emissions[0]!.name, "drag");
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
