import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { clone, blankProject } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { GenerationRequests } from "./generation.ts";
import { ProjectSaveState } from "./saveState.ts";

// 直接执行 App 的真实保存与生成调用链，只替换网络、对话框和视图边界。
const appSource = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const appScript = ts.createSourceFile("App.ts", appSource, ts.ScriptTarget.Latest, true);
const functions = new Set(["request", "action", "notice", "recentStorage", "saveCurrent", "currentProjectRequest", "generate"]);
const workflowSource = appScript.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && functions.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError"),
).map(statement => statement.getText(appScript)).join("\n");
const workflowJS = ts.transpileModule(workflowSource, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 用可控响应承诺精确复现保存期间编辑，不依赖计时或轮询。
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

// 建立最小编辑会话，真实保存状态仍使用工程完整内容判断。
function editor(options: { dirty?: boolean; name?: string; chosenName?: string; saveError?: boolean; saveWait?: Promise<void> } = {}) {
  const project = { value: blankProject() };
  const saveState = new ProjectSaveState();
  const name = options.name ?? "project.json";
  saveState.reset(name ? stringifyJSON(project.value) : undefined);
  if (options.dirty) {
    project.value.name = "修改后的工程";
    saveState.changed();
  }
  const calls: { path: string; body: any }[] = [];
  const context = {
    Error, stringifyJSON, parseJSON, clone, project, saveState,
    projectReady: { value: true },
    fileName: { value: name }, suggestedName: { value: "project.json" },
    workspace: { value: "E:/workspace" }, files: { value: name ? [name] : [] },
    dirty: { get value() { return saveState.dirty; } },
    busy: { value: false }, message: { value: "" }, error: { value: false },
    diagnostics: { value: [] }, semanticRevision: { value: 0 }, editRevision: 0,
    generationRequests: new GenerationRequests(),
    window: { localStorage: undefined }, rememberProject: () => {},
    askProject: async (_kind: string) => options.chosenName,
    showOutput: () => {}, acceptCodeSnapshot: () => {},
    fetch: async (path: string, init: { body: string }) => {
      calls.push({ path, body: parseJSON(init.body) });
      if (path === "/api/project") await options.saveWait;
      const failed = path === "/api/project" && options.saveError;
      return { ok: !failed, status: failed ? 500 : 200,
        text: async () => stringifyJSON(failed ? { error: "磁盘写入失败" } : { version: "123456789012abcdef" }) };
    },
  };
  const generate = runInNewContext(`${workflowJS}\ngenerate;`, context) as (write?: boolean) => Promise<void>;
  return { context, calls, generate };
}

// 未保存工程必须先写入 JSON，写入成功后才能发送生成请求。
test("生成到目录等待工程保存成功，保存与生成内容一致", async () => {
  const pending = deferred();
  const { context, calls, generate } = editor({ dirty: true, saveWait: pending.promise });
  const finished = generate();
  assert.deepEqual(calls.map(call => call.path), ["/api/project"]);
  assert.equal(context.busy.value, true);
  pending.resolve();
  await finished;
  assert.deepEqual(calls.map(call => call.path), ["/api/project", "/api/generate"]);
  assert.deepEqual(calls[0]!.body.project, calls[1]!.body);
  assert.equal(context.dirty.value, false);
  assert.equal(context.busy.value, false);
});

// 已保存且无变更时无需重复写 JSON，预览始终不触发保存。
test("干净工程直接生成，未保存工程预览只请求预览接口", async () => {
  const clean = editor();
  await clean.generate();
  assert.deepEqual(clean.calls.map(call => call.path), ["/api/generate"]);
  const preview = editor({ dirty: true });
  await preview.generate(false);
  assert.deepEqual(preview.calls.map(call => call.path), ["/api/preview"]);
  assert.equal(preview.context.dirty.value, true);
});

// 保存失败必须保留未保存标记、显示错误，并终止生成。
test("生成前保存失败不会写入生成目录", async () => {
  const { context, calls, generate } = editor({ dirty: true, saveError: true });
  await generate();
  assert.deepEqual(calls.map(call => call.path), ["/api/project"]);
  assert.equal(context.dirty.value, true);
  assert.equal(context.error.value, true);
  assert.equal(context.message.value, "磁盘写入失败");
});

// 新建和导入工程沿用首次保存命名流程，取消不会产生任何写入。
test("首次生成取消保存命名时不发送保存或生成请求", async () => {
  const { context, calls, generate } = editor({ name: "" });
  await generate();
  assert.deepEqual(calls, []);
  assert.equal(context.fileName.value, "");
  assert.equal(context.dirty.value, true);
});

// 确认首次保存后绑定真实文件名，后续生成使用同一份工程内容。
test("首次生成先按所选名称保存并更新当前工程身份", async () => {
  const { context, calls, generate } = editor({ name: "", chosenName: "new.json" });
  await generate();
  assert.deepEqual(calls.map(call => call.path), ["/api/project", "/api/generate"]);
  assert.equal(calls[0]!.body.name, "new.json");
  assert.equal(context.fileName.value, "new.json");
  assert.equal(context.files.value.includes("new.json"), true);
  assert.equal(context.dirty.value, false);
});

// 保存异步响应不能让生成使用尚未落盘的新修改，也不能清除未保存提示。
test("生成前保存期间继续编辑时停止生成并保留新修改", async () => {
  const pending = deferred();
  const { context, calls, generate } = editor({ dirty: true, saveWait: pending.promise });
  const finished = generate();
  context.project.value.name = "保存请求发出后的修改";
  context.editRevision++;
  context.saveState.changed();
  pending.resolve();
  await finished;
  assert.deepEqual(calls.map(call => call.path), ["/api/project"]);
  assert.equal(context.dirty.value, true);
  assert.equal(context.project.value.name, "保存请求发出后的修改");
  assert.match(context.message.value, /停止生成/);
});
