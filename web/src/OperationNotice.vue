<script setup lang="ts">
import { onScopeDispose, ref, watch } from "vue";

const props = defineProps<{
  message: string; // 最近一次操作的完整结果，包含实际保存路径。
  failed: boolean; // 失败时使用醒目的错误样式。
  busy: boolean; // 等待操作完成后再展示结果，避免把旧消息当成新结果。
}>();
const visible = ref(false); // 提示可手动关闭，下次操作完成后重新显示。
const displayDurationMs = 2000; // 每条提示显示两秒，新结果重新计时。
let dismissTimer: ReturnType<typeof setTimeout> | undefined; // 仅保留一个单次计时器，不轮询。
// 手动关闭、开始新操作或组件卸载时取消旧计时，避免旧消息关闭新提示。
function dismiss() {
  if (dismissTimer !== undefined) clearTimeout(dismissTimer);
  dismissTimer = undefined;
  visible.value = false;
}
watch(() => [props.message, props.failed, props.busy], () => {
  dismiss();
  // 同样的操作结果也会随 busy 结束重新显示，并获得完整的展示时长。
  visible.value = !props.busy && !!props.message;
  if (visible.value) dismissTimer = setTimeout(dismiss, displayDurationMs);
});
onScopeDispose(dismiss);
</script>

<template>
  <aside v-if="visible" class="operation-notice" :class="{ failed }" :role="failed ? 'alert' : 'status'" aria-live="polite" aria-atomic="true">
    <span class="operation-notice-icon" aria-hidden="true">{{ failed ? '!' : '✓' }}</span>
    <p>{{ message }}</p>
    <button type="button" aria-label="关闭操作提示" @click="dismiss">×</button>
  </aside>
</template>

<style scoped>
.operation-notice {
  position: fixed;
  z-index: 30;
  top: clamp(140px, 22vh, 240px);
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: flex-start;
  gap: 14px;
  width: max-content;
  min-width: min(320px, calc(100vw - 32px));
  max-width: min(680px, calc(100vw - 32px));
  padding: 18px 22px;
  box-sizing: border-box;
  border: 2px solid #80edc3;
  border-radius: 12px;
  background: #14513f;
  color: #f0fff8;
  box-shadow: 0 16px 48px #0009, 0 0 0 4px #80edc31a;
  font-size: 16px;
  font-weight: 600;
}
.operation-notice.failed { border-color: #ff9fa9; background: #672d3b; color: #fff1f3; box-shadow: 0 16px 48px #0009, 0 0 0 4px #ff9fa91a; }
.operation-notice-icon { flex-shrink: 0; display: grid; place-items: center; width: 28px; height: 28px; border-radius: 50%; background: #ffffff20; font-size: 21px; font-weight: 700; }
.operation-notice p { flex: 1; margin: 0; line-height: 1.7; overflow-wrap: anywhere; white-space: pre-wrap; }
.operation-notice button { flex-shrink: 0; padding: 2px 5px; border: 0; background: transparent; color: inherit; font-size: 24px; line-height: 1; }
</style>
