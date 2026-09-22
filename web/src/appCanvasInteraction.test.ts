import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, nextTick, reactive, ref, shallowRef, watch } from "vue";
import { blankProject, clone } from "./project.ts";
import type { BTNode, Tree } from "./project.ts";
import { NodeIdentityIndex } from "./nodeIdentity.ts";
import { TreeIdentityIndex, captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import type { EditorSnapshot } from "./treeIdentity.ts";
import { ProjectSaveState } from "./saveState.ts";
import { semanticSignature } from "./generation.ts";
import { stringifyJSON } from "./json.ts";

// 运行真实画布处理和历史恢复函数，仅替换 Vue Flow 与浏览器事件边界。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set([
  "moveNode", "deleteSelected", "confirmDeleteCanvasNode", "openCanvasNodeMenu", "openCanvasPaneMenu",
  "syncCanvasSelection", "finishCanvasSelection", "selectCanvasNode", "keydown",
  "checkpoint", "mutate", "restore", "undo", "redo", "rebuildIDs",
  "applyCanvasNodeComment",
]);
const handlers = script.statements.filter(statement => ts.isFunctionDeclaration(statement)
  && names.has(statement.name?.text ?? "")).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 测试图节点独立持有位置及选中标记，模拟 Vue Flow 不直接写入工程的边界。
interface CanvasNode {
  id: string; // 与工程节点对应的稳定身份。
  position: { x: number; y: number }; // 画布内部位置。
  selected: boolean; // 图层的多选标记。
}

// 菜单捕获真实目标对象，允许在确认前主动切换当前选择验证身份保护。
interface CanvasMenu {
  tree: Tree; // 打开菜单时的实际树。
  node?: BTNode; // 打开菜单时的实际节点。
  x: number; // 菜单横坐标。
  y: number; // 菜单纵坐标。
  initialMode?: "menu" | "delete"; // 是否直接进入删除确认。
}

