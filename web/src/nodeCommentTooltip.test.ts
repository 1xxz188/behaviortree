import test from "node:test";
import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, effectScope, markRaw, nextTick, reactive, ref, shallowRef, watch } from "vue";
import type { Ref } from "vue";
import type { BTNode, Tree } from "./project.ts";
import { kinds } from "./project.ts";

// 执行真实组件脚本，只替换浏览器与计时边界，不复制浮层状态实现。
const source = readFileSync(new URL("./NodeCommentTooltip.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const parsed = ts.createSourceFile("NodeCommentTooltip.ts", source, ts.ScriptTarget.Latest, true);
const script = parsed.statements.filter(statement => !ts.isImportDeclaration(statement))
  .map(statement => statement.getText(parsed)).join("\n");
const js = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// DOM 替身只提供组件读取的元素关系、悬浮状态、焦点和边界。
class TestElement {
  parent?: TestElement; // 当前元素的父级，模拟 contains 与 closest。
  hovered = false; // 浏览器 :hover 状态。
  toggle = false; // 是否为节点顶部注释按钮。
  isConnected = true; // 节点是否仍在文档中。
  scrollTop = 0; // 浮层重新打开时恢复滚动位置。
  capturedPointers = new Set<number>(); // 拖动尺寸时持有的指针捕获。
  boundsReads = 0; // 统计边界读取，保证移动期间不重复测量 DOM。
  focus = () => {}; // 场景初始化时接入文档焦点。
  bounds = { left: 100, right: 220, top: 100, bottom: 180, width: 120, height: 80 }; // 元素视口尺寸。

  // 真实 DOM 不会被 Vue 代理，替身也保留对象身份关系。
  constructor() { markRaw(this); }

  // 按父链判断包含关系，支持文本输入和按钮作为浮层内部目标。
  contains(other: unknown): boolean {
    return other instanceof TestElement && (other === this || this.contains(other.parent));
  }

  // 模拟组件所需的当前悬浮状态。
  matches(selector: string): boolean { return selector === ":hover" && this.hovered; }

  // 返回最近的注释按钮，防止全局捕获事件提前关闭待切换的浮层。
  closest(selector: string): TestElement | null {
    return selector === ".node-comment-toggle" && this.toggle ? this : this.parent?.closest(selector) ?? null;
  }

  // 返回可控边界，让真实浮层定位代码正常执行。
  getBoundingClientRect() { this.boundsReads++; return this.bounds; }

  // 模拟拖动手柄捕获指针，允许指针移出浮层后继续调整。
  setPointerCapture(id: number) { this.capturedPointers.add(id); }

  // 查询停止或关闭时需要释放的捕获。
  hasPointerCapture(id: number) { return this.capturedPointers.has(id); }

  // 结束调整后恢复正常鼠标交互。
  releasePointerCapture(id: number) { this.capturedPointers.delete(id); }
}

// 尺寸拖动事件只保留真实组件读取的字段。
interface ResizeEvent {
  pointerId: number; // 当前捕获指针身份。
  clientX: number; // 视口横坐标。
  clientY: number; // 视口纵坐标。
  button: number; // 只允许主按钮开始调整。
  currentTarget: TestElement; // 触发拖动的手柄。
}

// 组件状态只供断言读取，交互通过真实事件处理函数发起。
interface Popover {
  target: Ref<BTNode | undefined>; // 当前编辑目标。
  tooltip: Ref<TestElement | undefined>; // 浮层 DOM 引用。
  commentInput: Ref<TestElement | undefined>; // 文本输入 DOM 引用。
  draft: Ref<string>; // 不直接写回工程的注释草稿。
  dirty: Ref<boolean>; // 应用和取消按钮的展示条件。
  pinned: Ref<boolean>; // 顶部按钮显式展开状态。
  activeNode: Ref<BTNode | undefined>; // 任意方式展开时用于隐藏重复编辑气泡的目标。
  title: Ref<string>; // 与节点画布展示规则一致的浮层标题。
  size: Ref<{ width: number; height: number }>; // 用户拖动后的浮层尺寸。
  position: Ref<{ left: number; top: number }>; // 计算可用视口空间的浮层起点。
  maximized: Ref<boolean>; // 当前是否使用视口内的最大尺寸。
  show(node: BTNode, element: TestElement): Promise<void>; // 被动悬浮入口。
  edit(node: BTNode, element: TestElement): Promise<void>; // 显式编辑只打开并聚焦，不反向关闭。
  hide(): void; // 暂时关闭并保留草稿。
  reset(): void; // 树替换时清理旧草稿。
  scheduleHide(event?: { relatedTarget?: TestElement }): void; // 节点或浮层离开事件。
  retain(): void; // 移入浮层取消待关闭任务。
  cancel(): void; // 放弃草稿并保留浮层。
  apply(): void; // 提交至工程历史入口。
  onPointerDown(event: { target: TestElement }): void; // 文档捕获阶段指针事件。
  onKeydown(event: { key: string; preventDefault(): void; stopPropagation(): void }): void; // Escape 草稿保护。
  startResize(event: ResizeEvent): void; // 开始捕获用于调整尺寸的指针。
  resize(event: ResizeEvent): void; // 根据捕获指针移动调整尺寸。
  stopResize(event?: ResizeEvent): void; // 结束拖动并释放捕获。
  toggleMaximized(): Promise<void>; // 切换全屏和普通尺寸并保留输入。
}

// 每个测试独立持有真实响应式属性、可控一次性计时器及提交记录。
function session(t: TestContext) {
  const first: BTNode = { id: "one", type: "wait", comment: "原始说明" };
  const second: BTNode = { id: "two", type: "wait", comment: "另一节点" };
  const tree: Tree = { id: "tree", name: "测试树", root: first.id, nodes: [first, second] };
  const commits: Array<{ tree: Tree; node: BTNode; value: string }> = [];
  let accepted = true;
  const props = reactive({
    tree, disabled: false, autoOpen: true,
    // 模拟父组件校验和提交，更新真实目标后返回是否接受。
    commit(tree: Tree, node: BTNode, value: string) {
      commits.push({ tree, node, value });
      if (!accepted) return false;
      if (value) node.comment = value;
      else delete node.comment;
      return true;
    },
  });
  const timers = new Map<number, () => void>();
  let timerID = 0;
  const mounted: Array<() => void> = [];
  const unmounted: Array<() => void> = [];
  const document = { activeElement: undefined as TestElement | undefined, addEventListener() {}, removeEventListener() {} };
  const scope = effectScope();
  const names = "target, tooltip, commentInput, draft, dirty, pinned, activeNode, title, size, position, maximized, show, edit, hide, reset, scheduleHide, retain, cancel, apply, onPointerDown, onKeydown, startResize, resize, stopResize, toggleMaximized";
  const app = scope.run(() => runInNewContext(`${js}\n({ ${names} });`, {
    computed, nextTick, reactive, ref, shallowRef, watch, kinds,
    defineProps: () => props, defineExpose: () => {},
    onMounted: (callback: () => void) => mounted.push(callback),
    onBeforeUnmount: (callback: () => void) => unmounted.push(callback),
    document, window: { innerWidth: 1024, innerHeight: 768, addEventListener() {}, removeEventListener() {} },
    Node: TestElement, Element: TestElement, HTMLElement: TestElement,
    setTimeout: (callback: () => void) => { const id = ++timerID; timers.set(id, callback); return id; },
    clearTimeout: (id: number) => timers.delete(id),
  })) as Popover;
  const anchor = new TestElement();
  const panel = new TestElement();
  const input = new TestElement();
  input.parent = panel;
  input.focus = () => { document.activeElement = input; };
  app.tooltip.value = panel;
  app.commentInput.value = input;
  for (const callback of mounted) callback();
  t.after(() => { for (const callback of unmounted) callback(); scope.stop(); });
  return {
    app, props, anchor, panel, input, document, commits, timers,
    first: props.tree.nodes[0]!, second: props.tree.nodes[1]!,
    rejectCommit: () => { accepted = false; },
    // 只执行当次已排入的任务，模拟一次宽限到期，不进行轮询。
    expire: async () => { const pending = [...timers.values()]; timers.clear(); for (const callback of pending) callback(); await nextTick(); },
  };
}

// 移入浮层先于节点离开时，relatedTarget 和实时悬浮均阻止错误关闭。
test("跨越节点与浮层边界不会被旧离开任务关闭", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  s.app.retain();
  s.app.scheduleHide({ relatedTarget: s.input });
  await s.expire();
  assert.equal(s.app.target.value, s.first);
  s.app.scheduleHide();
  assert.equal(s.timers.size, 1);
  s.app.retain();
  await s.expire();
  assert.equal(s.app.target.value, s.first);
  s.panel.hovered = true;
  s.app.scheduleHide();
  await s.expire();
  assert.equal(s.app.target.value, s.first);
  s.panel.hovered = false;
  s.app.scheduleHide();
  await s.expire();
  assert.equal(s.app.target.value, undefined);
});

// 输入和取消只修改草稿；提交才写入节点，并保留内部换行和清空语义。
test("浮层草稿支持取消、应用及清空且提交前不修改节点", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  assert.equal(s.app.dirty.value, false);
  s.app.apply();
  assert.equal(s.commits.length, 0);
  s.app.draft.value = "未提交说明";
  assert.equal(s.app.dirty.value, true);
  assert.equal(s.first.comment, "原始说明");
  s.app.cancel();
  assert.equal(s.app.draft.value, "原始说明");
  assert.equal(s.app.dirty.value, false);
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.commits.length, 0);
  s.app.draft.value = "  第一行\n第二行  ";
  s.app.apply();
  await nextTick();
  assert.equal(s.first.comment, "第一行\n第二行");
  assert.equal(s.app.dirty.value, false);
  assert.equal(s.commits.length, 1);
  assert.equal(s.commits[0]!.tree, s.props.tree);
  assert.equal(s.commits[0]!.node, s.first);
  s.app.draft.value = " \n ";
  s.app.apply();
  await nextTick();
  assert.equal(Object.hasOwn(s.first, "comment"), false);
  assert.equal(s.app.dirty.value, false);
});

