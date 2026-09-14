<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import CatalogManager from "./CatalogManager.vue";
import SourceViewer from "./SourceViewer.vue";
import { GenerationRequests, semanticSignature } from "./generation";
import type { SourceLocation } from "./generation";
import { VueFlow, Handle, Position, useVueFlow } from "@vue-flow/core";
import type { Connection, NodeDragEvent, NodeMouseEvent } from "@vue-flow/core";
import { Background } from "@vue-flow/background";
import { Controls } from "@vue-flow/controls";
import {
  autoLayout,
  clone,
  connect,
  emptyProject,
  kinds,
  removeNodes,
  allocateID,
} from "./project";
import type {
  BTNode,
  Definition,
  Diagnostic,
  Field,
  Position as NodePosition,
  Project,
  Tree,
  Value,
} from "./project";
import { parseInput, parseJSON, stringifyJSON } from "./json";
import {
  nodeTypes, valueTypes, parseValueType, validateProjectTypes,
} from "./enums";
import type { DefinitionKind, NodeType, ValueType } from "./enums";

// 生成接口统一返回源码、版本及节点位置；读取磁盘时附带工程匹配结果。
interface GeneratedCode {
  source: string; // 完整 Go 源码。
  sourceMap: SourceLocation[]; // 各展开实例的位置。
  version: string; // 生成内容的版本摘要。
  path?: string; // 实际落盘路径，预览无路径。
  matchesCurrent?: boolean; // 已保存源码是否对应当前工程。
}
// 结果保留生成时的修订号，编辑后只标记过期，不丢弃源码。
interface CodeSnapshot extends GeneratedCode {
  revision: number; // 对应当前编辑会话的语义修订。
  signature: string; // 撤销恢复时判断内容是否一致。
  origin: "preview" | "generated" | "saved"; // 区分预览和磁盘产物。
}
// 请求错误保留节点诊断，由仍有效的调用展示。
class RequestError extends Error {
  constructor(message: string, readonly status: number, readonly diagnostics?: Diagnostic[]) {
    super(message);
  }
}

const project = ref<Project>(emptyProject());
const treeID = ref(project.value.trees[0]!.id);
const selected = ref("root");
const inspectorOpen = ref(false);
const search = ref("");
const fileName = ref("project.json");
const files = ref<string[]>([]);
const message = ref("本地工程 · 修改后请保存");
const error = ref(false);
const busy = ref(false);
const dirty = ref(false);
const diagnostics = ref<Diagnostic[]>([]);
const codeSnapshot = shallowRef<CodeSnapshot>();
const scaffoldSnapshot = shallowRef<{ source: string; revision: number; signature: string }>();
const semanticRevision = ref(0);
let editRevision = 0; // 包含布局编辑，防止加载请求覆盖期间的新草稿。
const generationRequests = new GenerationRequests();
const source = computed(() => codeSnapshot.value?.source ?? "");
const sourceStale = computed(() => !!codeSnapshot.value && codeSnapshot.value.revision !== semanticRevision.value);
const scaffoldStale = computed(() => !!scaffoldSnapshot.value && scaffoldSnapshot.value.revision !== semanticRevision.value);
const outputPath = computed(() => codeSnapshot.value?.path ?? "仅预览 · 尚未写入目录");
const catalogDialog = ref<{ mode: "create" | "import" | "manage"; kind?: DefinitionKind }>();
// 加载工程时一次建立集合，后续新增与复制只做集合查重。
let occupiedIDs = new Set<string>();
rebuildIDs();
const tab = ref("nodes");
const bottomTab = ref("diagnostics");
const workbench = ref<HTMLElement>();
const outputPanel = ref<HTMLElement>();
const outputHeight = ref<number>();
const renderedOutputHeight = ref(170);
const minOutputHeight = 100;
const maxOutputHeight = ref(170);
const resizingOutput = ref(false);
let outputResizeObserver: ResizeObserver | undefined;
let outputDrag: { pointerId: number; startY: number; startHeight: number } | undefined;

function setOutputHeight(height: number) {
  outputHeight.value = Math.round(
    Math.max(minOutputHeight, Math.min(maxOutputHeight.value, height)),
  );
}

// 根据实际布局限制面板高度，为画布保留操作空间。
function updateOutputBounds() {
  if (!workbench.value || !outputPanel.value) return;
  const style = getComputedStyle(workbench.value);
  const minCanvasHeight = parseFloat(
    style.getPropertyValue("--canvas-min-height"),
  );
  const rows = style.gridTemplateRows.split(" ").map(parseFloat);
  maxOutputHeight.value = Math.max(
    minOutputHeight,
    Math.floor(
      workbench.value.clientHeight - rows[0]! - rows[3]! - minCanvasHeight,
    ),
  );
  if (outputHeight.value !== undefined) setOutputHeight(outputHeight.value);
  renderedOutputHeight.value = Math.round(outputPanel.value.getBoundingClientRect().height);
}

function startOutputResize(event: PointerEvent) {
  if (event.button !== 0 || outputDrag || !outputPanel.value) return;
  event.preventDefault();
  updateOutputBounds();
  const handle = event.currentTarget as HTMLElement;
  handle.focus();
  handle.setPointerCapture(event.pointerId);
  outputDrag = {
    pointerId: event.pointerId,
    startY: event.clientY,
    startHeight: outputPanel.value.getBoundingClientRect().height,
  };
  resizingOutput.value = true;
}

function resizeOutput(event: PointerEvent) {
  if (!outputDrag || outputDrag.pointerId !== event.pointerId) return;
  setOutputHeight(outputDrag.startHeight + outputDrag.startY - event.clientY);
}

function stopOutputResize(event: PointerEvent) {
  if (!outputDrag || outputDrag.pointerId !== event.pointerId) return;
  outputDrag = undefined;
  resizingOutput.value = false;
  const handle = event.currentTarget as HTMLElement;
  if (handle.hasPointerCapture(event.pointerId))
    handle.releasePointerCapture(event.pointerId);
}

function resizeOutputWithKeyboard(event: KeyboardEvent) {
  if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  updateOutputBounds();
  const height = outputPanel.value?.getBoundingClientRect().height ?? 170;
  setOutputHeight(
    event.key === "Home" ? minOutputHeight :
    event.key === "End" ? maxOutputHeight.value :
    height + (event.key === "ArrowUp" ? 20 : -20),
  );
}
const undoStack = ref<string[]>([]);
const redoStack = ref<string[]>([]);
const importInput = ref<HTMLInputElement>();
const { fitView, setCenter, screenToFlowCoordinate } = useVueFlow();
// 拖拽只保存节点模板，成功落入画布后才写入工程和撤销历史。
const paletteDrag = ref<{ type: NodeType; binding?: string }>();
const canvasDragOver = ref(false);

