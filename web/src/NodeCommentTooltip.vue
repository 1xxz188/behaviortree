<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import type { BTNode, Tree } from "./project";

const props = defineProps<{
  tree: Tree; // 树对象替换时关闭旧节点说明，不遍历节点列表。
  disabled: boolean; // 菜单、对话框或工程切换期间不展示说明。
}>();
const target = shallowRef<BTNode>(); // 全画布只保留当前悬浮节点。
const anchor = shallowRef<HTMLElement>(); // 当前节点元素，用于一次性测量视口坐标。
const tooltip = ref<HTMLElement>(); // 挂载到 body 的唯一说明浮层。
const position = ref({ left: 0, top: 0 }); // 经视口边界裁剪后的坐标。
const positioned = ref(false); // 测量完成前隐藏，避免首帧在左上角闪现。
let closeTimer: ReturnType<typeof setTimeout> | undefined; // 一次性离开宽限，允许跨越节点和浮层之间的间隙。

// 进入节点或浮层时取消尚未触发的离开动作，不使用轮询。
function retain() {
  if (closeTimer !== undefined) clearTimeout(closeTimer);
  closeTimer = undefined;
}

// 所有关闭入口同时释放目标和延迟任务，防止旧目标重新出现。
function hide() {
  retain();
  target.value = undefined;
  anchor.value = undefined;
  positioned.value = false;
}

// 仅在打开和窗口尺寸变化时测量两个元素，优先放到节点右侧。
function fit() {
  if (!anchor.value?.isConnected || !tooltip.value) return hide();
  const nodeBounds = anchor.value.getBoundingClientRect();
  const bounds = tooltip.value.getBoundingClientRect();
  const preferredLeft = nodeBounds.right + 8;
  position.value = {
    left: Math.max(8, Math.min(preferredLeft + bounds.width <= window.innerWidth - 8
      ? preferredLeft : nodeBounds.left - bounds.width - 8, window.innerWidth - bounds.width - 8)),
    top: Math.max(8, Math.min(nodeBounds.top, window.innerHeight - bounds.height - 8)),
  };
  positioned.value = true;
}

// 使用调用方已索引定位的节点；异步测量前复核目标，避免快速移出后重新打开。
async function show(node: BTNode, element: HTMLElement) {
  retain();
  if (props.disabled || !node.comment?.trim()) return hide();
  target.value = node;
  anchor.value = element;
  positioned.value = false;
  await nextTick();
  if (target.value === node && anchor.value === element) {
    fit();
    if (tooltip.value) tooltip.value.scrollTop = 0;
  }
}

// 单次短延迟保障浮层可滚动阅读，移入另一个节点会取消旧任务。
function scheduleHide() {
  retain();
  closeTimer = setTimeout(hide, 150);
}

// 浮层内部可选择文字和操作滚动条，其他位置开始交互即关闭说明。
function onPointerDown(event: PointerEvent) {
  if (event.target instanceof Node && tooltip.value?.contains(event.target)) return;
  hide();
}

// Escape 独立关闭说明，不吞掉画布或菜单原有的键盘操作。
function onKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") hide();
}

watch(() => props.tree, hide);
watch(() => props.disabled, disabled => { if (disabled) hide(); });
watch(() => target.value?.comment, comment => { if (!comment?.trim()) hide(); });
onMounted(() => {
  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("keydown", onKeydown, true);
  document.addEventListener("contextmenu", hide, true);
  window.addEventListener("blur", hide);
  window.addEventListener("resize", fit);
});
onBeforeUnmount(() => {
  hide();
  document.removeEventListener("pointerdown", onPointerDown, true);
  document.removeEventListener("keydown", onKeydown, true);
  document.removeEventListener("contextmenu", hide, true);
  window.removeEventListener("blur", hide);
  window.removeEventListener("resize", fit);
});

defineExpose({ show, hide, scheduleHide });
</script>

<template>
  <Teleport to="body">
    <div v-if="target" id="node-comment-tooltip" ref="tooltip" class="node-comment-tooltip" role="tooltip"
      :style="{ left: `${position.left}px`, top: `${position.top}px`, visibility: positioned ? 'visible' : 'hidden' }"
      @pointerenter="retain" @pointerleave="scheduleHide" @wheel.stop @pointerdown.stop>
      {{ target.comment }}
    </div>
  </Teleport>
</template>

<style scoped>
.node-comment-tooltip { position: fixed; z-index: 1900; width: max-content; max-width: min(360px, calc(100vw - 16px)); max-height: min(320px, calc(100vh - 16px)); overflow: auto; box-sizing: border-box; padding: 10px 12px; border: 1px solid #49606c; border-radius: 8px; background: #18242e; color: #e3edf3; box-shadow: 0 8px 24px #0008; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.6; font-size: 13px; user-select: text; overscroll-behavior: contain; }
</style>
