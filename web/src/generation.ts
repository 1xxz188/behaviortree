import { isLosslessNumber } from "lossless-json";
import { clone } from "./project.ts";
import type { Project } from "./project.ts";
import { stringifyJSON } from "./json.ts";
import { normalizeCodeNames } from "./codeNames.ts";

// SourceLocation 描述源节点的一次展开及其生成函数起始行。
export interface SourceLocation {
  file: string; // 对应生成文件名，行号仅在该文件内有效。
  treeId: string; // 定义树 ID，同名节点依靠树 ID 区分。
  nodeId: string; // 定义树中的稳定节点 ID。
  index: number; // 本次生成的独立执行槽位。
  line: number; // 生成文件中从 1 开始的函数声明行。
  functionName: string; // 实际生成函数名，必须与对应源码声明一致。
}

// SourceIndex 将一次线性扫描的结果用于节点和代码行的 O(1) 导航。
export interface SourceIndex {
  lines: string[]; // 接收快照时拆分，切换文件无需重新扫描源码。
  longestLineLength: number; // 预先统计宽度，虚拟视图直接读取。
  byNode: Map<string, SourceLocation[]>; // 同一源节点可能被多次展开。
  byLine: Map<number, SourceLocation>; // 仅收录节点函数自身范围内的行。
}

// GeneratedFile 保存一个定义树或公共 glue 的完整源码。
export interface GeneratedFile {
  name: string; // 服务端提供的稳定下载文件名。
  treeId: string; // 定义树 ID，公共 glue 为空字符串。
  source: string; // 完整 Go 源码，以字符串传输。
}

// GeneratedSourceIndex 在快照接收时建立跨文件导航索引。
export interface GeneratedSourceIndex {
  filesByName: Map<string, GeneratedFile>; // 按文件名直接取得显示与下载内容。
  fileByTreeID: Map<string, GeneratedFile>; // 定义树的全部展开实例位于同一文件。
  locationsByFile: Map<string, SourceLocation[]>; // 按文件分组，避免每次切换扫描全局映射。
  sourceIndexes: Map<string, SourceIndex>; // 各文件独立的节点和行号索引。
}

// 完整源码和映射只线性扫描一次，之后文件切换和节点定位均使用索引。
export function createGeneratedSourceIndex(files: GeneratedFile[], sourceMap: SourceLocation[]): GeneratedSourceIndex {
  const filesByName = new Map<string, GeneratedFile>();
  const fileByTreeID = new Map<string, GeneratedFile>();
  const locationsByFile = new Map<string, SourceLocation[]>();
  const sourceIndexes = new Map<string, SourceIndex>();
  for (const file of files) {
    if (filesByName.has(file.name)) throw new Error(`生成文件名重复：${file.name}`);
    if (file.treeId && fileByTreeID.has(file.treeId)) throw new Error(`定义树生成文件重复：${file.treeId}`);
    filesByName.set(file.name, file);
    if (file.treeId) fileByTreeID.set(file.treeId, file);
    locationsByFile.set(file.name, []);
  }
  for (const location of sourceMap) {
    const file = filesByName.get(location.file);
    if (file?.treeId && file.treeId === location.treeId) locationsByFile.get(location.file)!.push(location);
  }
  for (const file of files) sourceIndexes.set(file.name, createSourceIndex(file.source, locationsByFile.get(file.name)!));
  return { filesByName, fileByTreeID, locationsByFile, sourceIndexes };
}

// 节点联动优先选择定义树文件；普通文件切换保留指定文件，缺失时回到公共 glue。
export function selectGeneratedFile(index: GeneratedSourceIndex, name: string, treeId?: string): GeneratedFile | undefined {
  return (treeId ? index.fileByTreeID.get(treeId) : undefined)
    ?? index.filesByName.get(name)
    ?? index.filesByName.get("glue.gen.go")
    ?? index.filesByName.values().next().value;
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

// 工程集合的排序规则与生成器一致；树展示名、布局和集合原始顺序不使代码过期。
export function semanticSignature(project: Project): string {
  const snapshot = clone(project);
  delete snapshot.catalogOrganization; // 目录、标签和排序不改变生成语义。
  normalizeCodeNames(snapshot);
  const byID = (a: { id: string }, b: { id: string }) =>
    a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  snapshot.trees.sort(byID);
  snapshot.blackboard.sort(byID);
  snapshot.catalog.sort(byID);
  snapshot.generation.contextImport ??= "";
  for (const field of snapshot.blackboard) field.enum ??= [];
  for (const tree of snapshot.trees) {
    tree.name = "";
    delete tree.layout;
    tree.nodes.sort(byID);
    // Go 的 omitempty 会在保存后省略空值，统一表示保证重新打开时签名稳定。
    for (const node of tree.nodes) {
      node.name ??= "";
      node.comment ??= ""; // 节点注释参与生成，空值与 Go omitempty 保存后的缺省表示一致。
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
    for (const parameter of definition.params) {
      parameter.enum ??= [];
      parameter.comment ??= ""; // 与 Go omitempty 的空注释表示一致，保存后不误报源码过期。
    }
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
  let longestLineLength = 0;
  for (let index = 0; index < lines.length; index++) {
    const line = index + 1;
    const text = lines[index]!;
    longestLineLength = Math.max(longestLineLength, text.length);
    const location = starts.get(line);
    // 只接受明确提供且与源码一致的函数名，避免错配辅助函数。
    if (location?.functionName && text.startsWith(`func ${location.functionName}(`)) {
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
  return { byNode, byLine, lines, longestLineLength };
}
