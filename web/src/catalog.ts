import type { Definition, EventDefinition, Project } from "./project.ts";
import { validateCatalogTypes } from "./enums.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { allocateEventID, normalizeEventDescription, validateEventRegistry, validateNextEventID } from "./eventRegistry.ts";

// 同 ID 冲突必须由用户逐项决定，不能用默认覆盖策略。
export type CatalogChoice = "keep" | "replace";

// 目录冲突保存双方声明，供界面展示变更影响。
export interface CatalogConflict {
  id: string; // 稳定的业务定义 ID。
  current: Definition; // 当前工程使用的声明。
  incoming: Definition; // 待导入或编辑后的声明。
}
// 目录交换包显式携带依赖事件，不能由 eventIds 推导出新事件。
export interface CatalogPackage {
  kind: "behaviortree.catalog"; // 固定交换类型，拒绝旧数组格式。
  schemaVersion: 4; // 与工程 Schema 4 同步。
  eventEnumDescription?: string; // 来源工程的枚举功能注释。
  events: EventDefinition[]; // 仅实际导出定义所引用的事件。
  catalog: Definition[]; // 待交换的业务定义。
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

const definitionFields = new Set(["id", "name", "kind", "goName", "params", "eventIds"]);
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
    eventIds: item.eventIds ?? [],
  });
}

// 解析用户提供的完整交换包；旧定义数组、缺失事件表和旧 events 字段均直接拒绝。
export function parseCatalogPackage(value: unknown): CatalogPackage {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("业务目录必须使用 Schema 4 交换对象；旧数组格式已停用");
  const object = value as Record<string, unknown>;
  for (const key of Object.keys(object)) if (!["kind", "schemaVersion", "eventEnumDescription", "events", "catalog"].includes(key)) throw new Error(`目录交换包不支持字段 ${key}`);
  if (object.kind !== "behaviortree.catalog" || object.schemaVersion !== 4) throw new Error("业务目录必须使用 behaviortree.catalog Schema 4 交换格式");
  validateEventRegistry(object.events, object.catalog, object.eventEnumDescription);
  const catalog = parseCatalog(object.catalog);
  return { kind: "behaviortree.catalog", schemaVersion: 4, events: object.events as EventDefinition[], catalog,
    ...(normalizeEventDescription(object.eventEnumDescription, "eventEnumDescription") ? { eventEnumDescription: normalizeEventDescription(object.eventEnumDescription, "eventEnumDescription") } : {}),
  };
}

// 只选被导出定义实际依赖的成员，保持身份及两类注释。
export function exportCatalogPackage(project: Project, definitions: Definition[]): CatalogPackage {
  validateEventRegistry(project.events, project.catalog, project.eventEnumDescription);
  const used = new Set(definitions.flatMap(item => item.eventIds ?? []));
  const value: CatalogPackage = { kind: "behaviortree.catalog", schemaVersion: 4,
    events: project.events.filter(item => used.has(item.id)).map(item => ({ ...item,
      description: normalizeEventDescription(item.description, `events[${item.id}].description`) })),
    catalog: parseJSON<Definition[]>(stringifyJSON(definitions)),
  };
  const description = normalizeEventDescription(project.eventEnumDescription, "eventEnumDescription");
  if (description) value.eventEnumDescription = description;
  return parseCatalogPackage(value);
}

// 同 ID 的成员描述、名称和代码名差异均需显式选择。
export function eventConflicts(current: EventDefinition[], incoming: EventDefinition[]): { current: EventDefinition; incoming: EventDefinition }[] {
  const byID = new Map(current.map(item => [item.id, item]));
  return incoming.flatMap(item => {
    const prior = byID.get(item.id);
    if (!prior) return [];
    const signature = (event: EventDefinition) => stringifyJSON({ id: event.id, name: event.name, codeName: event.codeName,
      description: normalizeEventDescription(event.description, `events[${event.id}].description`) });
    return signature(prior) === signature(item) ? [] : [{ current: prior, incoming: item }];
  });
}

// 预先计算导入事件的目标身份，供冲突预览和正式合并使用同一份映射规则。
export function prepareImportedEvents(
  project: Project, incoming: CatalogPackage,
  eventMappings: ReadonlyMap<string, string> = new Map(), eventRenames: ReadonlyMap<string, string> = new Map(),
): { definitions: Definition[]; additions: EventDefinition[]; eventIDs: ReadonlyMap<string, string> } {
  validateNextEventID(project.nextEventId, project.events);
  const required = new Set(incoming.catalog.flatMap(item => item.eventIds ?? []));
  const existing = new Map(project.events.map(item => [item.id, item]));
  const resolved = new Map<string, string>();
  const additions: EventDefinition[] = [];
  let cursor = project.nextEventId;
  for (const raw of incoming.events) {
    if (!required.has(raw.id)) continue;
    const mapped = eventMappings.get(raw.id);
    if (mapped) {
      if (!existing.has(mapped)) throw new Error(`事件 ${raw.id} 映射的目标 ${mapped} 不存在`);
      resolved.set(raw.id, mapped);
      continue;
    }
    const prior = existing.get(raw.id);
    if (prior && !eventConflicts([prior], [raw]).length) {
      resolved.set(raw.id, raw.id);
      continue;
    }
    const allocated = allocateEventID(cursor);
    cursor = allocated.nextEventId;
    resolved.set(raw.id, allocated.id);
    additions.push({ ...raw, id: allocated.id, codeName: eventRenames.get(raw.id) ?? raw.codeName });
  }
  const definitions = incoming.catalog.map(definition => ({ ...definition,
    eventIds: (definition.eventIds ?? []).map(id => resolved.get(id) ?? id) }));
  return { definitions, additions, eventIDs: resolved };
}

// 外来事件按目标工程游标分配身份；仅完全相同的现有事件和显式映射复用身份。
export function mergeCatalogPackage(
  project: Project, incoming: CatalogPackage,
  definitionChoices: ReadonlyMap<string, CatalogChoice>,
  eventMappings: ReadonlyMap<string, string> = new Map(), eventRenames: ReadonlyMap<string, string> = new Map(),
  importEnumDescription = false,
): { catalog: Definition[]; events: EventDefinition[]; eventEnumDescription: string; nextEventId: string } {
  const prepared = prepareImportedEvents(project, incoming, eventMappings, eventRenames);
  const catalog = mergeCatalog(project.catalog, prepared.definitions, definitionChoices);
  const used = new Set(catalog.flatMap(item => item.eventIds ?? []));
  const events = [...project.events];
  let nextEventId = project.nextEventId;
  for (const member of prepared.additions) {
    if (!used.has(member.id)) continue;
    events.push(member);
    nextEventId = (BigInt(member.id) + 1n).toString();
  }
  const eventEnumDescription = importEnumDescription
    ? normalizeEventDescription(incoming.eventEnumDescription, "eventEnumDescription")
    : normalizeEventDescription(project.eventEnumDescription, "eventEnumDescription");
  validateEventRegistry(events, catalog, eventEnumDescription);
  validateNextEventID(nextEventId, events);
  const names = new Map<string, string>();
  for (const event of events) {
    const name = event.codeName.toLowerCase();
    if (names.has(name)) throw new Error(`事件代码名 Event${event.codeName} 与 ${names.get(name)} 冲突，请映射或修改导入代码名`);
    names.set(name, event.id);
  }
  return { catalog, events, eventEnumDescription, nextEventId };
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
