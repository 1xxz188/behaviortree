<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import type { BTNode } from "./project";

const props = defineProps<{
  x: number; // 右键操作的视口横坐标。
  y: number; // 右键操作的视口纵坐标。
  node?: BTNode; // 有目标节点时展示节点操作，否则展示画布操作。
  initialMode?: "menu" | "delete"; // 允许其他节点入口复用删除确认。
  disabled: boolean; // 无可编辑工程时禁用操作。
  pngBusy: boolean; // 图片导出过程中禁止重复提交。
}>();
const emit = defineEmits<{
  close: []; // 关闭整个菜单或取消确认。
  duplicate: []; // 复制当前右键目标节点。
  delete: []; // 仅在二次确认后提交节点删除。
  comment: [value: string]; // 应用注释草稿，由父组件校验目标身份并记录历史。
  save: []; // 保存当前工程。
  saveAs: []; // 将当前工程另存为。
  json: []; // 导出工程 JSON。
  png: []; // 导出当前行为树图片。
}>();
const mode = ref<"menu" | "delete" | "comment">(props.initialMode ?? "menu"); // 当前交互阶段。
const menu = ref<HTMLElement>(); // 一级菜单，用于测量和键盘导航。
const exportMenu = ref<HTMLElement>(); // 独立固定定位，避免被一级菜单裁剪。
const exportTrigger = ref<HTMLButtonElement>(); // 收起二级菜单时恢复焦点。
const dialog = ref<HTMLDialogElement>(); // 原生模态对话框约束删除确认焦点。
const cancelButton = ref<HTMLButtonElement>(); // 左右键切换时的取消目标。
const confirmButton = ref<HTMLButtonElement>(); // 删除确认默认聚焦的主要操作。
const commentInput = ref<HTMLTextAreaElement>(); // 注释窗口打开后聚焦多行输入框。
const commentDraft = ref(""); // 草稿只在应用时提交，输入与取消均不修改工程。
const exportOpen = ref(false); // 二级菜单由点击或键盘显式展开。
const position = ref({ left: props.x, top: props.y }); // 裁剪后的一级菜单坐标。
const exportPosition = ref({ left: props.x, top: props.y }); // 裁剪后的导出菜单坐标。
let previousFocus: HTMLElement | null = null; // 关闭后恢复到原控件。
let restoreFocus = true; // 外部点击和 Tab 导航保留用户的新焦点。

// 按菜单实际尺寸限制视口边界，仅在打开或窗口尺寸变化时测量。
function fitMenus() {
  if (!menu.value) return;
  const bounds = menu.value.getBoundingClientRect();
  const left = Math.max(8, Math.min(props.x, window.innerWidth - bounds.width - 8));
  const top = Math.max(8, Math.min(props.y, window.innerHeight - bounds.height - 8));
  position.value = { left, top };
  if (!exportMenu.value || !exportTrigger.value) return;
  const subBounds = exportMenu.value.getBoundingClientRect();
  const triggerBounds = exportTrigger.value.getBoundingClientRect();
  // 右侧空间不足时翻到左侧，纵坐标同步补偿一级菜单的本次位移。
  const preferredLeft = left + bounds.width + 4;
  exportPosition.value = {
    left: Math.max(8, Math.min(preferredLeft + subBounds.width <= window.innerWidth - 8
      ? preferredLeft : left - subBounds.width - 4, window.innerWidth - subBounds.width - 8)),
    top: Math.max(8, Math.min(triggerBounds.top + top - bounds.top, window.innerHeight - subBounds.height - 8)),
  };
}

// 展开导出子菜单并聚焦首个可用格式。
async function openExport() {
  if (props.disabled) return;
  exportOpen.value = true;
  await nextTick();
  fitMenus();
  exportMenu.value?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
}

// 返回一级菜单的导出入口，保留连续键盘操作。
function closeExport() {
  exportOpen.value = false;
  exportTrigger.value?.focus();
}

