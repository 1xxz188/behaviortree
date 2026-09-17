import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { blankProject } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { validateProjectTypes } from "./enums.ts";
import { TreeIdentityIndex } from "./treeIdentity.ts";
import { normalizeCodeNames } from "./codeNames.ts";
import { ProjectSaveState } from "./saveState.ts";

// 执行应用中的真实导入、错误分流与安装函数，仅替代浏览器和网络边界。
const appSource = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", appSource, ts.ScriptTarget.Latest, true);
const names = new Set(["importProject", "action", "notice", "request", "loadProject", "allowReplacement", "installProject"]);
const source = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError"),
).map(statement => statement.getText(script)).join("\n");
const workflowJS = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 可控响应覆盖后端校验拒绝、用户取消和读取期间产生新修改的情况。
interface ImportOptions {
  serverError?: string; // 服务端返回的工程格式错误。
  dirty?: boolean; // 导入前是否存在未保存内容。
  choice?: "discard" | "save"; // 未保存确认框的选择，缺省表示取消。
}

// 建立带历史和文件身份的原工程，便于确认失败时没有部分替换。
function session(options: ImportOptions = {}) {
  const project = { value: blankProject() };
  const originalProject = project.value;
  project.value.name = "导入前工程";
  const saveState = new ProjectSaveState();
  saveState.reset(stringifyJSON(project.value));
  if (options.dirty) saveState.changed();
  const requests: unknown[] = [];
  const dialogs: string[] = [];
  let resets = 0;
  const context = {
    Error, parseJSON, stringifyJSON, validateProjectTypes, TreeIdentityIndex, normalizeCodeNames,
    project, saveState, projectReady: { value: true },
    dirty: { get value() { return saveState.dirty; } },
    fileName: { value: "original.json" }, suggestedName: { value: "original.json" },
    workspace: { value: "E:/original" }, editRevision: 0,
    treeID: { value: project.value.trees[0]!.id }, selected: { value: project.value.trees[0]!.root },
    undoStack: { value: ["原撤销记录"] }, redoStack: { value: ["原重做记录"] },
    busy: { value: false }, message: { value: "原状态" }, error: { value: false },
    diagnostics: { value: [] }, importFailure: { value: undefined as { name: string; message: string } | undefined },
    workspaceError: { value: "" }, failedOpen: { value: "" }, inspectorOpen: { value: true },
    askProject: async (kind: string) => { dialogs.push(kind); return options.choice; },
    saveCurrent: async () => true,
    ensureIdentityDraftsApplied: () => true,
    showOutput: () => {}, resetResults: () => { resets++; },
    nextTick: async (fn?: () => void) => fn?.(), updateOutputBounds: () => {}, fitView: () => {},
    fetch: async (path: string, init: { body: string }) => {
      assert.equal(path, "/api/import");
      const body = parseJSON(init.body);
      requests.push(body);
      return {
        ok: !options.serverError, status: options.serverError ? 400 : 200,
        text: async () => stringifyJSON(options.serverError ? { error: options.serverError } : body),
      };
    },
  };
  const workflow = runInNewContext(`${workflowJS}\n({ importProject, action });`, context) as {
    importProject: (event: unknown) => Promise<void>; // 接收文件选择事件并运行真实导入流程。
    action: (fn: () => Promise<void>) => Promise<void>; // 验证其他操作保留原状态栏错误行为。
  };
  return { context, workflow, requests, dialogs, originalProject, resetCount: () => resets };
}

// 模拟同一文件选择器，记录读取次数并允许重新选择同名修正后的内容。
function fileInput(text: string, name = "incoming.json") {
  let reads = 0;
  const input = { value: name, files: [{ name, text: async () => { reads++; return text; } }] };
  return { input, event: { target: input }, reads: () => reads };
}

// 失败只能设置模态错误内容，原工程、文件身份、草稿历史和状态栏必须保持不变。
function assertPreserved(s: ReturnType<typeof session>) {
  assert.equal(s.context.project.value, s.originalProject);
  assert.equal(s.context.fileName.value, "original.json");
  assert.equal(s.context.suggestedName.value, "original.json");
  assert.deepEqual(s.context.undoStack.value, ["原撤销记录"]);
  assert.deepEqual(s.context.redoStack.value, ["原重做记录"]);
  assert.equal(s.context.selected.value, s.originalProject.trees[0]!.root);
  assert.equal(s.context.message.value, "原状态");
  assert.equal(s.context.error.value, false);
  assert.equal(s.context.busy.value, false);
  assert.equal(s.resetCount(), 0);
}

