<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";

// 注释目标仅以对象身份跟踪；实际字段由调用方读取与提交。
export type CommentTarget = object;
const props = withDefaults(defineProps<{
  context: object; // 工程或树替换时重置旧对象草稿。
  disabled: boolean; // 菜单、对话框或工程切换期间不展示说明。
  autoOpen: boolean; // 仅控制鼠标悬浮，手动入口始终可用。
  commit: (target: CommentTarget, value: string) => boolean; // 调用方复核目标身份并记录历史。
  getTitle: (target: CommentTarget) => string; // 浮层标题。
  getComment: (target: CommentTarget) => string | undefined; // 读取节点或事件注释。
  label: string; // 文本框的无障碍标签和占位符。
  tooltipId: string; // 与入口 aria-controls 对应。
  allowEmptyOnHover?: boolean; // 事件可在空注释时悬停创建说明。
}>(), { allowEmptyOnHover: false });
const target = shallowRef<CommentTarget>(); // 仅保留当前悬浮目标。
const anchor = shallowRef<HTMLElement>(); // 当前入口元素，用于一次性测量视口坐标。
const tooltip = ref<HTMLElement>(); // 挂载到 body 的唯一说明浮层。
const commentInput = ref<HTMLTextAreaElement>(); // 气泡打开时聚焦，悬浮打开时不抢焦点。
const bridgeSide = ref<"left" | "right">("left"); // 透明跨缝区域朝向节点的一侧。
const visible = ref(false); // 当前浮层是否展开。
const pinned = ref(false); // 气泡显式打开后，移出鼠标也保持显示。
const draft = ref(""); // 编辑草稿与工程隔离，只有应用才提交。
const error = ref(""); // 校验失败时保留草稿并展示原因。
const dirty = computed(() => !!target.value && draft.value !== (props.getComment(target.value) ?? ""));
const activeTarget = computed(() => visible.value ? target.value : undefined); // 调用方可据此隐藏入口气泡。
const title = computed(() => target.value ? props.getTitle(target.value) : ""); // 标题由调用方按目标类型确定。
const size = ref({ width: 360, height: 260 }); // 本次页面会话复用用户调整后的尺寸。
const maximized = ref(false); // 全屏编辑复用原草稿，普通浮层尺寸单独保留。
const arrowTop = ref(32); // 指向节点的箭头在浮层侧边上的纵向位置。
// 一次右下角拖拽只保存起点，不扫描其他节点或轮询 DOM。
interface ResizeState {
  pointerID: number; // 限定当前拖拽指针。
  x: number; // 指针起始横坐标。
  y: number; // 指针起始纵坐标。
  width: number; // 拖拽开始时的实际宽度。
  height: number; // 拖拽开始时的实际高度。
  handle: HTMLElement; // 持有指针捕获的右下角控件。
}
const resizeState = shallowRef<ResizeState>(); // 拖动期间阻止自动收起。
let drafts = new WeakMap<CommentTarget, string>(); // 收起后按对象身份保留草稿，不额外遍历节点。
const position = ref({ left: 0, top: 0 }); // 经视口边界裁剪后的坐标。
const positioned = ref(false); // 测量完成前隐藏，避免首帧在左上角闪现。
let closeTimer: ReturnType<typeof setTimeout> | undefined; // 一次性离开宽限，允许跨越节点和浮层之间的间隙。

// 节点和浮层统一使用鼠标事件，防止 pointerenter 后又被 mouseleave 安排关闭。
function retain() {
  if (closeTimer !== undefined) clearTimeout(closeTimer);
  closeTimer = undefined;
}

// 显式收起及临时禁用只隐藏浮层，未应用内容留到同一节点再次打开。
function hide() {
  retain();
  stopResize();
  maximized.value = false;
  if (target.value) {
    if (dirty.value) drafts.set(target.value, draft.value);
    else drafts.delete(target.value);
  }
  visible.value = false;
  pinned.value = false;
  error.value = "";
  target.value = undefined;
  anchor.value = undefined;
  positioned.value = false;
}

// 树替换或历史恢复后不复用旧对象的草稿。
function reset() {
  hide();
  drafts = new WeakMap();
}

