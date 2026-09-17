<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import type { Tree } from "./project";

const props = defineProps<{
  tree: Tree; // 右击的目标树，只读展示，修改交由父组件处理。
  x: number; // 菜单锚点的视口横坐标。
  y: number; // 菜单锚点的视口纵坐标。
  canDelete: boolean; // 工程至少保留一棵树。
  initialMode?: "menu" | "delete"; // 允许属性栏直接复用删除确认。
}>();
const emit = defineEmits<{
  close: []; // 取消或点击外部后关闭整个交互。
  rename: [name: string]; // 仅提交去除首尾空格后的展示名。
  delete: []; // 用户明确确认删除后才提交。
}>();
const mode = ref<"menu" | "rename" | "delete">(props.initialMode ?? "menu"); // 当前菜单或对话框。
const menu = ref<HTMLElement>(); // 浮层菜单，负责边界测量和键盘导航。
const dialog = ref<HTMLDialogElement>(); // 原生模态框提供焦点约束。
const nameInput = ref<HTMLInputElement>(); // 重命名时自动选中原展示名。
const cancelButton = ref<HTMLButtonElement>(); // 删除默认聚焦取消按钮。
const name = ref(props.tree.name); // 不直接修改目标树的名称草稿。
const validName = computed(() => name.value.trim().length > 0); // 空白名称禁止提交。
const position = ref({ left: props.x, top: props.y }); // 实际显示坐标经视口裁剪。
let previousFocus: HTMLElement | null = null; // 关闭后恢复到打开菜单的控件。
let restoreFocus = true; // 点击外部时保留用户刚选择的焦点。

// 根据浮层实际尺寸裁剪坐标；仅在打开和窗口尺寸改变时测量。
function fitMenu() {
  if (!menu.value) return;
  const bounds = menu.value.getBoundingClientRect();
  position.value = {
    left: Math.max(8, Math.min(props.x, window.innerWidth - bounds.width - 8)),
    top: Math.max(8, Math.min(props.y, window.innerHeight - bounds.height - 8)),
  };
}

// 菜单切换为原生对话框后，在 DOM 更新完成时打开并设置初始焦点。
async function openDialog(nextMode: "rename" | "delete") {
  if (nextMode === "delete" && !props.canDelete) return;
  mode.value = nextMode;
  await nextTick();
  dialog.value?.showModal();
  if (nextMode === "rename") {
    nameInput.value?.focus();
    nameInput.value?.select();
  } else {
    cancelButton.value?.focus();
  }
}

// 只在菜单阶段处理外部点击，删除确认不会因背景点击而误提交。
function onOutsidePointer(event: PointerEvent) {
  if (mode.value !== "menu" || menu.value?.contains(event.target as Node)) return;
  restoreFocus = false;
  emit("close");
}

// 方向键循环选择可用菜单项；Escape 关闭，Tab 交还普通页面导航。
function onMenuKeydown(event: KeyboardEvent) {
  if (event.key === "Escape" || event.key === "Tab") {
    if (event.key === "Escape") event.preventDefault();
    emit("close");
    return;
  }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const items = [...(menu.value?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? [])];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  const index = event.key === "Home" ? 0
    : event.key === "End" ? items.length - 1
    : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[index]?.focus();
}

// 再次校验名称，避免回车绕过按钮禁用状态。
function submitRename() {
  if (validName.value) emit("rename", name.value.trim());
}

// 确认时再次校验删除条件，父组件仍须针对最新工程状态复核。
function confirmDelete() {
  if (props.canDelete) emit("delete");
}

onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  document.addEventListener("pointerdown", onOutsidePointer, true);
  window.addEventListener("resize", fitMenu);
  if (mode.value === "delete") {
    dialog.value?.showModal();
    cancelButton.value?.focus();
  } else {
    fitMenu();
    menu.value?.querySelector<HTMLButtonElement>("button")?.focus();
  }
});
onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", onOutsidePointer, true);
  window.removeEventListener("resize", fitMenu);
  dialog.value?.close();
  if (restoreFocus && previousFocus?.isConnected) previousFocus.focus();
});
</script>

<template>
  <Teleport to="body">
    <div v-if="mode === 'menu'" ref="menu" class="tree-context-menu" role="menu" aria-label="行为树菜单"
      :style="{ left: `${position.left}px`, top: `${position.top}px` }" @contextmenu.prevent @keydown.stop="onMenuKeydown">
      <button type="button" role="menuitem" @click="openDialog('rename')">重命名行为树</button>
      <button type="button" role="menuitem" class="tree-delete-action" :disabled="!canDelete" :aria-describedby="!canDelete ? 'tree-delete-disabled' : undefined" @click="openDialog('delete')">删除行为树</button>
      <p v-if="!canDelete" id="tree-delete-disabled" class="muted">工程至少保留一棵行为树。</p>
    </div>
    <dialog v-else ref="dialog" class="project-dialog tree-operation-dialog" aria-labelledby="tree-operation-title" @keydown.stop @cancel.prevent="emit('close')">
      <h2 id="tree-operation-title">{{ mode === 'rename' ? '重命名行为树' : '删除行为树' }}</h2>
      <form v-if="mode === 'rename'" @submit.prevent="submitRename">
        <label class="field-label">行为树名称
          <input ref="nameInput" v-model="name" autofocus aria-label="行为树名称" aria-describedby="tree-rename-help" />
        </label>
        <p id="tree-rename-help" class="muted">仅修改展示名称，行为树 ID（{{ tree.id }}）保持不变。</p>
        <p v-if="!validName" class="identity-error" role="alert">行为树名称不能为空。</p>
        <div class="dialog-actions">
          <button type="button" @click="emit('close')">取消</button>
          <button type="submit" class="primary" :disabled="!validName">应用</button>
        </div>
      </form>
      <template v-else>
        <p>确定删除行为树“{{ tree.name }}”吗？</p>
        <p class="muted">行为树 ID：<span class="mono">{{ tree.id }}</span>，包含 {{ tree.nodes.length }} 个节点。</p>
        <p>删除后，其他行为树中引用它的子树节点需要重新选择目标。此操作可撤销。</p>
        <p v-if="!canDelete" class="identity-error">工程至少保留一棵行为树。</p>
        <div class="dialog-actions">
          <button ref="cancelButton" type="button" autofocus @click="emit('close')">取消</button>
          <button type="button" class="tree-delete-action" :disabled="!canDelete" @click="confirmDelete">确认删除</button>
        </div>
      </template>
    </dialog>
  </Teleport>
</template>

<style scoped>
.tree-context-menu { position: fixed; z-index: 2000; min-width: 190px; max-width: calc(100vw - 16px); max-height: calc(100vh - 16px); overflow-y: auto; padding: 6px; border: 1px solid #49606c; border-radius: 8px; background: #18242e; box-shadow: 0 12px 32px #0008; }
.tree-context-menu button { display: block; width: 100%; text-align: left; border-color: transparent; background: transparent; }
.tree-context-menu p { margin: 6px 8px; font-size: 12px; }
.tree-delete-action { color: #ffabab; }
.tree-operation-dialog p { overflow-wrap: anywhere; }
</style>
