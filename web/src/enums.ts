import { validateCatalogOrganization } from "./catalogOrganization.ts";

// 枚举名称与 Go 的 JSON 表示一致；联合类型不生成数值反向映射。
export const nodeTypes = [
  "sequence", "selector", "priority", "parallel", "condition", "action",
  "wait", "timeout", "repeat", "retry", "inverter", "succeed", "fail", "subtree",
] as const;
export type NodeType = (typeof nodeTypes)[number];

export const definitionKinds = ["action", "condition"] as const;
export type DefinitionKind = (typeof definitionKinds)[number];

export const valueTypes = [
  "bool", "int64", "uint64", "float64", "string", "enum", "entity", "duration",
] as const;
export type ValueType = (typeof valueTypes)[number];

const nodeTypeSet = new Set<string>(nodeTypes);
const definitionKindSet = new Set<string>(definitionKinds);
const valueTypeSet = new Set<string>(valueTypes);

// 输入边界以集合检查实际值，不把任意字符串断言为枚举。
export function isNodeType(value: unknown): value is NodeType {
  return typeof value === "string" && nodeTypeSet.has(value);
}
// 业务目录只接受动作或条件，不能复用完整节点种类集合。
export function isDefinitionKind(value: unknown): value is DefinitionKind {
  return typeof value === "string" && definitionKindSet.has(value);
}
// 值类型同时用于黑板字段和业务参数。
export function isValueType(value: unknown): value is ValueType {
  return typeof value === "string" && valueTypeSet.has(value);
}

// 统一给输入错误附带字段位置及非法值，包括缺失属性。
function invalidType(path: string, value: unknown): Error {
  return new Error(`${path}: 未知或缺失的类型 ${String(value)}`);
}
// 解析节点标签并在失败时保留输入位置。
export function parseNodeType(value: unknown, path = "type"): NodeType {
  if (!isNodeType(value)) throw invalidType(path, value);
  return value;
}
// 解析业务目录标签。
export function parseDefinitionKind(value: unknown, path = "kind"): DefinitionKind {
  if (!isDefinitionKind(value)) throw invalidType(path, value);
  return value;
}
// 解析字段或参数类型，不接受旧拼写及数值编号。
export function parseValueType(value: unknown, path = "type"): ValueType {
  if (!isValueType(value)) throw invalidType(path, value);
  return value;
}

// 只检查承载枚举的容器和标签；拓扑、绑定及值范围仍交给 Go 校验。
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
// 确认容器结构后读取标签，避免对任意 JSON 强制转换类型。
function record(value: unknown, path: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(`${path}: 应为对象`);
  return value;
}
// Go 允许省略或使用 null 表示空集合，枚举标签本身仍必须有效。
function items(value: unknown, path: string): unknown[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(`${path}: 应为数组`);
  return value;
}
// 验证导入目录以及工程内目录的所有种类、参数类型与可选注释。
export function validateCatalogTypes(value: unknown, path = "catalog"): void {
  items(value, path).forEach((item, index) => {
    const location = `${path}[${index}]`;
    const definition = record(item, location);
    parseDefinitionKind(definition.kind, `${location}.kind`);
    items(definition.params, `${location}.params`).forEach((item, index) => {
      const parameterPath = `${location}.params[${index}]`;
      const parameter = record(item, parameterPath);
      parseValueType(parameter.type, `${parameterPath}.type`);
      if (parameter.comment !== undefined && typeof parameter.comment !== "string") {
        throw new Error(`${parameterPath}.comment: 应为字符串`);
      }
    });
  });
}
// 验证工程内所有枚举字段；不妨碍根节点或连线尚未完成的草稿。
export function validateProjectTypes(value: unknown): void {
  const project = record(value, "project");
  if (project.schemaVersion !== 2) throw new Error("schemaVersion: 仅支持版本 2，请新建工程；旧版工程不兼容");
  items(project.blackboard, "blackboard").forEach((item, index) => {
    const path = `blackboard[${index}]`;
    parseValueType(record(item, path).type, `${path}.type`);
  });
  validateCatalogTypes(project.catalog);
  const catalog = items(project.catalog, "catalog").map((item, index) => {
    const definition = record(item, `catalog[${index}]`);
    if (typeof definition.id !== "string") throw new Error(`catalog[${index}].id: 应为字符串`);
    return { id: definition.id };
  });
  validateCatalogOrganization(catalog, project.catalogOrganization);
  items(project.trees, "trees").forEach((item, index) => {
    const path = `trees[${index}]`;
    const tree = record(item, path);
    items(tree.nodes, `${path}.nodes`).forEach((item, index) => {
      const nodePath = `${path}.nodes[${index}]`;
      const node = record(item, nodePath);
      parseNodeType(node.type, `${nodePath}.type`);
      if (node.comment !== undefined && typeof node.comment !== "string") {
        throw new Error(`${nodePath}.comment: 应为字符串`);
      }
    });
  });
}