// 仅在打开和窗口尺寸变化时测量两个元素，优先放到节点右侧。
function fit() {
  if (!visible.value) return;
  if (!anchor.value?.isConnected || !tooltip.value) return hide();
  if (maximized.value) {
    position.value = { left: 8, top: 8 };
    positioned.value = true;
    return;
  }
  const nodeBounds = anchor.value.getBoundingClientRect();
  const bounds = tooltip.value.getBoundingClientRect();
  const preferredLeft = nodeBounds.right + 12;
  const onRight = preferredLeft + bounds.width <= window.innerWidth - 8;
  bridgeSide.value = onRight ? "left" : "right";
  position.value = {
    left: Math.max(8, Math.min(onRight
      ? preferredLeft : nodeBounds.left - bounds.width - 12, window.innerWidth - bounds.width - 8)),
    top: Math.max(8, Math.min(nodeBounds.top, window.innerHeight - bounds.height - 8)),
  };
  arrowTop.value = Math.max(22, Math.min(nodeBounds.top + nodeBounds.height / 2 - position.value.top, bounds.height - 22));
  positioned.value = true;
}

// 打开时恢复该对象草稿；异步测量前复核目标，避免已收起后重新出现。
async function open(node: CommentTarget, element: HTMLElement, pin: boolean) {
  if (target.value !== node) {
    hide();
    target.value = node;
    draft.value = drafts.get(node) ?? props.getComment(node) ?? "";
    error.value = "";
  }
  retain();
  anchor.value = element;
  pinned.value = pin;
  visible.value = true;
  positioned.value = false;
  await nextTick();
  if (target.value !== node || anchor.value !== element || !visible.value) return;
  fit();
  // 定位后的可见样式写入 DOM 再聚焦，隐藏状态下浏览器会忽略 focus。
  if (pin) {
    await nextTick();
    if (target.value === node && anchor.value === element && visible.value && pinned.value) commentInput.value?.focus();
  }
}

// 被动悬浮遵循开关；显式信息入口可忽略开关，但不能覆盖正在编辑的目标。
function show(node: CommentTarget, element: HTMLElement, explicit = false) {
  if (props.disabled || (!props.autoOpen && !explicit)) return;
  retain();
  if (visible.value && (pinned.value || dirty.value || tooltip.value?.contains(document.activeElement))) return;
  if (!props.allowEmptyOnHover && !props.getComment(node)?.trim()) return hide();
  return open(node, element, false);
}

// 气泡和右键菜单共用显式编辑入口，不受自动悬浮开关影响。
function edit(node: CommentTarget, element: HTMLElement) {
  if (props.disabled) return;
  return open(node, element, true);
}

// 全屏与还原只切换布局，保留未应用输入、焦点及用户调整的普通窗口尺寸。
async function toggleMaximized() {
  stopResize();
  retain();
  maximized.value = !maximized.value;
  pinned.value = true;
  await nextTick();
  if (visible.value) fit();
}

// 右下角按住后捕获指针，移出浮层仍能完成拖拽且不会启动画布拖动。
function startResize(event: PointerEvent) {
  if (props.disabled || maximized.value || event.button !== 0 || !tooltip.value || !(event.currentTarget instanceof HTMLElement)) return;
  const bounds = tooltip.value.getBoundingClientRect();
  retain();
  pinned.value = true;
  resizeState.value = { pointerID: event.pointerId, x: event.clientX, y: event.clientY,
    width: bounds.width, height: bounds.height, handle: event.currentTarget };
  event.currentTarget.setPointerCapture(event.pointerId);
}

// 拖动中固定左上角，仅更新宽高；尺寸不得超出可用视口。
function resize(event: PointerEvent) {
  const state = resizeState.value;
  if (!state || event.pointerId !== state.pointerID) return;
  size.value = {
    width: Math.min(Math.max(240, state.width + event.clientX - state.x), window.innerWidth - position.value.left - 8),
    height: Math.min(Math.max(180, state.height + event.clientY - state.y), window.innerHeight - position.value.top - 8),
  };
  arrowTop.value = Math.min(arrowTop.value, size.value.height - 22);
}

// 结束、取消或失去捕获均清理拖拽；完成后重新对齐节点和指向箭头。
function stopResize(event?: PointerEvent) {
  const state = resizeState.value;
  if (!state || (event && event.pointerId !== state.pointerID)) return;
  resizeState.value = undefined;
  if (state.handle.hasPointerCapture(state.pointerID)) state.handle.releasePointerCapture(state.pointerID);
  void nextTick(() => { if (visible.value) fit(); });
}

// 焦点或鼠标仍在浮层内，以及存在草稿或显式固定时，不自动收起。
function keepOpen(): boolean {
  return !!resizeState.value || pinned.value || dirty.value || !!tooltip.value?.contains(document.activeElement)
    || !!tooltip.value?.matches(":hover") || !!anchor.value?.matches(":hover");
}

