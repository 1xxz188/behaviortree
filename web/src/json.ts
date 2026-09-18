import { LosslessNumber, parse, stringify } from "lossless-json";
import { parseValueType } from "./enums.ts";
import type { ValueType } from "./enums.ts";

// 危险键替换只针对对象键 token；普通字符串和数字词法保持原样。
interface ProtectedJSON {
  text: string; // 无损解析器可安全处理的输入文本。
  sentinel?: string; // 替代 __proto__ 的不冲突临时键，解析后立即恢复。
}

// 当前无损库通过 object[key] 写对象，先转义特殊键以避免丢失属性或改变原型。
function protectObjectKeys(text: string): ProtectedJSON {
  if (!/__proto__|\\u/i.test(text)) return { text };
  const names = new Set<string>();
  const replacements: { start: number; end: number }[] = [];
  // JSON 字符串边界按转义字符成对扫描；原始输入是否合法仍由解析器判定。
  const strings = /"(?:[^"\\]|\\[\s\S])*"/g;
  for (const match of text.matchAll(strings)) {
    const end = match.index! + match[0].length;
    let next = end;
    while (next < text.length && /[\x20\t\r\n]/.test(text[next])) next++;
    if (text[next] !== ":") continue;
    const key: string = JSON.parse(match[0]);
    names.add(key);
    if (key === "__proto__") replacements.push({ start: match.index!, end });
  }
  if (!replacements.length) return { text };
  let suffix = 0, sentinel = "__codex_json_proto_0__";
  while (names.has(sentinel)) sentinel = `__codex_json_proto_${++suffix}__`;
  const token = JSON.stringify(sentinel), chunks: string[] = [];
  let offset = 0;
  for (const replacement of replacements) {
    chunks.push(text.slice(offset, replacement.start), token); offset = replacement.end;
  }
  chunks.push(text.slice(offset));
  return { text: chunks.join(""), sentinel };
}

// 工程中的 64 位整数必须原样往返，画布坐标与普通小整数仍使用原生 number。
export function parseJSON<T = unknown>(text: string): T {
  const protectedJSON = protectObjectKeys(text);
  return parse(protectedJSON.text, protectedJSON.sentinel ? (_key, value) => {
    // 恢复自身数据属性，禁止触发 Object.prototype.__proto__ setter。
    if (value !== null && typeof value === "object" && Object.hasOwn(value, protectedJSON.sentinel!)) {
      const object = value as Record<string, unknown>;
      Object.defineProperty(object, "__proto__", { value: object[protectedJSON.sentinel!], enumerable: true, writable: true, configurable: true });
      delete object[protectedJSON.sentinel!];
    }
    return value;
  } : undefined, (value: string) => {
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
