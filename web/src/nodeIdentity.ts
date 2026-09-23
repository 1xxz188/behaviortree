import type { BTNode, Tree } from "./project.ts";
import { CodeNameIndex } from "./codeNames.ts";

// 每条引用记录父节点及孩子位置，改号时直接定位，避免扫描父节点的孩子列表。
interface ChildReference {
  parent: BTNode; // 持有工程中的实际父节点对象。
  position: number; // 孩子数组中的位置，不是运行时节点槽位。
}

// 重连预案保存受影响父节点的新子节点列表，校验完成前不修改工程。
export interface ReconnectPlan {
  updates: { parent: BTNode; children: string[] }[]; // 每个父节点最多更新一次。
  root: string; // 沿用普通连线连接当前根节点时的入口调整。
}

// 删除连线预案只包含实际父节点及移除目标后的子节点列表。
export interface DisconnectPlan {
  parent: BTNode; // 持有待删除连线的父节点。
  children: string[]; // 删除后保留原有顺序的子节点列表。
}

// 节点稳定身份仅要求非空，保留已有中文、空白和特殊字符 ID 的兼容性。
export function validNodeID(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

// 每棵树独立维护身份及反向引用索引；输入草稿不触发工程扫描。
export class NodeIdentityIndex {
  readonly byID = new Map<string, BTNode>(); // 树内常数时间身份查找及查重。
  readonly codeNames: CodeNameIndex; // 当前树的代码名占用与分配缓存。
  private readonly references = new Map<string, Set<ChildReference>>(); // 按孩子 ID 分组的入边。
  private readonly outgoing = new Map<BTNode, ChildReference[]>(); // 按父节点记录已登记出边。
  private readonly tree: Tree; // 身份和布局所属的实际树对象。
  private nextID = 1n; // 大于本索引已见数字身份的游标，不受浮点精度限制。

  // 打开、切换或恢复树时建立索引，普通编辑随后增量更新。
  constructor(tree: Tree) {
    this.tree = tree;
    this.codeNames = new CodeNameIndex(tree);
    this.observeID(tree.root);
    for (const id of Object.keys(tree.layout ?? {})) this.observeID(id);
    for (const node of tree.nodes) this.addNode(node);
  }

  // 只在加载、登记或改号时推进游标，保留旧字符串身份；不扫描其他节点。
  private observeID(id: string): void {
    if (!/^[1-9][0-9]*$/.test(id)) return;
    const value = BigInt(id);
    if (value >= this.nextID) this.nextID = value + 1n;
  }

  // 新增与复制共享树内递增序列，调用即预留编号，单次分配无需遍历占用表。
  allocateID(): string {
    return String(this.nextID++);
  }

  // 新增和复制完成后登记节点，不改变工程数组。
  addNode(node: BTNode): void {
    this.codeNames.add(node);
    this.byID.set(node.id, node);
    this.observeID(node.id);
    this.indexChildren(node);
  }

  // 登记父节点的孩子位置，悬挂引用也保留以支持未完成的草稿。
  private indexChildren(parent: BTNode): void {
    const edges = (parent.children ?? []).map((id, position) => {
      this.observeID(id);
      const edge = { parent, position };
      let bucket = this.references.get(id);
      if (!bucket) this.references.set(id, (bucket = new Set()));
      bucket.add(edge);
      return edge;
    });
    this.outgoing.set(parent, edges);
  }

  // 在更新数组前移除旧出边，空引用桶立即回收。
  private unindexChildren(parent: BTNode): void {
    for (const edge of this.outgoing.get(parent) ?? []) {
      const id = parent.children![edge.position]!;
      const bucket = this.references.get(id);
      bucket?.delete(edge);
      if (!bucket?.size) this.references.delete(id);
    }
    this.outgoing.delete(parent);
  }

  // 连线、断线和排序统一更新单个父节点的反向索引。
  setChildren(parent: BTNode, children: string[] | undefined): void {
    this.unindexChildren(parent);
    if (children === undefined) delete parent.children;
    else parent.children = children;
    this.indexChildren(parent);
  }

  // 借助入边索引定位唯一连线；确认前只复制受影响父节点的孩子列表。
  prepareDisconnect(source: string, target: string): DisconnectPlan | string {
    const parent = this.byID.get(source);
    if (!parent || !this.byID.has(target)) return "连线已失效，请重试";
    let position: number | undefined;
    for (const edge of this.references.get(target) ?? []) {
      if (edge.parent !== parent) continue;
      if (position !== undefined) return "连线存在重复引用，请先修复草稿";
      position = edge.position;
    }
    if (position === undefined) return "连线已失效，请重试";
    const children = [...(parent.children ?? [])];
    children.splice(position, 1);
    return { parent, children };
  }

  // 只查询两端相关的入边及新子树；返回错误、无操作或可原子提交的重连预案。
  prepareReconnect(oldSource: string, oldTarget: string, source: string, target: string): ReconnectPlan | string | null {
    const oldParent = this.byID.get(oldSource);
    const newParent = this.byID.get(source);
    if (!oldParent || !this.byID.has(oldTarget)) return "原连线已失效，请重试";
    // 旧边必须仍是当前树中唯一对应的引用，不能误改过期或重复的草稿边。
    let oldEdge: ChildReference | undefined;
    for (const edge of this.references.get(oldTarget) ?? []) {
      if (edge.parent !== oldParent) continue;
      if (oldEdge) return "原连线存在重复引用，请先修复草稿";
      oldEdge = edge;
    }
    if (!oldEdge) return "原连线已失效，请重试";
    if (source === oldSource && target === oldTarget) return null;
    if (source !== oldSource && target !== oldTarget) return "一次只能修改连线的一个端点";
    if (!newParent || !this.byID.has(target) || source === target) return "不能连接到自身或不存在的节点";
    if (["wait", "action", "condition", "subtree"].includes(newParent.type)) return "叶节点不能连接子节点";

    // 新目标只有一条其他入边时自动转移，同一父节点的重复连接仍拒绝。
    const targetEdges = this.references.get(target);
    if (targetEdges && targetEdges.size > (target === oldTarget ? 1 : 0)) {
      if (targetEdges.size !== 1 || target === oldTarget) return "目标节点已有多条入边，请先修复草稿";
      const prior = targetEdges.values().next().value as ChildReference;
      if (prior.parent === newParent) return "连接已存在";
    }
    const priorParent = target === oldTarget ? undefined
      : targetEdges?.values().next().value?.parent as BTNode | undefined;
    const nextChildren = new Map<BTNode, string[]>();
    // 每个受影响父节点只复制一次孩子列表，预检失败不会触及工程对象。
    const childrenOf = (parent: BTNode): string[] => {
      let children = nextChildren.get(parent);
      if (!children) nextChildren.set(parent, (children = [...(parent.children ?? [])]));
      return children;
    };
    childrenOf(oldParent).splice(oldEdge.position, 1);
    if (priorParent) {
      const prior = targetEdges!.values().next().value as ChildReference;
      childrenOf(priorParent).splice(prior.position, 1);
    }
    const destination = childrenOf(newParent);
    if (!["sequence", "selector", "priority", "parallel"].includes(newParent.type) && destination.length)
      return "装饰节点只能有一个子节点";

    // 仅遍历新子节点的后代，避免将祖先接回自身并在草稿环中有界结束。
    const pending = [target];
    const seen = new Set<string>();
    while (pending.length) {
      const id = pending.pop()!;
      if (id === source) return "连接会形成循环";
      if (seen.has(id)) continue;
      seen.add(id);
      pending.push(...(this.byID.get(id)?.children ?? []));
    }
    if (source === oldSource) destination.splice(oldEdge.position, 0, target);
    else destination.push(target);
    return {
      updates: [...nextChildren].map(([parent, children]) => ({ parent, children })),
      root: this.tree.root === target ? source : this.tree.root,
    };
  }

  // 预案只在统一的编辑历史边界内应用，并逐个维护受影响父节点的反向索引。
  applyReconnect(plan: ReconnectPlan): void {
    for (const { parent, children } of plan.updates) this.setChildren(parent, children);
    this.tree.root = plan.root;
  }

  // 删除只访问相关父节点及当前节点；数组删除仍保持原有节点顺序。
  removeNode(node: BTNode): void {
    const parents = new Set([...this.references.get(node.id) ?? []].map((edge) => edge.parent));
    for (const parent of parents)
      this.setChildren(parent, parent.children!.filter((id) => id !== node.id));
    this.unindexChildren(node);
    this.byID.delete(node.id);
    this.codeNames.remove(node);
    const position = this.tree.nodes.indexOf(node);
    if (position >= 0) this.tree.nodes.splice(position, 1);
    if (this.tree.root === node.id) this.tree.root = "";
    if (this.tree.layout) delete this.tree.layout[node.id];
  }

  // 草稿输入及显式提交共用常数时间校验，原值不变为合法无操作。
  validateRename(oldID: string, newID: string): string | null {
    if (!this.byID.has(oldID)) return "要修改的节点不存在";
    if (!validNodeID(newID)) return "节点 ID 不能为空";
    if (oldID !== newID && this.byID.has(newID)) return `当前树内节点 ID 已存在：${newID}`;
    return null;
  }

  // 一次改号仅访问 k 条入边，同步根与布局；选择由调用方在同一历史边界内更新。
  rename(oldID: string, newID: string): number {
    const failure = this.validateRename(oldID, newID);
    if (failure) throw new Error(failure);
    if (oldID === newID) return 0;
    const node = this.byID.get(oldID)!;
    const edges = this.references.get(oldID);
    const count = edges?.size ?? 0;
    if (edges) {
      // 新身份可能已有悬挂引用，合并后再次改号仍覆盖全部实际入边。
      const destination = this.references.get(newID);
      for (const edge of edges) {
        edge.parent.children![edge.position] = newID;
        destination?.add(edge);
      }
      this.references.set(newID, destination ?? edges);
      this.references.delete(oldID);
    }
    if (this.tree.root === oldID) this.tree.root = newID;
    if (this.tree.layout && Object.hasOwn(this.tree.layout, oldID)) {
      // 定义自身属性以兼容 __proto__ 等合法 ID，不触发对象原型 setter。
      Object.defineProperty(this.tree.layout, newID, {
        value: this.tree.layout[oldID], enumerable: true, configurable: true, writable: true,
      });
      delete this.tree.layout[oldID];
    }
    node.id = newID;
    this.byID.delete(oldID);
    this.byID.set(newID, node);
    this.observeID(newID);
    return count;
  }
}
