import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { setImmediate } from "node:timers/promises";
import ts from "typescript";
import { computed, nextTick, reactive, ref, watch } from "vue";
import { blankProject } from "./project.ts";
import { createSourceIndex, sourceNodeKey } from "./generation.ts";
import { createScaffoldSourceIndex, selectedScaffoldCode } from "./scaffoldNavigation.ts";

// 执行组件真实响应式逻辑，只替换浏览器布局和剪贴板边界。
function componentScript(file: string, names?: string[]) {
  const source = readFileSync(new URL(file, import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
  const script = ts.createSourceFile("component.ts", source, ts.ScriptTarget.Latest, true);
  const code = script.statements.filter(statement => names
    ? ts.isFunctionDeclaration(statement) && names.includes(statement.name?.text ?? "")
    : !ts.isImportDeclaration(statement)).map(statement => statement.getText(script)).join("\n");
  return ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
}

// 节点选择、共享函数切换和过期恢复应驱动实际滚动及高亮状态。
test("业务骨架选中节点后滚动到函数，过期时清除定位和复制入口", async () => {
  const project = blankProject();
  project.catalog = [{ id: "run", name: "动作", kind: "action", goName: "Run" }];
  project.trees = [{ id: "a", name: "树", root: "one", nodes: [
    { id: "one", type: "action", binding: "run" },
    { id: "two", type: "action", binding: "run" },
  ] }];
  const source = "package behavior\n" + "\n".repeat(20) + "func Run() {\n\treturn\n}\n";
  const props = reactive({ source, sourceIndex: createScaffoldSourceIndex(source, project), treeId: "a", nodeId: "one", stale: false, copySelection: true });
  const scope = runInNewContext(`${componentScript("./SourceViewer.vue")}\n({ viewport, locations, visible });`, {
    computed, nextTick, ref, watch, createSourceIndex, sourceNodeKey, selectedScaffoldCode,
    defineProps: () => props, defineEmits: () => () => {}, onMounted: () => {}, onUnmounted: () => {},
  });
  scope.viewport.value = { scrollTop: 0 };
  // VM 的异步回调跨 Promise realm，等待本轮微任务全部完成再检查布局。
  await setImmediate();
  assert.equal(scope.locations.value.length, 1);
  assert.equal(scope.viewport.value.scrollTop, (22 - 3) * 22);
  assert.ok(scope.visible.value.some((row: { active: boolean }) => row.active));
  scope.viewport.value.scrollTop = 0;
  props.nodeId = "two";
  await setImmediate();
  assert.equal(scope.viewport.value.scrollTop, (22 - 3) * 22);
  props.stale = true;
  await nextTick();
  assert.equal(scope.locations.value.length, 0);
  assert.ok(scope.visible.value.every((row: { active: boolean; location: unknown }) => !row.active && !row.location));
  props.stale = false;
  await setImmediate();
  assert.equal(scope.locations.value[0].nodeId, "two");
  props.nodeId = "unbound";
  await nextTick();
  assert.equal(scope.locations.value.length, 0);
});

// 复制按钮提交完整所选函数，过期不写剪贴板，失败时反馈明确错误。
test("复制骨架选中代码使用真实剪贴板流程并处理过期及失败", async () => {
  const copied: string[] = [];
  const notices: { message: string; failed?: boolean }[] = [];
  const scaffoldStale = { value: false };
  let fail = false;
  const copy = runInNewContext(`${componentScript("./App.vue", ["copyScaffoldCode"])}\ncopyScaffoldCode;`, {
    scaffoldStale,
    navigator: { clipboard: { writeText: async (text: string) => { if (fail) throw new Error("denied"); copied.push(text); } } },
    notice: (message: string, failed?: boolean) => notices.push({ message, failed }),
  });
  const code = "func Run() {\n\treturn\n}";
  await copy(code);
  assert.deepEqual(copied, [code]);
  assert.match(notices[0]!.message, /已复制/);
  scaffoldStale.value = true;
  await copy(code);
  assert.equal(copied.length, 1);
  scaffoldStale.value = false;
  fail = true;
  await copy(code);
  assert.equal(notices.at(-1)!.failed, true);
});