const tree = computed(
  () =>
    project.value.trees.find((t) => t.id === treeID.value) ??
    project.value.trees[0]!,
);
const nodeIndex = computed(() => new Map(tree.value.nodes.map((n) => [n.id, n])));
const node = computed(() => nodeIndex.value.get(selected.value));
const definitionIndex = computed(() => new Map(project.value.catalog.map((d) => [d.id, d])));
const definition = computed(() => definitionIndex.value.get(node.value?.binding ?? ""));
const bindingOptions = computed(() => project.value.catalog.filter((d) => d.kind === node.value?.type));
const invalidNodes = computed(() => new Set(diagnostics.value.filter((d) => d.treeId === tree.value.id).map((d) => d.nodeId)));
const availableKinds = computed(() =>
  nodeTypes.map((type) => ({ type, info: kinds[type] })).filter(({ type, info }) =>
    `${info.label} ${type}`.toLowerCase().includes(search.value.toLowerCase()),
  ),
);
const graphNodes = computed(() =>
  (tree.value?.nodes ?? []).map((n, i) => ({
    id: n.id,
    type: "behavior",
    position: tree.value.layout?.[n.id] ?? {
      x: 70 + (i % 3) * 250,
      y: 70 + Math.floor(i / 3) * 120,
    },
    selected: n.id === selected.value,
    data: {
      node: n,
      kind: kinds[n.type],
      root: n.id === tree.value.root,
      invalid: invalidNodes.value.has(n.id),
    },
  })),
);
const graphEdges = computed(() =>
  (tree.value?.nodes ?? []).flatMap((n) =>
    (n.children ?? []).map((child, i) => ({
      id: `${n.id}/${child}`,
      source: n.id,
      target: child,
      type: "smoothstep",
      label: String(i + 1),
      style: { stroke: "#607483", strokeWidth: 1.8 },
      labelStyle: { fill: "#c7d5df" },
      labelBgStyle: { fill: "#1e2a35" },
    })),
  ),
);

