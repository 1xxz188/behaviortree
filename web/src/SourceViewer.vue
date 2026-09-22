<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { createSourceIndex, sourceNodeKey } from "./generation";
import type { SourceIndex, SourceLocation } from "./generation";
import { selectedScaffoldCode } from "./scaffoldNavigation";

// 源码查看器只渲染可见行；大树生成的长文件不会创建同等数量的 DOM。
const props = defineProps<{
  source: string; // 完整只读源码。
  sourceIndex?: SourceIndex; // 接收快照时建立的生成代码或业务骨架索引。
  treeId?: string; // 当前选中树。
  nodeId?: string; // 当前选中节点。
  stale?: boolean; // 过期源码禁止按旧映射跳转当前画布。
  copySelection?: boolean; // 业务骨架允许复制当前节点对应的完整函数。
}>();
const emit = defineEmits<{
  locate: [location: SourceLocation]; // 将源码行定位回画布节点。
  copyCode: [source: string]; // 交由外层统一复制并提示结果。
}>();
const viewport = ref<HTMLElement>();
const scrollTop = ref(0);
const height = ref(240);
const occurrencePage = ref(0);
const pageSize = 6;
const lineHeight = 22;
const overscan = 8;
let observer: ResizeObserver | undefined;
const index = computed(() => props.sourceIndex ?? createSourceIndex(props.source, []));
const lines = computed(() => index.value.lines);
const locations = computed(() => props.stale ? [] : index.value.byNode.get(sourceNodeKey(props.treeId ?? "", props.nodeId ?? "")) ?? []);
const visibleLocations = computed(() => locations.value.slice(occurrencePage.value * pageSize, (occurrencePage.value + 1) * pageSize));
const activeIndices = computed(() => new Set(locations.value.map((location) => location.index)));
const first = computed(() => Math.max(0, Math.floor(scrollTop.value / lineHeight) - overscan));
const last = computed(() => Math.min(lines.value.length, first.value + Math.ceil(height.value / lineHeight) + overscan * 2));
const visible = computed(() => lines.value.slice(first.value, last.value).map((text, offset) => {
  const line = first.value + offset + 1;
  const location = props.stale ? undefined : index.value.byLine.get(line);
  return { text, line, location, active: !!location && activeIndices.value.has(location.index) };
}));
const totalWidth = computed(() => Math.min(index.value.longestLineLength, 10000) * 8 + 90);

// 只滚动代码区域，不改变画布、页面或用户的面板高度。
function jump(line: number) {
  if (!viewport.value) return;
  viewport.value.scrollTop = Math.max(0, (line - 3) * lineHeight);
  scrollTop.value = viewport.value.scrollTop;
}
// 选择节点后定位首个展开实例，其他实例通过上方按钮显式选择。
watch(locations, async (items) => {
  occurrencePage.value = 0;
  await nextTick();
  if (items[0]) jump(items[0].line);
}, { immediate: true });
watch(() => props.source, () => jump(1));
onMounted(() => {
  observer = new ResizeObserver(() => { height.value = viewport.value?.clientHeight ?? 240; });
  if (viewport.value) observer.observe(viewport.value);
});
onUnmounted(() => observer?.disconnect());
</script>

<template>
  <div class="source-viewer">
    <div v-if="locations.length" class="source-locations">
      <span>选中节点 · {{ locations.length }} 个展开位置</span>
      <button v-if="copySelection && locations[0]" @click="emit('copyCode', selectedScaffoldCode(index, locations[0]))">复制选中代码</button>
      <button v-if="locations.length > pageSize" :disabled="occurrencePage === 0" @click="occurrencePage--">上一页</button>
      <button v-for="location in visibleLocations" :key="location.index" @click="jump(location.line)">
        #{{ location.index }} · 行 {{ location.line }}
      </button>
      <button v-if="locations.length > pageSize" :disabled="(occurrencePage + 1) * pageSize >= locations.length" @click="occurrencePage++">下一页</button>
    </div>
    <div ref="viewport" class="source-viewport" aria-label="Go 源码" tabindex="0"
      @scroll="scrollTop = ($event.target as HTMLElement).scrollTop">
      <div class="source-spacer" :style="{ height: `${lines.length * lineHeight}px`, minWidth: `${totalWidth}px` }">
        <div class="source-lines" :style="{ transform: `translateY(${first * lineHeight}px)` }">
          <div v-for="row in visible" :key="row.line" class="source-line" :class="{ 'active-source-line': row.active }">
            <button v-if="row.location" class="line-number" :aria-label="`第 ${row.line} 行，定位节点 ${row.location.nodeId}`"
              title="定位对应画布节点" @click="emit('locate', row.location)">{{ row.line }}</button>
            <span v-else class="line-number">{{ row.line }}</span>
            <code>{{ row.text || ' ' }}</code>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.source-viewer { min-height: 0; flex: 1; display: flex; flex-direction: column; }
.source-locations { display: flex; align-items: center; gap: 8px; padding: 7px 12px; border-bottom: 1px solid #2b414d; font-size: 11px; flex-wrap: wrap; max-height: 85px; overflow: auto; }
.source-locations span { color: #99c9b5; }
.source-locations button { padding: 2px 6px; font-size: 11px; }
.source-viewport { min-height: 0; flex: 1; overflow: auto; scrollbar-width: thin; background: #101b24; }
.source-spacer { position: relative; width: 100%; }
.source-lines { position: absolute; top: 0; left: 0; width: 100%; }
.source-line { height: 22px; display: flex; white-space: pre; font-size: 12px; line-height: 22px; }
.source-line code { display: block; padding-right: 16px; }
.line-number { flex: 0 0 62px; display: block; position: sticky; left: 0; padding: 0 12px 0 4px; text-align: right; border: 0; border-radius: 0; font: 11px/22px Consolas, monospace; background: #14212b; color: #6d8799; user-select: none; }
button.line-number { color: #82cdb0; }
.active-source-line, .active-source-line .line-number { background: #203c37; }
</style>
