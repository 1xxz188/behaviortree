<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { kinds } from "./project";
import type { NodeType } from "./enums";

const props = defineProps<{ type: NodeType }>(); // 当前查看的内置节点类型。
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框管理焦点和 Escape 关闭。
let previousFocus: HTMLElement | null = null; // 关闭后返回打开说明的库按钮。

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
    <dialog ref="dialog" class="project-dialog node-help-dialog" aria-label="节点说明" @cancel.prevent="emit('close')" @keydown.stop>
      <h2>{{ kinds[props.type].label }}</h2>
      <code>{{ props.type }}</code>
      <p>{{ kinds[props.type].help }}</p>
      <p class="muted">从节点库拖入画布，即可添加此节点。</p>
      <div class="dialog-actions"><button autofocus @click="emit('close')">关闭</button></div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.node-help-dialog { width: min(480px, 90vw); }
p { line-height: 1.7; }
</style>