// 草稿、输入焦点和显式展开分别保证停留编辑时不会被悬浮关闭任务打断。
test("编辑或固定展开期间离开鼠标也不会丢失注释", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  s.document.activeElement = s.input;
  s.app.scheduleHide();
  await s.expire();
  assert.equal(s.app.target.value, s.first);
  s.document.activeElement = undefined;
  s.app.draft.value = "保留的草稿";
  s.app.scheduleHide();
  await s.expire();
  s.app.onPointerDown({ target: new TestElement() });
  await s.app.show(s.second, new TestElement());
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.app.draft.value, "保留的草稿");
  s.app.cancel();
  await s.app.edit(s.first, s.anchor);
  s.app.scheduleHide();
  await s.expire();
  await s.app.show(s.second, new TestElement());
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.app.pinned.value, true);
});

// 顶部按钮先经历文档捕获事件，再打开编辑；手动关闭后再次编辑恢复未提交草稿。
test("顶部按钮支持空注释编辑且关闭后重新打开保留草稿", async t => {
  const s = session(t);
  delete s.first.comment;
  await s.app.show(s.first, s.anchor);
  assert.equal(s.app.target.value, undefined);
  const button = new TestElement();
  button.toggle = true;
  s.app.onPointerDown({ target: button });
  await s.app.edit(s.first, s.anchor);
  assert.equal(s.app.activeNode.value, s.first);
  s.app.draft.value = "暂存说明";
  s.app.hide();
  assert.equal(s.app.target.value, undefined);
  assert.equal(s.app.activeNode.value, undefined);
  assert.equal(s.first.comment, undefined);
  await s.app.edit(s.first, s.anchor);
  assert.equal(s.app.draft.value, "暂存说明");
  assert.equal(s.app.dirty.value, true);
});

