import { computed } from "vue";
import type { ComputedRef } from "vue";
import type { BTNode, Tree } from "./project.ts";

// 树范围与节点状态独立组合，仅保存当前页面的显示偏好。
export type CatalogTreeScope = "all" | "current";
export type CatalogNodeStatus = "all" | "valid" | "invalid";

// 每棵树共享一次拓扑计算，目录筛选和画布草稿标记使用相同的可达语义。
interface TreeCatalogUsage {
  used: ReadonlySet<string>; // 本树动作、条件实例引用的定义，包含草稿。
  effective: ReadonlySet<string>; // 至少被一个根可达实例引用的定义。
  reachable: ReadonlySet<string>; // 根可达的节点 ID，树之间独立。
}

// 只读取身份、类型、绑定和连线，布局、注释及参数修改不会使缓存失效。
function treeCatalogUsage(tree: Tree): TreeCatalogUsage {
  const nodes = new Map<string, BTNode>();
  const used = new Set<string>();
  for (const node of tree.nodes) {
    nodes.set(node.id, node);
    if ((node.type === "action" || node.type === "condition") && node.binding) used.add(node.binding);
  }
  const reachable = new Set<string>();
  const effective = new Set<string>();
  const pending = [tree.root];
  // 迭代访问每个可达节点一次，草稿中的环、重复边和悬挂引用不会重复展开。
  while (pending.length) {
    const id = pending.pop()!;
    if (reachable.has(id)) continue;
    const node = nodes.get(id);
    if (!node) continue;
    reachable.add(id);
    if ((node.type === "action" || node.type === "condition") && node.binding) effective.add(node.binding);
    for (const child of node.children ?? []) pending.push(child);
  }
  return { used, effective, reachable };
}

// 按树对象缓存响应式结果，单树改线只重算该树，工程替换后旧对象可被回收。
export class CatalogUsageIndex {
  private readonly cache = new WeakMap<Tree, ComputedRef<TreeCatalogUsage>>(); // 不持有已删除或被历史快照替换的树。
  private readonly allEffective: ComputedRef<ReadonlySet<string>>; // 全工程有效定义并集，重复引用自然去重。
  private readonly boundDefinitions: () => ReadonlyMap<string, unknown>; // 复用工程已有定义引用索引，不再扫描所有树判断未使用定义。

  // 全树结果按需合并各树缓存，默认显示全部时不触发工程遍历。
  constructor(trees: () => readonly Tree[], boundDefinitions: () => ReadonlyMap<string, unknown>) {
    this.boundDefinitions = boundDefinitions;
    this.allEffective = computed(() => {
      const effective = new Set<string>();
      for (const tree of trees()) {
        for (const binding of this.forTree(tree).value.effective) effective.add(binding);
      }
      return effective;
    });
  }

  // 查找已有树缓存为 O(1)，首次使用才建立该树的节点和可达索引。
  forTree(tree: Tree): ComputedRef<TreeCatalogUsage> {
    let usage = this.cache.get(tree);
    if (!usage) {
      usage = computed(() => treeCatalogUsage(tree));
      this.cache.set(tree, usage);
    }
    return usage;
  }

  // 当前树保留自身引用及全工程未使用的公共定义，未绑定定义在全部和无效中都可见。
  matcher(tree: Tree, scope: CatalogTreeScope, status: CatalogNodeStatus): ((id: string) => boolean) | undefined {
    if (scope === "all" && status === "all") return undefined;
    const usage = scope === "current" ? this.forTree(tree).value : undefined;
    const effective = status === "all" ? undefined : usage?.effective ?? this.allEffective.value;
    // 有效集合必定属于所选范围，直接查询；全部和无效才需要检查公共未使用候选。
    if (status === "valid") return id => effective!.has(id);
    const bound = usage ? this.boundDefinitions() : undefined;
    return id => (!usage || usage.used.has(id) || !bound!.has(id)) && (!effective || !effective.has(id));
  }
}
