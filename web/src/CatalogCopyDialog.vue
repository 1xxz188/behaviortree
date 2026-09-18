<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from "vue";
const props = defineProps<{ content: string }>(); // 已校验、可再次导入的 JSON 数组。
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(); // 模态框约束焦点，防止误操作画布。
const text = ref<HTMLTextAreaElement>(); // 只读内容供原生选择和复制。
let previousFocus: HTMLElement | null = null; // 关闭后恢复到原控件。
// 打开时选中全部内容，剪贴板权限不可用时也能用系统快捷键复制。
onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  dialog.value?.showModal(); text.value?.select();
});
onBeforeUnmount(() => { dialog.value?.close(); if (previousFocus?.isConnected) previousFocus.focus(); });
</script>
<template>
  <Teleport to="body"><dialog ref="dialog" class="project-dialog catalog-copy-dialog" aria-label="手动复制业务定义 JSON" @cancel.prevent="emit('close')" @keydown.stop>
    <h2>复制业务定义 JSON</h2>
    <p>浏览器剪贴板不可用。请复制以下内容，再到目标工程粘贴导入。</p>
    <textarea ref="text" :value="props.content" readonly aria-label="业务定义 JSON" spellcheck="false" />
    <div class="dialog-actions"><button @click="text?.select()">全选</button><button @click="emit('close')">关闭</button></div>
  </dialog></Teleport>
</template>
<style scoped>
.catalog-copy-dialog { width: min(760px, 90vw); }
textarea { width: 100%; min-height: 300px; resize: vertical; font-family: monospace; font-size: 12px; }
</style>
