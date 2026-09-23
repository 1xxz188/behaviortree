import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { blankProject, clone } from "./project.ts";
import type { Definition, Diagnostic } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { GenerationRequests } from "./generation.ts";
import { synchronizeCatalog } from "./catalogSync.ts";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import { validateEventRegistry, validateNextEventID } from "./eventRegistry.ts";

// 抽取真实目录提交入口，避免只测试与界面脱节的同步辅助函数。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["applyCatalog", "invalidateCode", "notice", "request", "currentProjectRequest", "validate", "action"]);
const workflow = script.statements.filter(statement =>
  (ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  || (ts.isClassDeclaration(statement) && statement.name?.text === "RequestError"),
).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(workflow, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 最小复现包括旧参数实例、更新后的目录，以及实际发送给校验器的工程快照。
function editor() {
  const project = { value: blankProject() };
  const old: Definition = { id: "Check", name: "检查", kind: "condition", goName: "Check", params: [
    { name: "Target", type: "int64" }, { name: "en", type: "entity" },
  ] };
  const updated: Definition = { ...old, params: [{ name: "E", type: "entity" }, { name: "IsDeath", type: "bool" }] };
  project.value.catalog = [old];
  project.value.trees[0]!.nodes = [{ id: "1", type: "condition", binding: old.id, params: { Target: { value: 1 }, en: { value: 2 } } }];
  project.value.trees[0]!.root = "1";
  const calls: { path: string; body: any }[] = [];
  const context = {
    noticeRevision: { value: 0 }, // 实际发布操作结果的版本。
    Error, clone, parseJSON, stringifyJSON, project, synchronizeCatalog, CatalogOrganizationIndex, validateEventRegistry, validateNextEventID,
    catalogIndex: { value: new CatalogOrganizationIndex(project.value) }, catalogRevision: { value: 0 },
    node: { value: project.value.trees[0]!.nodes[0]! },
    catalogDialog: { value: {} as unknown }, diagnostics: { value: [] as Diagnostic[] },
    semanticRevision: { value: 0 }, generationRequests: new GenerationRequests(),
    busy: { value: false }, message: { value: "" }, error: { value: false }, workspace: { value: "test" },
    ensureIdentityDraftsApplied: () => true, showOutput: () => {},
    mutate: (fn: () => void) => { fn(); context.semanticRevision.value++; context.generationRequests.invalidate(); context.diagnostics.value = []; },
    fetch: async (path: string, init: { body: string }) => {
      calls.push({ path, body: parseJSON(init.body) });
      return { ok: true, text: async () => stringifyJSON({ diagnostics: [] }) };
    },
  };
  const apply = runInNewContext(`${js}\napplyCatalog;`, context) as (catalog: Definition[], bindID?: string) => Promise<void>;
  return { context, calls, updated, apply };
}

// 用显式承诺控制返回时序，验证真实异步调用链而不引入定时轮询。
function deferred() {
  let resolve!: (diagnostics: Diagnostic[]) => void;
  const promise = new Promise<Diagnostic[]>(done => { resolve = done; });
  return { promise, resolve };
}

// 删除并改名参数后，旧绑定必须从真实节点与校验请求同时消失。
test("目录更新同步已有节点，立即用新 Schema 校验", async () => {
  const { context, calls, updated, apply } = editor();
  await apply([updated]);
  const params = context.node.value.params!;
  assert.equal(Object.hasOwn(params, "Target"), false, "旧 Target 绑定不能隐藏残留");
  assert.equal(Object.hasOwn(params, "en"), false, "旧 en 绑定不能隐藏残留");
  assert.equal(calls.length, 1, "更新后必须立即请求整棵树校验");
  assert.equal(calls[0]!.path, "/api/validate");
  assert.deepEqual(calls[0]!.body.catalog, [updated]);
  assert.equal(Object.hasOwn(calls[0]!.body.trees[0].nodes[0].params, "Target"), false);
});

// 文件保存等操作占用 busy 时也必须校验，否则目录更新会静默跳过校验。
test("目录更新期间 busy 不会跳过自动校验", async () => {
  const { context, calls, updated, apply } = editor();
  context.busy.value = true;
  await apply([updated]);
  assert.equal(calls.length, 1);
  assert.equal(context.busy.value, true);
});

// 模拟服务器返回未绑定错误，底部结果必须立即替换此前的未知参数诊断。
test("自动校验结果替换旧诊断并明确显示新增参数未绑定", async () => {
  const { context, updated, apply } = editor();
  context.diagnostics.value = [{ message: "未知参数", field: "params.Target" }];
  const expected = ["E", "IsDeath"].map(name => ({ nodeId: "1", field: `params.${name}`, message: "未绑定参数" }));
  context.fetch = async () => ({ ok: true, text: async () => stringifyJSON({ diagnostics: expected }) });
  await apply([updated]);
  assert.equal(stringifyJSON(context.diagnostics.value), stringifyJSON(expected));
  assert.doesNotMatch(stringifyJSON(context.diagnostics.value), /未知参数/);
});

// 在自动校验返回前继续编辑时，旧响应不得复活已清除的错误。
test("目录自动校验的过期响应不会覆盖新修订诊断", async () => {
  const { context, updated, apply } = editor();
  const response = deferred();
  context.fetch = async () => ({ ok: true, text: async () => stringifyJSON({ diagnostics: await response.promise }) });
  const finished = apply([updated]);
  context.mutate(() => { context.node.value.params = { E: { value: 1 }, IsDeath: { value: true } }; });
  context.diagnostics.value = [{ message: "新修订的诊断" }];
  response.resolve([{ message: "旧修订未绑定参数" }]);
  await finished;
  assert.equal(context.diagnostics.value[0]!.message, "新修订的诊断");
});

// 目录同步成功而服务器失败时，必须显示失败原因，不能误报校验通过或恢复旧数据。
test("自动校验失败保留同步结果并报告错误", async () => {
  const { context, updated, apply } = editor();
  context.fetch = async () => { throw new Error("连接断开"); };
  await apply([updated]);
  assert.deepEqual(Object.keys(context.node.value.params!), []);
  assert.match(context.message.value, /校验失败.*连接断开/);
  assert.equal(context.error.value, true);
});

// 改型重置必须在底部解释原因，即使服务器只返回普通的未绑定诊断。
test("不兼容类型重置提示与新 Schema 校验结果一起显示", async () => {
  const { context, updated, apply } = editor();
  updated.params = [{ name: "Target", type: "bool" }];
  await apply([updated]);
  assert.equal(Object.hasOwn(context.node.value.params!, "Target"), false);
  assert.match(context.diagnostics.value[0]!.message, /int64 → bool.*旧绑定不兼容.*未绑定参数/);
});

// 新定义进入打开导入窗口时捕获的目录，已有定义分类不受当前目标影响。
test("导入新增定义放入目标目录且不改变已有定义归属", async () => {
  const { context, apply } = editor();
  context.catalogIndex.value.addFolder("业务", "", "business");
  context.catalogDialog.value = { folderId: "business" };
  const added: Definition = { id: "Move", name: "移动", kind: "action", goName: "Move" };
  await apply([...context.project.value.catalog, added]);
  assert.equal(context.catalogIndex.value.assignments.get("Move")!.folderId, "business");
  assert.deepEqual(context.catalogIndex.value.assignments.get("Move")!.tagIds, []);
  assert.equal(context.catalogIndex.value.assignments.get("Check")!.folderId, "");
  assert.equal(context.catalogRevision.value, 1);
  assert.equal(context.catalogDialog.value, undefined);
  assert.match(context.message.value, /已应用，请保存工程/);
});

// 同 ID 替换只更新业务声明，稳定分类、标签和排序不被导入目标覆盖。
test("同 ID 更新保留目录标签及排序", async () => {
  const { context, updated, apply } = editor();
  context.catalogIndex.value.addFolder("原目录", "", "original");
  context.catalogIndex.value.addFolder("导入目录", "", "destination");
  context.catalogIndex.value.addTag("共享", "shared");
  context.catalogIndex.value.move({ kind: "definition", id: "Check" }, "original");
  context.catalogIndex.value.setTags("Check", ["shared"]);
  const assignment = stringifyJSON(context.project.value.catalogOrganization!.assignments.Check);
  context.catalogDialog.value = { folderId: "destination" };
  await apply([updated]);
  assert.equal(stringifyJSON(context.project.value.catalogOrganization!.assignments.Check), assignment);
  assert.equal(context.catalogIndex.value.tagMembers.get("shared")!.has("Check"), true);
});

// 删除定义应同时清理持久化归属与标签倒排索引，保留画布失效引用供诊断。
test("删除定义清理分类关联且保留画布引用诊断入口", async () => {
  const { context, calls, apply } = editor();
  context.catalogIndex.value.addFolder("业务", "", "business");
  context.catalogIndex.value.addTag("共享", "shared");
  context.catalogIndex.value.move({ kind: "definition", id: "Check" }, "business");
  context.catalogIndex.value.setTags("Check", ["shared"]);
  await apply([]);
  assert.equal(Object.hasOwn(context.project.value.catalogOrganization!.assignments, "Check"), false);
  assert.equal(context.catalogIndex.value.assignments.has("Check"), false);
  assert.equal(context.catalogIndex.value.tagMembers.get("shared")!.size, 0);
  assert.equal(context.catalogIndex.value.isFolderEmpty("business"), true);
  assert.equal(context.node.value.binding, "Check");
  assert.equal(calls.length, 1);
});

// 捕获目标被移除时回退根目录，不把定义写入不存在的目录。
test("目标目录失效时新增定义归根目录", async () => {
  const { context, apply } = editor();
  context.catalogDialog.value = { folderId: "removed" };
  await apply([...context.project.value.catalog, { id: "Move", name: "移动", kind: "action", goName: "Move" }]);
  assert.equal(context.catalogIndex.value.assignments.get("Move")!.folderId, "");
});
