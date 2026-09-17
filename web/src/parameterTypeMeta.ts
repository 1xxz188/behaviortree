import type { ValueType } from "./enums.ts";

// 参数类型的展示元数据仅用于编辑器提示，不参与 JSON 序列化或类型校验。
interface ParameterTypeInfo {
  short: string; // 优先展示用途的简短语义名称。
  description: string; // 说明适用的业务场景。
  example: string; // 帮助用户对应实际业务参数。
  note: string; // 与容易混淆的类型进行区分。
  detail: string; // 第二层说明存储或输入约定。
  placeholder: string; // 示例必须符合参数声明现有的 JSON 默认值规则。
  inputPlaceholder?: string; // 非 JSON 的常量输入框可直接输入的示例。
}

// 八种类型的下拉提示、悬浮说明、辅助文字和默认值示例集中维护；新增类型时由编译器检查遗漏。
export const PARAM_TYPE_META: Readonly<Record<ValueType, Readonly<ParameterTypeInfo>>> = {
  bool: {
    short: "布尔开关 / 条件",
    description: "布尔值，用于表示开关、条件或二元状态。",
    example: "是否允许攻击、是否循环巡逻、是否忽略障碍。",
    note: "", detail: "", placeholder: "true / false",
  },
  int64: {
    short: "有符号整数",
    description: "用于可能出现负数的普通数值。",
    example: "坐标偏移、血量变化值、层级差值。",
    note: "", detail: "", placeholder: "例如：-1、100",
  },
  uint64: {
    short: "普通非负整数 / 普通 ID",
    description: "用于普通非负数值或非实体类 ID。",
    example: "技能 ID（skill_id）、地图 ID（map_id）、配置 ID（config_id）、物品配置 ID（item_id）、数量、计数器。",
    note: "角色、NPC、怪物、建筑、部队等运行时业务实体请优先使用 entity。",
    detail: "", placeholder: "例如：10001",
  },
  float64: {
    short: "浮点数",
    description: "用于距离、比例、速度、概率等小数值。",
    example: "移动速度、攻击距离、血量比例、随机概率。",
    note: "纯时间长度请优先使用 duration。",
    detail: "", placeholder: "例如：1.5",
  },
  string: {
    short: "自由文本",
    description: "用于名称、标签、Key 或其他自由文本数据。",
    example: "黑板 Key、状态名称、资源名称、自定义标签。",
    note: "如果取值范围固定，请优先考虑 enum。",
    detail: "", placeholder: '例如："target"',
  },
  enum: {
    short: "有限固定选项",
    description: "用于只能从有限选项中选择的业务状态或类型。",
    example: "目标类型 Enemy/Friend、移动方式 Walk/Run、攻击类型 Melee/Ranged、状态 Idle/Attack/Escape。",
    note: "有限固定取值请优先使用 enum；自由文本使用 string。",
    detail: "默认值需属于声明的允许枚举值。", placeholder: '例如："Enemy"',
  },
  entity: {
    short: "业务实体引用",
    description: "用于玩家、NPC、怪物、建筑、部队等运行时实体 ID。",
    example: "攻击目标（attack_target）、敌人（enemy）、跟随目标（follow_unit）、治疗目标、交互对象、建筑（building）、移动目标实体（target）；也适用于英雄、城市、地图对象。",
    note: "普通配置 ID（技能 ID、地图 ID、配置表 ID 等）请使用 uint64。",
    detail: "底层使用 uint64 表示，但具有明确的运行时业务实体语义。",
    placeholder: "例如：10001（实体 ID）",
  },
  duration: {
    short: "时间长度",
    description: "用于等待时间、超时时间、冷却时间等持续时间。",
    example: "等待 1.5 秒、攻击冷却 2 秒、寻路超时 5 秒。",
    note: "纯时间长度请优先使用 duration，不要依赖 float64 或 int64 的单位约定。",
    detail: 'JSON 默认值使用带单位的字符串，例如 "1s"、"500ms"、"2.5s"；节点常量和黑板默认值输入框直接填写 1s。支持 Go time.ParseDuration 的单位与组合，例如 1m30s；兼容旧的纳秒整数。',
    placeholder: '例如："1s"、"500ms"、"2.5s"',
    inputPlaceholder: "例如：1s、500ms、2.5s",
  },
};

// 按类型直接查表；先说明何时选用，再补充业务示例及底层表示。
export function parameterTypeTooltip(type: ValueType): string {
  const info = PARAM_TYPE_META[type];
  return [`${type} — ${info.short}`, info.description, info.note,
    `示例：${info.example}`, info.detail ? `详细说明：${info.detail}` : ""].filter(Boolean).join("\n\n");
}