// 使用真实索引、响应式工程和撤销栈，保证批量移动及删除能跨历史恢复。
function session() {
  const initial = blankProject();
  const initialTree = initial.trees[0]!;
  initialTree.nodes.push({ id: "2", type: "wait", codeName: "Wait", durationMs: 1 });
  initialTree.nodes[0]!.children = ["2"];
  initialTree.layout = { [initialTree.root]: { x: 10, y: 20 }, "2": { x: 100, y: 120 } };
  const project = ref(initial);
  const treeID = ref(initialTree.id);
  const treeIdentity = shallowRef(new TreeIdentityIndex(project.value));
  const tree = computed(() => treeIdentity.value.byID.get(treeID.value)!);
  const nodeIdentity = shallowRef(new NodeIdentityIndex(tree.value));
  const nodeIndex = computed(() => nodeIdentity.value.byID);
  const selected = ref(initialTree.root);
  const node = computed(() => nodeIndex.value.get(selected.value));
  watch(tree, current => { nodeIdentity.value = new NodeIdentityIndex(current); }, { flush: "sync" });
  const flowNodes = ref<CanvasNode[]>(tree.value.nodes.map(item => ({
    id: item.id, position: { ...tree.value.layout![item.id]! }, selected: true,
  })));
  const getSelectedNodes = computed(() => flowNodes.value.filter(item => item.selected));
  const selectionCalls: string[][] = [];
  const undoStack = ref<EditorSnapshot[]>([]);
  const redoStack = ref<EditorSnapshot[]>([]);
  const semanticRevision = ref(0);
  const context = {
    nextTick, reactive, captureSnapshot, restoreSnapshot, semanticSignature,
    project, treeID, tree, treeIdentity, nodeIdentity, nodeIndex, selected, node,
    canvasMenu: shallowRef<CanvasMenu>(), workspaceChanging: ref(false), projectReady: ref(true),
    undoStack, redoStack, editRevision: 0, saveState: new ProjectSaveState(), occupiedIDs: new Set<string>(),
    semanticRevision, invalidateCode: () => { semanticRevision.value++; },
    catalogMenu: shallowRef(), catalogDialog: shallowRef(), treeMenu: shallowRef(),
    projectDialog: shallowRef(), importFailure: shallowRef(),
    codeSnapshot: shallowRef(), scaffoldSnapshot: shallowRef(),
    cancelTreeID: () => {}, cancelNodeID: () => {}, followSourceSelection: () => {},
    getNodes: flowNodes, getSelectedNodes,
    findNode: (id: string) => flowNodes.value.find(item => item.id === id),
    addSelectedNodes: (items: CanvasNode[]) => {
      const ids = new Set(items.map(item => item.id));
      selectionCalls.push([...ids]);
      for (const item of flowNodes.value) item.selected = ids.has(item.id);
    },
    removeSelectedElements: () => {
      selectionCalls.push([]);
      for (const item of flowNodes.value) item.selected = false;
    },
    vueFlowRef: { value: { contains: (target: { onCanvas?: boolean }) => target.onCanvas === true } },
  };
  const app = runInNewContext(`${js}\n({ ${[...names].join(", ")} });`, context) as {
    moveNode: (event: { nodes: CanvasNode[] }) => void; // 一次拖动只产生一个历史边界。
    deleteSelected: () => void; // 只请求确认，不直接删除。
    confirmDeleteCanvasNode: () => void; // 确认后校验捕获目标并删除。
    openCanvasNodeMenu: (event: { event: unknown; node: { id: string } }) => void; // 节点右键入口。
    openCanvasPaneMenu: (event: unknown) => void; // 空白右键入口。
    syncCanvasSelection: () => Promise<void>; // 程序化选择同步。
    finishCanvasSelection: () => void; // 框选结束时同步属性面板。
    selectCanvasNode: (event: { node: CanvasNode }) => void; // 单击更新属性主节点。
    keydown: (event: unknown) => void; // 全选及文本输入保护。
    undo: () => void; // 真实撤销入口。
    redo: () => void; // 真实重做入口。
    applyCanvasNodeComment: (value: string) => void; // 向右键捕获目标应用注释草稿。
  };
  return { ...context, app, flowNodes, selectionCalls };
}

// 一次拖动保存整组选中位置、保持节点拓扑，撤销和重做均按整组恢复。
test("批量移动一次保存全部坐标且只记录一次历史", () => {
  const s = session();
  const before = stringifyJSON(s.project.value);
  for (const item of s.flowNodes.value) { item.position.x += 50; item.position.y += 30; }
  s.app.moveNode({ nodes: s.flowNodes.value });
  assert.equal(s.undoStack.value.length, 1);
  assert.equal(s.semanticRevision.value, 0);
  for (const item of s.flowNodes.value) {
    assert.deepEqual({ ...s.tree.value.layout![item.id] }, { ...item.position });
    assert.notEqual(s.tree.value.layout![item.id], item.position);
  }
  const after = stringifyJSON(s.project.value);
  s.app.undo();
  assert.equal(stringifyJSON(s.project.value), before);
  s.app.redo();
  assert.equal(stringifyJSON(s.project.value), after);
});

// 无位移结束、空拖动和已失效节点不污染保存状态及撤销栈。
test("无位移与无效节点拖动不生成历史", () => {
  const s = session();
  const before = stringifyJSON(s.project.value);
  s.app.moveNode({ nodes: s.flowNodes.value });
  s.app.moveNode({ nodes: [] });
  s.app.moveNode({ nodes: [{ id: "missing", position: { x: 1, y: 2 }, selected: true }] });
  assert.equal(s.undoStack.value.length, 0);
  assert.equal(stringifyJSON(s.project.value), before);
});

