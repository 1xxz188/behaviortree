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
import { startupProject, readTabSession, rememberTabSession, rememberProject, recentProjectKey, tabSessionKey } from "./workspace.ts";
import type { ProjectDialogResult } from "./workspace.ts";

// 提取并执行实际应用函数，仅以可控网络、对话框和画布边界替代浏览器环境。
const appSource = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", appSource, ts.ScriptTarget.Latest, true);
const functions = new Set([
  "request", "action", "notice", "recentStorage", "projectHistoryStorage", "refreshFiles", "initializeWorkspace", "saveCurrent", "save", "allowReplacement", "chooseWorkspace",
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
  initialFiles?: string[]; // 默认工作目录的工程候选。
  switchWait?: Promise<void>; // 延迟完成工作目录切换。
  switchStarted?: () => void; // 标记请求已发出，供测试进入在途阶段。
  records?: Map<string, string>; // 注入同一页签的存储，以测试刷新恢复。
  localRecords?: Map<string, string>; // 注入浏览器共享的最近工程记录。
  ready?: boolean; // false 模拟首屏尚未打开工程。
  invalidWorkspace?: boolean; // 恢复目录不可读取时服务端拒绝列表请求。
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
  const records = options.records ?? new Map<string, string>();
  const localRecords = options.localRecords ?? new Map<string, string>();
  const targetProject = blankProject();
  targetProject.name = "目标目录工程";
  const serverWorkspace = "E:/original";
  const context = {
    generationSaved: { value: { ...project.value.generation } }, // 工程切换后重置生成设置基准。
    noticeRevision: { value: 0 }, // 实际发布操作结果的版本。
    ensureIdentityDraftsApplied: () => true,
    Error, stringifyJSON, parseJSON, project, saveState, TreeIdentityIndex, normalizeCodeNames, validateProjectTypes,
    startupProject, readTabSession, rememberTabSession, rememberProject,
    projectReady: { value: options.ready ?? true }, fileName: { value: name }, suggestedName: { value: "original.json" },
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
    workspaceError: { value: "" }, failedOpen: { value: "" }, openingName: { value: "" }, inspectorOpen: { value: true }, initializing: { value: options.ready ?? true },
    catalogDialog: { value: undefined }, projectDialog: { value: undefined },
    eventManagerOpen: { value: false }, highlightedEventIDs: { value: new Set<string>() }, eventScope: { value: "tree" },
    importFailure: { value: undefined }, treeMenu: { value: undefined }, catalogMenu: { value: undefined }, canvasMenu: { value: undefined },
    window: {
      sessionStorage: { getItem: (key: string) => records.get(key) ?? null, setItem: (key: string, value: string) => records.set(key, value) },
      localStorage: { getItem: (key: string) => localRecords.get(key) ?? null, setItem: (key: string, value: string) => localRecords.set(key, value) },
    },
    askProject: async (kind: string) => { dialogs.push(kind); return choices.shift(); },
    showOutput: () => {}, cancelTreeID: () => {}, cancelNodeID: () => {},
    nextTick: async (fn?: () => void) => fn?.(), updateOutputBounds: () => {}, fitView: () => {}, requestCanvasFit: () => {}, readGenerated: async () => {},
    fetch: async (path: string, init: { body?: string; headers: Record<string, string> }) => {
      const body = init.body ? parseJSON<any>(init.body) : undefined;
      const requestWorkspace = decodeURIComponent(init.headers["X-BT-Workspace"]!);
      calls.push({ path, body, workspace: requestWorkspace });
      let status = 200;
      let data: unknown;
      if (path === "/api/workspace") {
        options.switchStarted?.();
        await options.switchWait;
        if (options.switchError) { status = 400; data = { error: "目标目录不可用" }; }
        else data = { workspace: body.directory, files: options.targetFiles ?? [] };
      } else if (path === "/api/project") {
        if (options.saveError) { status = 500; data = { error: "磁盘写入失败" }; }
        else {
          const moved = body.directory && body.directory !== requestWorkspace;
          data = { workspace: body.directory ?? requestWorkspace, ...(moved ? { files: [body.name, "existing.json"], allFiles: [body.name, "existing.json", "invalid.json"] } : {}) };
        }
      } else if (path === "/api/projects") {
        if (options.invalidWorkspace) { status = 400; data = { error: "目录不存在或不可访问" }; }
        else data = { workspace: requestWorkspace || serverWorkspace,
          files: requestWorkspace === "E:/target" ? options.targetFiles ?? [] : options.initialFiles ?? [name] };
      }
      else if (path.startsWith("/api/project?")) data = targetProject;
      else throw new Error(`未模拟的请求 ${path}`);
      return { ok: status === 200, status, text: async () => stringifyJSON(data) };
    },
  };
  const workflow = runInNewContext(`${workflowJS}\n({ save, chooseWorkspace, initializeWorkspace, keydown });`, context) as {
    save: (saveAs?: boolean) => Promise<void>; // 执行真实串行保存入口。
    chooseWorkspace: () => Promise<void>; // 执行真实目录切换入口。
    initializeWorkspace: () => Promise<void>; // 执行真实首屏恢复入口。
    keydown: (event: unknown) => void; // 验证切换期间真实键盘处理边界。
  };
  return { context, calls, dialogs, records, localRecords, workflow };
}

// 取消首次保存或另存为不发请求、不发布新提示，旧状态仅保留在状态栏。
test("取消保存保留旧状态但不发布新的操作提示", async () => {
  for (const name of ["", "original.json"]) {
    const s = session({ name, dirty: true, choices: [undefined] });
    s.context.message.value = "已打开 room_final_audited.json";
    await s.workflow.save(!!name);
    assert.deepEqual(s.dialogs, ["save"]);
    assert.deepEqual(s.calls, []);
    assert.equal(s.context.noticeRevision.value, 0);
    assert.equal(s.context.message.value, "已打开 room_final_audited.json");
    assert.equal(s.context.fileName.value, name);
    assert.equal(s.context.dirty.value, true);
    assert.equal(s.context.busy.value, false);
  }
});

// 保存成功后身份与最近工程归属目标目录，后续保存必须携带新的目录身份。
test("跨目录另存为更新身份与候选列表，清除旧源码并继续保存到新目录", async () => {
  const s = session({ dirty: true, choices: [{ name: "copy.json", directory: "E:/目标 目录", overwrite: true }] });
  await s.workflow.save(true);
  assert.equal(s.context.noticeRevision.value, 1);
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
  assert.deepEqual(readTabSession({ getItem: key => s.records.get(key) ?? null, setItem: () => {} }), { workspace: "E:/目标 目录", fileName: "copy.json" });
  assert.equal(s.localRecords.get(recentProjectKey("E:/目标 目录")), "copy.json");
  await s.workflow.save();
  assert.equal(s.context.noticeRevision.value, 2);
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
  assert.deepEqual(JSON.parse(s.records.get(tabSessionKey)!), { workspace: "E:/target", fileName: "target.json" });
  assert.equal(s.localRecords.get(recentProjectKey("E:/original")), "original.json");
  assert.equal(s.localRecords.get(recentProjectKey("E:/target")), "target.json");
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

// 刷新后只读取本页签保存的目录与文件，两个目录的请求头互不干扰。
test("不同目录页签刷新后分别恢复工程", async () => {
  const firstRecords = new Map([[tabSessionKey, JSON.stringify({ workspace: "E:/original", fileName: "first.json" })]]);
  const secondRecords = new Map([[tabSessionKey, JSON.stringify({ workspace: "E:/target", fileName: "second.json" })]]);
  const first = session({ ready: false, name: "first.json", records: firstRecords });
  const second = session({ ready: false, name: "second.json", records: secondRecords, targetFiles: ["second.json"] });
  await Promise.all([first.workflow.initializeWorkspace(), second.workflow.initializeWorkspace()]);
  assert.deepEqual(first.calls.slice(0, 2).map(call => call.workspace), ["E:/original", "E:/original"]);
  assert.deepEqual(second.calls.slice(0, 2).map(call => call.workspace), ["E:/target", "E:/target"]);
  assert.equal(first.context.fileName.value, "first.json");
  assert.equal(second.context.fileName.value, "second.json");
});

// 同目录不同工程页签各自从会话记录恢复自己的文件身份。
test("同一目录不同文件页签分别恢复", async () => {
  for (const name of ["first.json", "second.json"]) {
    const records = new Map([[tabSessionKey, JSON.stringify({ workspace: "E:/original", fileName: name })]]);
    const s = session({ ready: false, name, records });
    await s.workflow.initializeWorkspace();
    assert.equal(s.context.fileName.value, name);
    assert.equal(s.calls[1]!.path, `/api/project?name=${name}`);
  }
});

// 没有页签身份的新页签沿用共享最近工程，同目录的已有页签仍保持自己的选择。
test("新页签恢复目录最近工程而已有页签身份优先", async () => {
  const localRecords = new Map([[recentProjectKey("E:/original"), "second.json"]]);
  const files = ["first.json", "second.json"];
  const existing = session({ ready: false, name: "first.json", initialFiles: files, localRecords,
    records: new Map([[tabSessionKey, JSON.stringify({ workspace: "E:/original", fileName: "first.json" })]]) });
  const fresh = session({ ready: false, name: "second.json", initialFiles: files, localRecords });
  await fresh.workflow.initializeWorkspace();
  assert.equal(fresh.context.fileName.value, "second.json");
  await existing.workflow.initializeWorkspace();
  assert.equal(existing.context.fileName.value, "first.json");
  assert.equal(fresh.context.fileName.value, "second.json");
  assert.equal(localRecords.get(recentProjectKey("E:/original")), "first.json");
});

// 最近工程消失时不擅自打开剩余单候选，由用户确认新文件。
test("新页签最近工程缺失时等待重新选择", async () => {
  const localRecords = new Map([[recentProjectKey("E:/original"), "removed.json"]]);
  const s = session({ ready: false, initialFiles: ["remaining.json"], localRecords });
  await s.workflow.initializeWorkspace();
  assert.deepEqual(s.calls.map(call => call.path), ["/api/projects"]);
  assert.equal(s.context.projectReady.value, false);
  assert.equal(s.context.workspace.value, "E:/original");
});

// 恢复文件被删除时保留原目录，提示重新选择，不自动打开剩余单候选。
test("刷新时文件缺失保留目录并提示重新选择", async () => {
  const records = new Map([[tabSessionKey, JSON.stringify({ workspace: "E:/target", fileName: "removed.json" })]]);
  const s = session({ ready: false, records, targetFiles: ["other.json"] });
  await s.workflow.initializeWorkspace();
  assert.deepEqual(s.calls.map(call => call.path), ["/api/projects"]);
  assert.equal(s.context.workspace.value, "E:/target");
  assert.equal(s.context.projectReady.value, false);
  assert.match(s.context.workspaceError.value, /请重新选择工程/);
  assert.deepEqual(JSON.parse(records.get(tabSessionKey)!), { workspace: "E:/target", fileName: "" });
});

// 无法访问的恢复目录报告服务端错误，页签记录留待用户重新选择目录。
test("恢复目录不可访问时显示错误并保留所选目录", async () => {
  const records = new Map([[tabSessionKey, JSON.stringify({ workspace: "E:/missing", fileName: "project.json" })]]);
  const s = session({ ready: false, records, invalidWorkspace: true });
  await s.workflow.initializeWorkspace();
  assert.equal(s.context.workspace.value, "E:/missing");
  assert.equal(s.context.projectReady.value, false);
  assert.match(s.context.workspaceError.value, /目录不存在或不可访问/);
  assert.deepEqual(JSON.parse(records.get(tabSessionKey)!), { workspace: "E:/missing", fileName: "project.json" });
});
