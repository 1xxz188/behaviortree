<script setup lang="ts">
import { onMounted, ref } from "vue";

defineProps<{
  path: string; // 将被覆盖的实际业务文件路径。
}>();
const emit = defineEmits<{
  close: [confirmed: boolean]; // 只有点击确认覆盖才授权写入。
}>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框限制焦点并阻止背景编辑。
onMounted(() => dialog.value?.showModal());
</script>

<template>
  <dialog ref="dialog" class="project-dialog" aria-labelledby="scaffold-overwrite-title" @cancel.prevent="emit('close', false)">
    <h2 id="scaffold-overwrite-title">确认覆盖 actions.go？</h2>
    <p>目标文件已存在：</p>
    <p class="dialog-path mono">{{ path }}</p>
    <p>覆盖会替换整个文件，已有的业务实现将丢失。取消可保留原文件。</p>
    <div class="dialog-actions">
      <button autofocus @click="emit('close', false)">取消</button>
      <button class="primary" @click="emit('close', true)">确认覆盖</button>
    </div>
  </dialog>
</template>