// 合法特殊身份不能借坐标写入触发对象原型 setter。
test("批量移动兼容特殊节点 ID 并隔离画布位置对象", () => {
  const s = session();
  const special: BTNode = { id: "__proto__", codeName: "Special", type: "wait" };
  s.tree.value.nodes.push(special);
  s.nodeIdentity.value.addNode(s.tree.value.nodes.at(-1)!);
  const position = { x: 8, y: 9 };
  s.app.moveNode({ nodes: [{ id: special.id, position, selected: true }] });
  assert.equal(Object.hasOwn(s.tree.value.layout!, special.id), true);
  position.x = 100;
  assert.equal(s.tree.value.layout![special.id]!.x, 8);
});

// 删除按钮只打开二次确认，取消对应关闭菜单，不产生任何工程或历史修改。
test("请求删除和取消确认均不修改工程", () => {
  const s = session();
  const before = stringifyJSON(s.project.value);
  s.app.deleteSelected();
  assert.equal(s.canvasMenu.value?.initialMode, "delete");
  assert.equal(s.canvasMenu.value?.node, s.node.value);
  assert.equal(s.undoStack.value.length, 0);
  s.canvasMenu.value = undefined;
  s.app.confirmDeleteCanvasNode();
  assert.equal(stringifyJSON(s.project.value), before);
  assert.equal(s.undoStack.value.length, 0);
});

// 确认删除捕获的节点而非后来选中的节点，并在同一历史边界维护相关连线。
test("确认删除固定目标、断开入边且撤销恢复节点及连线", () => {
  const s = session();
  s.selected.value = "2";
  s.app.deleteSelected();
  s.selected.value = s.tree.value.root;
  const before = stringifyJSON(s.project.value);
  s.app.confirmDeleteCanvasNode();
  assert.equal(s.nodeIndex.value.has("2"), false);
  assert.equal(s.nodeIndex.value.has(s.selected.value), true);
  assert.equal(s.tree.value.nodes[0]!.children!.length, 0);
  assert.equal(s.undoStack.value.length, 1);
  assert.equal(s.canvasMenu.value, undefined);
  s.app.undo();
  assert.equal(stringifyJSON(s.project.value), before);
  assert.equal(s.nodeIndex.value.has("2"), true);
});

// 同 ID 的新对象、另一棵树和工作目录切换不能让旧确认误删当前工程。
test("删除确认拒绝过期对象、过期树和切换中的工作目录", () => {
  for (const stale of ["node", "tree", "workspace"] as const) {
    const s = session();
    s.app.deleteSelected();
    if (stale === "node") s.canvasMenu.value!.node = clone(s.node.value!);
    else if (stale === "tree") s.canvasMenu.value!.tree = clone(s.tree.value);
    else s.workspaceChanging.value = true;
    const before = stringifyJSON(s.project.value);
    s.app.confirmDeleteCanvasNode();
    assert.equal(stringifyJSON(s.project.value), before);
    assert.equal(s.undoStack.value.length, 0);
    assert.equal(s.canvasMenu.value, undefined);
  }
});

// 框选与点击已选节点只调整属性主节点，不收缩 Vue Flow 的多选集合。
test("框选结束及主节点同步保留多选", async () => {
  const s = session();
  s.app.finishCanvasSelection();
  await s.app.syncCanvasSelection();
  s.app.selectCanvasNode({ node: s.flowNodes.value[1]! });
  await s.app.syncCanvasSelection();
  assert.equal(s.selected.value, "2");
  assert.equal(s.getSelectedNodes.value.length, 2);
  assert.equal(s.selectionCalls.length, 0);
});

// 程序化定位未选节点应同步新选择，清空属性主节点则清空画布选区。
test("程序化定位新节点及清空选择同步到画布", async () => {
  const s = session();
  s.flowNodes.value[1]!.selected = false;
  s.selected.value = "2";
  await s.app.syncCanvasSelection();
  assert.deepEqual(s.selectionCalls, [["2"]]);
  assert.equal(s.getSelectedNodes.value[0]!.id, "2");
  s.selected.value = "";
  await s.app.syncCanvasSelection();
  assert.equal(s.getSelectedNodes.value.length, 0);
});

