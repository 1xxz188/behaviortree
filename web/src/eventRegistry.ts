import type { BTNode, Definition, EventDefinition, Project } from "./project.ts";
import { clone } from "./project.ts";

const idPattern = /^[1-9][0-9]*$/;
const maxEventID = (1n << 64n) - 1n;
const exhaustedEventCursor = maxEventID + 1n;
const codePattern = /^[A-Z][A-Za-z0-9_]{0,79}$/;
const encoder = new TextEncoder();

// 注释统一采用 LF，保留有意义的段落与缩进，并按 UTF-8 字节限制长度。
export function normalizeEventDescription(value: unknown, path: string): string {
  if (value === undefined) return "";
  if (typeof value !== "string") throw new Error(`${path}: 应为字符串`);
  const normalized = value.replace(/\r\n?|\n/g, "\n");
  if (normalized.includes("\0")) throw new Error(`${path}: 不能包含 NUL`);
  if (encoder.encode(normalized).byteLength > 16 * 1024) throw new Error(`${path}: 超过 16 KiB UTF-8`);
  return /^[ \t\n]*$/.test(normalized) ? "" : normalized;
}

// 检查 uint64 的规范十进制 JSON 字符串，绝不经 JavaScript number 转换大整数 ID。
export function validateEventID(value: unknown, path: string): asserts value is string {
  if (typeof value !== "string" || value.length > 20 || !idPattern.test(value) || BigInt(value) > maxEventID) {
    throw new Error(`${path}: 应为非零 uint64 的规范十进制事件 ID`);
  }
}

// 耗尽哨兵只允许用在下一个 ID 游标，不允许作为事件身份。
export function validateNextEventID(value: unknown, events: readonly EventDefinition[]): asserts value is string {
  if (typeof value !== "string" || value.length > 20 || !idPattern.test(value) || BigInt(value) > exhaustedEventCursor) {
    throw new Error(`nextEventId: 应为 1 到 ${exhaustedEventCursor} 的规范十进制字符串`);
  }
  const next = BigInt(value);
  for (const event of events) {
    validateEventID(event.id, "events.id");
    if (BigInt(event.id) >= next) throw new Error(`nextEventId: 必须大于现存事件 ID ${event.id}`);
  }
}

// 从已校验的持久游标分配一次事件身份，删除成员不会回退游标。
export function allocateEventID(nextEventId: string): { id: string; nextEventId: string } {
  if (typeof nextEventId !== "string" || nextEventId.length > 20 || !idPattern.test(nextEventId) || BigInt(nextEventId) > exhaustedEventCursor) throw new Error("nextEventId: 无效的事件 ID 游标");
  if (BigInt(nextEventId) === exhaustedEventCursor) throw new Error("事件 ID 已耗尽");
  return { id: nextEventId, nextEventId: (BigInt(nextEventId) + 1n).toString() };
}

// 对工程与交换包共用同一事件声明及依赖校验，不修改传入对象。
export function validateEventRegistry(events: unknown, catalog: unknown, enumDescription?: unknown): void {
  normalizeEventDescription(enumDescription, "eventEnumDescription");
  if (!Array.isArray(events)) throw new Error("events: 必须显式提供数组");
  if (!Array.isArray(catalog)) throw new Error("catalog: 应为数组");
  const ids = new Set<string>();
  const names = new Set<string>();
  for (let index = 0; index < events.length; index++) {
    const item = events[index] as EventDefinition;
    const path = `events[${index}]`;
    if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error(`${path}: 应为对象`);
    for (const key of Object.keys(item)) if (!["id", "name", "codeName", "description"].includes(key)) throw new Error(`${path}.${key}: 不支持字段`);
    validateEventID(item.id, `${path}.id`);
    if (ids.has(item.id)) throw new Error(`${path}.id: 重复事件 ID ${item.id}`);
    ids.add(item.id);
    if (typeof item.name !== "string" || !item.name.trim()) throw new Error(`${path}.name: 不能为空`);
    if (typeof item.codeName !== "string" || !codePattern.test(item.codeName)) throw new Error(`${path}.codeName: 应以大写字母开头，且至多 80 个 ASCII 字符`);
    const folded = item.codeName.toLowerCase();
    if (names.has(folded)) throw new Error(`${path}.codeName: 重复代码名 ${item.codeName}`);
    names.add(folded);
    normalizeEventDescription(item.description, `${path}.description`);
  }
  for (let index = 0; index < catalog.length; index++) {
    const item = catalog[index] as Definition;
    const path = `catalog[${index}]`;
    if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error(`${path}: 应为对象`);
    if (Object.hasOwn(item, "events")) throw new Error(`${path}.events: 旧字符串事件字段已停用`);
    if (item.eventIds === undefined) continue;
    if (!Array.isArray(item.eventIds)) throw new Error(`${path}.eventIds: 应为数组`);
    const seen = new Set<string>();
    for (let offset = 0; offset < item.eventIds.length; offset++) {
      const id = item.eventIds[offset];
      validateEventID(id, `${path}.eventIds[${offset}]`);
      if (seen.has(id)) throw new Error(`${path}.eventIds[${offset}]: 重复事件 ID ${id}`);
      if (!ids.has(id)) throw new Error(`${path}.eventIds[${offset}]: 未定义事件 ID ${id}`);
      seen.add(id);
    }
  }
}