// Escape 先撤销未提交输入，再关闭只读浮层，且不产生任何父级提交。
test("Escape 优先取消草稿然后关闭浮层", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  s.app.draft.value = "修改未提交";
  const event = { key: "Escape", preventDefault() {}, stopPropagation() {} };
  s.app.onKeydown(event);
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.app.draft.value, "原始说明");
  s.app.onKeydown(event);
  assert.equal(s.app.target.value, undefined);
  assert.equal(s.commits.length, 0);
});

// 树替换使旧草稿失效，父级拒绝过期提交时不能将草稿当作已保存内容。
test("树重置清除草稿并正确处理父级拒绝提交", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  s.app.draft.value = "旧工程草稿";
  s.app.hide();
  s.props.tree = { ...s.props.tree, id: "new-tree" };
  await s.app.show(s.first, s.anchor);
  assert.equal(s.app.draft.value, "原始说明");
  s.rejectCommit();
  s.app.draft.value = "不可提交";
  s.app.apply();
  assert.equal(s.first.comment, "原始说明");
  assert.equal(s.app.target.value, undefined);
  assert.equal(s.commits.length, 1);
});

// 切换目标时不能把上一节点正文误当作当前缓存草稿的干净基准。
test("切换节点保留恰好等于上一节点正文的缓存草稿", async t => {
  const s = session(t);
  await s.app.show(s.second, s.anchor);
  s.app.draft.value = s.first.comment!;
  s.app.hide();
  await s.app.show(s.first, s.anchor);
  await nextTick();
  await s.app.edit(s.second, s.anchor);
  await nextTick();
  assert.equal(s.app.target.value, s.second);
  assert.equal(s.app.draft.value, s.first.comment);
  assert.equal(s.app.dirty.value, true);
  assert.equal(s.second.comment, "另一节点");
  assert.equal(s.commits.length, 0);
});