// 全选快捷键仅作用于画布，文本输入及打开的确认框均保留各自键盘操作。
test("Ctrl+A 仅在画布生效且确认框阻止后续删除快捷键", () => {
  const s = session();
  s.flowNodes.value[1]!.selected = false;
  let prevented = 0;
  const event = { key: "a", ctrlKey: true, preventDefault: () => { prevented++; }, target: { onCanvas: true, closest: () => null } };
  s.app.keydown({ ...event, target: { onCanvas: false, closest: () => null } });
  s.app.keydown({ ...event, target: { onCanvas: true, closest: () => ({}) } });
  assert.equal(prevented, 0);
  s.app.keydown(event);
  assert.equal(prevented, 1);
  assert.equal(s.getSelectedNodes.value.length, 2);
  s.app.keydown({ ...event, key: "Delete", ctrlKey: false });
  assert.equal(s.canvasMenu.value?.initialMode, "delete");
  s.app.keydown({ ...event, key: "Delete", ctrlKey: false });
  assert.equal(s.undoStack.value.length, 0);
  assert.equal(s.tree.value.nodes.length, 2);
});

// 右键菜单捕获点击目标，不影响已有主节点、多选和未提交属性草稿。
test("节点右键捕获目标，空白右键切换为工程菜单", () => {
  const s = session();
  let prevented = 0;
  let stopped = 0;
  const event = { clientX: 120, clientY: 230, preventDefault: () => { prevented++; }, stopPropagation: () => { stopped++; } };
  s.app.openCanvasNodeMenu({ event, node: { id: "2" } });
  assert.equal(s.canvasMenu.value?.node?.id, "2");
  assert.equal(s.selected.value, s.tree.value.root);
  assert.equal(s.getSelectedNodes.value.length, 2);
  assert.equal(stopped, 1);
  s.app.openCanvasPaneMenu(event);
  assert.equal(s.canvasMenu.value?.node, undefined);
  assert.equal(s.canvasMenu.value?.x, 120);
  assert.equal(s.canvasMenu.value?.y, 230);
  assert.equal(prevented, 2);
  assert.equal(s.undoStack.value.length, 0);
});

// 注释只修改右击目标，每次应用一次历史；撤销重做、脏状态与生成修订一起恢复。
test("注释填写修改与清空均可撤销重做并保持多选", () => {
  const s = session();
  const before = stringifyJSON(s.project.value);
  s.saveState.reset(before);
  const open = () => { s.canvasMenu.value = { tree: s.tree.value, node: s.nodeIndex.value.get("2"), x: 0, y: 0 }; };
  open();
  s.app.applyCanvasNodeComment("  首行\n第二行  ");
  assert.equal(s.nodeIndex.value.get("2")!.comment, "首行\n第二行");
  assert.equal(s.node.value!.comment, undefined);
  assert.equal(s.undoStack.value.length, 1);
  assert.equal(s.semanticRevision.value, 1);
  assert.equal(s.saveState.dirty, true);
  assert.equal(s.canvasMenu.value, undefined);
  assert.equal(s.getSelectedNodes.value.length, 2);
  s.app.undo();
  assert.equal(stringifyJSON(s.project.value), before);
  assert.equal(s.saveState.dirty, false);
  s.app.redo();
  assert.equal(s.nodeIndex.value.get("2")!.comment, "首行\n第二行");
  assert.equal(s.saveState.dirty, true);
  open();
  s.app.applyCanvasNodeComment("修改后");
  assert.equal(s.nodeIndex.value.get("2")!.comment, "修改后");
  open();
  s.app.applyCanvasNodeComment(" \n ");
  assert.equal(Object.hasOwn(s.nodeIndex.value.get("2")!, "comment"), false);
  s.app.undo();
  assert.equal(s.nodeIndex.value.get("2")!.comment, "修改后");
  s.app.redo();
  assert.equal(s.nodeIndex.value.get("2")!.comment, undefined);
  assert.equal(s.selectionCalls.length, 0);
});

