<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import CommentTooltip from "./CommentTooltip.vue";
import type { EventDefinition, Project } from "./project.ts";

const props = defineProps<{
  project: Project; // 事务提交和草稿失效使用同一工程身份。
  events: EventDefinition[]; // 当前工程事件成员。
  enumDescription?: string; // 工程事件的整体说明。
  highlightedIDs: ReadonlySet<string>; // 多选高亮仅属当前会话。
  blocked?: boolean; // 管理弹窗期间关闭注释。
  commit: (project: Project, event: EventDefinition | null, value: string) => boolean; // 父层负责校验身份及记录历史。
}>();
const emit = defineEmits<{ manage: []; highlight: [id: string]; clearHighlight: [] }>();
const collapsed = ref(false);
const searching = ref(false);
const query = ref("");
const searchInput = ref<HTMLInputElement>();
const tooltip = ref<InstanceType<typeof CommentTooltip>>();
const enumTarget = {}; // 整体说明使用稳定目标，工程替换由 tooltip 的 context 失效。

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

// 单项与整体说明共用同一悬停浮层，空注释也能直接编辑。
function showComment(target: EventDefinition | typeof enumTarget, event: MouseEvent): void {
  if (props.blocked || !(event.currentTarget instanceof HTMLElement)) return;
  void tooltip.value?.show(target, event.currentTarget);
}

