import { validateProjectTypes } from "./enums.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import type { BTNode, Project, Tree } from "./project.ts";
import { normalizeCodeNames } from "./codeNames.ts";

// 树 ID 是区分大小写的运行时主键，不裁剪或转换输入。
export function validTreeID(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 80 && !/[^A-Za-z0-9_]/.test(value);
}

// 树身份索引持有实际节点对象，新增和删除操作只维护索引，由调用方更新工程数组。
export class TreeIdentityIndex {
  // 按稳定 ID 查找树，同时提供常数时间的唯一性检查。
  readonly byID = new Map<string, Tree>();
  private readonly byFileID = new Map<string, Tree>(); // 跨平台生成文件名按 ASCII 小写查重。
  // 每个目标 ID 对应实际引用节点，包含尚未创建目标树的草稿引用。
  private readonly references = new Map<string, Set<BTNode>>();

  // 加载或恢复工程时一次扫描，后续编辑增量维护。
  constructor(project: Project) {
    for (const tree of project.trees ?? []) this.addTree(tree);
  }

  // 添加树及其节点引用；校验失败时不改变索引。
  addTree(tree: Tree): void {
    if (!validTreeID(tree.id))
      throw new Error("行为树 ID 需为 1–80 位英文字母、数字或下划线");
    if (this.byFileID.has(tree.id.toLowerCase()))
      throw new Error(`行为树 ID 已存在：${tree.id}`);
    this.byID.set(tree.id, tree);
    this.byFileID.set(tree.id.toLowerCase(), tree);
    for (const node of tree.nodes ?? []) this.addNode(node);
  }

  // 删除树内的引用成员，其他树指向此 ID 的草稿引用继续保留。
  removeTree(tree: Tree): void {
    if (this.byID.get(tree.id) !== tree) return;
    for (const node of tree.nodes ?? []) this.removeNode(node);
    this.byID.delete(tree.id);
    this.byFileID.delete(tree.id.toLowerCase());
  }

  // 任何非空 tree 字段都参与同步，避免暂时改变节点类型时遗漏引用。
  addNode(node: BTNode): void {
    if (!node.tree) return;
    let nodes = this.references.get(node.tree);
    if (!nodes) this.references.set(node.tree, (nodes = new Set()));
    nodes.add(node);
  }

  // 移除实际节点对象的引用，空桶立即回收。
  removeNode(node: BTNode): void {
    if (!node.tree) return;
    const nodes = this.references.get(node.tree);
    if (!nodes) return;
    nodes.delete(node);
    if (!nodes.size) this.references.delete(node.tree);
  }

  // 修改引用必须先移除旧目标，再将同一节点对象加入新目标。
  setReference(node: BTNode, target: string | undefined): void {
    if (node.tree === target) return;
    this.removeNode(node);
    if (target === undefined) delete node.tree;
    else node.tree = target;
    this.addNode(node);
  }

  // 提交前进行常数时间查重，原 ID 不变时视为合法的无操作。
  validateRename(oldID: string, newID: string): string | null {
    if (!this.byID.has(oldID)) return "要修改的行为树不存在";
    if (!validTreeID(newID))
      return "行为树 ID 需为 1–80 位英文字母、数字或下划线";
    const fileOwner = this.byFileID.get(newID.toLowerCase());
    if (fileOwner && fileOwner !== this.byID.get(oldID))
      return `行为树 ID 已存在：${newID}`;
    return null;
  }

  // 一次改号同步全部实际引用，只遍历旧 ID 的 k 个引用节点。
  rename(oldID: string, newID: string): number {
    const error = this.validateRename(oldID, newID);
    if (error) throw new Error(error);
    if (oldID === newID) return 0;
    const tree = this.byID.get(oldID)!;
    const nodes = this.references.get(oldID);
    const count = nodes?.size ?? 0;
    if (nodes) {
      // 新 ID 可能已有悬挂引用，合并桶以保证后续再次改号仍覆盖两组引用。
      const destination = this.references.get(newID);
      for (const node of nodes) {
        node.tree = newID;
        destination?.add(node);
      }
      this.references.set(newID, destination ?? nodes);
      this.references.delete(oldID);
    }
    tree.id = newID;
    this.byID.delete(oldID);
    this.byID.set(newID, tree);
    this.byFileID.delete(oldID.toLowerCase());
    this.byFileID.set(newID.toLowerCase(), tree);
    return count;
  }
}

// 编辑快照同时保存工程和选择，撤销改号后仍定位到原树及节点。
export interface EditorSnapshot {
  // 无损 JSON 保留工程中的完整 64 位整数。
  project: string;
  // 当时选中的树 ID。
  treeID: string;
  // 当时选中的节点 ID，空字符串表示未选择。
  selected: string;
}

// 创建与当前对象隔离的快照，避免后续编辑改变历史。
export function captureSnapshot(
  project: Project,
  treeID: string,
  selected: string,
): EditorSnapshot {
  return { project: stringifyJSON(project), treeID, selected };
}

// 历史恢复沿用输入边界校验，拒绝非法身份或枚举但允许未完成的拓扑。
export function restoreSnapshot(
  snapshot: EditorSnapshot,
  wrapProject: (project: Project) => Project = (project) => project,
): {
  // 校验后的独立工程对象。
  project: Project;
  // 在解析时一并建立的身份索引，避免恢复流程重复扫描。
  index: TreeIdentityIndex;
} {
  const decoded = parseJSON<Project>(snapshot.project);
  validateProjectTypes(decoded);
  normalizeCodeNames(decoded);
  // Vue 等调用方可先包装响应式对象，使索引和后续编辑始终使用同一节点身份。
  const project = wrapProject(decoded);
  return { project, index: new TreeIdentityIndex(project) };
}
