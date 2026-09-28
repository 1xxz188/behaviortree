<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { projectFileKey, validProjectFileName, selectNativeDirectory } from "./workspace";
import type { WorkspaceFiles, ProjectDialogResult } from "./workspace";

const props = defineProps<{
  kind: "save" | "switch"; // 保存位置或替换工程确认。
  reload?: boolean; // 同名重载时区分读取磁盘与先写入当前修改。
  workspace: string; // 文件将保存到的绝对目录。
  suggestion: string; // 建议名称，不代表已经保存。
  files: string[]; // 用于提示覆盖已有文件。
}>();
const emit = defineEmits<{
  close: [value?: ProjectDialogResult]; // 保存位置、保存/放弃选择，或取消。
}>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框负责焦点约束和恢复。
const name = ref(props.suggestion); // 用户尚未提交的文件名。
const nameInput = ref<HTMLInputElement>(); // 意外关闭时将焦点留在文件名草稿。
const overwrite = ref(false); // 覆盖已有文件需要明确勾选。
const selecting = ref(false); // 系统窗口打开期间阻止重复选择或提交。
const directoryError = ref(""); // 原生窗口或目录读取失败的具体原因。
const destination = ref<WorkspaceFiles>({ workspace: props.workspace, files: props.files }); // 取消选择保留上一次成功的保存位置。
const existing = computed(() => new Map((destination.value.allFiles ?? destination.value.files).map(file => [projectFileKey(destination.value.workspace, file), file]))); // 目标目录变化时建立一次索引，无效工程也须确认覆盖。
const collision = computed(() => existing.value.get(projectFileKey(destination.value?.workspace ?? "", name.value.trim())));
const valid = computed(() => validProjectFileName(name.value.trim()));
const hasPending = computed(() => props.kind === "save" && (name.value !== props.suggestion || destination.value.workspace !== props.workspace || overwrite.value));
const pendingWarning = ref(""); // Escape 放弃输入前提示显式保存或取消。
// 保存对话框遇到未应用位置或文件名时保留当前输入。
function focusPending() {
  pendingWarning.value = "保存设置有未应用更改，请先保存或点击“取消”放弃。";
  nameInput.value?.focus();
}
defineExpose({ hasPending, focusPending });
// Escape 不直接丢弃已编辑的保存设置，取消按钮仍可明确放弃。
function requestClose() {
  if (selecting.value) return;
  if (hasPending.value) focusPending();
  else emit("close");
}
// 保存名称验证通过后交给调用方执行 IO。
function submit() {
  if (!selecting.value && valid.value && (!collision.value || overwrite.value)) {
    emit("close", { name: collision.value || name.value.trim(), directory: destination.value.workspace, overwrite: overwrite.value });
  }
}
// 在系统窗口中更换目录；取消或失败保留原位置，成功后重新检查同名覆盖。
async function selectDirectory() {
  if (selecting.value) return;
  selecting.value = true;
  directoryError.value = "";
  try {
    const selected = await selectNativeDirectory(props.workspace, destination.value.workspace);
    if (selected) {
      destination.value = selected;
      overwrite.value = false;
    }
  } catch (e) {
    directoryError.value = e instanceof Error ? e.message : String(e);
  } finally {
    selecting.value = false;
  }
}
onMounted(() => dialog.value?.showModal());
</script>

<template>
  <dialog ref="dialog" class="project-dialog" aria-labelledby="project-dialog-title" @cancel.prevent="requestClose">
    <h2 id="project-dialog-title">{{ kind === 'save' ? '保存工程' : '当前工程有未保存修改' }}</h2>
    <template v-if="kind === 'save'">
      <p class="muted">保存目录</p>
      <p class="dialog-path mono">{{ destination.workspace }}</p>
      <button type="button" :disabled="selecting" @click="selectDirectory">{{ selecting ? '等待系统目录窗口…' : '选择目录…' }}</button>
      <p v-if="directoryError" class="identity-error" role="alert">{{ directoryError }}</p>
      <p v-if="destination.workspace !== workspace" class="muted">保存成功后切换到此工作目录，后续保存和生成使用此目录。</p>
      <form @submit.prevent="submit">
        <label class="field-label" :class="{ 'pending-field': name !== suggestion }">文件名
          <input ref="nameInput" v-model="name" autofocus aria-label="工程文件名" @input="overwrite = false" />
        </label>
        <p v-if="!valid" class="identity-error">请输入顶层 JSON 文件名，例如 bt_project.json。</p>
        <label v-if="collision" class="overwrite-choice"><input v-model="overwrite" type="checkbox" />确认覆盖已有文件 {{ collision }}</label>
        <p v-if="pendingWarning" class="identity-error" role="alert">{{ pendingWarning }}</p>
        <div class="dialog-actions"><button type="button" :disabled="selecting" @click="emit('close')">取消</button><button type="submit" class="primary" :class="{ 'pending-action': hasPending }" :disabled="selecting || !valid || (!!collision && !overwrite)">保存{{ hasPending ? ' · 未应用' : '' }}</button></div>
      </form>
    </template>
    <template v-else>
      <p v-if="reload">重载会读取磁盘版本。放弃修改将丢弃当前草稿；保存并重载会先将当前内容写入文件。取消将保留当前内容。</p>
      <p v-else>保存当前修改后继续，或放弃修改。取消将留在当前工程。</p>
      <div class="dialog-actions"><button autofocus @click="emit('close')">取消</button><button @click="emit('close', 'discard')">{{ reload ? '放弃修改并重载' : '放弃修改' }}</button><button class="primary" @click="emit('close', 'save')">{{ reload ? '保存并重载' : '保存并切换' }}</button></div>
    </template>
  </dialog>
</template>
