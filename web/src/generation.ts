import { isLosslessNumber } from "lossless-json";
import { clone } from "./project.ts";
import type { Project } from "./project.ts";
import { stringifyJSON } from "./json.ts";

// SourceLocation 描述源节点的一次展开及其生成函数起始行。
export interface SourceLocation {
  treeId: string; // 定义树 ID，同名节点依靠树 ID 区分。
  nodeId: string; // 定义树中的稳定节点 ID。
  index: number; // 本次生成的独立执行槽位。
  line: number; // 生成文件中从 1 开始的函数声明行。
}

// SourceIndex 将一次线性扫描的结果用于节点和代码行的 O(1) 导航。
export interface SourceIndex {
  byNode: Map<string, SourceLocation[]>; // 同一源节点可能被多次展开。
  byLine: Map<number, SourceLocation>; // 仅收录节点函数自身范围内的行。
}

// GenerationToken 固定请求顺序及发起时的工程语义，用于丢弃迟到结果。
export interface GenerationToken {
  revision: number; // 每次请求及工程切换均递增的序号。
  signature: string; // 不包含画布布局的工程快照。
}

// 按两个独立 JSON 字符串建立键，避免树名或节点名中的分隔符导致碰撞。
export function sourceNodeKey(treeId: string, nodeId: string): string {
  return JSON.stringify([treeId, nodeId]);
}

// 对普通对象的键排序，同时原样保留无损整数及具有业务顺序的数组。
function canonicalValue(value: unknown): unknown {
  if (isLosslessNumber(value)) return value;
  if (Array.isArray(value)) return value.map(canonicalValue);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([key, item]) => [key, canonicalValue(item)]),
    );
  }
  return value;
}

// 工程集合的排序规则与生成器一致；布局和集合原始顺序不使已生成代码过期。
export function semanticSignature(project: Project): string {
  const snapshot = clone(project);
  const byID = (a: { id: string }, b: { id: string }) =>
    a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  snapshot.trees.sort(byID);
  snapshot.blackboard.sort(byID);
  snapshot.catalog.sort(byID);
  snapshot.generation.contextImport ??= "";
  for (const field of snapshot.blackboard) field.enum ??= [];
  for (const tree of snapshot.trees) {
    delete tree.layout;
    tree.nodes.sort(byID);
    // Go 的 omitempty 会在保存后省略空值，统一表示保证重新打开时签名稳定。
    for (const node of tree.nodes) {
      node.name ??= "";
      node.children ??= [];
      node.binding ??= "";
      node.params ??= {};
      node.tree ??= "";
      node.count ??= 0;
      node.durationMs ??= 0;
      for (const parameter of Object.values(node.params)) parameter.field ??= "";
    }
  }
  for (const definition of snapshot.catalog) {
    definition.params ??= [];
    definition.events ??= [];
    definition.params.sort((a, b) =>
      a.name < b.name ? -1 : a.name > b.name ? 1 : 0,
    );
    for (const parameter of definition.params) parameter.enum ??= [];
    definition.events.sort();
  }
  return stringifyJSON(canonicalValue(snapshot));
}

// GenerationRequests 以请求代次隔离切换工程和连续预览，不依赖定时轮询。
export class GenerationRequests {
  private revision = 0; // 最新请求代次，不属于工程的持久化内容。

  // 发起新请求会自动淘汰前一个请求，即使两个请求的工程内容相同。
  begin(project: Project): GenerationToken {
    return { revision: ++this.revision, signature: semanticSignature(project) };
  }

  // 工程切换、重置或取消请求时主动失效；纯布局操作不需要调用。
  invalidate(): void {
    this.revision++;
  }

  // 调用方在每次语义编辑时主动失效后，可用 O(1) 检查完成响应。
  acceptsRevision(token: GenerationToken): boolean {
    return token.revision === this.revision;
  }

  // 接受结果时同时检查工程语义，避免请求期间的编辑被旧源码覆盖。
  accepts(token: GenerationToken, project: Project): boolean {
    return token.revision === this.revision && token.signature === semanticSignature(project);
  }
}

// 扫描 gofmt 生成代码的顶层函数边界；缩进的嵌套大括号不会提前结束函数。
// 生成器将函数声明、函数体和最后的顶层右括号分别输出成行，字符串常量使用转义引号。
export function createSourceIndex(source: string, sourceMap: SourceLocation[]): SourceIndex {
  const byNode = new Map<string, SourceLocation[]>();
  const byLine = new Map<number, SourceLocation>();
  const starts = new Map(sourceMap.map((location) => [location.line, location]));
  let active: SourceLocation | undefined;
  const lines = source.split("\n");
  for (let index = 0; index < lines.length; index++) {
    const line = index + 1;
    const text = lines[index]!;
    const location = starts.get(line);
    // 同时核对函数名与展开槽位，错误或过期映射不能把辅助函数关联到节点。
    if (location && text.startsWith(`func btNode${location.index}(`)) {
      active = location;
      const key = sourceNodeKey(location.treeId, location.nodeId);
      const occurrences = byNode.get(key);
      if (occurrences) occurrences.push(location);
      else byNode.set(key, [location]);
    } else if (/^(?:func|type|var|const|package|import)\b/.test(text)) {
      active = undefined;
    }
    if (active) {
      byLine.set(line, active);
      if (/^}\s*$/.test(text)) active = undefined;
    }
  }
  return { byNode, byLine };
}
