import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import * as Vue from "vue";
import type { VNode } from "vue";
import { compile } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";

// 执行真实组件及模板，只替代浏览器原生模态和焦点边界，验证入口与取消语义。
const component = readFileSync(new URL("./CatalogContextMenu.vue", import.meta.url), "utf8");
const source = parse(component).descriptor;
const script = ts.createSourceFile("CatalogContextMenu.ts", source.scriptSetup!.content, ts.ScriptTarget.Latest, true);
const setup = script.statements.filter(statement => !ts.isImportDeclaration(statement)).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(setup, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const render = new Function("Vue", compile(source.template!.content, { mode: "function" }).code)(Vue);

// 模拟原生触发控件，记录取消后的焦点回到编辑页删除按钮。
class FocusTarget {
  isConnected = true; // 编辑页仍挂载时允许恢复焦点。
  focused = 0; // 实际调用焦点恢复的次数。
  focus() { this.focused++; } // 替代浏览器 HTMLElement.focus 边界。
}

// 使用真实 setup 状态和生命周期回调，捕获 showModal、close 与父级事件。
function menu(initialMode: "menu" | "delete") {
  const props = { definition: { id: "Move", name: "移动", kind: "action", goName: "Move" }, x: 40, y: 80,
    initialMode, referenceTrees: 2, referenceNodes: 3 };
  const trigger = new FocusTarget();
  const cancel = new FocusTarget();
  const lifecycle: { mounted?: () => void; unmount?: () => void } = {}; // 显式驱动原组件挂载与清理。
  const calls = { shown: 0, closed: 0 };
  const emitted: string[] = [];
  const nativeDialog = { showModal: () => { calls.shown++; }, close: () => { calls.closed++; } };
  const context = {
    props, ref: Vue.ref, nextTick: Vue.nextTick, HTMLElement: FocusTarget,
    defineProps: () => props, defineEmits: () => (name: string) => emitted.push(name),
    onMounted: (callback: () => void) => { lifecycle.mounted = callback; },
    onBeforeUnmount: (callback: () => void) => { lifecycle.unmount = callback; },
    document: { activeElement: trigger, addEventListener: () => {}, removeEventListener: () => {} },
    window: { innerWidth: 1000, innerHeight: 700, addEventListener: () => {}, removeEventListener: () => {} },
  };
  const state = runInNewContext(`${js}\n({ confirming, dialog, cancelButton, position, requestDelete, edit, organize, onMenuKeydown, emit });`, context) as {
    confirming: Vue.Ref<boolean>; // 真实菜单和确认切换状态。
    dialog: Vue.Ref<typeof nativeDialog | undefined>; // 原生对话框边界。
    cancelButton: Vue.Ref<FocusTarget | undefined>; // 默认焦点的取消按钮。
    requestDelete: () => Promise<void>; // 菜单内删除复用同一确认。
  };
  state.dialog.value = nativeDialog;
  state.cancelButton.value = cancel;
  const nodes = () => elements(render(Vue.proxyRefs({ ...props, ...state }), []));
  return { props, state, lifecycle, calls, emitted, trigger, cancel, nodes };
}

// 扁平收集真实渲染元素，以模板回调驱动取消和确认，不复制按钮处理逻辑。
function elements(node: VNode): VNode[] {
  return [node, ...(Array.isArray(node.children) ? node.children.flatMap(child => child && typeof child === "object" ? elements(child as VNode) : []) : [])];
}

// 编辑页直接挂载确认，主动打开原生模态、聚焦取消，取消后恢复触发按钮且不删除。
test("业务定义直接删除模式挂载确认并默认聚焦取消", () => {
  const view = menu("delete");
  view.lifecycle.mounted!();
  assert.equal(view.calls.shown, 1);
  assert.equal(view.cancel.focused, 1);
  const nodes = view.nodes();
  assert.equal(nodes.some(node => node.props?.role === "menu"), false);
  assert.equal(nodes.some(node => node.type === "dialog"), true);
  assert.equal(nodes.some(node => node.type === "p" && String(node.children).includes("2 棵树、3 个节点")), true);
  const cancel = nodes.find(node => node.type === "button" && node.children === "取消")!;
  cancel.props!.onClick();
  assert.deepEqual(view.emitted, ["close"]);
  view.lifecycle.unmount!();
  assert.equal(view.calls.closed, 1);
  assert.equal(view.trigger.focused, 1);
});

// 原菜单删除继续复用同一确认，打开确认和背景操作均不能提交，只有确认按钮发出删除。
test("业务定义菜单删除仍须显式确认", async () => {
  const view = menu("menu");
  view.lifecycle.mounted!();
  assert.equal(view.calls.shown, 0);
  assert.equal(view.nodes().some(node => node.props?.role === "menu"), true);
  await view.state.requestDelete();
  assert.equal(view.calls.shown, 1);
  assert.deepEqual(view.emitted, []);
  const confirm = view.nodes().find(node => node.type === "button" && node.children === "确认删除")!;
  confirm.props!.onClick();
  assert.deepEqual(view.emitted, ["delete"]);
  view.trigger.isConnected = false;
  view.lifecycle.unmount!();
  assert.equal(view.trigger.focused, 0, "删除后的编辑按钮已卸载，不应恢复到悬空控件");
});
