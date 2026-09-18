import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref, watch } from "vue";
import { catalogConflicts, mergeCatalog, parseCatalog } from "./catalog.ts";
import { validateCatalog, validatedCatalogJSON } from "./catalogTransfer.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import type { Definition } from "./project.ts";

// 创建最小动作定义，便于把断言集中在导入导出边界。
function action(id: string): Definition {
  return { id, name: id, kind: "action", goName: id };
}

// 模拟 Go 校验接口时，仍然检查网络传输内容而不替换无损序列化路径。
function validResponse(_input: unknown, options?: RequestInit): Promise<Response> {
  return Promise.resolve(new Response(String(options?.body), { status: 200 }));
}

// 单条与全部导出必须保持数组、顺序、中文和大整数，同时不修改源定义。
test("复制与导出共用无损 JSON 并可直接回导", async (t) => {
  const calls: string[] = [];
  t.mock.method(globalThis, "fetch", async (_input: unknown, options?: RequestInit) => {
    calls.push(String(options?.body));
    return validResponse(_input, options);
  });
  const definitions = parseCatalog(parseJSON('[{"id":"Z","name":"读取","kind":"action","goName":"Read","params":[{"name":"Value","type":"uint64","default":18446744073709551615}]},{"id":"A","name":"动作","kind":"action","goName":"Act"}]'));
  const original = stringifyJSON(definitions);
  const all = await validatedCatalogJSON(definitions);
  assert.match(all, /\n  \{/);
  assert.match(all, /18446744073709551615/);
  assert.deepEqual(parseCatalog(parseJSON(all)).map(item => item.id), ["Z", "A"]);
  assert.equal(stringifyJSON(mergeCatalog([], parseCatalog(parseJSON(all)))), original);
  const one = await validatedCatalogJSON([definitions[0]!]);
  assert.equal(parseCatalog(parseJSON(one)).length, 1);
  assert.equal(stringifyJSON(definitions), original);
  assert.equal(calls.length, 2);
  assert.match(calls[0]!, /18446744073709551615/);
});

// Go 参数约束、离线错误和无效响应都不得输出可能无法回导的文件。
test("导出前校验失败阻止生成 JSON 并保留输入", async (t) => {
  const definitions = [action("Move")];
  const before = stringifyJSON(definitions);
  const fetch = t.mock.method(globalThis, "fetch", async () => new Response('{"error":"参数 Speed 的默认值无效"}', { status: 400 }));
  await assert.rejects(validatedCatalogJSON(definitions), /参数 Speed/);
  fetch.mock.mockImplementation(async () => { throw new Error("连接失败"); });
  await assert.rejects(validatedCatalogJSON(definitions), /连接失败/);
  fetch.mock.mockImplementation(async () => new Response("<html>offline</html>", { status: 502 }));
  await assert.rejects(validatedCatalogJSON(definitions), /无效 JSON.*502/);
  assert.equal(stringifyJSON(definitions), before);
});

// 定义携带组织数据或重复 ID 时本地拒绝，不能依赖 Go 的未知字段忽略行为。
test("非法定义在网络请求前拒绝", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", validResponse);
  await assert.rejects(validatedCatalogJSON([{ ...action("Move"), folderID: "private" } as Definition]), /folderID/);
  await assert.rejects(validatedCatalogJSON([action("Move"), action("Move")]), /重复 ID/);
  assert.equal(fetch.mock.callCount(), 0);
});

