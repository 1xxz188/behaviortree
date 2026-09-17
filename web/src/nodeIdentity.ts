import type { BTNode, Tree } from "./project.ts";
import { CodeNameIndex } from "./codeNames.ts";

// 每条引用记录父节点及孩子位置，改号时直接定位，避免扫描父节点的孩子列表。
interface ChildReference {
  parent: BTNode; // 持有工程中的实际父节点对象。
  position: number; // 孩子数组中的位置，不是运行时节点槽位。
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
