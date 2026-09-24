<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import CommentTooltip from "./CommentTooltip.vue";
import type { EventDefinition, Project } from "./project.ts";

const props = defineProps<{
  project: Project; // 事务提交和草稿失效使用同一工程身份。
  events: EventDefinition[]; // 当前工程事件成员。
  enumDescription?: string; // 工程事件的整体说明。
  highlightedIDs: ReadonlySet<string>; // 多选高亮仅属当前会话。
  matchedNodeCount: number; // 当前行为树中被已选事件命中的去重节点数。
  autoOpenComments: boolean; // 与画布节点共用悬浮注释开关。
  blocked?: boolean; // 管理弹窗期间关闭注释。
  commit: (project: Project, event: EventDefinition | null, value: string) => boolean; // 父层负责校验身份及记录历史。
}>();
const emit = defineEmits<{ manage: []; highlight: [id: string]; clearHighlight: [] }>();
const collapsed = ref(false);
const searching = ref(false);
const query = ref("");
const overlay = ref<HTMLElement>(); // 事件窗口仅在开始拖拽时测量边界。
// 用户本次会话调整的事件窗口尺寸。
interface PanelSize {
  width: number; // 窗口宽度。
  height: number; // 窗口高度。
}
const panelSize = ref<PanelSize>(); // 收起时保留，重新展开时恢复。
// 一次拖拽的起点和画布边界，移动期间不重复读取布局。
interface ResizeState {
  pointerID: number; // 当前拖拽指针。
  x: number; // 指针起始横坐标。
  y: number; // 指针起始纵坐标。
  width: number; // 窗口起始宽度。
  height: number; // 窗口起始高度。
  maxWidth: number; // 画布右边界允许的最大宽度。
  maxHeight: number; // 画布下边界允许的最大高度。
  handle: HTMLElement; // 持有指针捕获的手柄。
}
const resizeState = shallowRef<ResizeState>(); // 指针结束或取消时清除。
const searchInput = ref<HTMLInputElement>();
const enumButton = ref<HTMLButtonElement>(); // 事件功能注释按钮用于判断再次点击与外部点击。
const tooltip = ref<InstanceType<typeof CommentTooltip>>();
const enumTarget = {}; // 整体说明使用稳定目标，工程替换由 tooltip 的 context 失效。
const enumCommentOpen = computed(() => tooltip.value?.activeTarget === enumTarget); // 仅跟踪整体注释浮层。

// 搜索只在输入或事件表变化时遍历成员，不计算列表不再展示的引用数。
const rows = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase();
  return props.events.filter(item => !needle || `${item.name}\nEvent${item.codeName}\n${item.id}`.toLocaleLowerCase().includes(needle));
});

// 标题行展开搜索时聚焦输入；收起后恢复完整列表。
async function toggleSearch(): Promise<void> {
  searching.value = !searching.value;
  if (searching.value) await nextTick(() => searchInput.value?.focus());
  else query.value = "";
}

// 右下角手柄捕获指针，并一次测量事件窗口及画布可用范围。
function startResize(event: PointerEvent): void {
  if (collapsed.value || event.button !== 0 || resizeState.value || !overlay.value || !(event.currentTarget instanceof HTMLElement)) return;
  const bounds = overlay.value.getBoundingClientRect();
  const canvasBounds = overlay.value.parentElement?.getBoundingClientRect();
  if (!canvasBounds) return;
  tooltip.value?.hide();
  resizeState.value = {
    pointerID: event.pointerId, x: event.clientX, y: event.clientY,
    width: bounds.width, height: bounds.height,
    maxWidth: Math.max(0, canvasBounds.right - bounds.left - 12),
    maxHeight: Math.max(0, canvasBounds.bottom - bounds.top - 12),
    handle: event.currentTarget,
  };
  event.currentTarget.setPointerCapture(event.pointerId);
}

// 宽高限制在画布内；收窄画布时允许低于通常的最小宽高。
function resize(event: PointerEvent): void {
  const state = resizeState.value;
  if (!state || event.pointerId !== state.pointerID) return;
  panelSize.value = {
    width: Math.min(Math.max(340, state.width + event.clientX - state.x), state.maxWidth),
    height: Math.min(Math.max(160, state.height + event.clientY - state.y), state.maxHeight),
  };
}