// 提取实际组件的导入函数，避免测试重新实现输入失效、确认和异步提交逻辑。
const componentSource = readFileSync(new URL("./CatalogManager.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("CatalogManager.ts", componentSource, ts.ScriptTarget.Latest, true);
const functionNames = new Set(["resetPreview", "readCatalog", "fillExample", "previewCatalog", "validatePreviewMerged", "changeChoice", "applyCatalog"]);
const workflow = script.statements.filter(statement => ts.isFunctionDeclaration(statement) && functionNames.has(statement.name?.text ?? ""))
  .map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(workflow, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 使用 Vue 响应式对象执行组件原函数，覆盖编辑文本到发出 apply 事件的流程。
function importer(catalog: Definition[]) {
  const incoming = ref<Definition[]>([]);
  const choices = ref<Record<string, "keep" | "replace">>({});
  const conflictRows = computed(() => catalogConflicts(catalog, incoming.value));
  const emissions: Definition[][] = [];
  const context = {
    parseJSON, stringifyJSON, parseCatalog, mergeCatalog, catalogConflicts, validateCatalog,
    props: { catalog }, incoming, choices, conflictRows,
    pendingChoices: computed(() => conflictRows.value.some(row => !choices.value[row.id])),
    updatesExisting: computed(() => conflictRows.value.some(row => choices.value[row.id] === "replace")),
    mode: ref("import"), importText: ref(""), fileLabel: ref(""), acknowledged: ref(false),
    previewReady: ref(false), previewValidated: ref(false), importRevision: ref(0), busy: ref(false), error: ref(""),
    emit: (_name: string, value: Definition[]) => emissions.push(value),
  };
  const methods = runInNewContext(`${js}\n({ resetPreview, readCatalog, fillExample, previewCatalog, changeChoice, applyCatalog });`, context) as {
    resetPreview(): void;
    readCatalog(event: Event): Promise<void>;
    fillExample(): void;
    previewCatalog(): Promise<void>;
    changeChoice(): Promise<void>;
    applyCatalog(): Promise<void>;
  };
  const stop = watch(context.importText, methods.resetPreview, { flush: "sync" });
  return { ...context, ...methods, emissions, stop };
}

// 粘贴后须显式预览；文本再次编辑会立即清除预览、冲突选择与确认。
test("粘贴导入只在预览且最终校验通过后应用", async (t) => {
  t.mock.method(globalThis, "fetch", validResponse);
  const editor = importer([action("Old")]);
  t.after(editor.stop);
  editor.importText.value = stringifyJSON([action("New")]);
  await editor.applyCatalog();
  assert.equal(editor.emissions.length, 0);
  await editor.previewCatalog();
  assert.equal(editor.previewValidated.value, true);
  assert.equal(editor.emissions.length, 0);
  await editor.applyCatalog();
  assert.deepEqual(editor.emissions[0]!.map(item => item.id), ["Old", "New"]);
  editor.choices.value.New = "replace";
  editor.acknowledged.value = true;
  editor.importText.value += " ";
  assert.equal(editor.previewReady.value, false);
  assert.equal(editor.previewValidated.value, false);
  assert.equal(editor.acknowledged.value, false);
  assert.equal(Object.keys(editor.choices.value).length, 0);
});

// 冲突必须先选择处理方式，替换还须确认影响；网络失败不产生 apply 事件。
test("冲突选择、影响确认和失败请求共同保护当前工程", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", validResponse);
  const catalog = [action("Move")];
  const editor = importer(catalog);
  t.after(editor.stop);
  editor.importText.value = stringifyJSON([{ ...action("Move"), name: "新移动" }]);
  await editor.previewCatalog();
  assert.equal(editor.pendingChoices.value, true);
  await editor.applyCatalog();
  assert.equal(editor.emissions.length, 0);
  editor.choices.value.Move = "replace";
  await editor.changeChoice();
  await editor.applyCatalog();
  assert.equal(editor.emissions.length, 0);
  editor.acknowledged.value = true;
  fetch.mock.mockImplementation(async () => new Response('{"error":"服务校验失败"}', { status: 400 }));
  await editor.applyCatalog();
  assert.match(editor.error.value, /服务校验失败/);
  assert.equal(editor.previewValidated.value, false);
  assert.equal(editor.emissions.length, 0);
  assert.equal(catalog[0]!.name, "Move");
});

// 文件选择只填入文本，示例不覆盖已有内容；跨 ID Go 名冲突停留在错误预览。
test("文件需再次解析且最终 Go 名冲突禁止应用", async (t) => {
  t.mock.method(globalThis, "fetch", validResponse);
  const editor = importer([action("Move")]);
  t.after(editor.stop);
  const raw = stringifyJSON([{ ...action("Other"), goName: "Move" }]);
  const input = { files: [{ name: "definitions.json", text: async () => raw }], value: "file" };
  await editor.readCatalog({ target: input } as unknown as Event);
  assert.equal(editor.importText.value, raw);
  assert.equal(editor.previewReady.value, false);
  editor.fillExample();
  assert.equal(editor.importText.value, raw);
  await editor.previewCatalog();
  assert.match(editor.error.value, /Go 函数名 Move/);
  assert.equal(editor.previewValidated.value, false);
  await editor.applyCatalog();
  assert.equal(editor.emissions.length, 0);
});

// 请求进行中切换工程或重置输入时，晚到的响应不能恢复旧预览或应用旧定义。
test("异步预览失效后丢弃服务端响应", async (t) => {
  let complete!: (response: Response) => void;
  t.mock.method(globalThis, "fetch", () => new Promise<Response>(resolve => { complete = resolve; }));
  const editor = importer([]);
  t.after(editor.stop);
  editor.importText.value = stringifyJSON([action("Move")]);
  const pending = editor.previewCatalog();
  editor.resetPreview();
  complete(new Response(stringifyJSON([action("Move")]), { status: 200 }));
  await pending;
  assert.equal(editor.previewReady.value, false);
  assert.equal(editor.previewValidated.value, false);
  assert.equal(editor.incoming.value.length, 0);
  assert.equal(editor.emissions.length, 0);
});
