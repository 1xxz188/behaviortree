import type { EventRegistryIndex } from "./eventRegistry.ts";

// 高亮集合只属于当前编辑会话，每次切换创建新集合以触发视图更新。
export function toggleHighlightedEvent(selected: ReadonlySet<string>, id: string): Set<string> {
  const next = new Set(selected);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
}

// 删除或恢复工程后，仅检查已选事件，避免重新扫描整个事件表。
export function pruneHighlightedEvents(selected: ReadonlySet<string>, available: ReadonlyMap<string, unknown>): Set<string> {
  const next = new Set<string>();
  for (const id of selected) if (available.has(id)) next.add(id);
  return next;
}

// 当前树只合并已选事件的节点引用，同一节点命中多个事件仍只高亮一次。
export function matchedEventNodes(selected: ReadonlySet<string>, index: EventRegistryIndex, treeID: string): Set<string> {
  const nodes = new Set<string>();
  for (const id of selected) for (const nodeID of index.nodes(id, treeID)) nodes.add(nodeID);
  return nodes;
}
