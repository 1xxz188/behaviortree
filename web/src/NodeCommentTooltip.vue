<script setup lang="ts">
import { computed, ref } from "vue";
import CommentTooltip, { type CommentTarget } from "./CommentTooltip.vue";
import type { BTNode, Tree } from "./project";
import { kinds } from "./project";

const props = defineProps<{
  tree: Tree; // 树对象替换时关闭旧节点说明，不遍历节点列表。
  disabled: boolean; // 菜单、对话框或工程切换期间不展示说明。
  autoOpen: boolean; // 仅控制鼠标悬浮，手动入口始终可用。
  commit: (tree: Tree, node: BTNode, value: string) => boolean; // 工程入口校验身份并统一记录历史。
}>();
const tooltip = ref<InstanceType<typeof CommentTooltip>>(); // 节点入口共用通用注释浮层。
const activeNode = computed(() => tooltip.value?.activeTarget as BTNode | undefined); // 兼容画布气泡显示条件。

// 节点提交仍经过 App 的身份校验及历史事务。
function commit(target: CommentTarget, value: string): boolean {
  return props.commit(props.tree, target as BTNode, value);
}

// 节点名称沿用画布标题的回退顺序。
function getTitle(target: CommentTarget): string {
  const node = target as BTNode;
  return node.name || kinds[node.type]?.label || node.id;
}

// 读注释时不改变节点对象，缺省值交由通用浮层处理。
function getComment(target: CommentTarget): string | undefined {
  return (target as BTNode).comment;
}

// 保持画布鼠标与快捷菜单的原有组件 API。
function show(node: BTNode, element: HTMLElement) { return tooltip.value?.show(node, element); }
function edit(node: BTNode, element: HTMLElement) { return tooltip.value?.edit(node, element); }
function hide() { tooltip.value?.hide(); }
function scheduleHide(event?: MouseEvent) { tooltip.value?.scheduleHide(event); }

defineExpose({ show, edit, hide, scheduleHide, activeNode });
</script>

<template>
  <CommentTooltip ref="tooltip" :context="tree" :disabled="disabled" :auto-open="autoOpen"
    :commit="commit" :get-title="getTitle" :get-comment="getComment"
    label="节点注释" tooltip-id="node-comment-tooltip" />
</template>
