import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { blankProject, clone } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 执行 App 的真实下载流程，仅替换网络和模态框边界。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["request", "action", "notice", "downloadScaffold", "closeScaffoldOverwrite"]);
const code = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError"),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 使用事件承诺等待确认框出现，不依赖定时轮询。
function session(conflict = false, changed = false) {
  const calls: { path: string; body: any }[] = [];
  let opened!: () => void;
  const dialogOpened = new Promise<void>(resolve => { opened = resolve; });
  let pending: { path: string; resolve: (confirmed: boolean) => void } | undefined;
  const context = {
    Error, clone, parseJSON, stringifyJSON,
    project: { value: blankProject() }, workspace: { value: "E:/workspace" },
    semanticRevision: { value: 1 }, scaffoldSnapshot: { value: { source: "preview" } },
    scaffoldStale: { value: false }, busy: { value: false },
    message: { value: "" }, error: { value: false }, diagnostics: { value: [] },
    ensureIdentityDraftsApplied: () => true, showOutput: () => {},
    scaffoldOverwrite: {
      get value() { return pending; },
      set value(value) { pending = value; if (value) opened(); },
    },
    fetch: async (path: string, init: { body: string }) => {
      const body = parseJSON<any>(init.body);
      calls.push({ path, body });
      const fail = conflict && (calls.length === 1 || changed);
      return { ok: !fail, status: fail ? 409 : 200, text: async () => stringifyJSON(fail
        ? { error: changed && calls.length > 1 ? "文件已变化，请重新确认" : "文件已存在", path: "E:/workspace/behavior/actions.go", existingHash: "original-hash" }
        : { path: "E:/workspace/behavior/actions.go" }) };
    },
  };
  context.project.value.generation.packagePath = "behavior";
  const workflow = runInNewContext(`${js}\n({ downloadScaffold, closeScaffoldOverwrite });`, context) as {
    downloadScaffold: () => Promise<void>; // 运行真实下载流程。
    closeScaffoldOverwrite: (confirmed: boolean) => void; // 模拟用户明确确认或取消。
  };
  return { ...workflow, context, calls, dialogOpened };
}

// 首次下载直接请求生成包内保存，绝不默认携带覆盖授权。
test("下载 actions.go 默认保存到生成包路径且不覆盖", async () => {
  const s = session();
  await s.downloadScaffold();
  assert.equal(s.calls.length, 1);
  assert.equal(s.calls[0]!.path, "/api/scaffold/save");
  assert.equal(s.calls[0]!.body.project.generation.packagePath, "behavior");
  assert.equal(s.calls[0]!.body.overwrite, undefined);
  assert.match(s.context.message.value, /下载成功/);
  assert.match(s.context.message.value, /E:\/workspace\/behavior\/actions.go/);
});

// 冲突出现后必须等待用户决定，取消不发送第二次写入请求。
test("同名文件取消确认保留原文件", async () => {
  const s = session(true);
  const done = s.downloadScaffold();
  await s.dialogOpened;
  assert.equal(s.calls.length, 1);
  assert.equal(s.context.scaffoldOverwrite.value?.path, "E:/workspace/behavior/actions.go");
  s.closeScaffoldOverwrite(false);
  await done;
  assert.equal(s.calls.length, 1);
  assert.match(s.context.message.value, /已取消/);
});

// 确认只覆盖前一次检查的文件版本，保持同一工程快照。
test("二次确认后携带文件摘要覆盖", async () => {
  const s = session(true);
  const done = s.downloadScaffold();
  await s.dialogOpened;
  s.closeScaffoldOverwrite(true);
  await done;
  assert.equal(s.calls.length, 2);
  assert.equal(s.calls[1]!.body.overwrite, true);
  assert.equal(s.calls[1]!.body.expectedHash, "original-hash");
  assert.deepEqual(s.calls[1]!.body.project, s.calls[0]!.body.project);
});

// 过期骨架和确认期间工程变化均不得写入新位置或提交旧快照覆盖。
test("过期骨架及确认期间工程变化会停止下载", async () => {
  const stale = session();
  stale.context.scaffoldStale.value = true;
  await stale.downloadScaffold();
  assert.equal(stale.calls.length, 0);
  const s = session(true);
  const done = s.downloadScaffold();
  await s.dialogOpened;
  s.context.semanticRevision.value++;
  s.closeScaffoldOverwrite(true);
  await done;
  assert.equal(s.calls.length, 1);
  assert.equal(s.context.error.value, true);
});

// 确认后磁盘版本变化时显示冲突，不能自动重复授权覆盖。
test("确认后文件变化不自动重试覆盖", async () => {
  const s = session(true, true);
  const done = s.downloadScaffold();
  await s.dialogOpened;
  s.closeScaffoldOverwrite(true);
  await done;
  assert.equal(s.calls.length, 2);
  assert.equal(s.context.error.value, true);
  assert.match(s.context.message.value, /文件已变化/);
});
