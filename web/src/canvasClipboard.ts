import { LosslessNumber } from "lossless-json";
import { validCodeName } from "./codeNames.ts";
import { isNodeType } from "./enums.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import type { BTNode, Position } from "./project.ts";
import { businessNameError, businessNamePrefix } from "./businessNames.ts";

// 复制条目保存节点快照和相对选区左上角的位置。
export interface CanvasClipboardEntry {
  node: BTNode; // 节点配置及选区内部的子节点关系。
  position: Position; // 相对坐标，粘贴时加上目标落点。
}

// 带版本的剪贴板载荷可在不同画布和工程间传递。
export interface CanvasClipboardPayload {
  kind: "behaviortree/nodes"; // 与普通文本及其他 JSON 内容区分。
  version: 1; // 剪贴板数据格式版本。
  nodes: CanvasClipboardEntry[]; // 复制的节点及其相对位置。
  root?: string; // 仅在原树根节点属于选区时记录。
}

// 只复制选区内部的连线，并把位置归一到选区的左上边界。
export function encodeCanvasSelection(entries: CanvasClipboardEntry[], root?: string): string | undefined {
  if (!entries.length) return undefined;
  const selected = new Set<string>();
  let minX = Infinity, minY = Infinity;
  for (const { node, position } of entries) {
    if (typeof node?.id !== "string" || !node.id || selected.has(node.id) || !isNodeType(node.type)
      || !Number.isFinite(position?.x) || !Number.isFinite(position?.y)) return undefined;
    selected.add(node.id);
    minX = Math.min(minX, position.x);
    minY = Math.min(minY, position.y);
  }
  const nodes: CanvasClipboardEntry[] = [];
  for (const { node, position } of entries) {
    const x = position.x - minX, y = position.y - minY;
    if (!Number.isFinite(x) || !Number.isFinite(y)) return undefined;
    const snapshot = parseJSON<BTNode>(stringifyJSON(node));
    if (snapshot.children !== undefined) {
      if (!Array.isArray(snapshot.children) || !snapshot.children.every(child => typeof child === "string")) return undefined;
      snapshot.children = snapshot.children.filter(child => selected.has(child));
    }
    nodes.push({ node: snapshot, position: { x, y } });
  }
  const payload: CanvasClipboardPayload = { kind: "behaviortree/nodes", version: 1, nodes };
  if (root && selected.has(root)) payload.root = root;
  return stringifyJSON(payload);
}

// 拒绝不属于本格式或不能安全重建节点映射的文本；解析错误不会传给画布事件。
export function decodeCanvasSelection(text: string): CanvasClipboardPayload | undefined {
  try {
    const value: unknown = parseJSON(text);
    if (!isRecord(value) || value.kind !== "behaviortree/nodes" || value.version !== 1
      || !Array.isArray(value.nodes) || value.nodes.length === 0) return undefined;
    const ids = new Set<string>();
    const links: string[][] = [];
    for (const entry of value.nodes) {
      if (!isRecord(entry) || !isRecord(entry.node) || !isRecord(entry.position)) return undefined;
      const { node, position } = entry;
      if (typeof node.id !== "string" || !node.id || ids.has(node.id) || !isNodeType(node.type)
        || typeof position.x !== "number" || !Number.isFinite(position.x)
        || typeof position.y !== "number" || !Number.isFinite(position.y)) return undefined;
      if (node.codeName !== undefined && !validCodeName(node.codeName)) return undefined;
      if (businessNameError(node.type, node.codeName ?? businessNamePrefix(node.type) + "1", node.namingVersion)) return undefined;
      for (const field of ["name", "comment", "binding", "tree"] as const) {
        if (node[field] !== undefined && typeof node[field] !== "string") return undefined;
      }
      if (node.children !== undefined && !isStringArray(node.children)) return undefined;
      if (node.params !== undefined) {
        if (!isRecord(node.params)) return undefined;
        for (const param of Object.values(node.params)) {
          if (!isRecord(param) || (param.field !== undefined && typeof param.field !== "string")) return undefined;
        }
      }
      if (node.count !== undefined && !isGoInt64(node.count)) return undefined;
      if (node.reselectOnCompletion !== undefined && (typeof node.reselectOnCompletion !== "boolean"
        || node.reselectOnCompletion && node.type !== "priority")) return undefined;
      if (node.durationMs !== undefined && (typeof node.durationMs !== "number"
        || !Number.isSafeInteger(node.durationMs) || node.durationMs < 0 || node.durationMs > 9_223_372_036_854)) return undefined;
      ids.add(node.id);
      links.push(node.children ?? []);
    }
    if (value.root !== undefined && (typeof value.root !== "string" || !ids.has(value.root))) return undefined;
    for (const children of links) {
      // 外部载荷也只允许重建选区内部连线。
      if (children.some(child => !ids.has(child))) return undefined;
    }
    return value as unknown as CanvasClipboardPayload;
  } catch {
    return undefined;
  }
}

// JSON 对象必须排除 null 和数组，避免把容器误作节点或位置。
function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// 子节点 ID 必须全部为字符串，供旧、新节点映射直接查找。
function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(child => typeof child === "string");
}

// Go 的 int 在当前 64 位目标上允许完整 int64；超出 JS 安全范围的值保留无损对象。
function isGoInt64(value: unknown): boolean {
  if (typeof value === "number") return Number.isSafeInteger(value);
  if (!(value instanceof LosslessNumber) || !/^-?(?:0|[1-9]\d*)$/.test(value.value)) return false;
  const digits = value.value.startsWith("-") ? value.value.length - 1 : value.value.length;
  if (digits > 19) return false;
  const integer = BigInt(value.value);
  return integer >= -9_223_372_036_854_775_808n && integer <= 9_223_372_036_854_775_807n;
}
