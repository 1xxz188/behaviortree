<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from "vue";

const props = defineProps<{
  disabled: boolean; // 尚未打开工程时禁用所有导出。
  pngBusy: boolean; // 生成图片期间阻止重复提交。
}>();
const emit = defineEmits<{ json: []; png: [] }>();
const open = ref(false); // 二级菜单仅由用户交互打开。
const root = ref<HTMLElement>(); // 用于判断点击和焦点是否离开菜单。
const trigger = ref<HTMLButtonElement>(); // 关闭后恢复键盘焦点。
const menu = ref<HTMLElement>(); // 菜单内仅有两个可选格式。

// 展开后聚焦首项，使键盘与鼠标使用同一组导出操作。
async function toggle() {
  open.value = !open.value;
  if (open.value) {
    await nextTick();
    menu.value?.querySelector<HTMLButtonElement>("button")?.focus();
  }
}
// 选择格式前关闭菜单，图片处理中保留 JSON 下载能力。
function choose(format: "json" | "png") {
  open.value = false;
  trigger.value?.focus();
  if (format === "json") emit("json");
  else emit("png");
}
// 外部点击只关闭菜单，不干扰用户点击的其他控件。
function outside(event: PointerEvent) {
  if (event.target instanceof Node && !root.value?.contains(event.target)) open.value = false;
}
// Tab 正常离开菜单；上下方向键在可用格式间切换，Escape 返回入口。
function keydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    event.preventDefault();
    open.value = false;
    trigger.value?.focus();
  } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    const items = Array.from(menu.value?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []);
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    items[(index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length]?.focus();
  }
}
// 焦点移出整个导出区域时收起菜单，不阻止浏览器默认 Tab 顺序。
function blur(event: FocusEvent) {
  if (!(event.relatedTarget instanceof Node) || !root.value?.contains(event.relatedTarget)) open.value = false;
}
watch(() => props.disabled, disabled => { if (disabled) open.value = false; });
onMounted(() => document.addEventListener("pointerdown", outside));
onUnmounted(() => document.removeEventListener("pointerdown", outside));
</script>

<template>
  <div ref="root" class="export-menu" @focusout="blur" @keydown.stop="keydown">
    <button ref="trigger" :disabled="disabled" aria-haspopup="menu" :aria-expanded="open" aria-controls="export-options"
      @click="toggle" @keydown.down.prevent.stop="!open && toggle()">导出 <span aria-hidden="true">▾</span></button>
    <div v-if="open" id="export-options" ref="menu" class="export-options" role="menu" aria-label="导出格式">
      <button role="menuitem" @click="choose('json')">导出Json</button>
      <button role="menuitem" :disabled="pngBusy" title="导出当前行为树的完整画布" @click="choose('png')">导出PNG</button>
    </div>
  </div>
</template>

<style scoped>
.export-menu { position: relative; }
.export-options { position: absolute; top: calc(100% + 6px); left: 0; z-index: 100; min-width: 140px; padding: 5px; border: 1px solid #3a5060; border-radius: 6px; background: #18242e; box-shadow: 0 8px 24px #0006; }
.export-options button { display: block; width: 100%; border-color: transparent; background: transparent; text-align: left; white-space: nowrap; }
.export-options button:hover:not(:disabled), .export-options button:focus-visible { background: #2d414e; }
</style>