// 释放捕获并清除拖拽状态，保留用户最后调整的尺寸。
function stopResize(event?: PointerEvent): void {
  const state = resizeState.value;
  if (!state || (event && event.pointerId !== state.pointerID)) return;
  resizeState.value = undefined;
  if (state.handle.hasPointerCapture(state.pointerID)) state.handle.releasePointerCapture(state.pointerID);
}

// 事件行遵循悬浮开关，整体注释按钮不走悬浮入口。
function showComment(target: EventDefinition, event: MouseEvent): void {
  if (props.blocked || !(event.currentTarget instanceof HTMLElement)) return;
  void tooltip.value?.show(target, event.currentTarget);
}

// 键盘和触屏可显式打开注释，不依赖鼠标悬停。
function editComment(target: EventDefinition | typeof enumTarget, element: HTMLElement): void {
  if (!props.blocked) void tooltip.value?.edit(target, element);
}

// 整体注释由按钮点击切换，收起时沿用通用浮层的草稿保留逻辑。
function toggleEnumComment(element: HTMLElement): void {
  if (props.blocked) return;
  if (enumCommentOpen.value) tooltip.value?.hide();
  else editComment(enumTarget, element);
}

// 通用浮层读取实时正文，避免旧草稿覆盖其他目标的注释。
function getComment(target: object): string | undefined {
  return target === enumTarget ? props.enumDescription : (target as EventDefinition).description;
}

// 事件项标题同时提供省略在单行列表中的代码名与 ID。
function getTitle(target: object): string {
  if (target === enumTarget) return "事件功能注释";
  const item = target as EventDefinition;
  return `${item.name} · Event${item.codeName} · ID ${item.id}`;
}

// 通用浮层不直接修改工程，仍由父层完成事件事务和身份复核。
function commitComment(target: object, value: string): boolean {
  return props.commit(props.project, target === enumTarget ? null : target as EventDefinition, value);
}

// Esc 先关闭搜索，再清除当前高亮；事件焦点不把按键传到画布。
function overlayKeydown(event: KeyboardEvent): void {
  event.stopPropagation();
  if (event.key !== "Escape") return;
  event.preventDefault();
  if (searching.value) { searching.value = false; query.value = ""; }
  else if (props.highlightedIDs.size) emit("clearHighlight");
}

// 空搜索时点击事件列表或输入框以外的位置关闭搜索栏。
function closeEmptySearchOutsideEventList(event: MouseEvent): void {
  if (!searching.value || query.value.trim()) return;
  const target = event.target;
  if (!(target instanceof Element) || target.closest(".event-overlay-list, .event-search-input")) return;
  searching.value = false;
}

// 整体注释点击按钮或浮层以外的区域即收起，编辑中的草稿仍由浮层保存。
function closeEnumCommentOutside(event: MouseEvent): void {
  if (!enumCommentOpen.value || !(event.target instanceof Node)) return;
  if (enumButton.value?.contains(event.target) || document.getElementById("event-comment-tooltip")?.contains(event.target)) return;
  tooltip.value?.hide();
}

// 使用捕获阶段识别面板外点击；卸载时移除监听避免重复注册。
onMounted(() => {
  document.addEventListener("click", closeEmptySearchOutsideEventList, true);
  document.addEventListener("click", closeEnumCommentOutside, true);
});
onUnmounted(() => {
  stopResize();
  document.removeEventListener("click", closeEmptySearchOutsideEventList, true);
  document.removeEventListener("click", closeEnumCommentOutside, true);
});

watch(() => props.blocked, blocked => { if (blocked) tooltip.value?.hide(); });
// 用户清空已经输入的搜索内容后关闭搜索，初次打开空输入框时保持展开。
watch(query, (value, previous) => {
  if (searching.value && previous.length > 0 && value.length === 0) searching.value = false;
});
watch([query, collapsed], () => tooltip.value?.hide());
watch(collapsed, hidden => { if (hidden) { stopResize(); searching.value = false; query.value = ""; } });
watch(() => props.events, () => tooltip.value?.hide());
</script>

