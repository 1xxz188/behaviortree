import { isLosslessNumber } from "lossless-json";
import { clone } from "./project.ts";
import type { Definition, Diagnostic, Field, Parameter, Project, Value } from "./project.ts";

// 每份声明只构造一次参数查找表与枚举集合，所有引用节点复用。
interface ParameterSchema {
  parameter: Parameter; // 当前参数声明。
  allowed?: ReadonlySet<string>; // 非空枚举约束，成员检查为 O(1)。
  fields: Map<string, boolean>; // 缓存同一参数与黑板字段的兼容性，重复引用不再扫描枚举域。
}

// 黑板枚举的所有可能取值都必须在目标参数允许范围内。
function compatibleField(field: Field | undefined, schema: ParameterSchema): boolean {
  if (!field || field.type !== schema.parameter.type) return false;
  const cached = schema.fields.get(field.id);
  if (cached !== undefined) return cached;
  const compatible = !schema.allowed || !!field.enum?.length && field.enum.every(value => schema.allowed!.has(value));
  schema.fields.set(field.id, compatible);
  return compatible;
}

// 类型改变时只保留可由新声明直接解释的常量，不做字符串转数字或时长单位推断。
function compatibleConstant(value: unknown, previous: Parameter, schema: ParameterSchema): boolean {
  const next = schema.parameter;
  if (next.type === "enum") return typeof value === "string" && (!schema.allowed || schema.allowed.has(value));
  if (previous.type === next.type) return true;
  if (next.type === "string") return typeof value === "string";
  if (next.type === "bool") return typeof value === "boolean";
  if (previous.type === "duration" || next.type === "duration") return false;
  if (typeof value !== "number" && !isLosslessNumber(value)) return false;
  const text = String(value);
  if (next.type === "float64") return Number.isFinite(Number(text));
  if (!/^-?\d+$/.test(text)) return false;
  const integer = BigInt(text);
  return next.type === "int64"
    ? integer >= -(1n << 63n) && integer < (1n << 63n)
    : integer >= 0n && integer < (1n << 64n);
}

// 更新目录时一次遍历节点，以定义及参数索引同步数据；不依赖面板选择、轮询或重复 IO。
export function synchronizeCatalog(project: Project, catalog: Definition[]): Diagnostic[] {
  const previous = new Map(project.catalog.map(definition => [definition.id,
    new Map((definition.params ?? []).map(parameter => [parameter.name, parameter]))]));
  const schemas = new Map(catalog.map(definition => [definition.id,
    new Map((definition.params ?? []).map(parameter => [parameter.name, {
      parameter,
      allowed: parameter.type === "enum" && parameter.enum?.length ? new Set(parameter.enum) : undefined,
      fields: new Map<string, boolean>(),
    } satisfies ParameterSchema]))]));
  const fields = new Map(project.blackboard.map(field => [field.id, field]));
  const resets: Diagnostic[] = [];
  for (const tree of project.trees) {
    for (const node of tree.nodes) {
      if (node.type !== "action" && node.type !== "condition") continue;
      const schema = schemas.get(node.binding ?? "");
      if (!schema) continue; // 未知业务定义由共用校验器报告，避免丢失无法解释的数据。
      const oldSchema = previous.get(node.binding!);
      const params: Record<string, Value> = Object.create(null);
      for (const [name, next] of schema) {
        const binding = node.params && Object.hasOwn(node.params, name) ? node.params[name] : undefined;
        const old = oldSchema?.get(name);
        const bound = binding && (binding.field !== undefined || binding.value !== undefined);
        const changed = old && (old.type !== next.parameter.type || next.parameter.type === "enum");
        const compatible = !changed || !bound || (binding.field !== undefined
          ? compatibleField(fields.get(binding.field), next)
          : compatibleConstant(binding.value, old, next));
        if (old && binding && compatible) params[name] = binding;
        else if (next.parameter.default !== undefined) params[name] = { value: clone(next.parameter.default) };
        // 缺失键即未绑定；不能写入空常量，也不能让已删除参数继续进入保存数据。
        if (old && bound && !compatible) resets.push({
          treeId: tree.id, nodeId: node.id, field: `params.${name}`,
          message: `参数类型或约束已变更（${old.type} → ${next.parameter.type}），旧绑定不兼容，已重置${next.parameter.default === undefined ? "：未绑定参数，请重新配置" : "为业务定义默认值"}`,
        });
      }
      node.params = params;
    }
  }
  project.catalog = catalog;
  return resets;
}
