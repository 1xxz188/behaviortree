<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
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
  uid,
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
  nodeTypes, valueTypes, parseValueType, validateProjectTypes, validateCatalogTypes,
} from "./enums";
import type { NodeType, ValueType } from "./enums";

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
const source = ref("");
const outputPath = ref("");
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
const catalogInput = ref<HTMLInputElement>();
const { fitView, setCenter, screenToFlowCoordinate } = useVueFlow();
// 拖拽只保存节点模板，成功落入画布后才写入工程和撤销历史。
const paletteDrag = ref<{ type: NodeType; binding?: string }>();
const canvasDragOver = ref(false);

const tree = computed(
  () =>
    project.value.trees.find((t) => t.id === treeID.value) ??
    project.value.trees[0]!,
);
const node = computed(() =>
  tree.value?.nodes.find((n) => n.id === selected.value),
);
const definition = computed(() =>
  project.value.catalog.find((d) => d.id === node.value?.binding),
);
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
      invalid: diagnostics.value.some(
        (d) => d.treeId === tree.value.id && d.nodeId === n.id,
      ),
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
  undoStack.value.push(stringifyJSON(project.value));
  if (undoStack.value.length > 100) undoStack.value.shift();
  redoStack.value = [];
  dirty.value = true;
  source.value = "";
  diagnostics.value = [];
}
// 在统一历史边界内修改工程。
function mutate(fn: () => void) {
  checkpoint();
  fn();
}
// 恢复工程快照，并清除旧源码和诊断。
function restore(snapshot: string) {
  const restored = parseJSON<Project>(snapshot);
  validateProjectTypes(restored);
  project.value = restored;
  if (!project.value.trees.some((t) => t.id === treeID.value))
    treeID.value = project.value.trees[0]?.id ?? "";
  selected.value = "";
  dirty.value = true;
  source.value = "";
  diagnostics.value = [];
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
    const id = uid(),
      item: BTNode = { id, type, name: kinds[type]?.label ?? type };
    if (binding) {
      item.binding = binding;
      item.name = project.value.catalog.find((d) => d.id === binding)?.name;
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
  });
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
    n.id = uid();
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
  mutate(() => autoLayout(tree.value));
  setTimeout(() => fitView({ padding: 0.18 }), 30);
}
// 创建可独立生成或作为子树引用的入口。
function addTree() {
  mutate(() => {
    const id = uid("tree");
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
    if (data.diagnostics) diagnostics.value = data.diagnostics;
    throw new Error(data.error ?? `请求失败 (${res.status})`);
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
    await request("/api/project", {
      name: fileName.value,
      project: project.value,
    });
    dirty.value = false;
    await refreshFiles();
    notice(`已保存 ${fileName.value}`);
  });
}
// 加载工程并重置该文件的编辑历史。
function open(name: string) {
  return action(async () => {
    if (dirty.value && !confirm("当前修改尚未保存，打开其他工程？")) return;
    const loaded = await request<Project>(
      `/api/project?name=${encodeURIComponent(name)}`,
    );
    validateProjectTypes(loaded);
    project.value = loaded;
    fileName.value = name;
    treeID.value = loaded.trees[0]!.id;
    selected.value = "";
    undoStack.value = [];
    redoStack.value = [];
    dirty.value = false;
    diagnostics.value = [];
    source.value = "";
    notice(`已打开 ${name}`);
  });
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
    diagnostics.value = (
      await request<{ diagnostics: Diagnostic[] }>(
        "/api/validate",
        project.value,
      )
    ).diagnostics;
    bottomTab.value = "diagnostics";
    notice(
      diagnostics.value.length
        ? `发现 ${diagnostics.value.length} 个问题`
        : "校验通过，可以生成 Go 代码",
      diagnostics.value.length > 0,
    );
  });
}
// 生成实际 Go 控制流，并展示源码及保存路径。
function generate() {
  return action(async () => {
    const result = await request<{
      source: string;
      path: string;
      version: string;
    }>("/api/generate", project.value);
    source.value = result.source;
    outputPath.value = result.path;
    diagnostics.value = [];
    bottomTab.value = "source";
    notice(`已生成 Go · ${result.version.slice(0, 12)}`);
  });
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
    const parsed: unknown = parseJSON(await file.text());
    validateProjectTypes(parsed);
    const loaded = await request<Project>("/api/import", parsed);
    validateProjectTypes(loaded);
    checkpoint();
    project.value = loaded;
    treeID.value = loaded.trees[0]!.id;
    selected.value = "";
    fileName.value = file.name;
    notice(`已导入 ${file.name}，尚未保存`);
  });
  input.value = "";
}
// 导入程序员用 Go 导出的动作、条件及参数声明。
async function importCatalog(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  if (!file) return;
  await action(async () => {
    const parsed = parseJSON(await file.text());
    validateCatalogTypes(parsed);
    const catalog = await request<Definition[]>("/api/catalog", parsed);
    validateCatalogTypes(catalog);
    mutate(() => {
      project.value.catalog = catalog;
    });
    notice(`已导入 ${catalog.length} 个 Go 节点定义`);
  });
  input.value = "";
}
// 添加带稳定 ID 的强类型黑板字段。
function addField() {
  mutate(() => {
    project.value.blackboard.push({
      id: uid("field"),
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
});
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
        <button class="primary" :disabled="busy" @click="generate">
          生成 Go <span>↗</span>
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
          Go 业务节点<button class="text-button" @click="catalogInput?.click()">
            导入目录
          </button>
        </div>
        <p v-if="!project.catalog.length" class="muted empty-note">
          导入 Go 导出的节点目录，即可配置业务动作和条件。
        </p>
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
              ><span v-else-if="data.node.binding">{{ data.node.binding }}</span
              ><span v-else
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
          >节点 ID<input :value="node.id" readonly class="mono"
        /></label>
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
                v-for="d in project.catalog.filter(
                  (d) => d.kind === node!.type,
                )"
                :key="d.id"
                :value="d.id"
              >
                {{ d.name }}
              </option>
            </select></label
          >
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
                tree.nodes.find((n) => n.id === child)?.name ?? child
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
        ><span class="muted output-path">{{
          bottomTab === "source" ? outputPath : "JSON → 校验 → Go"
        }}</span
        ><button
          v-if="source && bottomTab === 'source'"
          class="text-button"
          @click="download('tree_gen.go', source, 'text/plain')"
        >
          下载 Go
        </button>
      </div>
      <div class="output-content">
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
            点击「校验」检查结构与类型。点击「生成 Go」保存代码并查看结果。
          </p></template
        >
        <pre v-else>{{
          source || "生成代码将显示在这里。手写的动作实现不会被覆盖。"
        }}</pre>
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
    /><input
      ref="catalogInput"
      type="file"
      accept="application/json,.json"
      hidden
      @change="importCatalog"
    />
  </div>
</template>
