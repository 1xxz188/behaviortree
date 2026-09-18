<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, shallowRef, watch } from "vue";
import CatalogManager from "./CatalogManager.vue";
import CatalogBrowser from "./CatalogBrowser.vue";
import NodeHelpDialog from "./NodeHelpDialog.vue";
import CatalogCopyDialog from "./CatalogCopyDialog.vue";
import { CatalogOrganizationIndex } from "./catalogOrganization";
import { validatedCatalogJSON } from "./catalogTransfer";
import { synchronizeCatalog } from "./catalogSync";
import SourceViewer from "./SourceViewer.vue";
import ProjectDialog from "./ProjectDialog.vue";
import ImportErrorDialog from "./ImportErrorDialog.vue";
import ScaffoldOverwriteDialog from "./ScaffoldOverwriteDialog.vue";
import OperationNotice from "./OperationNotice.vue";
import TreeContextMenu from "./TreeContextMenu.vue";
import CatalogContextMenu from "./CatalogContextMenu.vue";
import { startupProject, rememberProject, selectNativeDirectory } from "./workspace";
import type { WorkspaceFiles, RecentStorage, ProjectDialogResult } from "./workspace";
import { ProjectSaveState } from "./saveState";
import { createGeneratedSourceIndex, GenerationRequests, selectGeneratedFile, semanticSignature } from "./generation";
import type { GeneratedFile, GeneratedSourceIndex, SourceLocation } from "./generation";
import { TreeIdentityIndex, captureSnapshot, restoreSnapshot } from "./treeIdentity";
import type { EditorSnapshot } from "./treeIdentity";
import { NodeIdentityIndex } from "./nodeIdentity";
import { normalizeCodeNames } from "./codeNames";
import { VueFlow, Handle, Position, useVueFlow } from "@vue-flow/core";
import type { Connection, NodeDragEvent, NodeMouseEvent } from "@vue-flow/core";
import { Background } from "@vue-flow/background";
import { Controls } from "@vue-flow/controls";
import {
  autoLayout,
  clone,
  connect,
  emptyProject,
  blankProject,
  kinds,
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
import { PARAM_TYPE_META, parameterTypeTooltip } from "./parameterTypeMeta";
import {
  nodeTypes, valueTypes, parseValueType, validateProjectTypes,
} from "./enums";
import type { DefinitionKind, NodeType, ValueType } from "./enums";

// 生成接口统一返回文件集合、版本及节点位置；读取磁盘时附带工程匹配结果。
interface GeneratedCode {
  files: GeneratedFile[]; // 每棵定义树及公共 glue 的独立源码。
  sourceMap: SourceLocation[]; // 各展开实例的位置。
  version: string; // 生成内容的版本摘要。
  directory?: string; // 实际落盘目录，预览无路径。
  matchesCurrent?: boolean; // 已保存源码是否对应当前工程。
}
// 结果保留生成时的修订号，编辑后只标记过期，不丢弃源码。
interface CodeSnapshot extends GeneratedCode {
  index: GeneratedSourceIndex; // 接收快照时建立，切换文件无需重新扫描。
  revision: number; // 对应当前编辑会话的语义修订。
  signature: string; // 撤销恢复时判断内容是否一致。
  origin: "preview" | "generated" | "saved"; // 区分预览和磁盘产物。
}
// 请求错误保留节点诊断，由仍有效的调用展示。
class RequestError extends Error {
  // 冲突响应保留实际路径与文件摘要，二次确认只授权覆盖用户看到的版本。
  constructor(message: string, readonly status: number, readonly diagnostics?: Diagnostic[], readonly path?: string, readonly existingHash?: string) {
    super(message);
  }
}

const project = ref<Project>(blankProject());
const catalogIndex = shallowRef(new CatalogOrganizationIndex(project.value)); // 分类缓存不扫描行为树。
const catalogRevision = ref(0); // 组织索引原地修改后的界面修订。
const catalogFolder = ref(""); // 新建和导入定义的默认目标目录。
const catalogBrowser = ref<InstanceType<typeof CatalogBrowser>>(); // 菜单到组织表单的入口。
const catalogCopy = ref<string>(); // 剪贴板不可用时展示可手动复制的文本。
const catalogTransferBusy = ref(false); // 防止重复发起复制或下载校验。
// 工程替换和撤销只重建一次组织索引，不深度监听每次属性编辑。
watch(project, () => {
  catalogIndex.value = new CatalogOrganizationIndex(project.value);
  if (!catalogIndex.value.folders.has(catalogFolder.value)) catalogFolder.value = "";
  catalogCopy.value = undefined;
}, { flush: "sync" });
// 路径末级仅供即时说明，合法性统一交由 Go 解析器校验。
const generationPackageName = computed(() => project.value.generation.packagePath.split("/").at(-1) ?? "");
const treeID = ref(project.value.trees[0]!.id);
const treeIDDraft = ref(treeID.value); // ID 输入草稿，应用前不影响工程。
let renamingTree = false; // 同一棵树改号时保留选择与视口。
const selected = ref(project.value.trees[0]!.root);
const nodeIDDraft = ref(selected.value); // 节点身份草稿，显式应用前不修改工程。
const codeNameDraft = ref(""); // 代码名草稿仅在显式应用后写入工程。
const codeNameError = ref(""); // 代码名格式或树内占用校验结果。
const inspectorOpen = ref(false);
const nodeHelp = ref<NodeType>(); // 库节点的说明窗口独立于画布选择和工程历史。
const search = ref("");
const fileName = ref(""); // 仅表示当前工作目录内已成功打开或保存的文件。
const suggestedName = ref("project.json"); // 新建及导入只提供首次保存建议。
const workspace = ref(""); // 服务端返回的实际绝对工作目录。
const projectReady = ref(false); // 完成打开或显式新建后才展示编辑区。
const initializing = ref(true); // 首屏等待目录信息，不提前显示示例。
const openingName = ref(""); // 加载提示中的目标文件。
const failedOpen = ref(""); // 保留失败目标供用户重试。
const workspaceError = ref(""); // 首屏错误保留具体原因。
const projectDialog = shallowRef<{
  kind: "save" | "switch"; // 保存位置或未保存修改的网页确认框。
  reload: boolean; // 同名重载时明确说明读取磁盘和覆盖当前内容。
  resolve: (value?: ProjectDialogResult) => void; // 用户关闭对话框后继续原操作。
}>();
const files = ref<string[]>([]);
const allFiles = ref<string[]>([]); // 保存覆盖确认使用完整候选，不受工程有效性筛选影响。
const importFailure = shallowRef<{ name: string; message: string }>(); // 导入错误的文件名及具体原因，由模态框展示。
const message = ref("本地工程 · 修改后请保存");
const error = ref(false);
const busy = ref(false);
const workspaceChanging = ref(false); // 切换请求期间冻结编辑，避免丢弃响应途中产生的修改。
const saveState = reactive(new ProjectSaveState()); // 保存基准与编辑修订各自维护。
const dirty = computed(() => saveState.dirty);
const diagnostics = ref<Diagnostic[]>([]);
const validationResult = shallowRef<{
  revision: number; // 校验对应的语义修订，工程修改后不再显示旧的通过结论。
  count: number; // 本次完成的校验问题数，零表示明确通过。
}>();
const codeSnapshot = shallowRef<CodeSnapshot>();
const scaffoldSnapshot = shallowRef<{ source: string; revision: number; signature: string }>();
const scaffoldOverwrite = shallowRef<{
  path: string; // 后端确认的实际目标文件路径。
  resolve: (confirmed: boolean) => void; // 关闭对话框后恢复保存流程。
}>();
const semanticRevision = ref(0);
let editRevision = 0; // 包含布局编辑，防止加载请求覆盖期间的新草稿。
const generationRequests = new GenerationRequests();
const sourceFileName = ref(""); // 当前显示文件；手动切换不改变画布选择。
const sourceFile = computed(() => codeSnapshot.value?.index.filesByName.get(sourceFileName.value));
const source = computed(() => sourceFile.value?.source ?? "");
const sourceIndex = computed(() => codeSnapshot.value?.index.sourceIndexes.get(sourceFileName.value));
const sourceStale = computed(() => !!codeSnapshot.value && codeSnapshot.value.revision !== semanticRevision.value);
const scaffoldStale = computed(() => !!scaffoldSnapshot.value && scaffoldSnapshot.value.revision !== semanticRevision.value);
const outputPath = computed(() => codeSnapshot.value?.directory ?? "仅预览 · 尚未写入目录");
// 选中树或节点后自动切到定义树文件，旧映射在工程修改后禁止联动。
function followSourceSelection() {
  if (!codeSnapshot.value || sourceStale.value) return;
  sourceFileName.value = selectGeneratedFile(codeSnapshot.value.index, sourceFileName.value, treeID.value)?.name ?? "";
}
watch([treeID, selected, sourceStale], followSourceSelection);
// 再次点击已选节点时仍恢复其源码文件，支持用户先手动查看公共 glue 的场景。
function selectCanvasNode({ node: item }: NodeMouseEvent) {
  selected.value = item.id;
  followSourceSelection();
}
const catalogDialog = ref<{
  mode: "create" | "edit" | "import" | "manage"; // 区分新建、编辑和目录操作。
  kind?: DefinitionKind; // 新建时可绑定当前节点的种类。
  definition?: Definition; // 编辑入口捕获的业务定义。
  folderId?: string; // 打开弹窗时捕获导入或新建的目标目录。
}>();
const catalogMenu = shallowRef<{
  definition: Definition; // 捕获右击对象，确认时校验身份以排除过期菜单。
  x: number; // 菜单视口横坐标。
  y: number; // 菜单视口纵坐标。
}>();
// 保存右击的实际树对象，不依赖当前画布选择，避免对另一棵树误操作。
const treeMenu = shallowRef<{
  tree: Tree; // 菜单与确认框操作的目标。
  x: number; // 菜单在视口中的横坐标。
  y: number; // 菜单在视口中的纵坐标。
  initialMode?: "menu" | "delete"; // 右侧删除按钮可直接打开同一确认框。
}>();
// 加载工程时一次建立黑板字段占用集合；树和节点使用各自的递增索引。
let occupiedIDs = new Set<string>();
const treeIdentity = shallowRef(new TreeIdentityIndex(project.value)); // 索引持有响应式树和节点。
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
const undoStack = ref<EditorSnapshot[]>([]);
const redoStack = ref<EditorSnapshot[]>([]);
const importInput = ref<HTMLInputElement>();
const { fitView, setCenter, screenToFlowCoordinate } = useVueFlow();
// 拖拽只保存节点模板，成功落入画布后才写入工程和撤销历史。
const paletteDrag = ref<{ type: NodeType; binding?: string }>();
const canvasDragOver = ref(false);

const tree = computed(
  () =>
    treeIdentity.value.byID.get(treeID.value) ??
    project.value.trees[0]!,
);
const nodeIdentity = shallowRef(reactive(new NodeIdentityIndex(tree.value))); // 当前树的增量身份及入边索引。
const nodeIndex = computed(() => nodeIdentity.value.byID);
const node = computed(() => nodeIndex.value.get(selected.value));
// 草稿逐次输入只查询当前树索引，冲突即时提示且禁止提交。
const nodeIDError = computed(() => node.value
  ? nodeIdentity.value.validateRename(selected.value, nodeIDDraft.value) ?? "" : "");
// 树 ID 草稿同样实时检查工程内占用，不允许冲突提交。
const treeIDError = computed(() => treeIdentity.value.validateRename(treeID.value, treeIDDraft.value) ?? "");
// 身份草稿与工程保存状态分开维护；未提交的输入不能被保存或生成静默忽略。
const identityDraftPending = computed(() => treeIDDraft.value !== treeID.value
  || (!!node.value && (nodeIDDraft.value !== node.value.id || codeNameDraft.value !== (node.value.codeName ?? ""))));
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
  undoStack.value.push(captureSnapshot(project.value, treeID.value, selected.value));
  if (undoStack.value.length > 100) undoStack.value.shift();
  redoStack.value = [];
  saveState.changed();
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
}
// 工程切换隔离源码、诊断和请求，避免显示另一工程的结果。
function resetResults() {
  catalogMenu.value = undefined;
  catalogDialog.value = undefined;
  treeMenu.value = undefined;
  editRevision++;
  invalidateCode();
  codeSnapshot.value = undefined;
  scaffoldSnapshot.value = undefined;
  treeIdentity.value = new TreeIdentityIndex(project.value);
  rebuildIDs();
  cancelTreeID();
  cancelNodeID();
}
// 恢复工程和选择快照；布局与展示名撤销不使源码过期。
function restore(snapshot: EditorSnapshot) {
  catalogMenu.value = undefined;
  catalogDialog.value = undefined;
  treeMenu.value = undefined;
  editRevision++;
  const { project: restored, index } = restoreSnapshot(snapshot, reactive);
  const signature = semanticSignature(restored);
  if (signature !== semanticSignature(project.value)) invalidateCode();
  project.value = restored;
  treeIdentity.value = index;
  rebuildIDs();
  treeID.value = treeIdentity.value.byID.has(snapshot.treeID)
    ? snapshot.treeID : project.value.trees[0]?.id ?? "";
  selected.value = nodeIndex.value.has(snapshot.selected) ? snapshot.selected : "";
  cancelTreeID();
  cancelNodeID();
  saveState.restore(snapshot.project);
  if (codeSnapshot.value?.signature === signature)
    codeSnapshot.value = { ...codeSnapshot.value, revision: semanticRevision.value };
  if (scaffoldSnapshot.value?.signature === signature)
    scaffoldSnapshot.value = { ...scaffoldSnapshot.value, revision: semanticRevision.value };
}
// 撤销最近一次工程修改。
function undo() {
  const state = undoStack.value.pop();
  if (state) {
    redoStack.value.push(captureSnapshot(project.value, treeID.value, selected.value));
    restore(state);
  }
}
// 恢复被撤销的工程修改。
function redo() {
  const state = redoStack.value.pop();
  if (state) {
    undoStack.value.push(captureSnapshot(project.value, treeID.value, selected.value));
    restore(state);
  }
}
// 文本框原生撤销不经过工程历史；仅在原生历史操作后核对保存基准。
function nativeHistory(event: Event) {
  if (!projectReady.value || catalogDialog.value || projectDialog.value || importFailure.value || treeMenu.value || catalogMenu.value) return;
  if (event instanceof InputEvent && (event.inputType === "historyUndo" || event.inputType === "historyRedo")) {
    saveState.restore(stringifyJSON(project.value));
  }
}
// 提交文本属性变更，保留可撤销历史。
function changeText(event: Event, fn: (text: string) => void) {
  mutate(() => fn((event.target as HTMLInputElement).value));
}
// 配置层统一使用正斜杠；保留重复分隔符和 .、..，让后端严格拒绝非法路径。
function changeGenerationPackagePath(event: Event) {
  changeText(event, value => { project.value.generation.packagePath = value.trim().replace(/\\/g, "/"); });
}
// 清空可选节点名恢复缺省字段，使原生撤销不会凭空留下 name: ""。
function changeNodeName(event: Event) {
  const name = (event.target as HTMLInputElement).value;
  if (name === (node.value?.name ?? "")) return;
  mutate(() => {
    if (name) node.value!.name = name;
    else delete node.value!.name;
  });
}
// 展示名只进入保存和历史，不淘汰生成结果或在途请求。
function changeTreeName(event: Event) {
  const name = (event.target as HTMLInputElement).value;
  if (name !== tree.value.name) mutate(() => { tree.value.name = name; }, false);
}
// 取消尚未提交的 ID 输入，不产生工程修改。
function cancelTreeID() {
  treeIDDraft.value = treeID.value;
}
// 统一阻止对旧身份快照的保存、导出与生成，不隐式提交可能冲突的 ID。
function ensureIdentityDraftsApplied(): boolean {
  if (!identityDraftPending.value) return true;
  notice("存在尚未应用的 ID 或代码名修改，请先点击属性面板的“应用”或“取消”，再保存或生成", true);
  return false;
}
// 放弃尚未应用的节点身份草稿，不产生历史记录。
function cancelNodeID() {
  nodeIDDraft.value = selected.value;
  cancelCodeName();
}
// 放弃代码名草稿，选中节点变化或历史恢复时同时刷新输入。
function cancelCodeName() {
  codeNameDraft.value = node.value?.codeName ?? "";
  codeNameError.value = "";
}
// 代码名在单次历史边界内提交，只更新生成符号并使已有源码过期。
function applyCodeName() {
  if (!node.value) return;
  const failure = nodeIdentity.value.codeNames.validateRename(node.value, codeNameDraft.value);
  if (failure) {
    codeNameError.value = failure;
    return;
  }
  if (codeNameDraft.value === node.value.codeName) return cancelCodeName();
  mutate(() => nodeIdentity.value.codeNames.rename(node.value!, codeNameDraft.value));
  cancelCodeName();
  notice("代码名已更新，请保存并重新生成");
}
// 根、入边、布局和选择在同一次历史操作中更新，并使源码与诊断过期。
function applyNodeID() {
  const previous = selected.value;
  const next = nodeIDDraft.value;
  const failure = nodeIdentity.value.validateRename(previous, next);
  if (failure) return;
  if (previous === next) return cancelNodeID();
  mutate(() => {
    nodeIdentity.value.rename(previous, next);
    selected.value = next;
  });
  cancelNodeID();
  notice("节点 ID 已更新，已同步根、连线和布局，请保存并重新生成");
}
// 先校验再在一次历史边界内更新 ID、反向引用和当前选择。
function applyTreeID() {
  const previous = treeID.value;
  const next = treeIDDraft.value;
  const failure = treeIdentity.value.validateRename(previous, next);
  if (failure) return;
  if (previous === next) return cancelTreeID();
  let references = 0;
  mutate(() => {
    references = treeIdentity.value.rename(previous, next);
    // 同一棵树改号保留当前节点选择，避免触发切树时的清空逻辑。
    renamingTree = true;
    try { treeID.value = next; } finally { renamingTree = false; }
  });
  cancelTreeID();
  notice(`行为树 ID 已更新，已同步 ${references} 处引用，请保存并重新生成`);
}
// 子树目标变更同步维护反向索引，不扫描其他节点。
function changeTreeReference(event: Event) {
  const target = (event.target as HTMLSelectElement).value;
  if (!node.value || (node.value.tree ?? "") === target) return;
  mutate(() => treeIdentity.value.setReference(node.value!, target));
}
// 创建稳定节点 ID，并放置到当前树画布。
function addNode(type: NodeType, binding?: string, position?: NodePosition) {
  mutate(() => {
    const id = nodeIdentity.value.allocateID(),
      item: BTNode = { id, type, name: kinds[type]?.label ?? type };
    if (binding) {
      item.binding = binding;
      const business = definitionIndex.value.get(binding);
      item.name = business?.name;
      // 业务目录创建时一次分配可读名称，之后改绑定或业务函数名不改节点代码名。
      if (business) item.codeName = nodeIdentity.value.codeNames.allocateBusiness(business.goName);
    }
    if (["repeat", "retry"].includes(type)) item.count = 3;
    if (["wait", "timeout"].includes(type)) item.durationMs = 1000;
    tree.value.nodes.push(item);
    treeIdentity.value.addNode(tree.value.nodes[tree.value.nodes.length - 1]!);
    nodeIdentity.value.addNode(tree.value.nodes[tree.value.nodes.length - 1]!);
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
    // 只写实际变化的父连接，保留反向引用索引持有的节点对象。
    nodeIdentity.value.setChildren(nodeIndex.value.get(connection.source!)!, copy.nodes.find((n) => n.id === connection.source)!.children);
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
      treeIdentity.value.removeNode(node.value!);
      nodeIdentity.value.removeNode(node.value!);
      selected.value = "";
    });
}
// 复制单个节点属性，分配新 ID 并断开子节点。
function duplicate() {
  if (!node.value) return;
  mutate(() => {
    const n = clone(node.value!);
    n.id = nodeIdentity.value.allocateID();
    n.codeName = nodeIdentity.value.codeNames.allocateCopy(n.codeName!);
    n.name = `${n.name ?? kinds[n.type]?.label} 副本`;
    n.children = [];
    tree.value.nodes.push(n);
    treeIdentity.value.addNode(tree.value.nodes[tree.value.nodes.length - 1]!);
    nodeIdentity.value.addNode(tree.value.nodes[tree.value.nodes.length - 1]!);
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
    const list = [...node.value!.children!],
      other = index + offset;
    [list[index], list[other]] = [list[other]!, list[index]!];
    nodeIdentity.value.setChildren(node.value!, list);
  });
}
// 解除一条父子连接，保留被断开的节点。
function disconnect(index: number) {
  mutate(() => nodeIdentity.value.setChildren(node.value!, node.value!.children!.filter((_, position) => position !== index)));
}
// 重新计算画布布局，保持行为语义不变。
function layout() {
  mutate(() => autoLayout(tree.value), false);
  setTimeout(() => fitView({ padding: 0.18 }), 30);
}
// 创建可独立生成或作为子树引用的入口。
function addTree() {
  let id: string;
  try { id = treeIdentity.value.allocateID(); }
  catch (error) { return notice(String(error), true); }
  mutate(() => {
    project.value.trees.push({
      id,
      name: "新行为树",
      root: "",
      nodes: [],
      layout: {},
    });
    treeIdentity.value.addTree(project.value.trees[project.value.trees.length - 1]!);
    treeID.value = id;
    selected.value = "";
  });
}
// 鼠标右击或键盘菜单键打开目标树菜单，不更换画布中正在编辑的树。
function openTreeMenu(event: MouseEvent | KeyboardEvent, target: Tree) {
  if (workspaceChanging.value) return;
  const button = event.currentTarget as HTMLElement;
  const bounds = button.getBoundingClientRect();
  button.focus();
  treeMenu.value = {
    tree: target,
    x: "clientX" in event && event.clientX ? event.clientX : bounds.left,
    y: "clientY" in event && event.clientY ? event.clientY : bounds.bottom,
  };
}
// 菜单重命名只修改展示名，保留树 ID、全部引用及有效源码；一次操作可以撤销。
function renameTree(name: string) {
  const target = treeMenu.value?.tree;
  treeMenu.value = undefined;
  if (!target || treeIdentity.value.byID.get(target.id) !== target) return;
  const next = name.trim();
  if (!next || next === target.name) return;
  mutate(() => { target.name = next; }, false);
  notice("行为树名称已更新");
}
// 属性面板删除与右键菜单共用确认框，打开确认框本身不修改工程。
function deleteTree() {
  if (project.value.trees.length === 1) return notice("至少保留一棵树", true);
  treeMenu.value = { tree: tree.value, x: 0, y: 0, initialMode: "delete" };
}
// 二次确认后只删除原目标；非当前树被删除时保留当前树和节点选择。
function confirmDeleteTree() {
  const target = treeMenu.value?.tree;
  treeMenu.value = undefined;
  if (!target || treeIdentity.value.byID.get(target.id) !== target) return;
  if (project.value.trees.length === 1) return notice("至少保留一棵树", true);
  const position = project.value.trees.indexOf(target);
  if (position < 0) return;
  const current = treeID.value === target.id;
  mutate(() => {
    treeIdentity.value.removeTree(target);
    project.value.trees.splice(position, 1);
    if (current) {
      treeID.value = project.value.trees[0]!.id;
      selected.value = "";
    }
  });
  notice("行为树已删除，可撤销恢复；引用它的子树节点需要重新选择");
}
// 新建和示例各自开启独立编辑会话，避免撤销恢复另一文件的内容。
function resetProject(example = false) {
  return action(async () => {
    if (!await allowReplacement()) return;
    installProject(example ? emptyProject() : blankProject(), "", true);
    suggestedName.value = example ? "example.json" : "new-project.json";
    notice(example ? "已打开示例，尚未保存" : "已创建空白工程，尚未保存");
  });
}
// 统一使用无损 JSON，防止黑板中的 64 位整数被截断。
async function request<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(
    path,
    body === undefined
      ? { headers: { "X-BT-Workspace": encodeURIComponent(workspace.value) } }
      : {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-BT-Workspace": encodeURIComponent(workspace.value) },
          body: stringifyJSON(body),
        },
  );
  const data = parseJSON<{ error?: string; diagnostics?: Diagnostic[]; path?: string; existingHash?: string }>(
    await res.text(),
  );
  if (!res.ok) {
    throw new RequestError(data.error ?? `请求失败 (${res.status})`, res.status, data.diagnostics, data.path, data.existingHash);
  }
  return data as T;
}
// 串行化文件请求；调用方可指定错误弹窗，其余操作沿用状态栏。
async function action(fn: () => Promise<void>, onError?: (message: string) => void) {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } catch (e) {
    if (e instanceof RequestError && e.diagnostics) {
      diagnostics.value = e.diagnostics;
      showOutput("diagnostics");
    }
    const detail = e instanceof Error ? e.message : String(e);
    if (onError) onError(detail);
    else notice(detail, true);
  } finally {
    busy.value = false;
  }
}
// 浏览器禁用存储时降级为手动选择，不影响文件读写。
function recentStorage(): RecentStorage | undefined {
  try { return window.localStorage; } catch { return undefined; }
}
// 按需读取一次顶层列表，同时更新服务端实际工作目录。
async function refreshFiles() {
  const result = await request<WorkspaceFiles>("/api/projects");
  if (projectReady.value && workspace.value && workspace.value !== result.workspace) {
    throw new Error("工作目录已被其他页面切换，请先导出当前草稿，再刷新页面");
  }
  workspace.value = result.workspace;
  files.value = result.files;
  allFiles.value = result.allFiles ?? result.files;
  return result;
}
// 列表刷新不修改当前工程，也不清除撤销历史或未保存标记。
function refreshProjectList() {
  return action(async () => {
    await refreshFiles();
    notice("工程列表已刷新，当前编辑内容保持不变");
  });
}
// 工作目录使用系统选择窗口；命名和未保存修改使用网页确认框。
async function askProject(kind: "save" | "switch" | "workspace", reload = false): Promise<ProjectDialogResult | undefined> {
  if (kind === "workspace") {
    const selected = await selectNativeDirectory(workspace.value);
    return selected ? { name: "", directory: selected.workspace, overwrite: false } : undefined;
  }
  return new Promise((resolve) => { projectDialog.value = { kind, reload, resolve }; });
}
// 先卸载旧对话框再继续，确保保存并切换中的下一对话框重新获得焦点。
async function closeProjectDialog(value?: ProjectDialogResult) {
  const pending = projectDialog.value;
  projectDialog.value = undefined;
  await nextTick();
  pending?.resolve(value);
}
// 写入成功才绑定文件身份；修订号使保存期间的新编辑保持未保存状态。
async function saveCurrent(saveAs = false): Promise<boolean> {
  if (!projectReady.value) return false;
  if (!ensureIdentityDraftsApplied()) return false;
  const target = saveAs || !fileName.value ? await askProject("save") : { name: fileName.value };
  if (!target || typeof target === "string") return false;
  const name = target.name;
  const revision = editRevision;
  const snapshot = stringifyJSON(project.value);
  const result = await request<{ workspace: string; files?: string[]; allFiles?: string[] }>("/api/project", { ...target, project: parseJSON(snapshot) });
  const moved = workspace.value !== result.workspace;
  workspace.value = result.workspace;
  if (result.files) files.value = result.files;
  if (result.allFiles || result.files) allFiles.value = result.allFiles ?? result.files!;
  if (moved) resetResults();
  fileName.value = name;
  suggestedName.value = name;
  saveState.saved(snapshot, revision === editRevision ? snapshot : stringifyJSON(project.value));
  rememberProject(workspace.value, name, recentStorage());
  // 本次写入已知成功，只更新内存列表，避免保存后重复枚举目录。
  if (!files.value.includes(name)) files.value = [...files.value, name].sort();
  if (!allFiles.value.includes(name)) allFiles.value = [...allFiles.value, name].sort();
  notice(`已保存 ${name}${dirty.value ? "，后续修改尚未保存" : ""}`);
  return !dirty.value;
}
// 工具栏和快捷键共用同一串行保存入口。
function save(saveAs = false) {
  return action(async () => { await saveCurrent(saveAs); });
}
// 未保存时必须完成保存或明确放弃；保存失败、取消或新编辑都会停止替换。
async function allowReplacement(reload = false): Promise<boolean> {
  if (projectReady.value && !ensureIdentityDraftsApplied()) return false;
  if (!projectReady.value || !dirty.value) return true;
  const choice = await askProject("switch", reload);
  return choice === "discard" || (choice === "save" && await saveCurrent());
}
// 工程内容、文件身份和历史在同一个同步步骤更新。
function installProject(loaded: Project, name: string, unsaved = false) {
  normalizeCodeNames(loaded);
  // 替换响应式工程前完成身份校验，失败时继续保留当前工程。
  new TreeIdentityIndex(loaded);
  project.value = loaded;
  fileName.value = name;
  treeID.value = loaded.trees[0]!.id;
  selected.value = "";
  undoStack.value = [];
  redoStack.value = [];
  saveState.reset(unsaved ? undefined : stringifyJSON(loaded));
  resetResults();
  projectReady.value = true;
  workspaceError.value = "";
  failedOpen.value = "";
  inspectorOpen.value = false;
  void nextTick(() => { updateOutputBounds(); fitView({ padding: 0.18 }); });
}
// 内部加载步骤可供启动及手动切换复用，避免嵌套 action 被 busy 跳过。
async function openCurrent(name: string, reload = false) {
  openingName.value = name;
  failedOpen.value = "";
  workspaceError.value = "";
  try {
    const loaded = await loadProject(() => request<Project>(`/api/project?name=${encodeURIComponent(name)}`));
    if (!loaded) return;
    validateProjectTypes(loaded);
    installProject(loaded, name);
    suggestedName.value = name;
    rememberProject(workspace.value, name, recentStorage());
    notice(`${reload ? "已从磁盘重新加载" : "已打开"} ${name}`);
  } catch (e) {
    failedOpen.value = name;
    workspaceError.value = `打开 ${name} 失败：${e instanceof Error ? e.message : String(e)}`;
    throw e;
  } finally {
    openingName.value = "";
  }
  // 工程已成功打开，附属产物失败只报告产物错误，不误报文件切换失败。
  try { await readGenerated(true); }
  catch (e) { notice(`工程已打开，读取上次生成结果失败：${e instanceof Error ? e.message : String(e)}`, true); }
}
// 用户切换前先保护当前修改，失败时保持原文件身份与画布。
function open(name: string, reload = false) {
  return action(async () => {
    if (await allowReplacement(reload)) await openCurrent(name, reload);
  });
}
// 首屏只自动读取单候选或本工作目录仍存在的最近工程。
function initializeWorkspace() {
  return action(async () => {
    initializing.value = true;
    workspaceError.value = "";
    try {
      const list = await refreshFiles();
      const name = startupProject(list, recentStorage());
      if (name) await openCurrent(name);
      else notice(list.files.length ? "请选择要打开的工程" : "工作目录中尚无工程");
    } catch (e) {
      workspaceError.value ||= e instanceof Error ? e.message : String(e);
      throw e;
    } finally {
      initializing.value = false;
    }
  });
}
// 先选目录再保护当前修改，成功切换后按启动规则恢复目标目录中的工程。
function chooseWorkspace() {
  return action(async () => {
    const target = await askProject("workspace");
    if (!target || typeof target === "string" || !await allowReplacement()) return;
    workspaceChanging.value = true;
    try {
      const list = await request<WorkspaceFiles>("/api/workspace", { directory: target.directory });
      workspace.value = list.workspace;
      files.value = list.files;
      allFiles.value = list.allFiles ?? list.files;
      fileName.value = "";
      suggestedName.value = "project.json";
      projectReady.value = false;
      saveState.reset();
      undoStack.value = [];
      redoStack.value = [];
      resetResults();
      workspaceError.value = "";
      failedOpen.value = "";
      const name = startupProject(list, recentStorage());
      if (name) await openCurrent(name);
      else notice(list.files.length ? "工作目录已切换，请选择要打开的工程" : "工作目录已切换，尚无工程");
    } finally {
      workspaceChanging.value = false;
    }
  });
}
// 复制服务端路径，供用户在文件管理器中定位工作目录。
async function copyWorkspace() {
  try { await navigator.clipboard.writeText(workspace.value); notice("已复制工作目录"); }
  catch { notice("复制失败，可选中工作目录文字手动复制", true); }
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
// 选择器始终反映实际文件，取消或失败不会停留在尚未打开的目标。
function selectProject(event: Event) {
  const element = event.target as HTMLSelectElement;
  const name = element.value;
  element.value = fileName.value;
  if (name && name !== fileName.value) void open(name);
}
// 调用 Go 共用校验器，并展示可定位到节点的诊断。
function validate() {
  return action(async () => {
    validationResult.value = undefined;
    diagnostics.value = [];
    showOutput("diagnostics");
    notice("正在校验…");
    const result = await currentProjectRequest<{ diagnostics: Diagnostic[] }>("/api/validate");
    if (!result) return;
    diagnostics.value = result.data.diagnostics;
    validationResult.value = { revision: result.revision, count: diagnostics.value.length };
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
  if (!ensureIdentityDraftsApplied()) return;
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
// 写盘前保存当前工程，确保源码对应已落盘内容；预览仍只读取当前草稿。
function generate(write = true) {
  return action(async () => {
    if (!ensureIdentityDraftsApplied()) return;
    // 复用首次命名和保存快照校验，取消、失败或保存期间的新编辑都会停止生成。
    if (write && (dirty.value || !fileName.value) && !await saveCurrent()) {
      notice("工程尚未完成保存，已停止生成，请保存后重试");
      return;
    }
    const result = await currentProjectRequest<GeneratedCode>(write ? "/api/generate" : "/api/preview");
    if (!result) return;
    acceptCodeSnapshot(result.data, result.signature, result.revision, write ? "generated" : "preview");
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
    acceptCodeSnapshot(result.data, matches ? result.signature : "", matches ? result.revision : -1, "saved");
    showOutput("source");
    notice(matches ? "已载入上次生成，内容与当前工程一致" : "已载入上次生成，内容与当前工程不同，请更新预览");
  } catch (e) {
    if (silent && e instanceof RequestError && e.status === 404) return;
    throw e;
  }
}
// 在响应边界一次建立文件与节点索引，并保持旧快照只在新快照完整可用时被替换。
function acceptCodeSnapshot(data: GeneratedCode, signature: string, revision: number, origin: CodeSnapshot["origin"]) {
  const index = createGeneratedSourceIndex(data.files, data.sourceMap);
  const file = selectGeneratedFile(index, sourceFileName.value, revision === semanticRevision.value ? treeID.value : undefined);
  codeSnapshot.value = { ...data, index, signature, revision, origin };
  sourceFileName.value = file?.name ?? "";
}
// 预览业务骨架，实际业务由生成包中的 actions.go 实现。
function previewScaffold() {
  return action(async () => {
    const result = await currentProjectRequest<{ source: string }>("/api/scaffold");
    if (!result) return;
    scaffoldSnapshot.value = { source: result.data.source, revision: result.revision, signature: result.signature };
    showOutput("scaffold");
    notice("业务骨架已生成，请实现 TODO 后与生成文件一起编译");
  });
}
// 关闭覆盖确认并恢复等待中的下载操作，取消和 Escape 均不写入文件。
function closeScaffoldOverwrite(confirmed = false) {
  const pending = scaffoldOverwrite.value;
  scaffoldOverwrite.value = undefined;
  pending?.resolve(confirmed);
}
// 默认写入生成包路径；仅在同名文件存在且用户明确确认后提交覆盖请求。
function downloadScaffold() {
  return action(async () => {
    if (!ensureIdentityDraftsApplied()) return;
    if (!scaffoldSnapshot.value || scaffoldStale.value) {
      notice("工程已变化，请先更新业务骨架再下载", true);
      return;
    }
    const snapshot = clone(project.value);
    const revision = semanticRevision.value;
    const directory = workspace.value;
    let result: { path: string };
    try {
      result = await request<{ path: string }>("/api/scaffold/save", { project: snapshot });
    } catch (e) {
      if (!(e instanceof RequestError) || e.status !== 409 || !e.path || !e.existingHash) throw e;
      // 异步检查期间编辑工程后，不再使用旧路径或旧骨架请求覆盖授权。
      if (revision !== semanticRevision.value || directory !== workspace.value) {
        notice("工程已变化，请更新业务骨架后重新下载", true);
        return;
      }
      const confirmed = await new Promise<boolean>(resolve => {
        scaffoldOverwrite.value = { path: e.path!, resolve };
      });
      if (!confirmed) {
        notice("已取消下载，原 actions.go 已保留");
        return;
      }
      if (revision !== semanticRevision.value || directory !== workspace.value) {
        notice("工程已变化，请更新业务骨架后重新下载", true);
        return;
      }
      result = await request<{ path: string }>("/api/scaffold/save", {
        project: snapshot, overwrite: true, expectedHash: e.existingHash,
      });
    }
    notice(`下载成功，业务骨架已保存到 ${result.path}`);
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
    if (!await allowReplacement()) return;
    const loaded = await loadProject(async () => {
      const parsed: unknown = parseJSON(await file.text());
      validateProjectTypes(parsed);
      return request<Project>("/api/import", parsed);
    });
    if (!loaded) return;
    validateProjectTypes(loaded);
    installProject(loaded, "", true);
    suggestedName.value = file.name;
    notice(`已导入 ${file.name}，尚未保存`);
  }, message => { importFailure.value = { name: file.name, message }; });
  input.value = "";
}
// 从节点属性或节点库打开同一目录管理入口。
function manageCatalog(mode: "create" | "import" | "manage", forNode = false, folderId = catalogFolder.value) {
  const kind = forNode && (node.value?.type === "action" || node.value?.type === "condition") ? node.value.type : undefined;
  catalogDialog.value = { mode, kind, folderId };
}
// 分类变更成功后才登记历史；校验失败保留原工程与当前表单。
function commitCatalogOrganization(change: () => void): boolean {
  if (workspaceChanging.value) return false;
  const snapshot = captureSnapshot(project.value, treeID.value, selected.value);
  try {
    change();
    undoStack.value.push(snapshot);
    if (undoStack.value.length > 100) undoStack.value.shift();
    redoStack.value = [];
    editRevision++;
    saveState.changed();
    catalogRevision.value++;
    notice("分类已更新，请保存工程");
    return true;
  } catch (cause) {
    notice(cause instanceof Error ? cause.message : String(cause), true);
    return false;
  }
}
// 定义菜单的移动和标签操作使用捕获对象身份，避免作用于已替换的工程。
function organizeCatalogDefinition(operation: "move" | "tags") {
  const target = catalogMenu.value?.definition;
  catalogMenu.value = undefined;
  if (!target || workspaceChanging.value || definitionIndex.value.get(target.id) !== target) return;
  if (operation === "move") catalogBrowser.value?.openMoveDefinition(target.id);
  else catalogBrowser.value?.openDefinitionTags(target.id);
}
// 单条与全部共享服务端校验及无损 JSON，复制和下载不进入工程历史。
async function transferCatalog(operation: "copy" | "download", target?: Definition) {
  catalogMenu.value = undefined;
  if (catalogTransferBusy.value || workspaceChanging.value || (target && definitionIndex.value.get(target.id) !== target)) return;
  const definitions = target ? [target] : project.value.catalog;
  if (!definitions.length) return;
  const owner = project.value;
  catalogTransferBusy.value = true;
  try {
    const content = await validatedCatalogJSON(clone(definitions));
    if (project.value !== owner || workspaceChanging.value) return;
    if (operation === "download") {
      download(target ? "business-definition.json" : "business-definitions.json", content);
      notice(`已导出 ${definitions.length} 个业务定义`);
    } else {
      try {
        await navigator.clipboard.writeText(content);
        if (project.value === owner && !workspaceChanging.value) notice(`已复制 ${definitions.length} 个业务定义 JSON`);
      } catch { if (project.value === owner && !workspaceChanging.value) catalogCopy.value = content; }
    }
  } catch (cause) { if (project.value === owner && !workspaceChanging.value) notice(cause instanceof Error ? cause.message : String(cause), true); }
  finally { catalogTransferBusy.value = false; }
}
// 鼠标和键盘菜单入口只捕获定义，不添加节点或改变画布选择。
function openCatalogMenu(event: MouseEvent | KeyboardEvent, target: Definition) {
  if (workspaceChanging.value) return;
  const button = event.currentTarget as HTMLElement;
  const bounds = button.getBoundingClientRect();
  button.focus();
  treeMenu.value = undefined;
  catalogMenu.value = {
    definition: target,
    x: "clientX" in event ? event.clientX : bounds.left,
    y: "clientY" in event ? event.clientY : bounds.bottom,
  };
}
// 按对象身份验证编辑目标，直接进入已有定义表单并保持节点绑定不变。
function editCatalogDefinition(target: Definition) {
  catalogMenu.value = undefined;
  if (workspaceChanging.value || definitionIndex.value.get(target.id) !== target) return;
  catalogDialog.value = { mode: "edit", definition: target };
}
// 二次确认后复用目录同步的撤销和校验边界；保留失效引用，供用户重新绑定。
async function confirmDeleteDefinition() {
  const target = catalogMenu.value?.definition;
  catalogMenu.value = undefined;
  if (!target || workspaceChanging.value || definitionIndex.value.get(target.id) !== target) return;
  await applyCatalog(project.value.catalog.filter(item => item !== target));
}
// 目录与所有节点参数在同一撤销边界内同步，随后立即用同一快照校验整个工程。
async function applyCatalog(catalog: Definition[], bindID?: string) {
  let resets: Diagnostic[] = [];
  const previousIDs = new Set(project.value.catalog.map(item => item.id));
  const nextIDs = new Set(catalog.map(item => item.id));
  const folder = catalogDialog.value?.folderId ?? "";
  mutate(() => {
    for (const id of previousIDs) if (!nextIDs.has(id)) catalogIndex.value.removeDefinition(id);
    if (bindID && node.value) {
      node.value.binding = bindID;
      node.value.params = {};
    }
    resets = synchronizeCatalog(project.value, catalog);
    catalogIndex.value = new CatalogOrganizationIndex(project.value);
    catalogIndex.value.assignNewDefinitions(catalog.filter(item => !previousIDs.has(item.id)).map(item => item.id), catalogIndex.value.folders.has(folder) ? folder : "");
    catalogRevision.value++;
  });
  catalogDialog.value = undefined;
  const revision = semanticRevision.value;
  diagnostics.value = resets;
  showOutput("diagnostics");
  notice(`业务定义已应用，请保存工程；共 ${catalog.length} 个定义，正在校验`);
  // 不经过文件操作的 busy 锁，避免在保存或旧校验期间更新目录时跳过本次校验。
  try {
    const result = await request<{ diagnostics: Diagnostic[] }>("/api/validate", clone(project.value));
    if (revision !== semanticRevision.value) return;
    diagnostics.value = [...resets, ...(result.diagnostics ?? [])];
    notice(diagnostics.value.length ? `业务定义已应用，请保存工程；发现 ${diagnostics.value.length} 条校验提示` : "业务定义已应用，请保存工程；校验通过", diagnostics.value.length > 0);
  } catch (cause) {
    if (revision !== semanticRevision.value) return;
    notice(`业务目录已同步，但校验失败：${cause instanceof Error ? cause.message : String(cause)}`, true);
  }
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
  if (workspaceChanging.value) return;
  if (catalogDialog.value || projectDialog.value || importFailure.value || treeMenu.value || catalogMenu.value || !projectReady.value) return;
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
  if (projectReady.value && (dirty.value || identityDraftPending.value)) e.preventDefault();
}
// 仅在实际树对象替换时重建；普通输入、改号和连线通过增量索引处理。
watch(tree, (current) => {
  nodeIdentity.value = reactive(new NodeIdentityIndex(current));
  cancelNodeID();
}, { flush: "sync" });
watch(selected, cancelNodeID, { flush: "sync" });
watch(treeID, () => {
  cancelTreeID();
  if (renamingTree) return;
  endPaletteDrag();
  selected.value = "";
  setTimeout(() => fitView({ padding: 0.18 }), 30);
}, { flush: "sync" });
onMounted(() => {
  outputResizeObserver = new ResizeObserver(updateOutputBounds);
  if (workbench.value) outputResizeObserver.observe(workbench.value);
  if (outputPanel.value) outputResizeObserver.observe(outputPanel.value);
  updateOutputBounds();
  void initializeWorkspace();
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
    execute: () => {
      if (!projectReady.value) throw new Error("请先打开或新建工程");
      return clone(project.value);
    },
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
      if (!projectReady.value) throw new Error("请先打开或新建工程");
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
    :inert="workspaceChanging"
    @input="nativeHistory"
    :class="{ 'resizing-output': resizingOutput }"
    :style="outputHeight === undefined ? {} : { '--output-height': `${outputHeight}px` }"
  >
    <header class="topbar">
      <div class="brand">
        <span class="brandmark">⑂</span><strong>行为树工作台</strong
        ><span class="local-badge">LOCAL</span>
      </div>
      <div class="workspace-context">
        <div class="workspace-location">
          <span>工作目录</span><span class="workspace-path mono" :title="workspace">{{ workspace || '正在读取…' }}</span>
          <button class="open-workspace" title="选择工作目录并重新加载，相当于切换 --workspace" aria-label="打开工作目录" :disabled="busy || !workspace" @click="chooseWorkspace">📁 打开目录</button>
          <button class="text-button" :disabled="!workspace" @click="copyWorkspace">复制路径</button>
        </div>
        <div class="project-identity">
          <label for="current-project">当前工程</label>
          <select id="current-project" aria-label="当前工程" :value="fileName" :disabled="busy || !workspace" @change="selectProject">
            <!-- 无文件时只显示占位文字，展开列表仅列出实际工程文件。 -->
            <option v-if="!fileName" value="" disabled hidden>{{ projectReady ? '草稿（尚未保存）' : '请选择工程…' }}</option>
            <option v-if="fileName && !files.includes(fileName)" :value="fileName" disabled hidden>{{ fileName }}（磁盘文件不可用）</option>
            <option v-for="file in files" :key="file" :value="file">{{ file }}</option>
          </select>
          <button class="refresh-projects" title="只更新可打开的文件列表，保留当前编辑内容" :disabled="busy || !workspace" @click="refreshProjectList">刷新列表</button>
          <button class="refresh-projects" title="从磁盘重新加载当前文件" :disabled="busy || !fileName" @click="open(fileName, true)">重载文件</button>
          <span v-if="projectReady" class="save-state" :class="{ unsaved: dirty || identityDraftPending }">{{ identityDraftPending ? '● 有未应用修改' : dirty ? '● 未保存' : '已保存' }}</span>
          <span v-if="projectReady" class="project-caption" :title="project.name">{{ project.name }}</span>
        </div>
      </div>
      <div class="toolbar">
        <button :disabled="busy || !workspace" @click="resetProject()">新建</button
        ><button title="将 JSON 内容读入未保存草稿，不会直接修改源文件；保存时选择目录和文件名，确认覆盖同名文件后才会覆盖。" :disabled="busy || !workspace" @click="importInput?.click()">导入</button>
        <button :disabled="!projectReady" @click="ensureIdentityDraftsApplied() && download(fileName || suggestedName, stringifyJSON(project, 2))">
          导出 JSON
        </button>
        <button :disabled="busy || !projectReady" @click="save()">保存 <kbd>Ctrl S</kbd></button>
        <button title="选择目录和文件名另存为；保存成功后使用目标工作目录" :disabled="busy || !projectReady" @click="save(true)">另存为</button>
        <button :disabled="busy || !projectReady" @click="validate">校验</button>
        <button :disabled="busy || !projectReady" @click="generate(false)">预览 Go</button>
        <button class="primary" :disabled="busy || !projectReady" @click="generate(true)">
          生成到目录 <span>↗</span>
        </button>
      </div>
    </header>

    <section v-if="!projectReady" class="workspace-start" aria-live="polite">
      <div class="workspace-start-card">
        <h1>{{ busy ? (openingName ? `正在打开 ${openingName}` : '正在读取工作目录') : files.length ? '打开工作目录中的工程' : '开始创建行为树' }}</h1>
        <p class="muted">{{ files.length ? '选择一个工程继续编辑。' : '新建空白工程，或打开示例了解编辑方式。' }}</p>
        <p v-if="workspaceError" class="identity-error" role="alert">{{ workspaceError }}</p>
        <div v-if="!initializing" class="workspace-project-list">
          <button v-for="file in files" :key="file" :disabled="busy" @click="open(file)"><span class="mono">{{ file }}</span><span>打开 →</span></button>
        </div>
        <div class="workspace-start-actions">
          <button v-if="failedOpen" :disabled="busy" @click="open(failedOpen)">重试打开</button>
          <button :disabled="busy" @click="initializeWorkspace">刷新目录</button>
          <button :disabled="busy || !workspace" @click="resetProject(true)">打开示例</button>
          <button class="primary" :disabled="busy || !workspace" @click="resetProject()">新建工程</button>
        </div>
      </div>
    </section>

    <aside v-show="projectReady" class="library">
      <div class="panel-heading">
        行为树
        <button class="icon-button" title="新增行为树" @click="addTree">
          ＋
        </button>
      </div>
      <nav class="tree-list">
        <button
          v-for="item in project.trees"
          :key="item.id"
          :class="{ active: item.id === treeID }"
          aria-haspopup="menu"
          :aria-label="`${item.name}，行为树 ID：${item.id}，${item.nodes.length} 个节点`"
          @contextmenu.prevent.stop="openTreeMenu($event, item)"
          @keydown.shift.f10.prevent.stop="openTreeMenu($event, item)"
          @keydown.prevent.stop.context-menu="openTreeMenu($event, item)"
          @click="treeID = item.id"
        >
          <span>⑂</span><span class="tree-caption">{{ item.name }}<code :title="`当前生效的行为树 ID：${item.id}`">ID: {{ item.id }}</code></span><small :title="`${item.nodes.length} 个节点`">{{ item.nodes.length }}</small>
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
            :title="info.help"
            @dragstart="startPaletteDrag($event, type)"
            @dragend="endPaletteDrag"
            :disabled="workspaceChanging"
            @click="nodeHelp = type"
          >
            <span :class="['kind-icon', info.color]">{{ info.icon }}</span
            ><span
              >{{ info.label }}<small>{{ type }}</small></span
            ><span class="add-sign" aria-hidden="true">ⓘ</span>
          </button>
        </div>
        <CatalogBrowser ref="catalogBrowser" :index="catalogIndex" :revision="catalogRevision" :search="search"
          :disabled="workspaceChanging" :commit="commitCatalogOrganization" :failure-message="error ? message : ''"
          @select="catalogFolder = $event" @clear-search="search = ''"
          @create="manageCatalog('create', false, $event)" @import="manageCatalog('import', false, $event)" @manage="manageCatalog('manage')"
          @inspect="editCatalogDefinition" @menu="openCatalogMenu"
          @drag="(event, definition) => startPaletteDrag(event, definition.kind, definition.id)" @dragend="endPaletteDrag"
          @transfer="transferCatalog($event)" />
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
              :placeholder="PARAM_TYPE_META[field.type].inputPlaceholder"
              :title="parameterTypeTooltip(field.type)"
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

    <main v-show="projectReady" class="canvas-area">
      <div v-if="workspaceError" class="project-open-error" role="alert">{{ workspaceError }} <button :disabled="busy" @click="open(failedOpen)">重试</button><button @click="workspaceError = ''">关闭</button></div>
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
        @node-click="selectCanvasNode"
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
              <!-- 画布读取已提交代码名；业务绑定 ID 由属性面板独立管理。 -->
              <span v-if="data.node.codeName" class="node-code-name" :title="data.node.codeName">{{ data.node.codeName }}</span>
              <span v-if="['wait', 'timeout'].includes(data.node.type)"
                >{{ data.node.durationMs ?? 0 }} ms</span
              ><span v-else-if="['repeat', 'retry'].includes(data.node.type)"
                >{{ data.node.count ?? 0 }} 次</span
              >
              <span v-else-if="['action', 'condition'].includes(data.node.type) && !data.node.binding" class="unbound-node">未绑定业务定义</span>
              <span v-else-if="!['action', 'condition'].includes(data.node.type)"
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

    <aside v-show="projectReady" :class="['inspector', { opened: inspectorOpen }]">
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
            @input="changeNodeName"
        /></label>
        <label class="field-label"
          >节点代码名<input v-model="codeNameDraft" class="mono" maxlength="40"
            :aria-invalid="!!codeNameError" aria-describedby="code-name-help code-name-error"
            @input="codeNameError = ''" @keydown.enter.prevent="applyCodeName" @keydown.esc.prevent="cancelCodeName"
        /></label>
        <div class="identity-actions">
          <button @click="applyCodeName" :disabled="codeNameDraft === node.codeName">应用代码名</button>
          <button @click="cancelCodeName" :disabled="codeNameDraft === node.codeName && !codeNameError">取消</button>
        </div>
        <p id="code-name-help" class="muted identity-help">树内唯一，1–40 位，英文开头，可含数字和下划线，不能是 Go 关键字。用于生成节点常量和节点函数；从业务目录创建时默认采用业务函数名，之后独立保存，修改绑定不会自动改名。</p>
        <p v-if="codeNameError" id="code-name-error" class="identity-error" role="alert">{{ codeNameError }}</p>
        <label class="field-label"
          >Node ID<input v-model="nodeIDDraft" class="mono"
            :aria-invalid="!!nodeIDError" aria-describedby="node-id-help node-id-error"
            @keydown.enter.prevent="applyNodeID" @keydown.esc.prevent="cancelNodeID"
        /></label>
        <div class="identity-actions">
          <button @click="applyNodeID" :disabled="nodeIDDraft === node.id || !!nodeIDError">应用 ID</button>
          <button @click="cancelNodeID" :disabled="nodeIDDraft === node.id && !nodeIDError">取消</button>
        </div>
        <p id="node-id-help" class="muted identity-help">仅当前树内唯一，不同树可重复；新增和复制自动分配递增数字 ID。不能为空或与本树其他节点重复，冲突时不生效。应用时同步根、连线和布局；显式改号后需更新外部旧 ID 关联。</p>
        <p v-if="nodeIDError" id="node-id-error" class="identity-error" role="alert">{{ nodeIDError }}</p>
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
            @change="changeTreeReference"
          >
            <option value="">请选择…</option>
            <option
              v-for="t in project.trees.filter((t) => t.id !== treeID)"
              :key="t.id"
              :value="t.id"
            >
              {{ t.name }} · {{ t.id }}
            </option>
          </select></label
        >
        <template v-if="['action', 'condition'].includes(node.type)">
          <label class="field-label"
            >业务实现函数<select
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
            尚无{{ node.type === 'action' ? '动作' : '条件' }}定义。请新建或导入业务定义，再选择绑定。
          </p>
          <p v-else-if="node.binding && (!definition || definition.kind !== node.type)" class="binding-note">
            当前绑定 {{ node.binding }} 不存在或种类不匹配，请重新选择。
          </p>
          <div class="binding-actions">
            <button @click="manageCatalog('create', true)">新建业务定义</button>
            <button @click="manageCatalog('import', true)">导入业务定义</button>
            <button @click="manageCatalog('manage', true)">管理定义</button>
            <button v-if="definition" @click="editCatalogDefinition(definition)">编辑定义</button>
            <button :disabled="busy || !project.catalog.length" @click="previewScaffold">预览业务骨架</button>
          </div>
          <p class="muted empty-note">可供多个节点复用的手写 Go 函数，请写入同包的独立 Go 文件。切换绑定不会修改节点代码名。</p>
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
              :placeholder="PARAM_TYPE_META[p.type].inputPlaceholder"
              :title="parameterTypeTooltip(p.type)"
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
            @input="changeTreeName"
        /></label>
        <label class="field-label"
          >行为树 ID<input v-model="treeIDDraft" class="mono"
            :aria-invalid="!!treeIDError" aria-describedby="tree-id-help tree-id-error"
            @keydown.enter.prevent="applyTreeID" @keydown.esc.prevent="cancelTreeID"
        /></label>
        <div class="identity-actions">
          <button @click="applyTreeID" :disabled="treeIDDraft === tree.id || !!treeIDError">应用 ID</button>
          <button @click="cancelTreeID" :disabled="treeIDDraft === tree.id && !treeIDError">取消</button>
        </div>
        <p v-if="treeIDDraft !== tree.id" class="identity-help" role="status">ID 修改尚未应用。当前生效 ID：<code>{{ tree.id }}</code>。点击“应用 ID”或按 Enter 后同步左侧列表。</p>
        <p v-if="treeIDError" id="tree-id-error" class="identity-error" role="alert">{{ treeIDError }}</p>
        <p id="tree-id-help" class="muted identity-help">工程内唯一，新建树自动分配递增数字 ID；手动改为更大数字后继续递增。允许 1–80 位英文、数字或下划线，不能仅大小写不同；冲突时不生效。应用时同步全部子树引用，外部入口调用需同步调整。</p>
        <div class="panel-heading small-heading">Go 生成设置</div>
        <label class="field-label"
          >生成包路径<input
            :value="project.generation.packagePath"
            placeholder="例如：ai/brawl"
            aria-describedby="generation-package-help generation-package-preview"
            @input="changeGenerationPackagePath"
        /></label>
        <p id="generation-package-preview" class="identity-help" role="status">
          生成目录：<code>{{ project.generation.packagePath || "（未填写）" }}</code><br>
          Go package：<code>{{ generationPackageName || "（未填写）" }}</code>
        </p>
        <p id="generation-package-help" class="muted identity-help">生成目录相对于当前工程目录，路径最后一级目录名将作为 Go package 名。例如 ai/brawl 会生成到 ai/brawl/，package 为 brawl。</p>
        <p class="muted identity-help">更改路径后会在新目录生成，旧目录保留；手写代码请自行迁移。</p>
        <label class="field-label"
          >业务上下文导入路径<input
            :value="project.generation.contextImport"
            placeholder="例如 bt_context 或 bt_test/bt_context；留空使用同包类型"
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
        <p class="muted identity-help">例如填写 bt_context 和 *Context：生成或下载业务骨架时首次创建 bt_context/context.go，已有类型不覆盖。短路径按当前目录 go.mod 补全；留空则在生成包创建，any 不创建。预览不写盘。</p>
        <button class="danger tree-delete" @click="deleteTree">
          删除当前行为树
        </button>
      </template>
    </aside>

    <section v-show="projectReady" id="output-panel" ref="outputPanel" class="output-panel">
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
          <select v-if="bottomTab === 'source'" v-model="sourceFileName" class="source-file-select" aria-label="生成文件">
            <option v-for="file in codeSnapshot!.files" :key="file.name" :value="file.name">{{ file.name }}</option>
          </select>
          <button @click="copySource">复制</button>
          <button :disabled="bottomTab === 'scaffold' && (busy || scaffoldStale)" @click="bottomTab === 'scaffold' ? downloadScaffold() : download(sourceFile!.name, sourceFile!.source, 'text/plain')">下载 {{ bottomTab === 'scaffold' ? 'actions.go' : '当前文件' }}</button>
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
      <p v-if="bottomTab === 'scaffold' && scaffoldSnapshot" class="code-warning">{{ scaffoldStale ? '工程已变化，请更新骨架。' : '' }}骨架中的 TODO 需要手动实现；默认保存到 {{ project.generation.packagePath }}/actions.go，同名文件需再次确认才会覆盖。</p>
      <div v-if="bottomTab === 'diagnostics'" class="output-content">
        <p v-if="validationResult && validationResult.revision === semanticRevision" class="validation-result" :class="{ 'identity-error': validationResult.count > 0 }" role="status">
          {{ validationResult.count ? `校验完成，发现 ${validationResult.count} 个问题，请点击下方条目定位。` : '校验通过，未发现结构与类型问题，可以生成 Go 代码。' }}
        </p>
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
          <p v-if="!diagnostics.length && (!validationResult || validationResult.revision !== semanticRevision)" class="muted">
            点击「校验」检查结构与类型。「预览 Go」查看源码，「生成到目录」先保存工程再写入生成文件。
          </p></template
        >
      </div>
      <SourceViewer v-else-if="bottomTab === 'source' && source" :source="source" :source-index="sourceIndex"
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
      ><span v-if="projectReady"
        >Schema {{ project.schemaVersion }}<b>·</b
        >{{ project.trees.length }} 棵树<b>·</b
        >{{ project.blackboard.length }} 个字段</span
      >
    </footer>
    <OperationNotice :message="message" :failed="error" :busy="busy" />
    <input
      ref="importInput"
      type="file"
      accept="application/json,.json"
      hidden
      @change="importProject"
    />
    <NodeHelpDialog v-if="nodeHelp" :type="nodeHelp" @close="nodeHelp = undefined" />
    <CatalogManager v-if="catalogDialog" :catalog="project.catalog" :initial-mode="catalogDialog.mode" :initial-kind="catalogDialog.kind"
      :initial-definition="catalogDialog.definition"
      @apply="applyCatalog" @close="catalogDialog = undefined" />
    <CatalogContextMenu v-if="catalogMenu" :key="`${catalogMenu.definition.id}:${catalogMenu.x}:${catalogMenu.y}`"
      :definition="catalogMenu.definition" :x="catalogMenu.x" :y="catalogMenu.y"
      @close="catalogMenu = undefined" @edit="editCatalogDefinition(catalogMenu.definition)" @delete="confirmDeleteDefinition"
      @move="organizeCatalogDefinition('move')" @tags="organizeCatalogDefinition('tags')"
      @copy="transferCatalog('copy', catalogMenu.definition)" @export="transferCatalog('download', catalogMenu.definition)" />
    <CatalogCopyDialog v-if="catalogCopy !== undefined" :content="catalogCopy" @close="catalogCopy = undefined" />
    <ProjectDialog v-if="projectDialog" :kind="projectDialog.kind" :reload="projectDialog.reload" :workspace="workspace" :suggestion="fileName || suggestedName" :files="allFiles" @close="closeProjectDialog" />
    <ImportErrorDialog v-if="importFailure" :name="importFailure.name" :message="importFailure.message" @close="importFailure = undefined" />
    <ScaffoldOverwriteDialog v-if="scaffoldOverwrite" :path="scaffoldOverwrite.path" @close="closeScaffoldOverwrite" />
    <TreeContextMenu v-if="treeMenu" :key="`${treeMenu.tree.id}:${treeMenu.x}:${treeMenu.y}:${treeMenu.initialMode ?? 'menu'}`"
      :tree="treeMenu.tree" :x="treeMenu.x" :y="treeMenu.y"
      :initial-mode="treeMenu.initialMode" :can-delete="project.trees.length > 1"
      @close="treeMenu = undefined" @rename="renameTree" @delete="confirmDeleteTree" />
  </div>
</template>