<template>
  <section ref="overlay" class="event-overlay nodrag nopan nowheel" :class="{ collapsed, resized: !!panelSize && !collapsed }" :style="!collapsed && panelSize ? { width: `${panelSize.width}px`, height: `${panelSize.height}px` } : undefined" aria-label="画布事件" @pointerdown.stop @mousedown.stop @click.stop @dblclick.stop @contextmenu.stop.prevent @wheel.stop @keydown="overlayKeydown">
    <header class="event-overlay-header">
      <input v-if="!collapsed && searching" ref="searchInput" v-model="query" class="event-search-input" aria-label="搜索事件" placeholder="名称、代码名或 ID" />
      <button v-if="!collapsed && searching && highlightedIDs.size" type="button" class="event-overlay-summary" aria-label="清除事件高亮" title="清除事件高亮" @click="emit('clearHighlight')">已选 {{ highlightedIDs.size }} 项 ×</button>
      <span v-if="!collapsed && searching && highlightedIDs.size" class="event-overlay-hit-count">命中 {{ matchedNodeCount }} 个节点</span>
      <button v-if="collapsed || !searching" type="button" class="event-overlay-toggle" :aria-expanded="!collapsed" aria-controls="event-overlay-content" @click="collapsed = !collapsed">{{ collapsed ? '▸' : '▾' }} 事件（{{ events.length }}）</button>
      <button v-if="!collapsed && !searching" type="button" class="event-icon-button" aria-label="事件管理" title="事件管理" @click="emit('manage')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 2h4l.7 2.2 1.8.8 2.1-1 2.8 2.8-1 2.1.8 1.8L23 11v4l-2.2.7-.8 1.8 1 2.1-2.8 2.8-2.1-1-1.8.8L14 24h-4l-.7-2.2-1.8-.8-2.1 1-2.8-2.8 1-2.1-.8-1.8L.6 15v-4l2.2-.7.8-1.8-1-2.1L5.4 3.6l2.1 1 1.8-.8z" transform="translate(.2 -1) scale(.93)" /><circle cx="12" cy="12" r="3.2" /></svg></button>
      <button v-if="!collapsed && !searching" ref="enumButton" type="button" class="event-icon-button" aria-label="事件功能注释" title="事件功能注释" aria-controls="event-comment-tooltip" :aria-expanded="enumCommentOpen" @click="toggleEnumComment($event.currentTarget as HTMLElement)"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7h.01" /></svg></button>
      <button v-if="!collapsed && !searching" type="button" class="event-icon-button" :aria-expanded="searching" aria-label="搜索事件" title="搜索事件" @click="toggleSearch"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m15.5 15.5 5 5" /></svg></button>

      <button v-if="!collapsed && !searching && highlightedIDs.size" type="button" class="event-overlay-summary" aria-label="清除事件高亮" title="清除事件高亮" @click="emit('clearHighlight')">已选 {{ highlightedIDs.size }} 项 ×</button>
      <span v-if="!collapsed && !searching && highlightedIDs.size" class="event-overlay-hit-count">命中 {{ matchedNodeCount }} 个节点</span>
    </header>
    <div v-show="!collapsed" id="event-overlay-content">
      <p v-if="!rows.length" class="event-overlay-empty">{{ events.length ? '没有匹配的事件' : '尚无事件，可在事件管理中创建' }}</p>
      <div v-else class="event-overlay-list">
        <label v-for="item in rows" :key="item.id" class="event-overlay-row" :class="{ selected: highlightedIDs.has(item.id) }" @mouseenter="showComment(item, $event)" @mouseleave="tooltip?.scheduleHide($event)">
          <input type="checkbox" :checked="highlightedIDs.has(item.id)" :aria-label="`高亮${item.name}关联节点，按 Enter 编辑注释`" @change="emit('highlight', item.id)" @keydown.enter.prevent="editComment(item, $event.currentTarget as HTMLElement)" />
          <span class="event-overlay-details"><strong class="event-overlay-name" :title="item.name">{{ item.name }}</strong><span class="event-overlay-code" :title="`Event${item.codeName}`">Event{{ item.codeName }}</span><span class="event-overlay-id">ID {{ item.id }}</span></span>
        </label>
      </div>
    </div>
    <button v-if="!collapsed" type="button" class="event-overlay-resize-handle" aria-label="调整事件列表窗口大小" title="按住拖动调整窗口大小" @pointerdown.prevent.stop="startResize" @pointermove.stop="resize" @pointerup.stop="stopResize" @pointercancel.stop="stopResize" @lostpointercapture="stopResize"><span aria-hidden="true">◢</span></button>
    <CommentTooltip ref="tooltip" :context="project" :disabled="!!blocked" :auto-open="autoOpenComments" :allow-empty-on-hover="true" :commit="commitComment" :get-title="getTitle" :get-comment="getComment" label="事件注释" tooltip-id="event-comment-tooltip" />
  </section>
</template>