// 取消、同内容和空白的重复提交不能产生脏状态或令已有生成结果过期。
test("取消与未变化的注释不生成历史", () => {
  const s = session();
  s.node.value!.comment = "已有说明";
  const before = stringifyJSON(s.project.value);
  s.canvasMenu.value = { tree: s.tree.value, node: s.node.value, x: 0, y: 0 };
  s.canvasMenu.value = undefined;
  s.app.applyCanvasNodeComment("取消后的草稿");
  s.canvasMenu.value = { tree: s.tree.value, node: s.node.value, x: 0, y: 0 };
  s.app.applyCanvasNodeComment("  已有说明  ");
  s.canvasMenu.value = { tree: s.tree.value, node: s.nodeIndex.value.get("2"), x: 0, y: 0 };
  s.app.applyCanvasNodeComment(" \n ");
  assert.equal(stringifyJSON(s.project.value), before);
  assert.equal(s.undoStack.value.length, 0);
  assert.equal(s.semanticRevision.value, 0);
});

// 同 ID 的替换对象、已删除目标、另一棵树及工程切换都不能接收过期注释草稿。
test("注释提交拒绝过期目标及不可编辑工程", () => {
  for (const stale of ["node", "deleted", "tree", "workspace", "not-ready", "pane"] as const) {
    const s = session();
    s.canvasMenu.value = { tree: s.tree.value, node: s.node.value, x: 0, y: 0 };
    if (stale === "node") s.canvasMenu.value.node = clone(s.node.value!);
    else if (stale === "deleted") s.nodeIndex.value.delete(s.node.value!.id);
    else if (stale === "tree") s.canvasMenu.value.tree = clone(s.tree.value);
    else if (stale === "workspace") s.workspaceChanging.value = true;
    else if (stale === "not-ready") s.projectReady.value = false;
    else s.canvasMenu.value.node = undefined;
    const before = stringifyJSON(s.project.value);
    s.app.applyCanvasNodeComment("不可提交");
    assert.equal(stringifyJSON(s.project.value), before);
    assert.equal(s.undoStack.value.length, 0);
    assert.equal(s.canvasMenu.value, undefined);
  }
});

// 右键平移及缩放动画中经过指针的节点不能重新弹出说明，结束后正常悬浮可恢复。
test("按键拖动和视口移动期间不打开节点注释", () => {
  const declaration = script.statements.find(statement => ts.isFunctionDeclaration(statement)
    && statement.name?.text === "showCanvasNodeComment");
  assert.ok(declaration);
  const handler = ts.transpileModule(declaration.getText(script), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  // 仅替换 DOM 边界，目标查询和交互状态判断执行真实应用函数。
  class CanvasElement {
    // 模拟事件元素所属的画布节点。
    closest() { return this; }
  }
  const element = new CanvasElement();
  const target: BTNode = { id: "2", type: "wait", comment: "说明" };
  const shown: BTNode[] = [];
  const canvasMoving = ref(false);
  const show = runInNewContext(`${handler}\nshowCanvasNodeComment;`, {
    Element: CanvasElement, canvasMoving, nodeIndex: { value: new Map([[target.id, target]]) },
    nodeCommentTooltip: { value: { show: (node: BTNode) => { shown.push(node); } } },
  }) as (event: { event: { buttons: number; target: CanvasElement }; node: { id: string } }) => void;
  for (const buttons of [1, 2, 4]) show({ event: { buttons, target: element }, node: { id: "2" } });
  canvasMoving.value = true;
  show({ event: { buttons: 0, target: element }, node: { id: "2" } });
  assert.equal(shown.length, 0);
  canvasMoving.value = false;
  show({ event: { buttons: 0, target: element }, node: { id: "missing" } });
  assert.equal(shown.length, 0);
  show({ event: { buttons: 0, target: element }, node: { id: "2" } });
  assert.deepEqual(shown, [target]);
});
