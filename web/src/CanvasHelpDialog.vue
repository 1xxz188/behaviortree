<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";

const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>(); // 原生模态框负责焦点约束与 Escape 关闭。
let previousFocus: HTMLElement | null = null; // 关闭后将焦点交还帮助按钮。
const backdropPressed = ref(false); // 避免选择说明文字后在遮罩释放时误关闭。

// 原生 dialog 的遮罩也以自身为目标，通过坐标识别真正的窗口外操作。
function isOutsideDialog(event: MouseEvent): boolean {
  if (!dialog.value || event.target !== dialog.value) return false;
  const bounds = dialog.value.getBoundingClientRect();
  return event.clientX < bounds.left || event.clientX > bounds.right
    || event.clientY < bounds.top || event.clientY > bounds.bottom;
}
// 只有按下和释放都在遮罩时才关闭说明。
function closeFromBackdrop(event: MouseEvent) {
  const shouldClose = backdropPressed.value && isOutsideDialog(event);
  backdropPressed.value = false;
  if (shouldClose) emit("close");
}

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
    <dialog ref="dialog" class="project-dialog canvas-help-dialog" aria-labelledby="canvas-help-title"
      @cancel.prevent="emit('close')" @keydown.stop
      @pointerdown="backdropPressed = isOutsideDialog($event)" @pointercancel="backdropPressed = false" @click="closeFromBackdrop">
      <header>
        <h2 id="canvas-help-title" tabindex="-1" autofocus>画布操作帮助</h2>
        <button type="button" aria-label="关闭画布操作帮助" title="关闭" @click="emit('close')"><span aria-hidden="true">×</span></button>
      </header>
      <dl>
        <dt>选择与移动</dt><dd>左键拖动空白处框选节点；拖动选中节点可批量移动。<kbd>Ctrl+A</kbd> 全选节点。</dd>
        <dt>删除节点</dt><dd><kbd>Delete</kbd> 删除选区，确认后生效。</dd>
        <dt>复制与粘贴</dt><dd><kbd>Ctrl+C</kbd> 复制选区，<kbd>Ctrl+V</kbd> 粘贴到鼠标位置；鼠标在画布外时粘贴到视口中心。</dd>
        <dt>修改连线</dt><dd>点击连线后，拖动靠近节点的线段改连。</dd>
        <dt>移动与缩放画布</dt><dd>在空白处按住右键拖动画布；滚轮缩放，点击“适应画布”查看整棵树。</dd>
        <dt>右键菜单</dt><dd>右键单击画布、节点或连线，打开对应功能菜单。</dd>
        <dt>查看输出</dt><dd>下方可查看校验结果、生成代码和业务骨架。向下拖动分隔线或点击“最小化”收起；点击标签或“展开”重新打开。</dd>
      </dl>
    </dialog>
  </Teleport>
</template>

<style scoped>
.canvas-help-dialog { width: min(620px, 90vw); max-height: 85vh; overflow-y: auto; }
header { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
h2 { margin: 0; }
header button { display: grid; place-items: center; width: 32px; height: 32px; padding: 0; border: 0; background: transparent; font-size: 26px; }
dl { margin: 20px 0 0; }
dt { color: #a9eed1; font-weight: 600; margin-top: 16px; }
dd { margin: 6px 0 0; line-height: 1.8; color: #b9cad6; }
kbd { padding: 2px 5px; border: 1px solid #354958; border-radius: 4px; background: #101a23; font-size: 12px; white-space: nowrap; }
</style>
