<script setup lang="ts">
import { onMounted, ref } from "vue";

defineProps<{
  name: string; // 导入失败的本地文件名。
  message: string; // JSON 解析或工程校验返回的具体原因。
}>();
const emit = defineEmits<{
  close: []; // 确认或按 Escape 后关闭提示。
}>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框约束焦点并阻止背景交互。
onMounted(() => dialog.value?.showModal());
</script>

<template>
  <dialog ref="dialog" class="project-dialog import-error-dialog" aria-labelledby="import-error-title" aria-describedby="import-error-detail" @cancel.prevent="emit('close')">
    <h2 id="import-error-title">导入失败</h2>
    <p>无法导入 {{ name }}，当前工程已保留。</p>
    <p id="import-error-detail" class="identity-error">{{ message }}</p>
    <div class="dialog-actions"><button autofocus class="primary" @click="emit('close')">确定</button></div>
  </dialog>
</template>

<style scoped>
.import-error-dialog p { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