// 引用索引将定义和各树节点分层保存，查询高亮只遍历引用当前事件的定义。
export class EventRegistryIndex {
  readonly eventByID = new Map<string, EventDefinition>(); // 稳定 ID 到事件声明。
  readonly definitionByID = new Map<string, Definition>(); // 定义 ID 到当前声明。
  readonly eventToDefs = new Map<string, Set<string>>(); // 事件到直接订阅的业务定义。
  readonly definitionToNodes = new Map<string, Map<string, Set<string>>>(); // 定义到各树画布节点。

  // 装载工程或事务快照时线性构建索引；视图操作不调用此方法。
  constructor(project: Project) {
    for (const event of project.events) {
      this.eventByID.set(event.id, event);
      this.eventToDefs.set(event.id, new Set());
    }
    for (const definition of project.catalog) {
      this.definitionByID.set(definition.id, definition);
      for (const id of definition.eventIds ?? []) this.eventToDefs.get(id)?.add(definition.id);
    }
    for (const tree of project.trees) for (const node of tree.nodes) this.addNode(tree.id, node);
  }

  // 仅 action/condition 的直接 binding 才算事件节点引用。
  private addNode(treeID: string, node: BTNode): void {
    if ((node.type !== "action" && node.type !== "condition") || !node.binding) return;
    let byTree = this.definitionToNodes.get(node.binding);
    if (!byTree) this.definitionToNodes.set(node.binding, byTree = new Map());
    let nodes = byTree.get(treeID);
    if (!nodes) byTree.set(treeID, nodes = new Set());
    nodes.add(node.id);
  }

  // 返回目标树的去重匹配集合，保留不同树中相同节点 ID 的隔离。
  nodes(eventID: string, treeID: string): Set<string> {
    const matched = new Set<string>();
    for (const definitionID of this.eventToDefs.get(eventID) ?? []) {
      for (const nodeID of this.definitionToNodes.get(definitionID)?.get(treeID) ?? []) matched.add(nodeID);
    }
    return matched;
  }

  // 同一事件在一个节点上只计一次，便于删除确认和画布计数。
  counts(eventID: string): { definitions: number; trees: number; nodes: number } {
    const byTree = new Map<string, Set<string>>();
    const definitions = this.eventToDefs.get(eventID);
    for (const definitionID of definitions ?? []) for (const [treeID, nodes] of this.definitionToNodes.get(definitionID) ?? []) {
      let unique = byTree.get(treeID);
      if (!unique) byTree.set(treeID, unique = new Set());
      for (const nodeID of nodes) unique.add(nodeID);
    }
    let count = 0;
    for (const nodes of byTree.values()) count += nodes.size;
    return { definitions: definitions?.size ?? 0, trees: byTree.size, nodes: count };
  }
}

// 在独立候选工程里删除事件及所有依赖，调用方校验后一次提交历史。
export function removeEvent(project: Project, id: string): Project {
  if (!project.events.some(event => event.id === id)) throw new Error(`事件 ${id} 已不存在`);
  const candidate = clone(project);
  candidate.events = candidate.events.filter(event => event.id !== id);
  for (const definition of candidate.catalog) definition.eventIds = (definition.eventIds ?? []).filter(value => value !== id);
  validateEventRegistry(candidate.events, candidate.catalog, candidate.eventEnumDescription);
  return candidate;
}