// 切换到删除确认，默认选中确认按钮。
async function openDelete() {
  if (props.disabled || !props.node) return;
  mode.value = "delete";
  exportOpen.value = false;
  await nextTick();
  dialog.value?.showModal();
  confirmButton.value?.focus();
}

// 左右键在两个可用按钮间切换，回车和空格沿用按钮原生确认行为。
function onDialogKeydown(event: KeyboardEvent) {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  const target = document.activeElement === confirmButton.value ? cancelButton.value : confirmButton.value;
  if (target && !target.disabled) target.focus();
}

// 二次确认时复核目标和禁用状态，删除由父组件执行。
function confirmDelete() {
  if (!props.disabled && props.node) emit("delete");
}

// 回显右键目标的注释，复用原生模态框隔离画布快捷键。
async function openComment() {
  if (props.disabled || !props.node) return;
  commentDraft.value = props.node.comment ?? "";
  mode.value = "comment";
  exportOpen.value = false;
  await nextTick();
  dialog.value?.showModal();
  commentInput.value?.focus();
}

// 保留正文换行；空白草稿表示清除注释，身份与历史交由工程入口处理。
function submitComment() {
  if (!props.disabled && props.node) emit("comment", commentDraft.value.trim());
}

// 点击两个菜单以外的区域时关闭浮层，不抢走新目标的焦点。
function onOutsidePointer(event: PointerEvent) {
  if (mode.value !== "menu" || !(event.target instanceof Node)) return;
  if (menu.value?.contains(event.target) || exportMenu.value?.contains(event.target)) return;
  restoreFocus = false;
  emit("close");
}

// 上下方向键循环导航，左右键进入/退出子菜单，Escape 分层关闭。
function onMenuKeydown(event: KeyboardEvent, submenu = false) {
  if (event.key === "Tab") {
    restoreFocus = false;
    emit("close");
    return;
  }
  if (event.key === "Escape" || (submenu && event.key === "ArrowLeft")) {
    event.preventDefault();
    if (exportOpen.value) closeExport();
    else emit("close");
    return;
  }
  if (!submenu && event.key === "ArrowRight" && document.activeElement === exportTrigger.value) {
    event.preventDefault();
    void openExport();
    return;
  }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const root = submenu ? exportMenu.value : menu.value;
  const items = [...(root?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? [])];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  const index = event.key === "Home" ? 0
    : event.key === "End" ? items.length - 1
    : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[index]?.focus();
}

onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  document.addEventListener("pointerdown", onOutsidePointer, true);
  window.addEventListener("resize", fitMenus);
  if (mode.value === "delete") {
    dialog.value?.showModal();
    confirmButton.value?.focus();
  } else {
    fitMenus();
    const firstItem = menu.value?.querySelector<HTMLButtonElement>("button:not(:disabled)");
    (firstItem ?? menu.value)?.focus();
  }
});
onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", onOutsidePointer, true);
  window.removeEventListener("resize", fitMenus);
  dialog.value?.close();
  if (restoreFocus && previousFocus?.isConnected) previousFocus.focus();
});
</script>