<style scoped>
.event-overlay{position:absolute;top:60px;left:12px;z-index:6;box-sizing:border-box;width:min(340px,calc(100% - 24px));max-width:calc(100% - 24px);max-height:calc(100% - 72px);display:flex;flex-direction:column;color:#e2f1f3;background:#14232de8;border:1px solid #54717a;border-radius:10px;box-shadow:0 8px 30px #0006;font:12px/1.4 system-ui,sans-serif}.event-overlay.collapsed{width:max-content}
.event-overlay.resized #event-overlay-content{display:flex;flex:1;flex-direction:column;min-height:0}
.event-overlay-header{display:flex;align-items:center;gap:4px;padding:8px}
.event-overlay button,.event-search-input{font:inherit;color:inherit;border:1px solid #49656c;border-radius:5px;background:#182c35;cursor:pointer}
.event-overlay button:hover{border-color:#8fe2cb}.event-overlay button:focus-visible,.event-overlay input:focus-visible{outline:2px solid #8fe2cb;outline-offset:1px}
.event-overlay-toggle{flex:none;padding:5px 7px;font-weight:700;white-space:nowrap}.event-search-input{flex:1;min-width:0;padding:5px 6px;box-sizing:border-box}
.event-icon-button{width:27px;height:27px;flex:none;display:grid;place-items:center;padding:5px}.event-icon-button svg{width:15px;height:15px;fill:none;stroke:currentColor;stroke-width:1.8;stroke-linecap:round;stroke-linejoin:round}
.event-overlay-summary{flex:none;margin-left:auto;padding:5px;color:#a9dfc9;white-space:nowrap}.event-overlay-hit-count{flex:none;color:#a9dfc9;white-space:nowrap}
.event-overlay-list{max-height:min(360px,45vh);overflow-y:auto;overflow-x:hidden;padding:4px 8px 8px}.event-overlay-row{display:flex;align-items:center;gap:7px;min-width:0;min-height:30px;margin:3px 0;padding:0 8px;border:1px solid #49656c;border-radius:6px;background:#182c35;cursor:pointer}.event-overlay-row:hover{border-color:#8fe2cb}.event-overlay-row.selected{border-color:#68dfc6;background:#245047}.event-overlay-row input[type="checkbox"]{width:15px;height:15px;flex:0 0 15px;box-sizing:border-box;padding:0;margin:0;accent-color:#69ddbb}.event-overlay-details{display:flex;align-items:center;gap:6px;flex:1;min-width:0;overflow:hidden;white-space:nowrap}.event-overlay-name{flex:0 1 auto;max-width:45%;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:600}.event-overlay-code{flex:1 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#b5cbd1}.event-overlay-id{flex:none;color:#b5cbd1;white-space:nowrap}.event-overlay-empty{padding:2px 9px 9px;color:#b3c8cf}
/* 事件列表与注释窗口共用细轨道、圆角滑块及深色配色。 */
.event-overlay-list{scrollbar-width:thin;scrollbar-color:#52716f #13222b}
@supports selector(::-webkit-scrollbar) {
  .event-overlay-list{scrollbar-width:auto;scrollbar-color:auto}
  .event-overlay-list::-webkit-scrollbar{width:10px;height:10px}
  .event-overlay-list::-webkit-scrollbar-track{background:transparent;margin-block:6px}
  .event-overlay-list::-webkit-scrollbar-thumb{min-height:32px;border:2px solid transparent;border-radius:999px;background:#52716f;background-clip:padding-box}
  .event-overlay-list::-webkit-scrollbar-thumb:hover{background-color:#6b928b}
  .event-overlay-list::-webkit-scrollbar-thumb:active{background-color:#8bc9b6}
  .event-overlay-list::-webkit-scrollbar-button{display:none;width:0;height:0}
  .event-overlay-list::-webkit-scrollbar-corner{background:transparent}
}
.event-overlay.resized .event-overlay-list{flex:1;max-height:none;min-height:0}
.event-overlay-resize-handle{align-self:flex-end;flex:none;width:19px;height:19px;margin:0 2px 2px 0;padding:0;border:0;background:transparent;color:#8eaaa9;cursor:nwse-resize;touch-action:none;font-size:13px;line-height:19px}
@media(max-width:300px){.event-overlay-header{gap:2px;padding:6px}.event-overlay-toggle{padding:4px}.event-icon-button{width:24px;height:25px}.event-overlay-row{padding:0 5px}}
</style>
