<script setup lang="ts">
import { computed } from "vue";
import { kinds } from "./project";
import type { NodeType } from "./enums";
import { nodeHelpExamples } from "./nodeHelpExamples";
import type { HelpExampleNode } from "./nodeHelpExamples";

const props = defineProps<{ type: NodeType }>(); // 当前说明对应的示例，不接入工程编辑状态。
// 布局节点仅保存静态示意图需要的坐标与内容。
interface PositionedNode {
  node: HelpExampleNode; // 示例节点及配置摘要。
  x: number; // 从左到右的层级坐标。
  y: number; // 按子节点顺序排列的纵坐标。
}
// 连线序号表示父节点中配置的子节点顺序，不代表跨层执行序号。
interface ExampleEdge {
  from: PositionedNode; // 父节点位置。
  to: PositionedNode; // 子节点位置。
  order: number; // 从一开始的子节点序号。
}
const cardWidth = 176;
const cardHeight = 78;
// 仅在说明类型变化时对小型示例树做一次线性布局，不测量 DOM 或定时刷新。
const drawing = computed(() => {
  const nodes: PositionedNode[] = [];
  const edges: ExampleEdge[] = [];
  let leaf = 0;
  let depthMax = 0;
  // 叶节点依次占行，父节点居中对齐首尾子节点，避免分支重叠。
  function place(node: HelpExampleNode, depth: number): PositionedNode {
    depthMax = Math.max(depthMax, depth);
    const current: PositionedNode = { node, x: 20 + depth * 236, y: 0 };
    nodes.push(current);
    const children = (node.children ?? []).map((child, index) => {
      const position = place(child, depth + 1);
      edges.push({ from: current, to: position, order: index + 1 });
      return position;
    });
    current.y = children.length ? (children[0]!.y + children[children.length - 1]!.y) / 2 : 20 + leaf++ * 102;
    return current;
  }
  place(nodeHelpExamples[props.type].root, 0);
  return { nodes, edges, width: 40 + depthMax * 236 + cardWidth, height: 40 + (leaf - 1) * 102 + cardHeight };
});
// 贝塞尔连线连接父节点右端与子节点左端，不将兄弟节点串成父子关系。
function edgePath(edge: ExampleEdge): string {
  const x = edge.from.x + cardWidth;
  const y = edge.from.y + cardHeight / 2;
  const targetY = edge.to.y + cardHeight / 2;
  const middle = (x + edge.to.x) / 2;
  return `M ${x} ${y} C ${middle} ${y}, ${middle} ${targetY}, ${edge.to.x} ${targetY}`;
}
</script>

<template>
  <figure class="help-canvas">
    <div class="canvas-heading"><strong>画布示例</strong><span>只读示意 · 连线数字为子节点顺序</span></div>
    <div class="canvas-scroll" tabindex="0" role="region" aria-label="示例画布，可横向滚动">
      <svg :viewBox="`0 0 ${drawing.width} ${drawing.height}`" :style="{ width: `${drawing.width}px` }" role="img" :aria-label="`${kinds[type].label}画布示例`">
        <title>{{ kinds[type].label }}：{{ nodeHelpExamples[type].caption }}</title>
        <g v-for="(edge, index) in drawing.edges" :key="`edge-${index}`" class="example-edge">
          <path :d="edgePath(edge)" />
          <text :x="edge.to.x - 16" :y="edge.to.y + cardHeight / 2 - 8">{{ edge.order }}</text>
        </g>
        <g v-for="(item, index) in drawing.nodes" :key="index" :transform="`translate(${item.x}, ${item.y})`" :class="['example-node', kinds[item.node.type].color, { featured: item.node.type === type }]">
          <title>{{ item.node.label }}，{{ kinds[item.node.type].label }}。{{ item.node.detail }}</title>
          <rect :width="cardWidth" :height="cardHeight" rx="7" />
          <path class="accent" :d="`M 1 9 V ${cardHeight - 9}`" />
          <text class="kind" x="12" y="19">{{ kinds[item.node.type].icon }} {{ kinds[item.node.type].label }}</text>
          <text class="label" x="12" y="42">{{ item.node.label }}</text>
          <text class="detail" x="12" y="62">{{ item.node.detail ?? item.node.type }}</text>
          <circle v-if="index !== 0" cx="0" :cy="cardHeight / 2" r="3" />
          <circle v-if="item.node.children?.length" :cx="cardWidth" :cy="cardHeight / 2" r="3" />
        </g>
      </svg>
    </div>
    <figcaption>{{ nodeHelpExamples[type].caption }}</figcaption>
  </figure>
</template>

<style scoped>
.help-canvas { margin: 16px 0 8px; }
.canvas-heading { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 6px; margin-bottom: 8px; font-size: 12px; }
.canvas-heading span, figcaption { color: #9caebb; font-size: 11px; line-height: 1.6; }
.canvas-scroll { overflow-x: auto; border: 1px solid #354958; border-radius: 8px; background-color: #101d27; background-image: radial-gradient(#30424f 1px, transparent 1px); background-size: 16px 16px; }
.canvas-scroll:focus-visible { outline: 2px solid #68dfc6; outline-offset: 2px; }
svg { display: block; max-width: none; margin: auto; }
.example-edge path { fill: none; stroke: #738999; stroke-width: 1.5; }
.example-edge text { fill: #b7c9d6; font-size: 11px; text-anchor: middle; }
.example-node { --node-color: #68dfc6; }
.example-node.blue { --node-color: #85baff; }
.example-node.amber { --node-color: #f0c478; }
.example-node.purple { --node-color: #bf9bfa; }
.example-node rect { fill: #1b2d39; stroke: #3a5363; }
.example-node.featured rect { stroke: var(--node-color); stroke-width: 2; }
.example-node .accent { stroke: var(--node-color); stroke-width: 3; }
.example-node circle { fill: #14222d; stroke: #a4becf; stroke-width: 1.5; }
.kind { fill: var(--node-color); font-size: 11px; }
.label { fill: #e4edf3; font-size: 13px; font-weight: 600; }
.detail { fill: #9eafbd; font-size: 10px; }
figcaption { margin-top: 8px; }
</style>