<template>
  <Teleport to="body">
    <div v-if="mode === 'menu'" ref="menu" class="canvas-context-menu" role="menu" tabindex="-1"
      :aria-label="node ? '节点菜单' : '画布菜单'" :style="{ left: `${position.left}px`, top: `${position.top}px` }"
      @contextmenu.prevent @keydown.stop="onMenuKeydown($event)">
      <template v-if="node">
        <button type="button" role="menuitem" :disabled="disabled" @click="emit('duplicate')">复制节点</button>
        <button type="button" role="menuitem" :disabled="disabled" @click="openComment">修改注释</button>
        <button type="button" role="menuitem" class="canvas-delete-action" :disabled="disabled" @click="openDelete">删除节点</button>
      </template>
      <template v-else>
        <button type="button" role="menuitem" :disabled="disabled" @click="emit('save')">保存</button>
        <button type="button" role="menuitem" :disabled="disabled" @click="emit('saveAs')">另存为</button>
        <button ref="exportTrigger" type="button" role="menuitem" :disabled="disabled" aria-haspopup="menu"
          :aria-expanded="exportOpen" aria-controls="canvas-export-options" @click="exportOpen ? closeExport() : openExport()">
          导出 <span class="submenu-arrow" aria-hidden="true">▸</span>
        </button>
      </template>
    </div>
    <div v-if="mode === 'menu' && exportOpen" id="canvas-export-options" ref="exportMenu"
      class="canvas-context-menu canvas-export-options" role="menu" aria-label="导出格式"
      :style="{ left: `${exportPosition.left}px`, top: `${exportPosition.top}px` }"
      @contextmenu.prevent @keydown.stop="onMenuKeydown($event, true)">
      <button type="button" role="menuitem" :disabled="disabled" @click="emit('json')">导出 JSON</button>
      <button type="button" role="menuitem" :disabled="disabled || pngBusy" @click="emit('png')">{{ pngBusy ? '正在导出 PNG…' : '导出 PNG' }}</button>
    </div>
    <dialog v-if="mode === 'delete'" ref="dialog" class="project-dialog canvas-operation-dialog"
      aria-labelledby="canvas-delete-title" @keydown.stop="onDialogKeydown" @cancel.prevent="emit('close')">
      <h2 id="canvas-delete-title">删除节点</h2>
      <p>确定删除节点“{{ node?.name || node?.id }}”吗？</p>
      <p class="muted">节点 ID：<span class="mono">{{ node?.id }}</span>。此操作可撤销。</p>
      <div class="dialog-actions">
        <button ref="cancelButton" type="button" @click="emit('close')">取消</button>
        <button ref="confirmButton" type="button" autofocus class="canvas-delete-action" :disabled="disabled || !node" @click="confirmDelete">确认删除</button>
      </div>
    </dialog>
    <dialog v-if="mode === 'comment'" ref="dialog" class="project-dialog canvas-operation-dialog"
      aria-labelledby="canvas-comment-title" @keydown.stop @cancel.prevent="emit('close')">
      <h2 id="canvas-comment-title">修改注释</h2>
      <p>节点：{{ node?.name || node?.id }}</p>
      <form @submit.prevent="submitComment">
        <label class="field-label">节点注释
          <textarea ref="commentInput" v-model="commentDraft" rows="7" autofocus aria-label="节点注释"
            aria-describedby="canvas-comment-help" :disabled="disabled" />
        </label>
        <p id="canvas-comment-help" class="muted">支持多行，清空后应用可删除注释。</p>
        <div class="dialog-actions">
          <button type="button" @click="emit('close')">取消</button>
          <button type="submit" class="primary" :disabled="disabled || !node">应用</button>
        </div>
      </form>
    </dialog>
  </Teleport>
</template>

<style scoped>
.canvas-context-menu { position: fixed; z-index: 2000; min-width: min(180px, calc(100vw - 16px)); max-width: calc(100vw - 16px); max-height: calc(100vh - 16px); overflow-y: auto; box-sizing: border-box; padding: 6px; border: 1px solid #49606c; border-radius: 8px; background: #18242e; box-shadow: 0 12px 32px #0008; }
.canvas-context-menu button { display: block; width: 100%; text-align: left; border-color: transparent; background: transparent; }
/* 菜单用背景色标识键盘焦点，避免右键打开时出现全局绿色轮廓。 */
.canvas-context-menu:focus, .canvas-context-menu button:focus { outline: none; }
.canvas-context-menu button:hover:not(:disabled), .canvas-context-menu button:focus-visible { background: #2d414e; }
.canvas-export-options { z-index: 2001; }
.submenu-arrow { float: right; margin-left: 24px; }
.canvas-delete-action { color: #ffabab; }
.canvas-operation-dialog p { overflow-wrap: anywhere; }
.canvas-operation-dialog textarea { width: 100%; box-sizing: border-box; resize: vertical; }
</style>
