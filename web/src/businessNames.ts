// 新业务名称规则随声明持久保存；缺省或零值属于历史数据，禁止隐式迁移。
export const BUSINESS_NAMING_VERSION = 1;

// 动作与条件共用固定前缀表，查询不依赖工程扫描。
export function businessNamePrefix(kind: string): string {
  return kind === "action" ? "Action" : kind === "condition" ? "Is" : "";
}

// 输入边界与表单共用规则检查，历史名称继续使用原有标识符约束。
export function businessNameError(kind: string, name: unknown, version: unknown): string | null {
  if (version === undefined || version === 0) return null;
  if (version !== BUSINESS_NAMING_VERSION) return "namingVersion 必须为 0 或 1";
  const prefix = businessNamePrefix(kind);
  if (!prefix) return "固定业务前缀仅适用于动作和条件节点";
  return typeof name === "string" && name.startsWith(prefix) && name.length > prefix.length
    ? null : `业务名称必须以 ${prefix} 开头，并在前缀后填写名称`;
}

// 仅创建新实例时补前缀；已经符合规则的名称不会叠加 Action 或 Is。
export function prefixedBusinessName(kind: string, name: string): string {
  const prefix = businessNamePrefix(kind);
  return name.startsWith(prefix) ? name : prefix + name;
}
