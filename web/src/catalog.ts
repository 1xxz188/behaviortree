import type { Definition } from "./project.ts";
import { validateCatalogTypes } from "./enums.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 同 ID 冲突必须由用户逐项决定，不能用默认覆盖策略。
export type CatalogChoice = "keep" | "replace";

// 目录冲突保存双方声明，供界面展示变更影响。
export interface CatalogConflict {
  id: string; // 稳定的业务定义 ID。
  current: Definition; // 当前工程使用的声明。
  incoming: Definition; // 待导入或编辑后的声明。
}

// 校验目录容器和唯一索引；复杂 Go 标识符与参数约束由服务端统一检查。
export function parseCatalog(value: unknown): Definition[] {
  if (!Array.isArray(value)) throw new Error("业务定义应为 JSON 数组，不能使用完整工程文件");
  validateCatalogTypes(value);
  const ids = new Set<string>();
  for (const item of value) {
    rejectUnknownFields(item, definitionFields, `定义 ${item.id ?? ""}`);
    if (typeof item.id !== "string" || !item.id.trim()) throw new Error("业务定义 ID 不能为空");
    if (typeof item.name !== "string" || !item.name.trim()) throw new Error(`${item.id}：显示名称不能为空`);
    if (typeof item.goName !== "string" || !item.goName.trim()) throw new Error(`${item.id}：Go 函数名不能为空`);
    if (ids.has(item.id)) throw new Error(`目录内存在重复 ID：${item.id}`);
    ids.add(item.id);
    for (const parameter of item.params ?? []) {
      rejectUnknownFields(parameter, parameterFields, `${item.id} 参数 ${parameter.name ?? ""}`);
    }
  }
  return value;
}

const definitionFields = new Set(["id", "name", "kind", "goName", "params", "events"]);
const parameterFields = new Set(["name", "type", "comment", "default", "enum"]);

// 严格限制独立定义格式，避免误把工程分类或拼错的字段静默丢弃。
function rejectUnknownFields(value: object, fields: ReadonlySet<string>, path: string): void {
  for (const key of Object.keys(value)) {
    if (!fields.has(key)) throw new Error(`${path}：不支持字段 ${key}，请仅提供业务定义声明`);
  }
}

// 按声明字段而非输入键顺序比较；Go 的空集合省略不应产生伪冲突。
function definitionSignature(item: Definition): string {
  return stringifyJSON({
    id: item.id, name: item.name, kind: item.kind, goName: item.goName,
    params: (item.params ?? []).map(parameter => ({
      name: parameter.name, type: parameter.type, comment: parameter.comment ?? "",
      ...(parameter.default !== undefined ? { default: parameter.default } : {}),
      enum: parameter.enum ?? [],
    })),
    events: item.events ?? [],
  });
}

// 使用一次索引定位同 ID 变更，复杂度为 O(当前目录数 + 导入目录数)。
export function catalogConflicts(current: Definition[], incoming: Definition[]): CatalogConflict[] {
  parseCatalog(current);
  parseCatalog(incoming);
  const existing = new Map(current.map((item) => [item.id, item]));
  const conflicts: CatalogConflict[] = [];
  for (const item of incoming) {
    const previous = existing.get(item.id);
    if (previous && definitionSignature(previous) !== definitionSignature(item)) {
      conflicts.push({ id: item.id, current: previous, incoming: item });
    }
  }
  return conflicts;
}

// 保留现有顺序并追加新定义，确认所有冲突及 Go 名唯一后才返回独立副本。
export function mergeCatalog(
  current: Definition[],
  incoming: Definition[],
  choices: ReadonlyMap<string, CatalogChoice> = new Map(),
): Definition[] {
  const conflicts = catalogConflicts(current, incoming);
  for (const conflict of conflicts) {
    if (!choices.has(conflict.id)) throw new Error(`请处理目录冲突：${conflict.id}`);
  }
  const merged = new Map(current.map((item) => [item.id, item]));
  for (const item of incoming) {
    if (!merged.has(item.id) || choices.get(item.id) === "replace") merged.set(item.id, item);
  }
  const goNames = new Map<string, string>();
  for (const item of merged.values()) {
    const previous = goNames.get(item.goName);
    if (previous !== undefined) throw new Error(`Go 函数名 ${item.goName} 被 ${previous} 和 ${item.id} 重复使用，请修改定义`);
    goNames.set(item.goName, item.id);
  }
  return parseJSON<Definition[]>(stringifyJSON([...merged.values()]));
}
