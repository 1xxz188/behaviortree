import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { compile, createSSRApp, ref } from "vue";
import { renderToString } from "@vue/server-renderer";
import { blankProject, clone } from "./project.ts";
import type { Diagnostic } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { GenerationRequests } from "./generation.ts";

// 执行真实校验请求链，并渲染真实诊断模板，避免仅验证底部消息已被赋值。
const app = readFileSync(new URL("./App.vue", import.meta.url), "utf8");
const source = app.split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["request", "action", "notice", "validate", "currentProjectRequest", "showOutput"]);
const code = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError"),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const outputStart = app.indexOf('<div v-if="bottomTab === \'diagnostics\'" class="output-content">');
const outputEnd = app.indexOf("<SourceViewer", outputStart);
const renderOutput = compile(app.slice(outputStart, outputEnd).trim());

// 使用真实响应式状态记录请求、面板切换及摘要，网络边界支持可控失败。
function session() {
  let failure = false;
  let returnedDiagnostics: Diagnostic[] = [];
  const calls: string[] = [];
  const context = {
    noticeRevision: { value: 0 }, // 实际发布操作结果的版本。
    Error, clone, stringifyJSON, parseJSON,
    project: ref(blankProject()), workspace: ref("E:/workspace"),
    semanticRevision: ref(1), generationRequests: new GenerationRequests(),
    diagnostics: ref<Diagnostic[]>([]), validationResult: ref<{ revision: number; count: number }>(),
    busy: ref(false), message: ref(""), error: ref(false), bottomTab: ref("source"),
    outputHeight: ref<number>(),
    ensureIdentityDraftsApplied: () => true,
    nextTick: (callback: () => void) => callback(), fitView: () => {},
    setOutputHeight: (height: number) => { context.outputHeight.value = height; },
    fetch: async (path: string) => {
      calls.push(path);
      if (failure) throw new Error("网络连接失败");
      return { ok: true, status: 200, text: async () => stringifyJSON({ diagnostics: returnedDiagnostics }) };
    },
  };
  const workflow = runInNewContext(`${js}\n({ validate });`, context) as {
    validate: () => Promise<void>; // 执行 App 中的真实校验入口。
  };
  return {
    ...workflow, context, calls,
    fail: () => { failure = true; },
    respond: (items: Diagnostic[]) => { returnedDiagnostics = items; },
    render: () => renderToString(createSSRApp({ setup: () => ({ ...context, focusDiagnostic: () => {} }), render: renderOutput })),
  };
}

// 空诊断是明确的成功结果，必须替换未校验占位提示且展开结果面板。
test("校验通过后显示明确结果而不是点击校验占位提示", async () => {
  const s = session();
  await s.validate();
  assert.deepEqual(s.calls, ["/api/validate"]);
  assert.equal(s.context.bottomTab.value, "diagnostics");
  const html = await s.render();
  assert.match(html, /校验通过/);
  assert.doesNotMatch(html, /点击「校验」/);
  assert.equal(s.context.validationResult.value?.count, 0);
  assert.equal(s.context.validationResult.value?.revision, 1);
});

// 有问题时同时展示数量及可定位节点的明细，不能误显示校验通过。
test("校验不通过时显示问题数量和诊断明细", async () => {
  const s = session();
  s.respond([{ treeId: "1", nodeId: "1", message: "入口节点不存在" }]);
  await s.validate();
  assert.equal(s.context.validationResult.value?.count, 1);
  assert.equal(s.context.error.value, true);
  const html = await s.render();
  assert.match(html, /1\s*个问题/);
  assert.match(html, /入口节点不存在/);
  assert.doesNotMatch(html, /校验通过/);
});

// 再次校验网络失败时清除旧的成功摘要，由统一通知显示本次错误。
test("校验网络失败不会继续显示上次校验通过", async () => {
  const s = session();
  await s.validate();
  s.fail();
  await s.validate();
  assert.equal(s.context.validationResult.value, undefined);
  assert.equal(s.context.error.value, true);
  assert.equal(s.context.message.value, "网络连接失败");
  assert.equal(s.context.busy.value, false);
  assert.doesNotMatch(await s.render(), /校验通过/);
});

// 相同结果重复校验仍重新请求并展示本次结果，不被已有摘要短路。
test("重复校验保持明确结果且工程修改后旧摘要失效", async () => {
  const s = session();
  await s.validate();
  const previous = s.context.validationResult.value;
  await s.validate();
  assert.equal(s.calls.length, 2);
  assert.notEqual(s.context.validationResult.value, previous);
  assert.match(await s.render(), /校验通过/);
  s.context.semanticRevision.value++;
  assert.doesNotMatch(await s.render(), /校验通过/);
});