// 节点和浮层互相移动不会关闭；离开两者后再给一次宽限，回调复核交互状态。
function scheduleHide(event?: MouseEvent) {
  retain();
  const next = event?.relatedTarget;
  if (next instanceof Node && (tooltip.value?.contains(next) || anchor.value?.contains(next))) return;
  if (keepOpen()) return;
  closeTimer = setTimeout(() => { if (!keepOpen()) hide(); }, 500);
}

// 取消只撤回草稿，保留浮层以便继续阅读或重新编辑。
function cancel() {
  if (!target.value) return;
  draft.value = props.getComment(target.value) ?? "";
  drafts.delete(target.value);
  error.value = "";
}

// 应用复用工程提交边界；失败时关闭失效对象，成功后恢复已提交的规范化文本。
function apply() {
  if (props.disabled || !target.value || !dirty.value) return;
  const node = target.value;
  try {
    if (!props.commit(node, draft.value.trim())) return hide();
  } catch (cause) {
    error.value = cause && typeof cause === "object" && "message" in cause
      ? String(cause.message) : String(cause);
    return;
  }
  drafts.delete(node);
  draft.value = props.getComment(node) ?? "";
  error.value = "";
}

// 捕获阶段排除气泡入口；外部点击不丢弃编辑内容。
function onPointerDown(event: PointerEvent) {
  if (event.target instanceof Node && (tooltip.value?.contains(event.target) || anchor.value?.contains(event.target))) return;
  if (event.target instanceof Element && event.target.closest(".node-comment-toggle")) return;
  if (!dirty.value) hide();
}

// Escape 优先退出全屏，其次撤销草稿，最后收起；不干扰画布快捷键。
function onKeydown(event: KeyboardEvent) {
  if (event.key !== "Escape" || !visible.value) return;
  event.preventDefault();
  event.stopPropagation();
  if (maximized.value) void toggleMaximized();
  else if (dirty.value) cancel();
  else hide();
}

// 输入框失焦后，待焦点转移完成再判断是否离开整个浮层。
async function onFocusOut() {
  await nextTick();
  if (visible.value) scheduleHide();
}

watch(() => props.context, reset, { flush: "sync" });
watch(() => props.disabled, disabled => { if (disabled) hide(); });
watch(() => props.autoOpen, enabled => { if (!enabled && !pinned.value) hide(); });
// 仅同步同一目标的外部提交，切换目标时不能用旧正文覆盖恢复的草稿。
watch(() => [target.value, target.value ? props.getComment(target.value) : undefined] as const, ([node, comment], [previousNode, previous]) => {
  if (node && node === previousNode && draft.value === (previous ?? "")) draft.value = comment ?? "";
});
watch(dirty, async () => { await nextTick(); if (visible.value) fit(); });
onMounted(() => {
  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("keydown", onKeydown, true);
  window.addEventListener("blur", hide);
  window.addEventListener("resize", fit);
});
onBeforeUnmount(() => {
  reset();
  document.removeEventListener("pointerdown", onPointerDown, true);
  document.removeEventListener("keydown", onKeydown, true);
  window.removeEventListener("blur", hide);
  window.removeEventListener("resize", fit);
});

defineExpose({ show, edit, hide, scheduleHide, activeTarget });
</script>

<template>
  <Teleport to="body">
    <section v-if="visible && target" :id="tooltipId" ref="tooltip" class="node-comment-tooltip"
      :class="[`bridge-${bridgeSide}`, { 'is-maximized': maximized }]" role="dialog" :aria-labelledby="`${tooltipId}-heading`"
      :style="{ left: maximized ? '8px' : `${position.left}px`, top: maximized ? '8px' : `${position.top}px`, width: maximized ? 'calc(100vw - 16px)' : `${size.width}px`, height: maximized ? 'calc(100vh - 16px)' : `${size.height}px`, '--arrow-top': `${arrowTop}px`, visibility: positioned ? 'visible' : 'hidden' }"
      @mouseenter="retain" @mouseleave="scheduleHide" @focusin="retain" @focusout="onFocusOut"
      @wheel.stop @pointerdown.stop @keydown.stop @contextmenu.stop>
      <header><strong :id="`${tooltipId}-heading`" :title="title">{{ title }}</strong>
        <div class="comment-window-actions">
          <button type="button" class="comment-close" :aria-label="maximized ? '还原注释窗口' : '全屏查看与编辑'"
            :title="maximized ? '还原注释窗口' : '全屏查看与编辑'" :aria-pressed="maximized" @click="toggleMaximized">{{ maximized ? '❐' : '⛶' }}</button>
          <button type="button" class="comment-close" aria-label="收起注释" title="收起注释" @click="hide">×</button>
        </div>
      </header>
      <textarea ref="commentInput" v-model="draft" :aria-label="`${label}内容`" :placeholder="`填写${label}…`" rows="6" :disabled="disabled" />
      <div v-if="error" class="comment-error" role="alert">{{ error }}</div>
      <div v-if="dirty" class="comment-actions">
        <button type="button" :disabled="disabled" @click="cancel">取消</button>
        <button type="button" class="primary" :disabled="disabled" @click="apply">应用</button>
      </div>
      <button v-if="!maximized" type="button" class="comment-resize-handle" aria-label="调整注释窗口大小" title="按住拖动调整大小"
        @pointerdown.prevent.stop="startResize" @pointermove.stop="resize" @pointerup.stop="stopResize"
        @pointercancel.stop="stopResize" @lostpointercapture="stopResize"><span aria-hidden="true">◢</span></button>
    </section>
  </Teleport>
