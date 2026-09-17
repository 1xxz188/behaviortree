import { LosslessNumber, parse, stringify } from "lossless-json";
import { parseValueType } from "./enums.ts";
import type { ValueType } from "./enums.ts";

// 工程中的 64 位整数必须原样往返，画布坐标与普通小整数仍使用原生 number。
export function parseJSON<T = unknown>(text: string): T {
  return parse(text, undefined, (value: string) => {
    const n = Number(value);
    return /^-?\d+$/.test(value) && !Number.isSafeInteger(n)
      ? new LosslessNumber(value)
      : n;
  }) as T;
}

// 统一用于网络、文件和撤销历史，避免某条编辑路径丢失整数精度。
export function stringifyJSON(value: unknown, space?: number): string {
  return stringify(value, undefined, space) ?? "null";
}

// 类型错误交给界面显示；完整取值范围由 Go 元数据验证器统一校验。
export function parseInput(text: string, type: ValueType): unknown {
  parseValueType(type);
  if (type === "string" || type === "enum") return text;
  // 可读时长原样保存为字符串，由 Go time.ParseDuration 统一校验单位与范围；旧纳秒整数仍走无损整数分支。
  if (type === "duration" && !/^-?\d+$/.test(text)) {
    if (!text.trim()) throw new Error("请输入带单位的时间长度");
    return text;
  }
  if (type === "bool") {
    if (!["true", "false"].includes(text))
      throw new Error("布尔值应为 true 或 false");
    return text === "true";
  }
  if (type !== "float64") {
    if (!/^-?\d+$/.test(text)) throw new Error("请输入整数");
    const n = Number(text);
    return Number.isSafeInteger(n) ? n : new LosslessNumber(text);
  }
  if (!text.trim() || !Number.isFinite(Number(text)))
    throw new Error("请输入有限浮点数");
  return Number(text);
}
