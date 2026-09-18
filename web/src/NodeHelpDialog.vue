<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { kinds } from "./project";
import { nodeHelp } from "./nodeHelp";
import NodeHelpCanvas from "./NodeHelpCanvas.vue";
import type { NodeType } from "./enums";

const props = defineProps<{ type: NodeType }>(); // 当前查看的内置节点类型。
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框管理焦点和 Escape 关闭。
let previousFocus: HTMLElement | null = null; // 关闭后返回打开说明的库按钮。
const backdropPressed = ref(false); // 仅从窗口外开始的点击可关闭，避免选择文字或拖动示例时误关。

// 原生 dialog 的遮罩事件也以 dialog 为目标，需按坐标排除窗口内留白和滚动条。
function isOutsideDialog(event: MouseEvent): boolean {
  if (!dialog.value || event.target !== dialog.value) return false;
  const bounds = dialog.value.getBoundingClientRect();
  return event.clientX < bounds.left || event.clientX > bounds.right
    || event.clientY < bounds.top || event.clientY > bounds.bottom;
}
// 按下和释放均在窗口外时才关闭，窗口内部的点击不影响阅读。
function closeFromBackdrop(event: MouseEvent) {
  const shouldClose = backdropPressed.value && isOutsideDialog(event);
  backdropPressed.value = false;
  if (shouldClose) emit("close");
}

// 查看说明只管理弹窗焦点，不创建节点或修改工程。
onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  dialog.value?.showModal();
});
onBeforeUnmount(() => {
  dialog.value?.close();
  if (previousFocus?.isConnected) previousFocus.focus();
});
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="project-dialog node-help-dialog" aria-label="节点说明" @cancel.prevent="emit('close')" @keydown.stop
      @pointerdown="backdropPressed = isOutsideDialog($event)" @pointercancel="backdropPressed = false" @click="closeFromBackdrop">
      <!-- 长说明先聚焦标题，避免底部按钮自动聚焦导致打开即滚到底部。 -->
      <header class="node-help-heading">
        <h2 tabindex="-1" autofocus>{{ kinds[props.type].label }}</h2>
        <button type="button" class="node-help-close" aria-label="关闭节点说明" title="关闭" @click="emit('close')"><span aria-hidden="true">×</span></button>
      </header>
      <code>{{ props.type }}</code>
      <section aria-label="功能说明"><h3>功能说明</h3><p>{{ nodeHelp[props.type].description }}</p></section>
      <section aria-label="执行结果"><h3>执行结果</h3><ul><li v-for="result in nodeHelp[props.type].results" :key="result">{{ result }}</li></ul></section>
      <section aria-label="配置要点"><h3>配置要点</h3><ul><li v-for="item in nodeHelp[props.type].configuration" :key="item">{{ item }}</li></ul></section>
      <section aria-label="使用场景"><h3>使用场景</h3><p>{{ nodeHelp[props.type].scenario }}</p></section>
      <section class="node-help-example" aria-label="具体示例"><h3>具体示例</h3><p>{{ nodeHelp[props.type].example }}</p><NodeHelpCanvas :type="props.type" /></section>
      <p class="muted">从节点库拖入画布，即可添加此节点。</p>
    </dialog>
  </Teleport>
</template>

<style scoped>
.node-help-dialog { width: min(820px, 90vw); max-height: 85vh; overflow-y: auto; overflow-wrap: anywhere; }
.node-help-heading { position: sticky; top: -24px; z-index: 1; display: flex; align-items: center; justify-content: space-between; gap: 16px; margin: -24px -24px 8px; padding: 16px 24px 12px; background: #18242e; border-bottom: 1px solid #354958; }
.node-help-heading h2 { margin: 0; }
.node-help-close { display: grid; place-items: center; flex: 0 0 32px; width: 32px; height: 32px; padding: 0; border-color: transparent; background: transparent; color: #b9cad6; font-size: 26px; line-height: 1; }
.node-help-close:hover { background: #304452; color: #fff; }
.node-help-close:focus-visible { outline: 2px solid #68dfc6; outline-offset: 2px; }
h3 { margin: 20px 0 8px; font-size: 14px; }
p, li { line-height: 1.7; }
p { margin: 8px 0; }
ul { margin: 8px 0; padding-left: 22px; }
li + li { margin-top: 6px; }
.node-help-example { padding: 1px 14px 8px; border-left: 3px solid currentColor; background: rgb(127 127 127 / 8%); }
.node-help-example h3 { margin-top: 12px; }
</style>