</template>

<style scoped>
.node-comment-tooltip { position: fixed; z-index: 1900; display: flex; flex-direction: column; max-width: calc(100vw - 16px); max-height: calc(100vh - 16px); box-sizing: border-box; padding: 12px 14px 20px; gap: 10px; border: 1px solid #527a78; border-radius: 12px; background: #1b2d35; color: #e3edf3; box-shadow: 0 12px 32px #0008; font-size: 13px; user-select: text; }
/* 透明桥覆盖指向箭头与节点之间的间隙，慢速移入也不自动关闭。 */
.node-comment-tooltip::before { content: ""; position: absolute; top: -1px; bottom: -1px; width: 16px; }
.bridge-left::before { left: -16px; }
.bridge-right::before { right: -16px; }
.node-comment-tooltip::after { content: ""; position: absolute; top: var(--arrow-top); width: 14px; height: 14px; background: #1b2d35; transform: translateY(-50%) rotate(45deg); pointer-events: none; }
.bridge-left::after { left: -8px; border-left: 1px solid #527a78; border-bottom: 1px solid #527a78; }
.bridge-right::after { right: -8px; border-right: 1px solid #527a78; border-top: 1px solid #527a78; }
.is-maximized::before, .is-maximized::after { display: none; }
.is-maximized { z-index: 2100; }
header { display: flex; align-items: center; justify-content: space-between; flex: 0 0 auto; color: #b9cad6; }
header strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #b9f0df; font-weight: 600; }
.comment-window-actions { display: flex; align-items: center; gap: 4px; flex: 0 0 auto; }
.comment-close { padding: 0 5px; border: 0; background: transparent; color: inherit; font-size: 20px; line-height: 22px; }
textarea { display: block; width: 100%; min-height: 0; min-width: 0; box-sizing: border-box; padding: 10px; border: 1px solid #3a555d; border-radius: 7px; background: #13222b; color: #dce6ee; resize: none; overflow: auto; flex: 1 1 auto; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; overscroll-behavior: contain; }
textarea:focus-visible { outline: 2px solid #7ee4b1; outline-offset: 2px; }
/* 滚动条沿用注释框的深色配色；标准属性作为非 WebKit 浏览器的回退。 */
textarea { scrollbar-width: thin; scrollbar-color: #52716f #13222b; }
/* 支持伪元素时保留细轨道、圆角滑块，并去掉系统默认的上下箭头。 */
@supports selector(::-webkit-scrollbar) {
  textarea { scrollbar-width: auto; scrollbar-color: auto; }
  textarea::-webkit-scrollbar { width: 10px; height: 10px; }
  textarea::-webkit-scrollbar-track { background: transparent; margin-block: 6px; }
  textarea::-webkit-scrollbar-thumb { min-height: 32px; border: 2px solid transparent; border-radius: 999px; background: #52716f; background-clip: padding-box; }
  textarea::-webkit-scrollbar-thumb:hover { background-color: #6b928b; }
  textarea::-webkit-scrollbar-thumb:active { background-color: #8bc9b6; }
  textarea::-webkit-scrollbar-button { display: none; width: 0; height: 0; }
  textarea::-webkit-scrollbar-corner { background: transparent; }
}
.comment-actions { display: flex; flex: 0 0 auto; justify-content: flex-end; gap: 8px; }
.comment-error { color: #ffb8b8; overflow-wrap: anywhere; }
.comment-resize-handle { position: absolute; right: 1px; bottom: 1px; width: 19px; height: 19px; padding: 0; border: 0; border-radius: 0 0 10px 0; background: transparent; color: #8eaaa9; cursor: nwse-resize; touch-action: none; font-size: 13px; }
</style>