// 同一节点从右键等入口更新时，干净正文同步刷新，未应用的输入保持不变。
test("同一节点外部修改仅同步无草稿的浮层", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  s.first.comment = "外部更新说明";
  await nextTick();
  assert.equal(s.app.draft.value, "外部更新说明");
  assert.equal(s.app.dirty.value, false);
  s.app.draft.value = "用户待提交草稿";
  s.first.comment = "再次外部更新说明";
  await nextTick();
  assert.equal(s.app.draft.value, "用户待提交草稿");
  assert.equal(s.app.dirty.value, true);
  assert.equal(s.commits.length, 0);
});

// 关闭自动悬浮只影响被动阅读，显式编辑仍可打开空注释并连续聚焦。
test("关闭自动悬浮后仍可手动打开且重复编辑不收起", async t => {
  const s = session(t);
  s.props.autoOpen = false;
  await s.app.show(s.first, s.anchor);
  assert.equal(s.app.target.value, undefined);
  delete s.first.comment;
  await s.app.edit(s.first, s.anchor);
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.app.activeNode.value, s.first);
  assert.equal(s.app.pinned.value, true);
  assert.equal(s.document.activeElement, s.input);
  s.app.draft.value = "手动输入草稿";
  s.document.activeElement = undefined;
  await s.app.edit(s.first, s.anchor);
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.app.draft.value, "手动输入草稿");
  assert.equal(s.document.activeElement, s.input);
  assert.equal(s.commits.length, 0);
});

// 被动展示同样报告活动节点，标题使用节点自定义名称或内置类型名称。
test("悬浮报告活动节点并使用与画布一致的节点标题", async t => {
  const s = session(t);
  s.first.name = "自定义等待节点";
  await s.app.show(s.first, s.anchor);
  assert.equal(s.app.activeNode.value, s.first);
  assert.equal(s.app.pinned.value, false);
  assert.equal(s.app.title.value, "自定义等待节点");
  s.first.name = "";
  assert.equal(s.app.title.value, kinds.wait.label);
  s.app.hide();
  assert.equal(s.app.activeNode.value, undefined);
});

