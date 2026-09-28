import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { ref } from "vue";

// 提取真实导航守卫，模拟输入焦点与页面状态，验证同步清空草稿前已阻止切换。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["guardPendingNavigation", "navigateTree", "navigateNode", "navigateTab", "openEventManager"]);
const handlers = script.statements.filter(statement => ts.isFunctionDeclaration(statement)
  && names.has(statement.name?.text ?? "")).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 输入与注释浮层使用最小替身，保留真实导航函数的调用顺序。
function session() {
  const notices: string[] = [];
  let focused = 0;
  const input = { scrollIntoView() {}, focus() { focused++; } };
  const context = {
    treeID: ref("tree-a"), selected: ref("node-a"), tab: ref("nodes"), eventManagerOpen: ref(false), inspectorOpen: ref(false),
    treeIDPending: ref(false), codeNamePending: ref(false), nodeIDPending: ref(false),
    treeIDInput: ref(input), codeNameInput: ref(input), nodeIDInput: ref(input),
    nodeCommentTooltip: ref<{ hasPending: boolean; focusPending: () => void }>(),
    eventOverlay: ref<{ hasPending: boolean; focusPending: () => void }>(),
    catalogBrowser: ref<{ hasPending: boolean; focusPending: () => void }>(),
    catalogManager: ref<{ hasPending: boolean; focusPending: () => void }>(),
    eventManager: ref<{ hasPending: boolean; focusPending: () => void }>(),
    treeContextMenu: ref<{ hasPending: boolean; focusPending: () => void }>(),
    projectDialogView: ref<{ hasPending: boolean; focusPending: () => void }>(),
    notice: (message: string) => notices.push(message),
  };
  const app = runInNewContext(`${js}\n({ navigateTree, navigateNode, navigateTab, openEventManager });`, context) as {
    navigateTree(id: string): void; // 切换行为树之前检查草稿。
    navigateNode(id: string): void; // 切换节点之前检查草稿。
    navigateTab(id: string): void; // 切换侧栏页之前检查草稿。
    openEventManager(): void; // 打开事件页之前检查草稿。
  };
  return { ...context, app, notices, focused: () => focused };
}

// 节点代码名和树 ID 在应用或取消之前必须保留原选择，并定位原输入框。
test("身份草稿阻止节点与树切换，处理后允许切换", () => {
  const s = session();
  s.codeNamePending.value = true;
  s.app.navigateNode("node-b");
  assert.equal(s.selected.value, "node-a");
  assert.equal(s.focused(), 1);
  assert.equal(s.inspectorOpen.value, true);
  assert.match(s.notices[0]!, /节点代码名.*未应用/);
  s.codeNamePending.value = false;
  s.app.navigateNode("node-b");
  assert.equal(s.selected.value, "node-b");
  s.treeIDPending.value = true;
  s.app.navigateTree("tree-b");
  assert.equal(s.treeID.value, "tree-a");
  s.treeIDPending.value = false;
  s.app.navigateTree("tree-b");
  assert.equal(s.treeID.value, "tree-b");
});

// 注释和分类弹窗草稿同样阻止页面切换，原表单处理后才开放导航。
test("注释及分类草稿阻止切页与打开事件管理", () => {
  const s = session();
  let focused = 0;
  s.nodeCommentTooltip.value = { hasPending: true, focusPending: () => { focused++; } };
  s.app.navigateTab("board");
  assert.equal(s.tab.value, "nodes");
  assert.equal(focused, 1);
  s.nodeCommentTooltip.value.hasPending = false;
  s.catalogBrowser.value = { hasPending: true, focusPending: () => { focused++; } };
  s.app.openEventManager();
  assert.equal(s.eventManagerOpen.value, false);
  assert.equal(focused, 2);
  s.catalogBrowser.value.hasPending = false;
  s.app.navigateTab("board");
  s.app.openEventManager();
  assert.equal(s.tab.value, "board");
  assert.equal(s.eventManagerOpen.value, true);
});
