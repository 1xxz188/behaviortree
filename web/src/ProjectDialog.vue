<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { projectFileKey, validProjectFileName } from "./workspace";

const props = defineProps<{
  kind: "save" | "switch"; // 首次保存、另存为或替换工程的确认。
  reload?: boolean; // 同名重载时区分读取磁盘与先写入当前修改。
  workspace: string; // 文件将保存到的绝对目录。
  suggestion: string; // 建议名称，不代表已经保存。
  files: string[]; // 用于提示覆盖已有文件。
}>();
const emit = defineEmits<{
  close: [value?: string]; // 文件名、保存/放弃选择，或取消。
}>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框负责焦点约束和恢复。
const name = ref(props.suggestion); // 用户尚未提交的文件名。
const overwrite = ref(false); // 覆盖已有文件需要明确勾选。
const existing = computed(() => new Map(props.files.map(file => [projectFileKey(props.workspace, file), file]))); // 列表变化时建立一次索引。
const collision = computed(() => existing.value.get(projectFileKey(props.workspace, name.value.trim())));
const valid = computed(() => validProjectFileName(name.value.trim()));
// 保存名称验证通过后交给调用方执行 IO。
function submit() {
  if (valid.value && (!collision.value || overwrite.value)) emit("close", collision.value || name.value.trim());
}
onMounted(() => dialog.value?.showModal());
</script>

<template>
  <dialog ref="dialog" class="project-dialog" aria-labelledby="project-dialog-title" @cancel.prevent="emit('close')">
    <h2 id="project-dialog-title">{{ kind === 'save' ? '保存工程' : '当前工程有未保存修改' }}</h2>
    <template v-if="kind === 'save'">
      <p class="muted">保存到工作目录</p>
      <p class="dialog-path mono">{{ workspace }}</p>
      <form @submit.prevent="submit">
        <label class="field-label">文件名
          <input v-model="name" autofocus aria-label="工程文件名" @input="overwrite = false" />
        </label>
        <p v-if="!valid" class="identity-error">请输入顶层 JSON 文件名，例如 bt_project.json。</p>
        <label v-if="collision" class="overwrite-choice"><input v-model="overwrite" type="checkbox" />确认覆盖已有文件 {{ collision }}</label>
        <div class="dialog-actions"><button type="button" @click="emit('close')">取消</button><button type="submit" class="primary" :disabled="!valid || (!!collision && !overwrite)">保存</button></div>
      </form>
    </template>
    <template v-else>
      <p v-if="reload">重载会读取磁盘版本。放弃修改将丢弃当前草稿；保存并重载会先将当前内容写入文件。取消将保留当前内容。</p>
      <p v-else>保存当前修改后继续，或放弃修改。取消将留在当前工程。</p>
      <div class="dialog-actions"><button autofocus @click="emit('close')">取消</button><button @click="emit('close', 'discard')">{{ reload ? '放弃修改并重载' : '放弃修改' }}</button><button class="primary" @click="emit('close', 'save')">{{ reload ? '保存并重载' : '保存并切换' }}</button></div>
    </template>
  </dialog>
</template>