// 更新底部状态，不弹出阻塞式错误窗口。
function notice(text: string, failed = false) {
  message.value = text;
  error.value = failed;
}
// 保存编辑前快照，使语义和布局都可以撤销。
function checkpoint() {
  editRevision++;
  undoStack.value.push(stringifyJSON(project.value));
  if (undoStack.value.length > 100) undoStack.value.shift();
  redoStack.value = [];
  dirty.value = true;
}
// 只有语义变更使生成结果和在途请求过期；画布坐标不参与。
function invalidateCode() {
  semanticRevision.value++;
  generationRequests.invalidate();
  diagnostics.value = [];
}
// 在统一历史边界内修改工程。
function mutate(fn: () => void, semantic = true) {
  checkpoint();
  fn();
  if (semantic) invalidateCode();
}
// 只在替换工程时扫描一次已有 ID，删除过的 ID 在本会话中也不复用。
function rebuildIDs() {
  occupiedIDs = new Set(project.value.blackboard.map((f) => f.id));
  for (const t of project.value.trees) {
    occupiedIDs.add(t.id);
    for (const n of t.nodes) occupiedIDs.add(n.id);
  }
}
// 工程切换隔离源码、诊断和请求，避免显示另一工程的结果。
function resetResults() {
  editRevision++;
  invalidateCode();
  codeSnapshot.value = undefined;
  scaffoldSnapshot.value = undefined;
  rebuildIDs();
}
// 恢复工程快照；纯布局撤销不使源码过期。
function restore(snapshot: string) {
  editRevision++;
  const restored = parseJSON<Project>(snapshot);
  validateProjectTypes(restored);
  const signature = semanticSignature(restored);
  if (signature !== semanticSignature(project.value)) invalidateCode();
  project.value = restored;
  rebuildIDs();
  if (!project.value.trees.some((t) => t.id === treeID.value))
    treeID.value = project.value.trees[0]?.id ?? "";
  selected.value = "";
  dirty.value = true;
  if (codeSnapshot.value?.signature === signature)
    codeSnapshot.value = { ...codeSnapshot.value, revision: semanticRevision.value };
  if (scaffoldSnapshot.value?.signature === signature)
    scaffoldSnapshot.value = { ...scaffoldSnapshot.value, revision: semanticRevision.value };
}
// 撤销最近一次工程修改。
function undo() {
  const state = undoStack.value.pop();
  if (state) {
    redoStack.value.push(stringifyJSON(project.value));
    restore(state);
  }
}
// 恢复被撤销的工程修改。
function redo() {
  const state = redoStack.value.pop();
  if (state) {
    undoStack.value.push(stringifyJSON(project.value));
    restore(state);
  }
}
// 提交文本属性变更，保留可撤销历史。
function changeText(event: Event, fn: (text: string) => void) {
  mutate(() => fn((event.target as HTMLInputElement).value));
}
// 创建稳定节点 ID，并放置到当前树画布。
function addNode(type: NodeType, binding?: string, position?: NodePosition) {
  mutate(() => {
    const id = allocateID(occupiedIDs),
      item: BTNode = { id, type, name: kinds[type]?.label ?? type };
    if (binding) {
      item.binding = binding;
      item.name = definitionIndex.value.get(binding)?.name;
    }
    if (["repeat", "retry"].includes(type)) item.count = 3;
    if (["wait", "timeout"].includes(type)) item.durationMs = 1000;
    tree.value.nodes.push(item);
    tree.value.layout ??= {};
    tree.value.layout[id] = position ?? {
      x: 100 + (tree.value.nodes.length % 3) * 230,
      y: 100 + Math.floor(tree.value.nodes.length / 3) * 100,
    };
    if (!tree.value.root) tree.value.root = id;
    selected.value = id;
  });
}
// 使用浏览器原生拖影，让节点从侧栏连续跟随鼠标进入画布。
function startPaletteDrag(event: DragEvent, type: NodeType, binding?: string) {
  if (!event.dataTransfer) return;
  paletteDrag.value = { type, binding };
  event.dataTransfer.effectAllowed = "copy";
  event.dataTransfer.setData("application/x-behaviortree-node", type);
}
// 清除悬停状态；取消拖拽或在画布外松手不产生工程修改。
function endPaletteDrag() {
  paletteDrag.value = undefined;
  canvasDragOver.value = false;
}
// 仅接受当前节点库发起的拖拽，避免把外部文本或文件误建成节点。
function dragOverCanvas(event: DragEvent) {
  if (!paletteDrag.value || !event.dataTransfer) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
  canvasDragOver.value = true;
}
// 在画布内部跨越节点和连线时保留高亮，真正离开画布时才清除。
function leaveCanvas(event: DragEvent) {
  const canvas = event.currentTarget as HTMLElement;
  if (event.relatedTarget instanceof Node && canvas.contains(event.relatedTarget)) return;
  canvasDragOver.value = false;
}
// 由 Vue Flow 换算屏幕落点，统一处理画布偏移、平移和缩放。
function dropPaletteNode(event: DragEvent) {
  if (!paletteDrag.value) return;
  event.preventDefault();
  const { type, binding } = paletteDrag.value;
  const position = screenToFlowCoordinate({ x: event.clientX, y: event.clientY });
  endPaletteDrag();
  addNode(type, binding, position);
}
// 复用拓扑检查，按连线顺序追加子节点。
function link(connection: Connection) {
  if (!connection.source || !connection.target) return;
  const copy = clone(tree.value),
    failure = connect(copy, connection.source, connection.target);
  if (failure) return notice(failure, true);
  mutate(() => {
    tree.value.nodes = copy.nodes;
    tree.value.root = copy.root;
  });
}
// 只更新画布坐标，不调整执行顺序。
function moveNode({ node: moved }: NodeDragEvent) {
  mutate(() => {
    tree.value.layout ??= {};
    tree.value.layout[moved.id] = { ...moved.position };
  }, false);
}
// 删除所选节点和相关连接，保留其他草稿节点。
function deleteSelected() {
  if (node.value)
    mutate(() => {
      removeNodes(tree.value, [selected.value]);
      selected.value = "";
    });
}
// 复制单个节点属性，分配新 ID 并断开子节点。
function duplicate() {
  if (!node.value) return;
  mutate(() => {
    const n = clone(node.value!);
    n.id = allocateID(occupiedIDs);
    n.name = `${n.name ?? kinds[n.type]?.label} 副本`;
    n.children = [];
    tree.value.nodes.push(n);
    tree.value.layout ??= {};
    const p = tree.value.layout[selected.value] ?? { x: 100, y: 100 };
    tree.value.layout[n.id] = { x: p.x + 35, y: p.y + 100 };
    selected.value = n.id;
  });
}
// 显式调整子节点的执行优先级。
function reorder(index: number, offset: number) {
  if (!node.value?.children) return;
  mutate(() => {
    const list = node.value!.children!,
      other = index + offset;
    [list[index], list[other]] = [list[other]!, list[index]!];
  });
}
// 解除一条父子连接，保留被断开的节点。
function disconnect(index: number) {
  mutate(() => node.value!.children!.splice(index, 1));
}
// 重新计算画布布局，保持行为语义不变。
function layout() {
  mutate(() => autoLayout(tree.value), false);
  setTimeout(() => fitView({ padding: 0.18 }), 30);
}
// 创建可独立生成或作为子树引用的入口。
function addTree() {
  mutate(() => {
    const id = allocateID(occupiedIDs, "tree");
    project.value.trees.push({
      id,
      name: "新行为树",
      root: "",
      nodes: [],
      layout: {},
    });
    treeID.value = id;
    selected.value = "";
  });
}
// 删除前确认，悬挂子树引用由共同校验器报告。
function deleteTree() {
  if (project.value.trees.length === 1) return notice("至少保留一棵树", true);
  if (!confirm(`删除“${tree.value.name}”？引用它的子树节点需要重新选择。`))
    return;
  mutate(() => {
    project.value.trees = project.value.trees.filter(
      (t) => t.id !== treeID.value,
    );
    treeID.value = project.value.trees[0]!.id;
    selected.value = "";
  });
}
// 新建示例工程，并为原内容保留撤销快照。
function resetProject() {
  if (dirty.value && !confirm("当前修改尚未保存，创建新工程？")) return;
  checkpoint();
  project.value = emptyProject();
  resetResults();
  treeID.value = project.value.trees[0]!.id;
  selected.value = "root";
  fileName.value = "new-project.json";
  notice("已创建工程，请保存为新的 JSON 文件");
}
// 统一使用无损 JSON，防止黑板中的 64 位整数被截断。
async function request<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(
    path,
    body === undefined
      ? undefined
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: stringifyJSON(body),
        },
  );
  const data = parseJSON<{ error?: string; diagnostics?: Diagnostic[] }>(
    await res.text(),
  );
  if (!res.ok) {
    throw new RequestError(data.error ?? `请求失败 (${res.status})`, res.status, data.diagnostics);
  }
  return data as T;
}
// 串行化文件请求并将服务端错误显示到状态栏。
async function action(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } catch (e) {
    if (e instanceof RequestError && e.diagnostics) {
      diagnostics.value = e.diagnostics;
      showOutput("diagnostics");
    }
    notice(e instanceof Error ? e.message : String(e), true);
  } finally {
    busy.value = false;
  }
}
// 按需读取工作目录中的工程文件。
async function refreshFiles() {
  files.value = (await request<{ files: string[] }>("/api/projects")).files;
}
// 保存工程后刷新文件列表，清除未保存标记。
function save() {
  return action(async () => {
    const snapshot = stringifyJSON(project.value);
    const name = fileName.value;
    await request("/api/project", {
      name,
      project: parseJSON(snapshot),
    });
    if (snapshot === stringifyJSON(project.value) && name === fileName.value) dirty.value = false;
    await refreshFiles();
    notice(`已保存 ${name}${dirty.value ? "，后续修改尚未保存" : ""}`);
  });
}
// 加载工程并重置该文件的编辑历史。
function open(name: string) {
  return action(async () => {
    if (dirty.value && !confirm("当前修改尚未保存，打开其他工程？")) return;
    const loaded = await loadProject(() => request<Project>(`/api/project?name=${encodeURIComponent(name)}`));
    if (!loaded) return;
    validateProjectTypes(loaded);
    project.value = loaded;
    fileName.value = name;
    treeID.value = loaded.trees[0]!.id;
    selected.value = "";
    undoStack.value = [];
    redoStack.value = [];
    dirty.value = false;
    diagnostics.value = [];
    resetResults();
    notice(`已打开 ${name}`);
    await readGenerated(true);
  });
}
// 替换工程之前检查编辑代次，用户在读取期间的新修改优先保留。
async function loadProject(load: () => Promise<Project>) {
  const revision = editRevision;
  try {
    const loaded = await load();
    if (revision === editRevision) return loaded;
    notice("读取期间工程已修改，已取消替换，请重新打开或导入");
  } catch (e) {
    if (revision === editRevision) throw e;
  }
}
// 第一次查看输出时提供足够的阅读高度，后续尊重手动调整。
function showOutput(tab: string) {
  bottomTab.value = tab;
  if (outputHeight.value === undefined) {
    setOutputHeight(320);
    void nextTick(() => fitView({ padding: 0.18 }));
  }
}
// 文件列表作为打开命令使用，复位选择后允许再次重开同名工程。
function selectProject(event: Event) {
  const element = event.target as HTMLSelectElement;
  const name = element.value;
  element.value = "";
  if (name) void open(name);
}
// 调用 Go 共用校验器，并展示可定位到节点的诊断。
function validate() {
  return action(async () => {
    const result = await currentProjectRequest<{ diagnostics: Diagnostic[] }>("/api/validate");
    if (!result) return;
    diagnostics.value = result.data.diagnostics;
    showOutput("diagnostics");
    notice(
      diagnostics.value.length
        ? `发现 ${diagnostics.value.length} 个问题`
        : "校验通过，可以生成 Go 代码",
      diagnostics.value.length > 0,
    );
  });
}
// 在请求边界获取快照，语义修改后以 O(1) 修订检查丢弃旧响应和旧诊断。
async function currentProjectRequest<T>(path: string) {
  const snapshot = clone(project.value);
  const token = generationRequests.begin(snapshot);
  const revision = semanticRevision.value;
  try {
    const data = await request<T>(path, snapshot);
    if (!generationRequests.acceptsRevision(token)) {
      notice(path === "/api/generate" ? "旧快照已生成到目录，当前修改仍需重新生成" : "工程已变化，已忽略旧请求结果");
      return;
    }
    return { data, signature: token.signature, revision };
  } catch (e) {
    if (generationRequests.acceptsRevision(token)) throw e;
  }
}
// 预览与写盘复用同一后端生成器；旧结果持续保留至新结果成功返回。
function generate(write = true) {
  return action(async () => {
    const result = await currentProjectRequest<GeneratedCode>(write ? "/api/generate" : "/api/preview");
    if (!result) return;
    codeSnapshot.value = { ...result.data, signature: result.signature, revision: result.revision, origin: write ? "generated" : "preview" };
    diagnostics.value = [];
    showOutput("source");
    notice(`${write ? "已生成到目录" : "预览已更新"} · ${result.data.version.slice(0, 12)}`);
  });
}
// 从生成目录读取上次产物；打开工程自动读取时，尚无产物不视为错误。
async function readGenerated(silent = false) {
  try {
    const result = await currentProjectRequest<GeneratedCode>("/api/generated");
    if (!result) return;
    const matches = result.data.matchesCurrent === true;
    codeSnapshot.value = { ...result.data, signature: matches ? result.signature : "", revision: matches ? result.revision : -1, origin: "saved" };
    showOutput("source");
    notice(matches ? "已载入上次生成，内容与当前工程一致" : "已载入上次生成，内容与当前工程不同，请更新预览");
  } catch (e) {
    if (silent && e instanceof RequestError && e.status === 404) return;
    throw e;
  }
}
// 骨架只供预览、复制或下载，实际业务仍由独立手写文件实现。
function previewScaffold() {
  return action(async () => {
    const result = await currentProjectRequest<{ source: string }>("/api/scaffold");
    if (!result) return;
    scaffoldSnapshot.value = { source: result.data.source, revision: result.revision, signature: result.signature };
    showOutput("scaffold");
    notice("业务骨架已生成，请实现 TODO 后与生成文件一起编译");
  });
}
// 使用系统剪贴板复制可见文件，不把源码写入工程目录。
async function copySource() {
  try {
    await navigator.clipboard.writeText(bottomTab.value === "scaffold" ? scaffoldSnapshot.value?.source ?? "" : source.value);
    notice("已复制源码");
  } catch {
    notice("剪贴板不可用，请使用下载按钮", true);
  }
}
// 仅当映射与当前工程一致时，从源码行定位画布节点。
function locateSource(location: SourceLocation) {
  if (sourceStale.value) return;
  focusDiagnostic({ treeId: location.treeId, nodeId: location.nodeId, message: "" });
}
// 将当前工程或源码交给浏览器下载机制。
function download(name: string, content: string, mime = "application/json") {
  const url = URL.createObjectURL(new Blob([content], { type: mime }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
// 从本地文件导入草稿，交给 Go 规范化后再替换视图。
async function importProject(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  if (!file) return;
  await action(async () => {
    const loaded = await loadProject(async () => {
      const parsed: unknown = parseJSON(await file.text());
      validateProjectTypes(parsed);
      return request<Project>("/api/import", parsed);
    });
    if (!loaded) return;
    validateProjectTypes(loaded);
    checkpoint();
    project.value = loaded;
    resetResults();
    treeID.value = loaded.trees[0]!.id;
    selected.value = "";
    fileName.value = file.name;
    notice(`已导入 ${file.name}，尚未保存`);
  });
  input.value = "";
}
// 从节点属性或节点库打开同一目录管理入口。
function manageCatalog(mode: "create" | "import" | "manage", forNode = false) {
  const kind = forNode && (node.value?.type === "action" || node.value?.type === "condition") ? node.value.type : undefined;
  catalogDialog.value = { mode, kind };
}
// 目录验证成功后一次更新并建立撤销记录，可将新定义绑定当前节点。
function applyCatalog(catalog: Definition[], bindID?: string) {
  mutate(() => {
    project.value.catalog = catalog;
    if (bindID && node.value) {
      node.value.binding = bindID;
      node.value.params = {};
    }
  });
  catalogDialog.value = undefined;
  notice(`业务目录已更新，共 ${catalog.length} 个定义；请校验现有绑定`);
}
// 添加带稳定 ID 的强类型黑板字段。
function addField() {
  mutate(() => {
    project.value.blackboard.push({
      id: allocateID(occupiedIDs, "field"),
      name: `Field${project.value.blackboard.length + 1}`,
      type: "int64",
      default: 0,
    });
  });
}
// 解析黑板默认值，保留大整数原始精度。
function setDefault(field: Field, event: Event) {
  try {
    const value = parseInput(
      (event.target as HTMLInputElement).value,
      field.type,
    );
    mutate(() => {
      field.default = value;
    });
  } catch (e) {
    notice(String(e), true);
  }
}
// 读取某个业务参数的字段绑定或常量配置。
function paramValue(name: string): Value {
  return node.value?.params?.[name] ?? {};
}
// 在常量与黑板字段绑定之间切换。
function setParamMode(name: string, mode: string) {
  mutate(() => {
    node.value!.params ??= {};
    node.value!.params[name] =
      mode === "field"
        ? { field: project.value.blackboard[0]?.id ?? "" }
        : {
            value:
              definition.value?.params?.find((p) => p.name === name)?.default ??
              "",
          };
  });
}
// 按元数据声明解析参数，完整范围由 Go 再校验。
function setParam(name: string, type: ValueType, event: Event) {
  const text = (event.target as HTMLInputElement).value;
  try {
    const value =
      paramValue(name).field !== undefined
        ? { field: text }
        : { value: parseInput(text, type) };
    mutate(() => {
      node.value!.params ??= {};
      node.value!.params[name] = value;
    });
  } catch (e) {
    notice(String(e), true);
  }
}
// 定位诊断所属的树与画布节点。
function focusDiagnostic(d: Diagnostic) {
  if (d.treeId) treeID.value = d.treeId;
  selected.value = d.nodeId ?? "";
  const p = tree.value?.layout?.[selected.value];
  if (p) setCenter(p.x + 90, p.y + 35, { zoom: 1, duration: 250 });
}
// 处理保存、撤销和删除快捷键，不干扰文本原生撤销。
function keydown(e: KeyboardEvent) {
  if (catalogDialog.value) return;
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
    e.preventDefault();
    (e.target as HTMLElement)?.blur();
    void save();
    return;
  }
  if (
    (e.target as HTMLElement)?.closest(
      "input,textarea,select,[contenteditable]",
    )
  )
    return;
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "z") {
    e.preventDefault();
    e.shiftKey ? redo() : undo();
  } else if (e.key === "Delete") {
    e.preventDefault();
    deleteSelected();
  }
}
// 离开页面前提示尚未保存的修改。
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) e.preventDefault();
}
watch(treeID, () => {
  endPaletteDrag();
  selected.value = "";
  setTimeout(() => fitView({ padding: 0.18 }), 30);
}, { flush: "sync" });
onMounted(() => {
  outputResizeObserver = new ResizeObserver(updateOutputBounds);
  if (workbench.value) outputResizeObserver.observe(workbench.value);
  if (outputPanel.value) outputResizeObserver.observe(outputPanel.value);
  updateOutputBounds();
  void action(refreshFiles);
  window.addEventListener("keydown", keydown);
  window.addEventListener("beforeunload", beforeUnload);
});
onUnmounted(() => {
  outputResizeObserver?.disconnect();
  window.removeEventListener("keydown", keydown);
  window.removeEventListener("beforeunload", beforeUnload);
});

