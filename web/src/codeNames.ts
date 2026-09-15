import type { BTNode, Project, Tree } from "./project.ts";
import { nodeTypes } from "./enums.ts";

// Go 关键字不能作为独立代码名，规则与后端输入校验一致。
const keywords = new Set("break default func interface select case defer go map struct chan else goto package switch const fallthrough if range type continue for import return var".split(" "));

// 代码名限制为短 ASCII 标识符，不修剪输入或隐式修正显式名称。
export function validCodeName(value: unknown): value is string {
  return typeof value === "string" && value.length <= 40
    && /^[A-Za-z]/.test(value) && !/[^A-Za-z0-9_]/.test(value) && !keywords.has(value);
}

// 默认名优先沿用可读短 ID；编辑器自动 UUID 回退到节点类型。
export function defaultCodeName(node: BTNode): { base: string; forceNumber: boolean } {
  const randomID = /^node_[0-9a-f]{32}$/i.test(node.id)
    || /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(node.id);
  const readableID = !randomID && validCodeName(node.id);
  const base = readableID ? node.id : nodeTypes.includes(node.type) ? node.type : "node";
  return { base: base.charAt(0).toUpperCase() + base.slice(1), forceNumber: !readableID };
}

// 占用表和共享后缀游标让批量创建及长名称碰撞的查重保持摊销 O(1)。
export class CodeNameIndex {
  readonly byName = new Map<string, BTNode>(); // 树内显式代码名对应实际节点对象。
  private readonly nextSuffix = new Map<string, number>(); // 同一截短前缀共享数字游标。

  // 建立索引时先保留全部显式值，再按与 Go 一致的 Unicode 顺序补全缺失值。
  constructor(tree: Tree) {
    const missing: { node: BTNode; points: number[] }[] = [];
    for (const node of tree.nodes) {
      if (node.codeName) this.add(node);
      else missing.push({ node, points: Array.from(node.id, (character) => character.codePointAt(0)!) });
    }
    missing.sort((a, b) => {
      for (let i = 0; i < Math.min(a.points.length, b.points.length); i++) {
        if (a.points[i] !== b.points[i]) return a.points[i]! - b.points[i]!;
      }
      return a.points.length - b.points.length;
    });
    for (const { node } of missing) this.add(node);
  }

  // 创建和复制共用分配入口；调用方可传入被复制节点的代码名作为可读基名。
  allocate(base: string, forceNumber = false): string {
    if (!forceNumber && !this.byName.has(base)) return base;
    for (let digits = 1; ; digits++) {
      const prefix = base.slice(0, 40 - digits);
      const key = `${digits}:${prefix}`;
      let suffix = this.nextSuffix.get(key) ?? (digits === 1 ? 1 : 10 ** (digits - 1));
      while (suffix < 10 ** digits) {
        const candidate = `${prefix}${suffix++}`;
        this.nextSuffix.set(key, suffix);
        if (!this.byName.has(candidate)) return candidate;
      }
    }
  }

  // 仅在从业务目录新建节点时取业务函数名；不适合作为代码名时交给类型默认规则。
  // 长函数名截取 40 位后使用同一占用表消歧，名称分配后由节点独立持久保存。
  allocateBusiness(goName: string): string | undefined {
    if (!/^[A-Za-z][A-Za-z0-9_]*$/.test(goName) || keywords.has(goName)) return undefined;
    return this.allocate(goName.slice(0, 40));
  }

  // 复制名称沿用可读基名并分配数字，Wait1 的副本为 Wait2，避免逐次堆叠数字。
  allocateCopy(name: string): string {
    return this.allocate(name.replace(/[0-9]+$/, ""), true);
  }

  // 缺省名称即时写回节点，已有名称校验失败则不改变占用索引。
  add(node: BTNode): void {
    const seed = defaultCodeName(node);
    const name = node.codeName || this.allocate(seed.base, seed.forceNumber);
    if (!validCodeName(name)) throw new Error(`代码名无效：${name}；需为 1–40 位英文开头的字母、数字或下划线，且不能是 Go 关键字`);
    if (this.byName.has(name) && this.byName.get(name) !== node) throw new Error(`当前树内代码名已存在：${name}`);
    node.codeName = name;
    this.byName.set(name, node);
  }

  // 删除不重新编号其他节点；缓存游标保留以避免重复扫描已占用后缀。
  remove(node: BTNode): void {
    if (this.byName.get(node.codeName!) === node) this.byName.delete(node.codeName!);
  }

  // 输入草稿只在应用时进行常数时间格式与唯一性检查。
  validateRename(node: BTNode, name: string): string | null {
    if (!validCodeName(name)) return "代码名需为 1–40 位英文开头的字母、数字或下划线，且不能是 Go 关键字";
    const owner = this.byName.get(name);
    return owner && owner !== node ? `当前树内代码名已存在：${name}` : null;
  }

  // 名称修改只更新节点和占用表，不改 ID、根节点或引用关系。
  rename(node: BTNode, name: string): void {
    const failure = this.validateRename(node, name);
    if (failure) throw new Error(failure);
    this.remove(node);
    node.codeName = name;
    this.add(node);
  }
}

// 未指定代码名的节点进入编辑器或参与语义比较时统一分配，后续保存保留稳定代码名。
export function normalizeCodeNames(project: Project): void {
  for (const tree of project.trees) new CodeNameIndex(tree);
}
