import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { blankProject } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { ProjectSaveState } from "./saveState.ts";
import { GenerationRequests } from "./generation.ts";
import { TreeIdentityIndex } from "./treeIdentity.ts";
import { normalizeCodeNames } from "./codeNames.ts";
import { validateProjectTypes } from "./enums.ts";
import { startupProject, rememberProject, recentProjectKey } from "./workspace.ts";
import type { ProjectDialogResult } from "./workspace.ts";

// 提取并执行实际应用函数，仅以可控网络、对话框和画布边界替代浏览器环境。
const appSource = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", appSource, ts.ScriptTarget.Latest, true);
const functions = new Set([
  "request", "action", "notice", "recentStorage", "saveCurrent", "save", "allowReplacement", "chooseWorkspace",
  "openCurrent", "loadProject", "installProject", "resetResults", "invalidateCode", "rebuildIDs", "keydown",
]);
const source = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && functions.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError"),
).map(statement => statement.getText(script)).join("\n");
const workflowJS = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 响应闸门精确控制切换请求的中间状态，不依赖计时或轮询。
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

// 会话配置明确描述用户选择和服务端结果，失败不会预先改变客户端状态。
interface SessionOptions {
  dirty?: boolean; // 当前工程是否有未保存修改。
  name?: string; // 空字符串表示尚未保存的导入草稿。
  choices?: (ProjectDialogResult | undefined)[]; // 按实际对话框出现顺序作答。
  saveError?: boolean; // 保存失败时返回磁盘错误。
  switchError?: boolean; // 目录切换失败时返回不可用目录错误。
  targetFiles?: string[]; // 新工作目录的顶层工程候选。
  switchWait?: Promise<void>; // 延迟完成工作目录切换。
  switchStarted?: () => void; // 标记请求已发出，供测试进入在途阶段。
}

// 建立完整文件身份、草稿历史及源码状态，实际工作流负责其全部转换。
function session(options: SessionOptions = {}) {
  const project = { value: blankProject() };
  const saveState = new ProjectSaveState();
  const name = options.name ?? "original.json";
  saveState.reset(name ? stringifyJSON(project.value) : undefined);
  if (options.dirty) {
    project.value.name = "尚未保存的原工程";
    saveState.changed();
  }
  const choices = [...(options.choices ?? [])];
  const dialogs: string[] = [];
  const calls: { path: string; body: any; workspace: string }[] = [];
  const records = new Map<string, string>();
  const targetProject = blankProject();
  targetProject.name = "目标目录工程";
  let serverWorkspace = "E:/original";
  const context = {
    Error, stringifyJSON, parseJSON, project, saveState, TreeIdentityIndex, normalizeCodeNames, validateProjectTypes,
    startupProject, rememberProject,
    projectReady: { value: true }, fileName: { value: name }, suggestedName: { value: "original.json" },
    workspace: { value: serverWorkspace }, files: { value: name ? [name] : [] },
    allFiles: { value: name ? [name] : [] },
    dirty: { get value() { return saveState.dirty; } },
    busy: { value: false }, workspaceChanging: { value: false }, message: { value: "" }, error: { value: false },
    diagnostics: { value: [{ message: "旧诊断" }] }, semanticRevision: { value: 0 }, editRevision: 0,
    generationRequests: new GenerationRequests(),
    codeSnapshot: { value: { source: "旧目录源码" } }, scaffoldSnapshot: { value: { source: "旧业务骨架" } },
    treeID: { value: project.value.trees[0]!.id }, selected: { value: project.value.trees[0]!.root },
    treeIdentity: { value: new TreeIdentityIndex(project.value) }, occupiedIDs: new Set<string>(),
    undoStack: { value: ["原撤销记录"] }, redoStack: { value: ["原重做记录"] },
    workspaceError: { value: "" }, failedOpen: { value: "" }, openingName: { value: "" }, inspectorOpen: { value: true },
    catalogDialog: { value: undefined }, projectDialog: { value: undefined },
    importFailure: { value: undefined },
    window: { localStorage: { getItem: (key: string) => records.get(key) ?? null, setItem: (key: string, value: string) => records.set(key, value) } },
    askProject: async (kind: string) => { dialogs.push(kind); return choices.shift(); },
    showOutput: () => {}, cancelTreeID: () => {}, cancelNodeID: () => {},
    nextTick: async (fn?: () => void) => fn?.(), updateOutputBounds: () => {}, fitView: () => {}, readGenerated: async () => {},
    fetch: async (path: string, init: { body?: string; headers: Record<string, string> }) => {
      const body = init.body ? parseJSON<any>(init.body) : undefined;
      calls.push({ path, body, workspace: decodeURIComponent(init.headers["X-BT-Workspace"]!) });
      let status = 200;
      let data: unknown;
      if (path === "/api/workspace") {
        options.switchStarted?.();
        await options.switchWait;
        if (options.switchError) { status = 400; data = { error: "目标目录不可用" }; }
        else { serverWorkspace = body.directory; data = { workspace: serverWorkspace, files: options.targetFiles ?? [] }; }
      } else if (path === "/api/project") {
        if (options.saveError) { status = 500; data = { error: "磁盘写入失败" }; }
        else {
          const moved = body.directory && body.directory !== serverWorkspace;
          serverWorkspace = body.directory ?? serverWorkspace;
          data = { workspace: serverWorkspace, ...(moved ? { files: [body.name, "existing.json"], allFiles: [body.name, "existing.json", "invalid.json"] } : {}) };
        }
      } else if (path.startsWith("/api/project?")) data = targetProject;
      else throw new Error(`未模拟的请求 ${path}`);
      return { ok: status === 200, status, text: async () => stringifyJSON(data) };
    },
  };
  const workflow = runInNewContext(`${workflowJS}\n({ save, chooseWorkspace, keydown });`, context) as {
    save: (saveAs?: boolean) => Promise<void>; // 执行真实串行保存入口。
    chooseWorkspace: () => Promise<void>; // 执行真实目录切换入口。
    keydown: (event: unknown) => void; // 验证切换期间真实键盘处理边界。
  };
  return { context, calls, dialogs, records, workflow };
}