// JSON 语法错误应直接进入模态提示，且不能向服务端提交内容。
test("导入语法无效 JSON 弹窗并保留原工程", async () => {
  const s = session();
  const file = fileInput('{"schemaVersion":');
  await s.workflow.importProject(file.event);
  assert.equal(s.context.importFailure.value?.name, "incoming.json");
  assert.ok(s.context.importFailure.value?.message);
  assert.equal(s.requests.length, 0);
  assert.equal(file.input.value, "");
  assertPreserved(s);
});

// 本地类型检查拒绝未知节点类型时，弹窗保留字段位置以便定位文件问题。
test("导入节点类型错误弹窗显示具体字段", async () => {
  const s = session();
  const incoming = stringifyJSON(blankProject()).replace('"sequence"', '"invalid-node"');
  const file = fileInput(incoming);
  await s.workflow.importProject(file.event);
  assert.match(s.context.importFailure.value?.message ?? "", /trees\[0\]\.nodes\[0\]\.type.*invalid-node/);
  assert.equal(s.requests.length, 0);
  assert.equal(file.input.value, "");
  assertPreserved(s);
});

// 服务端未知字段错误原样进入弹窗，用户修正同一文件后可以再次导入。
test("后端拒绝 unknown field 后清空选择器并允许再次选择同文件", async () => {
  const options: ImportOptions = { serverError: '读取行为树工程: json: unknown field "exclude"' };
  const s = session(options);
  const file = fileInput(stringifyJSON(blankProject()), "a_test.json");
  await s.workflow.importProject(file.event);
  assert.equal(s.context.importFailure.value?.name, "a_test.json");
  assert.equal(s.context.importFailure.value?.message, options.serverError);
  assert.equal(file.input.value, "");
  assertPreserved(s);
  s.context.importFailure.value = undefined;
  options.serverError = undefined;
  file.input.value = "a_test.json";
  await s.workflow.importProject(file.event);
  assert.equal(s.requests.length, 2);
  assert.equal(file.reads(), 2);
  assert.equal(file.input.value, "");
  assert.equal(s.context.importFailure.value, undefined);
  assert.equal(s.context.fileName.value, "");
  assert.equal(s.context.suggestedName.value, "a_test.json");
  assert.equal(s.context.dirty.value, true);
});

// 有效工程成功安装为未保存草稿，历史与旧输出只在成功时重置。
test("有效 JSON 导入为新草稿并正常显示成功状态", async () => {
  const s = session();
  const incoming = blankProject();
  incoming.name = "新导入工程";
  const file = fileInput(stringifyJSON(incoming));
  await s.workflow.importProject(file.event);
  assert.equal(s.context.project.value.name, "新导入工程");
  assert.equal(s.context.fileName.value, "");
  assert.equal(s.context.suggestedName.value, "incoming.json");
  assert.equal(s.context.dirty.value, true);
  assert.equal(s.context.undoStack.value.length, 0);
  assert.equal(s.context.redoStack.value.length, 0);
  assert.equal(s.resetCount(), 1);
  assert.equal(s.context.importFailure.value, undefined);
  assert.equal(s.context.message.value, "已导入 incoming.json，尚未保存");
  assert.equal(s.context.busy.value, false);
  assert.equal(file.input.value, "");
});

// 未保存修改确认取消后不读文件、不请求服务端，也不显示错误弹窗。
test("取消导入保留未保存草稿且不弹错误窗口", async () => {
  const s = session({ dirty: true });
  const file = fileInput("invalid json");
  await s.workflow.importProject(file.event);
  assert.deepEqual(s.dialogs, ["switch"]);
  assert.equal(file.reads(), 0);
  assert.equal(s.requests.length, 0);
  assert.equal(s.context.importFailure.value, undefined);
  assert.equal(s.context.dirty.value, true);
  assert.equal(file.input.value, "");
  assertPreserved(s);
});

// 可选错误回调只影响导入，其他文件操作继续使用状态栏错误提示。
test("未提供错误回调的操作仍在状态栏显示错误", async () => {
  const s = session();
  await s.workflow.action(async () => { throw new Error("磁盘读取失败"); });
  assert.equal(s.context.message.value, "磁盘读取失败");
  assert.equal(s.context.error.value, true);
  assert.equal(s.context.importFailure.value, undefined);
  assert.equal(s.context.busy.value, false);
});