// 在支持 WebMCP 的浏览器内复用同一组编辑、校验动作；普通浏览器不受影响。
const toolLifecycle = new AbortController();
onMounted(() => {
  const context = (
    document as unknown as {
      modelContext?: {
        registerTool(
          tool: unknown,
          options: { signal: AbortSignal },
        ): void | Promise<void>;
      };
    }
  ).modelContext;
  if (!context) return;
  const register = (tool: unknown) => {
    try {
      void Promise.resolve(
        context.registerTool(tool, { signal: toolLifecycle.signal }),
      ).catch(() => {});
    } catch {
      /* 浏览器扩展接口不可用时保留普通编辑功能。 */
    }
  };
  register({
    name: "read_behavior_project",
    description: "读取当前可见行为树工程，不保存或修改文件。",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: () => clone(project.value),
  });
  register({
    name: "validate_behavior_project",
    description: "校验当前工程，并在问题列表展示诊断，不写入文件。",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async () => {
      await validate();
      return { diagnostics: clone(diagnostics.value) };
    },
  });
});
onUnmounted(() => toolLifecycle.abort());
</script>

<template>
  <div
    ref="workbench"
    class="workbench"
    :class="{ 'resizing-output': resizingOutput }"
    :style="outputHeight === undefined ? {} : { '--output-height': `${outputHeight}px` }"
  >
    <header class="topbar">
      <div class="brand">
        <span class="brandmark">⑂</span><strong>行为树工作台</strong
        ><span class="local-badge">LOCAL</span>
      </div>
      <div class="project-title">
        {{ project.name
        }}<span v-if="dirty" class="unsaved" title="尚未保存">●</span>
      </div>
      <div class="toolbar">
        <button @click="resetProject">新建</button
        ><button @click="importInput?.click()">导入</button>
        <button @click="download(fileName, stringifyJSON(project, 2))">
          导出 JSON
        </button>
        <button :disabled="busy" @click="save">保存 <kbd>Ctrl S</kbd></button>
        <button :disabled="busy" @click="validate">校验</button>
        <button :disabled="busy" @click="generate(false)">预览 Go</button>
        <button class="primary" :disabled="busy" @click="generate(true)">
          生成到目录 <span>↗</span>
        </button>
      </div>
    </header>

    <aside class="library">
      <div class="panel-heading">
        工程
        <button class="icon-button" title="新增行为树" @click="addTree">
          ＋
        </button>
      </div>
      <label class="field-label"
        >文件名<input
          v-model="fileName"
          aria-label="工程文件名"
          placeholder="project.json"
      /></label>
      <select
        aria-label="打开已有工程"
        value=""
        @change="selectProject"
      >
        <option value="" disabled>打开已保存的工程…</option>
        <option v-for="file in files" :key="file">{{ file }}</option>
      </select>
      <nav class="tree-list">
        <button
          v-for="item in project.trees"
          :key="item.id"
          :class="{ active: item.id === treeID }"
          @click="treeID = item.id"
        >
          <span>⑂</span>{{ item.name }}<small>{{ item.nodes.length }}</small>
        </button>
      </nav>
      <div class="sidebar-tabs">
        <button :class="{ active: tab === 'nodes' }" @click="tab = 'nodes'">
          节点库</button
        ><button :class="{ active: tab === 'board' }" @click="tab = 'board'">
          黑板
        </button>
      </div>
      <template v-if="tab === 'nodes'">
        <input
          v-model="search"
          class="node-search"
          placeholder="搜索节点…"
          aria-label="搜索节点"
        />
        <div class="node-library">
          <button
            v-for="{ type, info } in availableKinds"
            :key="type"
            class="palette-node"
            draggable="true"
            :title="`${info.help} · 按住拖入画布，或点击添加`"
            @dragstart="startPaletteDrag($event, type)"
            @dragend="endPaletteDrag"
            @click="addNode(type)"
          >
            <span :class="['kind-icon', info.color]">{{ info.icon }}</span
            ><span
              >{{ info.label }}<small>{{ type }}</small></span
            ><span class="add-sign">＋</span>
          </button>
        </div>
        <div class="panel-heading small-heading">
          Go 业务节点<button class="text-button" @click="manageCatalog('manage')">
            管理目录
          </button>
        </div>
        <p v-if="!project.catalog.length" class="muted empty-note">
          尚无业务定义。新建定义或导入 Go 导出的目录后，即可绑定动作和条件。
        </p>
        <div class="binding-actions">
          <button @click="manageCatalog('create')">新建定义</button>
          <button @click="manageCatalog('import')">导入目录</button>
        </div>
        <button
          v-for="d in project.catalog"
          :key="d.id"
          class="palette-node"
          draggable="true"
          title="按住拖入画布，或点击添加"
          @dragstart="startPaletteDrag($event, d.kind, d.id)"
          @dragend="endPaletteDrag"
          @click="addNode(d.kind, d.id)"
        >
          <span
            :class="['kind-icon', d.kind === 'action' ? 'blue' : 'amber']"
            >{{ d.kind === "action" ? "▶" : "◇" }}</span
          ><span
            >{{ d.name }}<small>{{ d.goName }}</small></span
          >
        </button>
      </template>
      <template v-else>
        <div class="panel-heading small-heading">
          AI 字段 <button class="text-button" @click="addField">＋ 字段</button>
        </div>
        <p class="muted empty-note">
          字段 ID 决定热更时的数据身份。业务对象保留在 Go 上下文中。
        </p>
        <section
          v-for="(field, i) in project.blackboard"
          :key="field.id"
          class="field-card"
        >
          <label
            >名称<input
              :value="field.name"
              @input="changeText($event, (v) => (field.name = v))"
          /></label>
          <label
            >类型<select
              :value="field.type"
              @change="
                changeText($event, (v) => {
                  field.type = parseValueType(v);
                  field.default = ['string', 'enum'].includes(v)
                    ? ''
                    : v === 'bool'
                      ? false
                      : 0;
                })
              "
            >
              <option
                v-for="type in valueTypes"
                :key="type"
              >
                {{ type }}
              </option>
            </select></label
          >
          <label v-if="field.type === 'enum'"
            >枚举值（逗号分隔）<input
              :value="field.enum?.join(', ') ?? ''"
              @input="
                changeText(
                  $event,
                  (v) =>
                    (field.enum = v
                      .split(',')
                      .map((s) => s.trim())
                      .filter(Boolean)),
                )
              "
          /></label>
          <label
            >默认值<input
              :value="String(field.default ?? '')"
              @input="setDefault(field, $event)"
          /></label>
          <small class="mono">{{ field.id }}</small
          ><button
            class="text-button danger"
            @click="mutate(() => project.blackboard.splice(i, 1))"
          >
            删除字段
          </button>
        </section>
      </template>
    </aside>

    <main class="canvas-area">
      <div class="canvas-toolbar">
        <div>
          <strong>{{ tree?.name }}</strong
          ><span class="muted">{{ tree?.nodes.length ?? 0 }} 个节点</span>
        </div>
        <div class="canvas-actions">
          <button
            :disabled="!undoStack.length"
            title="撤销 Ctrl+Z"
            @click="undo"
          >
            ↶</button
          ><button
            :disabled="!redoStack.length"
            title="重做 Ctrl+Shift+Z"
            @click="redo"
          >
            ↷</button
          ><button @click="layout">自动布局</button
          ><button @click="fitView({ padding: 0.18 })">适应画布</button
          ><button class="narrow-only" @click="inspectorOpen = true">
            属性
          </button>
        </div>
      </div>
      <VueFlow
        :class="{ 'palette-drag-over': canvasDragOver }"
        :nodes="graphNodes"
        :edges="graphEdges"
        :min-zoom="0.15"
        :max-zoom="2"
        :delete-key-code="null"
        fit-view-on-init
        @connect="link"
        @node-click="({ node: n }: NodeMouseEvent) => (selected = n.id)"
        @pane-click="selected = ''"
        @node-drag-stop="moveNode"
        @dragover="dragOverCanvas"
        @dragleave="leaveCanvas"
        @drop="dropPaletteNode"
      >
        <Background pattern-color="#34434f" :gap="22" :size="1" />
        <Controls position="bottom-left" :show-interactive="false" />
        <template #node-behavior="{ data }">
          <div
            :class="[
              'bt-node',
              data.kind.color,
              { invalid: data.invalid },
            ]"
          >
            <Handle type="target" :position="Position.Left" />
            <div class="bt-node-top">
              <span>{{ data.kind.icon }}</span
              ><small>{{
                data.kind.label
              }}</small
              ><b v-if="data.root">ROOT</b>
            </div>
            <strong>{{
              data.node.name || data.kind.label
            }}</strong>
            <div class="node-detail">
              <span v-if="['wait', 'timeout'].includes(data.node.type)"
                >{{ data.node.durationMs ?? 0 }} ms</span
              ><span v-else-if="['repeat', 'retry'].includes(data.node.type)"
                >{{ data.node.count ?? 0 }} 次</span
              ><span v-else-if="data.node.binding">{{ data.node.binding }}</span>
              <span v-else-if="['action', 'condition'].includes(data.node.type)" class="unbound-node">未绑定业务定义</span>
              <span v-else
                >{{ data.node.children?.length ?? 0 }} 个子节点</span
              >
            </div>
            <Handle
              v-if="
                !['wait', 'condition', 'action', 'subtree'].includes(
                  data.node.type,
                )
              "
              type="source"
              :position="Position.Right"
            />
          </div>
        </template>
      </VueFlow>
      <div class="canvas-hint">从节点库拖入画布添加 · 拖动节点端点连接 · 子节点顺序在右侧调整</div>
    </main>

    <aside :class="['inspector', { opened: inspectorOpen }]">
      <button
        class="narrow-only text-button inspector-close"
        @click="inspectorOpen = false"
      >
        关闭属性 ×
      </button>
      <div class="panel-heading">
        {{ node ? "节点属性" : "行为树设置"
        }}<span class="muted">{{
          node ? kinds[node.type]?.label : "TREE"
        }}</span>
      </div>
      <template v-if="node">
        <p class="node-description">{{ kinds[node.type]?.help }}</p>
        <p v-if="node.type === 'priority'" class="muted empty-note">
          各优先分支为 Sequence，首节点为
          Condition；最后一个分支可以作为无条件回退。
        </p>
        <label class="field-label"
          >名称<input
            :value="node.name"
            @input="changeText($event, (v) => (node!.name = v))"
        /></label>
        <label class="field-label"
          >节点 ID（自动生成）<input :value="node.id" readonly class="mono" :title="node.id"
        /></label>
        <p class="muted empty-note">ID 是固定身份标识；修改上方名称即可重命名显示，不改变连线和绑定。</p>
        <label
          v-if="['repeat', 'retry'].includes(node.type)"
          class="field-label"
          >次数<input
            type="number"
            min="1"
            :value="node.count"
            @input="changeText($event, (v) => (node!.count = Number(v)))"
        /></label>
        <label
          v-if="['wait', 'timeout'].includes(node.type)"
          class="field-label"
          >时长（毫秒）<input
            type="number"
            min="0"
            :value="node.durationMs"
            @input="changeText($event, (v) => (node!.durationMs = Number(v)))"
        /></label>
        <label v-if="node.type === 'subtree'" class="field-label"
          >引用行为树<select
            :value="node.tree ?? ''"
            @change="changeText($event, (v) => (node!.tree = v))"
          >
            <option value="">请选择…</option>
            <option
              v-for="t in project.trees.filter((t) => t.id !== treeID)"
              :key="t.id"
              :value="t.id"
            >
              {{ t.name }}
            </option>
          </select></label
        >
        <template v-if="['action', 'condition'].includes(node.type)">
          <label class="field-label"
            >Go 节点绑定<select
              :value="node.binding ?? ''"
              @change="
                changeText($event, (v) => {
                  node!.binding = v;
                  node!.params = {};
                })
              "
            >
              <option value="">请选择…</option>
              <option
                v-for="d in bindingOptions"
                :key="d.id"
                :value="d.id"
              >
                {{ d.name }} · {{ d.goName }}
              </option>
            </select></label
          >
          <p v-if="!bindingOptions.length" class="binding-note">
            尚无{{ node.type === 'action' ? '动作' : '条件' }}定义。请新建业务定义或导入目录，再选择绑定。
          </p>
          <p v-else-if="node.binding && (!definition || definition.kind !== node.type)" class="binding-note">
            当前绑定 {{ node.binding }} 不存在或种类不匹配，请重新选择。
          </p>
          <div class="binding-actions">
            <button @click="manageCatalog('create', true)">新建业务定义</button>
            <button @click="manageCatalog('import', true)">导入目录</button>
            <button @click="manageCatalog('manage', true)">管理定义</button>
            <button :disabled="busy || !project.catalog.length" @click="previewScaffold">预览业务骨架</button>
          </div>
          <p class="muted empty-note">定义决定生成代码调用哪个函数，业务实现请写入同包的独立 Go 文件。</p>
          <div
            v-for="p in definition?.params ?? []"
            :key="p.name"
            class="param-editor"
          >
            <label
              >{{ p.name }} <small>{{ p.type }}</small></label
            ><select
              :value="
                paramValue(p.name).field !== undefined ? 'field' : 'value'
              "
              @change="
                setParamMode(p.name, ($event.target as HTMLSelectElement).value)
              "
            >
              <option value="value">常量</option>
              <option value="field">黑板字段</option></select
            ><select
              v-if="paramValue(p.name).field !== undefined"
              :value="paramValue(p.name).field"
              @change="setParam(p.name, p.type, $event)"
            >
              <option value="">请选择…</option>
              <option
                v-for="field in project.blackboard.filter(
                  (f) => f.type === p.type,
                )"
                :key="field.id"
                :value="field.id"
              >
                {{ field.name }}
              </option></select
            ><input
              v-else
              :value="String(paramValue(p.name).value ?? p.default ?? '')"
              @input="setParam(p.name, p.type, $event)"
            />
          </div>
          <p v-if="definition?.events?.length" class="muted empty-note">
            依赖事件：{{ definition.events.join("、") }}
          </p>
        </template>
        <section class="children-order">
          <div class="panel-heading small-heading">
            执行顺序 <span class="muted">{{ node.children?.length ?? 0 }}</span>
          </div>
          <div
            v-for="(child, i) in node.children ?? []"
            :key="child"
            class="child-row"
          >
            <b>{{ i + 1 }}</b
            ><button class="child-name" @click="selected = child">
              {{
              nodeIndex.get(child)?.name ?? child
              }}</button
            ><button :disabled="i === 0" title="上移" @click="reorder(i, -1)">
              ↑</button
            ><button
              :disabled="i === node.children!.length - 1"
              title="下移"
              @click="reorder(i, 1)"
            >
              ↓</button
            ><button title="断开连接" @click="disconnect(i)">×</button>
          </div>
        </section>
        <div class="node-actions">
          <button @click="mutate(() => (tree.root = selected))">
            设为根节点</button
          ><button @click="duplicate">复制节点</button
          ><button class="danger" @click="deleteSelected">删除节点</button>
        </div>
      </template>
      <template v-else>
        <p class="muted empty-note">选择节点编辑属性，或从左侧添加新节点。</p>
        <label class="field-label"
          >工程名称<input
            :value="project.name"
            @input="changeText($event, (v) => (project.name = v))"
        /></label>
        <label class="field-label"
          >行为树名称<input
            :value="tree.name"
            @input="changeText($event, (v) => (tree.name = v))"
        /></label>
        <label class="field-label"
          >行为树 ID<input :value="tree.id" readonly class="mono"
        /></label>
        <div class="panel-heading small-heading">Go 生成设置</div>
        <label class="field-label"
          >包名<input
            :value="project.generation.package"
            @input="
              changeText($event, (v) => (project.generation.package = v))
            "
        /></label>
        <label class="field-label"
          >业务上下文导入路径<input
            :value="project.generation.contextImport"
            placeholder="留空使用本地类型"
            @input="
              changeText($event, (v) => (project.generation.contextImport = v))
            "
        /></label>
        <label class="field-label"
          >上下文类型<input
            :value="project.generation.contextType"
            placeholder="any 或 *Context"
            @input="
              changeText($event, (v) => (project.generation.contextType = v))
            "
        /></label>
        <button class="danger tree-delete" @click="deleteTree">
          删除当前行为树
        </button>
      </template>
    </aside>

    <section id="output-panel" ref="outputPanel" class="output-panel">
      <div
        class="output-resizer"
        role="separator"
        tabindex="0"
        aria-label="调整输出面板高度"
        aria-orientation="horizontal"
        aria-controls="output-panel"
        :aria-valuemin="minOutputHeight"
        :aria-valuemax="maxOutputHeight"
        :aria-valuenow="renderedOutputHeight"
        title="上下拖动调整高度，双击恢复默认高度"
        @pointerdown="startOutputResize"
        @pointermove="resizeOutput"
        @pointerup="stopOutputResize"
        @pointercancel="stopOutputResize"
        @lostpointercapture="stopOutputResize"
        @keydown="resizeOutputWithKeyboard"
        @dblclick="outputHeight = undefined"
      ></div>
      <div class="output-tabs">
        <button
          :class="{ active: bottomTab === 'diagnostics' }"
          @click="bottomTab = 'diagnostics'"
        >
          校验结果
          <span v-if="diagnostics.length">{{
            diagnostics.length
          }}</span></button
        ><button
          :class="{ active: bottomTab === 'source' }"
          @click="bottomTab = 'source'"
        >
          生成代码</button
        ><button :class="{ active: bottomTab === 'scaffold' }" @click="bottomTab = 'scaffold'">业务骨架</button>
        <span class="output-tab-spacer"></span>
        <button :disabled="busy" @click="() => action(() => readGenerated())">查看上次生成</button>
      </div>
      <div v-if="bottomTab !== 'diagnostics'" class="code-toolbar">
        <button :disabled="busy" @click="bottomTab === 'scaffold' ? previewScaffold() : generate(false)">
          {{ bottomTab === 'scaffold' ? '更新业务骨架' : '更新预览' }}
        </button>
        <template v-if="bottomTab === 'source' ? !!source : !!scaffoldSnapshot">
          <button @click="copySource">复制</button>
          <button @click="bottomTab === 'scaffold' ? download('actions.go', scaffoldSnapshot!.source, 'text/plain') : download('tree_gen.go', source, 'text/plain')">下载 {{ bottomTab === 'scaffold' ? 'actions.go' : 'Go' }}</button>
          <span class="code-state" :class="{ stale: bottomTab === 'source' ? sourceStale : scaffoldStale }">
            {{ (bottomTab === 'source' ? sourceStale : scaffoldStale) ? '已过期 · 请更新' : bottomTab === 'scaffold' ? '待实现 TODO' : codeSnapshot?.origin === 'preview' ? '当前预览' : '已生成文件' }}
          </span>
        </template>
      </div>
      <div v-if="bottomTab === 'source' && source" class="code-metadata">
        <span class="mono">版本 {{ codeSnapshot!.version.slice(0, 12) }}</span>
        <span class="output-path" :title="outputPath">{{ outputPath }}</span>
      </div>
      <p v-if="bottomTab === 'source' && sourceStale" class="code-warning">当前工程已变化，以下保留旧源码；更新预览后恢复节点联动。</p>
      <p v-if="bottomTab === 'scaffold' && scaffoldSnapshot" class="code-warning">{{ scaffoldStale ? '工程已变化，请更新骨架。' : '' }}骨架中的 TODO 需要手动实现；下载文件不会覆盖已有业务实现。</p>
      <div v-if="bottomTab === 'diagnostics'" class="output-content">
        <template v-if="bottomTab === 'diagnostics'"
          ><button
            v-for="(d, i) in diagnostics"
            :key="i"
            class="diagnostic"
            @click="focusDiagnostic(d)"
          >
            <span>!</span><code>{{ d.nodeId ?? d.field ?? d.treeId }}</code
            >{{ d.message }}
          </button>
          <p v-if="!diagnostics.length" class="muted">
            点击「校验」检查结构与类型。「预览 Go」查看源码，「生成到目录」写入生成文件。
          </p></template
        >
      </div>
      <SourceViewer v-else-if="bottomTab === 'source' && source" :source="source" :source-map="codeSnapshot!.sourceMap"
        :tree-id="treeID" :node-id="selected" :stale="sourceStale" @locate="locateSource" />
      <SourceViewer v-else-if="bottomTab === 'scaffold' && scaffoldSnapshot" :source="scaffoldSnapshot.source" />
      <div v-else class="output-empty">
        <strong>{{ bottomTab === 'scaffold' ? '从业务目录创建 Go 函数骨架' : '先预览，再生成到目录' }}</strong>
        <p>{{ bottomTab === 'scaffold' ? '根据动作、条件和上下文生成函数签名及 TODO；可复制或下载为独立手写文件。' : '预览使用当前工程生成完整 Go 源码，不写文件。选中节点可定位对应函数，点击代码行号可返回画布。' }}</p>
        <button :disabled="busy" @click="bottomTab === 'scaffold' ? previewScaffold() : generate(false)">{{ bottomTab === 'scaffold' ? '生成业务骨架' : '预览当前工程' }}</button>
      </div>
    </section>
    <footer :class="['statusbar', { error }]">
      <span><i :class="{ busy }"></i>{{ message }}</span
      ><span
        >Schema {{ project.schemaVersion }}<b>·</b
        >{{ project.trees.length }} 棵树<b>·</b
        >{{ project.blackboard.length }} 个字段</span
      >
    </footer>
    <input
      ref="importInput"
      type="file"
      accept="application/json,.json"
      hidden
      @change="importProject"
    />
    <CatalogManager v-if="catalogDialog" :catalog="project.catalog" :initial-mode="catalogDialog.mode" :initial-kind="catalogDialog.kind"
      @apply="applyCatalog" @close="catalogDialog = undefined" />
  </div>
</template>