// 保存成功后身份与最近工程归属目标目录，后续保存必须携带新的目录身份。
test("跨目录另存为更新身份与候选列表，清除旧源码并继续保存到新目录", async () => {
  const s = session({ dirty: true, choices: [{ name: "copy.json", directory: "E:/目标 目录", overwrite: true }] });
  await s.workflow.save(true);
  assert.equal(s.calls[0]!.workspace, "E:/original");
  assert.equal(s.calls[0]!.body.directory, "E:/目标 目录");
  assert.equal(s.calls[0]!.body.overwrite, true);
  assert.equal(s.context.workspace.value, "E:/目标 目录");
  assert.equal(s.context.fileName.value, "copy.json");
  assert.deepEqual(Array.from(s.context.files.value), ["copy.json", "existing.json"]);
  // 无效 JSON 不出现在工程选择中，但仍参与另存为的同名覆盖确认。
  assert.deepEqual(Array.from(s.context.allFiles.value), ["copy.json", "existing.json", "invalid.json"]);
  assert.equal(s.context.codeSnapshot.value, undefined);
  assert.equal(s.context.dirty.value, false);
  assert.equal(s.records.get(recentProjectKey("E:/目标 目录")), "copy.json");
  await s.workflow.save();
  assert.equal(s.calls[1]!.workspace, "E:/目标 目录");
  assert.equal(s.calls[1]!.body.name, "copy.json");
  assert.deepEqual(s.dialogs, ["save"]);
});

// 保存请求失败前不安装目标身份，保留原草稿、撤销记录和生成内容供继续编辑。
test("另存为失败保留原目录、文件身份和未保存草稿", async () => {
  const s = session({ dirty: true, saveError: true, choices: [{ name: "copy.json", directory: "E:/target", overwrite: false }] });
  const original = stringifyJSON(s.context.project.value);
  await s.workflow.save(true);
  assert.equal(s.context.workspace.value, "E:/original");
  assert.equal(s.context.fileName.value, "original.json");
  assert.equal(stringifyJSON(s.context.project.value), original);
  assert.equal(s.context.dirty.value, true);
  assert.deepEqual(s.context.undoStack.value, ["原撤销记录"]);
  assert.equal(s.context.codeSnapshot.value?.source, "旧目录源码");
  assert.equal(s.context.message.value, "磁盘写入失败");
  assert.equal(s.records.size, 0);
});