// 键盘和触屏可显式打开注释，不依赖鼠标悬停。
function editComment(target: EventDefinition | typeof enumTarget, element: HTMLElement): void {
  if (!props.blocked) void tooltip.value?.edit(target, element);
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

watch(() => props.blocked, blocked => { if (blocked) tooltip.value?.hide(); });
watch([query, collapsed], () => tooltip.value?.hide());
watch(collapsed, hidden => { if (hidden) { searching.value = false; query.value = ""; } });
watch(() => props.events, () => tooltip.value?.hide());
</script>

<template>
  <section class="event-overlay nodrag nopan nowheel" :class="{ collapsed }" aria-label="画布事件" @pointerdown.stop @mousedown.stop @click.stop @dblclick.stop @contextmenu.stop.prevent @wheel.stop @keydown="overlayKeydown">
    <header class="event-overlay-header">
      <button type="button" class="event-overlay-toggle" :aria-expanded="!collapsed" aria-controls="event-overlay-content" @click="collapsed = !collapsed">{{ collapsed ? '▸' : '▾' }} 事件（{{ events.length }}）</button>
      <button v-if="!collapsed" type="button" class="event-icon-button" aria-label="事件管理" title="事件管理" @click="emit('manage')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 2h4l.7 2.2 1.8.8 2.1-1 2.8 2.8-1 2.1.8 1.8L23 11v4l-2.2.7-.8 1.8 1 2.1-2.8 2.8-2.1-1-1.8.8L14 24h-4l-.7-2.2-1.8-.8-2.1 1-2.8-2.8 1-2.1-.8-1.8L.6 15v-4l2.2-.7.8-1.8-1-2.1L5.4 3.6l2.1 1 1.8-.8z" transform="translate(.2 -1) scale(.93)" /><circle cx="12" cy="12" r="3.2" /></svg></button>
      <button v-if="!collapsed" type="button" class="event-icon-button" aria-label="事件功能注释" title="事件功能注释" aria-controls="event-comment-tooltip" @mouseenter="showComment(enumTarget, $event)" @mouseleave="tooltip?.scheduleHide($event)" @focus="editComment(enumTarget, $event.currentTarget as HTMLElement)" @click="editComment(enumTarget, $event.currentTarget as HTMLElement)"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7h.01" /></svg></button>
      <button v-if="!collapsed" type="button" class="event-icon-button" :aria-expanded="searching" aria-label="搜索事件" title="搜索事件" @click="toggleSearch"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m15.5 15.5 5 5" /></svg></button>
      <input v-if="!collapsed && searching" ref="searchInput" v-model="query" class="event-search-input" aria-label="搜索事件" placeholder="名称、代码名或 ID" />
    </header>
    <div v-if="!collapsed && highlightedIDs.size" class="event-overlay-summary">已选 {{ highlightedIDs.size }} 项 <button type="button" aria-label="清除事件高亮" @click="emit('clearHighlight')">×</button></div>
    <div v-show="!collapsed" id="event-overlay-content">
      <p v-if="!rows.length" class="event-overlay-empty">{{ events.length ? '没有匹配的事件' : '尚无事件，可在事件管理中创建' }}</p>
      <div v-else class="event-overlay-list">
        <label v-for="item in rows" :key="item.id" class="event-overlay-row" :class="{ selected: highlightedIDs.has(item.id) }" @mouseenter="showComment(item, $event)" @mouseleave="tooltip?.scheduleHide($event)">
          <input type="checkbox" :checked="highlightedIDs.has(item.id)" :aria-label="`高亮${item.name}关联节点，按 Enter 编辑注释`" @change="emit('highlight', item.id)" @keydown.enter.prevent="editComment(item, $event.currentTarget as HTMLElement)" />
          <span class="event-overlay-details"><strong class="event-overlay-name" :title="item.name">{{ item.name }}</strong><span class="event-overlay-code" :title="`Event${item.codeName}`">Event{{ item.codeName }}</span><span class="event-overlay-id">ID {{ item.id }}</span></span>
        </label>
      </div>
    </div>
    <CommentTooltip ref="tooltip" :context="project" :disabled="!!blocked" :auto-open="true" :allow-empty-on-hover="true" :commit="commitComment" :get-title="getTitle" :get-comment="getComment" label="事件注释" tooltip-id="event-comment-tooltip" />
  </section>
</template>

<style scoped>
.event-overlay{position:absolute;top:60px;left:12px;z-index:6;width:min(340px,calc(100% - 24px));max-height:calc(100% - 72px);display:flex;flex-direction:column;color:#e2f1f3;background:#14232de8;border:1px solid #54717a;border-radius:10px;box-shadow:0 8px 30px #0006;font:12px/1.4 system-ui,sans-serif}.event-overlay.collapsed{width:max-content}
.event-overlay-header{display:flex;align-items:center;gap:4px;padding:8px}
.event-overlay button,.event-search-input{font:inherit;color:inherit;border:1px solid #49656c;border-radius:5px;background:#182c35;cursor:pointer}
.event-overlay button:hover{border-color:#8fe2cb}.event-overlay button:focus-visible,.event-overlay input:focus-visible{outline:2px solid #8fe2cb;outline-offset:1px}
.event-overlay-toggle{flex:none;margin-right:auto;padding:5px 7px;font-weight:700;white-space:nowrap}.event-search-input{flex:1;min-width:0;padding:5px 6px;box-sizing:border-box}
.event-icon-button{width:27px;height:27px;flex:none;display:grid;place-items:center;padding:5px}.event-icon-button svg{width:15px;height:15px;fill:none;stroke:currentColor;stroke-width:1.8;stroke-linecap:round;stroke-linejoin:round}
.event-overlay-summary{padding:3px 8px;color:#a9dfc9}.event-overlay-summary button{float:right;padding:0 5px}
.event-overlay-list{max-height:min(360px,45vh);overflow-y:auto;overflow-x:hidden;padding:4px 8px 8px}.event-overlay-row{display:flex;align-items:center;gap:7px;min-width:0;min-height:30px;margin:3px 0;padding:0 8px;border:1px solid #49656c;border-radius:6px;background:#182c35;cursor:pointer}.event-overlay-row:hover{border-color:#8fe2cb}.event-overlay-row.selected{border-color:#68dfc6;background:#245047}.event-overlay-row input[type="checkbox"]{width:15px;height:15px;flex:0 0 15px;box-sizing:border-box;padding:0;margin:0;accent-color:#69ddbb}.event-overlay-details{display:flex;align-items:center;gap:6px;flex:1;min-width:0;overflow:hidden;white-space:nowrap}.event-overlay-name{flex:0 1 auto;max-width:45%;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:600}.event-overlay-code{flex:1 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#b5cbd1}.event-overlay-id{flex:none;color:#b5cbd1;white-space:nowrap}.event-overlay-empty{padding:2px 9px 9px;color:#b3c8cf}
@media(max-width:300px){.event-overlay-header{gap:2px;padding:6px}.event-overlay-toggle{padding:4px}.event-icon-button{width:24px;height:25px}.event-overlay-row{padding:0 5px}}
</style>
