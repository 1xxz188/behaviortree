<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import type { Definition } from "./project";

const props = defineProps<{
  definition: Definition; // 右击目标，只读显示，修改由工程入口提交。
  x: number; // 菜单视口横坐标。
  y: number; // 菜单视口纵坐标。
}>();
const emit = defineEmits<{
  close: []; // 取消或点击菜单外部。
  edit: []; // 打开目标定义的编辑表单。
  delete: []; // 仅在二次确认后提交删除。
  move: []; // 打开目录选择，不影响画布实例。
  tags: []; // 编辑工程内多标签关联。
  copy: []; // 复制单条定义的 JSON 数组。
  export: []; // 下载单条定义的 JSON 文件。
}>();
const confirming = ref(false); // 删除确认与菜单互斥显示。
const menu = ref<HTMLElement>(); // 浮层菜单，用于定位和键盘导航。
const dialog = ref<HTMLDialogElement>(); // 原生模态框约束焦点。
const cancelButton = ref<HTMLButtonElement>(); // 删除确认默认聚焦取消。
const position = ref({ left: props.x, top: props.y }); // 按视口边界修正的菜单坐标。
let previousFocus: HTMLElement | null = null; // 关闭时恢复菜单触发控件。
let restoreFocus = true; // 外部点击或进入编辑时保留新目标的焦点。

// 仅在打开及窗口尺寸变化时测量菜单，不轮询页面。
function fitMenu() {
  if (!menu.value) return;
  const bounds = menu.value.getBoundingClientRect();
  position.value = {
    left: Math.max(8, Math.min(props.x, window.innerWidth - bounds.width - 8)),
    top: Math.max(8, Math.min(props.y, window.innerHeight - bounds.height - 8)),
  };
}

// 删除按钮只打开确认框；取消、Escape 和背景操作均不会提交删除。
async function requestDelete() {
  confirming.value = true;
  await nextTick();
  dialog.value?.showModal();
  cancelButton.value?.focus();
}

// 编辑表单接管焦点，避免菜单卸载时将焦点移到弹窗外。
function edit() {
  restoreFocus = false;
  emit("edit");
}
// 分类表单接管焦点，卸载菜单时不再抢回原节点。
function organize(action: "move" | "tags") {
  restoreFocus = false;
  // 分类表单记录稳定的行按钮，避免取消后尝试聚焦已卸载的菜单项。
  if (previousFocus?.isConnected) previousFocus.focus();
  if (action === "move") emit("move"); else emit("tags");
}

// 只在菜单阶段响应外部点击，确认框由原生模态行为保护。
function onOutsidePointer(event: PointerEvent) {
  if (confirming.value || menu.value?.contains(event.target as Node)) return;
  restoreFocus = false;
  emit("close");
}

// 方向键选择菜单项，Escape 关闭，Tab 返回页面导航。
function onMenuKeydown(event: KeyboardEvent) {
  if (event.key === "Escape" || event.key === "Tab") {
    if (event.key === "Escape") event.preventDefault();
    emit("close");
    return;
  }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const items = [...(menu.value?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  const index = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
    : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[index]?.focus();
}

onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  document.addEventListener("pointerdown", onOutsidePointer, true);
  window.addEventListener("resize", fitMenu);
  fitMenu();
  menu.value?.querySelector<HTMLButtonElement>("button")?.focus();
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
    <div v-if="!confirming" ref="menu" class="catalog-context-menu" role="menu" aria-label="业务定义菜单"
      :style="{ left: `${position.left}px`, top: `${position.top}px` }" @contextmenu.prevent @keydown.stop="onMenuKeydown">
      <button type="button" role="menuitem" @click="edit">编辑定义</button>
      <button type="button" role="menuitem" @click="organize('move')">移动到</button>
      <button type="button" role="menuitem" @click="organize('tags')">设置标签</button>
      <button type="button" role="menuitem" @click="emit('copy')">复制定义 JSON</button>
      <button type="button" role="menuitem" @click="emit('export')">导出定义 JSON</button>
      <button type="button" role="menuitem" class="delete-action" @click="requestDelete">删除</button>
    </div>
    <dialog v-else ref="dialog" class="project-dialog" aria-labelledby="catalog-delete-title" @keydown.stop @cancel.prevent="emit('close')">
      <h2 id="catalog-delete-title">删除业务定义</h2>
      <p>确定删除“{{ definition.name }}”吗？</p>
      <p class="muted">定义 ID：{{ definition.id }} · {{ definition.goName }}</p>
      <p>已有节点会保留，引用此定义的节点需要重新绑定。此操作可撤销。</p>
      <div class="dialog-actions">
        <button ref="cancelButton" type="button" autofocus @click="emit('close')">取消</button>
        <button type="button" class="delete-action" @click="emit('delete')">确认删除</button>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.catalog-context-menu { position: fixed; z-index: 2000; min-width: 160px; max-width: calc(100vw - 16px); max-height: calc(100vh - 16px); overflow-y: auto; padding: 6px; border: 1px solid #49606c; border-radius: 8px; background: #18242e; box-shadow: 0 12px 32px #0008; }
.catalog-context-menu button { display: block; width: 100%; text-align: left; border-color: transparent; background: transparent; }
.delete-action { color: #ffabab; }
.project-dialog p { overflow-wrap: anywhere; }
</style>