// 目录选择取消、保留修改取消及首次保存取消都必须在网络写入之前停止切换。
test("切换目录各阶段取消均保留原工程且不发送请求", async () => {
  const target = { name: "", directory: "E:/target", overwrite: false };
  for (const options of [
    { dirty: true, choices: [undefined] },
    { dirty: true, choices: [target, undefined] },
    { name: "", choices: [target, "save" as const, undefined] },
  ]) {
    const s = session(options);
    const original = stringifyJSON(s.context.project.value);
    const name = s.context.fileName.value;
    await s.workflow.chooseWorkspace();
    assert.deepEqual(s.calls, []);
    assert.equal(s.context.workspace.value, "E:/original");
    assert.equal(s.context.fileName.value, name);
    assert.equal(stringifyJSON(s.context.project.value), original);
    assert.equal(s.context.dirty.value, true);
    assert.deepEqual(s.context.undoStack.value, ["原撤销记录"]);
    assert.equal(s.context.workspaceChanging.value, false);
  }
});

// 保存并切换必须等待保存成功；保存失败或目标不可用均不能清空当前工程。
test("切换前保存失败或目标目录失败保留当前工程", async () => {
  for (const saveError of [true, false]) {
    const s = session({ dirty: true, saveError, switchError: !saveError, choices: [
      { name: "", directory: "E:/target", overwrite: false }, saveError ? "save" : "discard",
    ] });
    const original = stringifyJSON(s.context.project.value);
    await s.workflow.chooseWorkspace();
    assert.deepEqual(s.calls.map(call => call.path), [saveError ? "/api/project" : "/api/workspace"]);
    assert.equal(s.context.workspace.value, "E:/original");
    assert.equal(s.context.fileName.value, "original.json");
    assert.equal(stringifyJSON(s.context.project.value), original);
    assert.equal(s.context.dirty.value, true);
    assert.deepEqual(s.context.undoStack.value, ["原撤销记录"]);
    assert.equal(s.context.codeSnapshot.value?.source, "旧目录源码");
    assert.equal(s.context.workspaceChanging.value, false);
    assert.equal(s.context.error.value, true);
  }
});

// 确认保存后先写旧目录，再切换并加载目标单候选，历史与最近工程不串目录。
test("保存并切换按旧目录保存、新目录读取顺序完成真实加载", async () => {
  const s = session({ dirty: true, targetFiles: ["target.json"], choices: [
    { name: "", directory: "E:/target", overwrite: false }, "save",
  ] });
  await s.workflow.chooseWorkspace();
  assert.deepEqual(s.calls.map(call => [call.path, call.workspace]), [
    ["/api/project", "E:/original"], ["/api/workspace", "E:/original"], ["/api/project?name=target.json", "E:/target"],
  ]);
  assert.equal(s.context.workspace.value, "E:/target");
  assert.equal(s.context.fileName.value, "target.json");
  assert.equal(s.context.project.value.name, "目标目录工程");
  assert.equal(s.context.projectReady.value, true);
  assert.equal(s.context.dirty.value, false);
  assert.equal(s.context.undoStack.value.length, 0);
  assert.equal(s.records.get(recentProjectKey("E:/original")), "original.json");
  assert.equal(s.records.get(recentProjectKey("E:/target")), "target.json");
});

// 请求在途阶段冻结快捷键，响应成功后空目录保持未打开状态且恢复交互。
test("切换请求期间冻结快捷键，进入空目录后重置文件与历史", async () => {
  const started = deferred(), pending = deferred();
  const s = session({ switchWait: pending.promise, switchStarted: started.resolve, choices: [
    { name: "", directory: "E:/empty", overwrite: false },
  ] });
  const finished = s.workflow.chooseWorkspace();
  await started.promise;
  assert.equal(s.context.workspaceChanging.value, true);
  assert.equal(s.context.workspace.value, "E:/original");
  let touched = false;
  s.workflow.keydown({ key: "s", ctrlKey: true, preventDefault: () => { touched = true; } });
  assert.equal(touched, false);
  assert.equal(s.calls.length, 1);
  pending.resolve();
  await finished;
  assert.equal(s.context.workspace.value, "E:/empty");
  assert.equal(s.context.fileName.value, "");
  assert.equal(s.context.projectReady.value, false);
  assert.equal(s.context.undoStack.value.length, 0);
  assert.equal(s.context.redoStack.value.length, 0);
  assert.equal(s.context.codeSnapshot.value, undefined);
  assert.equal(s.context.workspaceChanging.value, false);
  assert.equal(s.context.busy.value, false);
});