// 尺寸拖动只认捕获指针，依据初始测量计算增量，并限制最小尺寸和视口边界。
test("拖动浮层尺寸保持指针身份并限制边界且不重复测量", async t => {
  const s = session(t);
  s.panel.bounds = { left: 232, right: 552, top: 100, bottom: 340, width: 320, height: 240 };
  await s.app.show(s.first, s.anchor);
  const handle = new TestElement();
  const event: ResizeEvent = { pointerId: 7, clientX: 550, clientY: 340, button: 0, currentTarget: handle };
  s.app.startResize(event);
  assert.equal(handle.hasPointerCapture(7), true);
  assert.equal(s.app.pinned.value, true);
  const reads = s.panel.boundsReads;
  const before = { ...s.app.size.value };
  s.app.resize({ ...event, pointerId: 8, clientX: 650 });
  assert.deepEqual({ ...s.app.size.value }, before);
  s.app.resize({ ...event, clientX: 630, clientY: 400 });
  assert.deepEqual({ ...s.app.size.value }, { width: 400, height: 300 });
  s.app.resize({ ...event, clientX: -1000, clientY: -1000 });
  assert.deepEqual({ ...s.app.size.value }, { width: 240, height: 180 });
  s.app.resize({ ...event, clientX: 10000, clientY: 10000 });
  assert.equal(s.app.size.value.width + s.app.position.value.left + 8, 1024);
  assert.equal(s.app.size.value.height + s.app.position.value.top + 8, 768);
  assert.equal(s.panel.boundsReads, reads);
  s.app.stopResize({ ...event, pointerId: 8 });
  assert.equal(handle.hasPointerCapture(7), true);
  s.app.stopResize(event);
  assert.equal(handle.hasPointerCapture(7), false);
  const finished = { ...s.app.size.value };
  s.app.resize({ ...event, clientX: 650 });
  assert.deepEqual({ ...s.app.size.value }, finished);
  assert.equal(s.commits.length, 0);
});

// 非主按钮和禁用状态不开始拖动，关闭浮层必须清理已捕获的指针。
test("尺寸拖动拒绝非法起点且浮层关闭释放捕获", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  const handle = new TestElement();
  const event: ResizeEvent = { pointerId: 3, clientX: 220, clientY: 180, button: 0, currentTarget: handle };
  s.app.startResize({ ...event, button: 2 });
  assert.equal(handle.hasPointerCapture(3), false);
  s.props.disabled = true;
  s.app.startResize(event);
  assert.equal(handle.hasPointerCapture(3), false);
  s.props.disabled = false;
  s.app.startResize(event);
  assert.equal(handle.hasPointerCapture(3), true);
  s.app.hide();
  assert.equal(handle.hasPointerCapture(3), false);
  assert.equal(s.app.activeNode.value, undefined);
});

// 全屏只是临时改变展示范围，普通尺寸、待应用文字和工程内容均保持不变。
test("全屏和还原保留草稿及普通尺寸且全屏禁止拖动尺寸", async t => {
  const s = session(t);
  await s.app.show(s.first, s.anchor);
  s.app.size.value = { width: 440, height: 300 };
  s.app.draft.value = "全屏编辑草稿";
  await s.app.toggleMaximized();
  await nextTick();
  assert.equal(s.app.maximized.value, true);
  assert.equal(s.app.pinned.value, true);
  assert.deepEqual({ ...s.app.position.value }, { left: 8, top: 8 });
  const handle = new TestElement();
  s.app.startResize({ pointerId: 11, clientX: 440, clientY: 300, button: 0, currentTarget: handle });
  assert.equal(handle.hasPointerCapture(11), false);
  await s.app.toggleMaximized();
  await nextTick();
  assert.equal(s.app.maximized.value, false);
  assert.deepEqual({ ...s.app.size.value }, { width: 440, height: 300 });
  assert.equal(s.app.draft.value, "全屏编辑草稿");
  assert.equal(s.app.dirty.value, true);
  assert.equal(s.first.comment, "原始说明");
  assert.equal(s.commits.length, 0);
});

// Escape 优先退出全屏，保留草稿继续编辑；关闭窗口不能把全屏状态带到下次打开。
test("Escape 先还原全屏不取消草稿且关闭重置全屏状态", async t => {
  const s = session(t);
  await s.app.edit(s.first, s.anchor);
  s.app.draft.value = "需要保留的文字";
  await s.app.toggleMaximized();
  s.app.onKeydown({ key: "Escape", preventDefault() {}, stopPropagation() {} });
  await nextTick();
  assert.equal(s.app.maximized.value, false);
  assert.equal(s.app.target.value, s.first);
  assert.equal(s.app.draft.value, "需要保留的文字");
  assert.equal(s.app.dirty.value, true);
  assert.equal(s.commits.length, 0);
  await s.app.toggleMaximized();
  s.app.hide();
  assert.equal(s.app.maximized.value, false);
  await s.app.edit(s.first, s.anchor);
  assert.equal(s.app.maximized.value, false);
  assert.equal(s.app.draft.value, "需要保留的文字");
});
