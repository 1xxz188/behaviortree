import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { CatalogOrganizationIndex } from "./catalogOrganization.ts";
import { GenerationRequests, semanticSignature } from "./generation.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject, clone } from "./project.ts";
import type { Definition } from "./project.ts";
import { ProjectSaveState } from "./saveState.ts";
import { captureSnapshot } from "./treeIdentity.ts";
import type { EditorSnapshot } from "./treeIdentity.ts";

// 执行 App 的真实分类与复制下载入口，避免只验证脱离 UI 的辅助实现。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["commitCatalogOrganization", "transferCatalog", "notice"]);
const workflow = script.statements.filter(statement => ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  .map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(workflow, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 为真实入口提供历史、保存状态和剪贴板边界，记录外部副作用用于断言。
function editor() {
  const project = { value: blankProject() };
  project.value.catalog = [
    { id: "Z", name: "读取", kind: "action", goName: "Read", params: [{ name: "Value", type: "uint64", default: parseJSON("18446744073709551615") }] },
    { id: "A", name: "移动", kind: "action", goName: "Move" },
  ];
  const saveState = new ProjectSaveState();
  saveState.reset(stringifyJSON(project.value));
  const downloads: { name: string; content: string }[] = [];
  const copies: string[] = [];
  const validated: Definition[][] = [];
  const context = {
    noticeRevision: { value: 0 }, // 实际发布操作结果的版本。
    Error, clone, captureSnapshot, project, saveState,
    catalogIndex: { value: new CatalogOrganizationIndex(project.value) },
    workspaceChanging: { value: false }, treeID: { value: project.value.trees[0]!.id }, selected: { value: "" },
    undoStack: { value: [] as EditorSnapshot[] }, redoStack: { value: [] as EditorSnapshot[] },
    editRevision: 7, semanticRevision: { value: 11 }, catalogRevision: { value: 3 }, generationRequests: new GenerationRequests(),
    message: { value: "" }, error: { value: false }, catalogMenu: { value: {} as unknown },
    catalogTransferBusy: { value: false }, catalogCopy: { value: undefined as string | undefined },
    definitionIndex: { value: new Map(project.value.catalog.map(item => [item.id, item])) },
    validatedCatalogJSON: async (definitions: Definition[]) => { validated.push(definitions); return stringifyJSON(definitions, 2); },
    download: (name: string, content: string) => downloads.push({ name, content }),
    navigator: { clipboard: { writeText: async (content: string) => { copies.push(content); } } },
  };
  const methods = runInNewContext(`${js}\n({ commitCatalogOrganization, transferCatalog });`, context) as {
    commitCatalogOrganization(change: () => void): boolean;
    transferCatalog(operation: "copy" | "download", target?: Definition): Promise<void>;
  };
  return { context, downloads, copies, validated, ...methods };
}

// 分类成功产生一次可撤销快照和未保存标记，但不使生成请求或语义签名过期。
test("分类操作登记历史和保存状态且保持生成语义", () => {
  const { context, commitCatalogOrganization } = editor();
  const before = stringifyJSON(context.project.value);
  const signature = semanticSignature(context.project.value);
  const token = context.generationRequests.begin(context.project.value);
  context.redoStack.value = [captureSnapshot(context.project.value, context.treeID.value, "")];
  const success = commitCatalogOrganization(() => { context.catalogIndex.value.addFolder("业务", "", "business"); });
  assert.equal(success, true);
  assert.equal(context.undoStack.value.length, 1);
  assert.equal(context.undoStack.value[0]!.project, before);
  assert.equal(context.redoStack.value.length, 0);
  assert.equal(context.saveState.dirty, true);
  assert.equal(context.editRevision, 8);
  assert.equal(context.catalogRevision.value, 4);
  assert.equal(context.semanticRevision.value, 11);
  assert.equal(semanticSignature(context.project.value), signature);
  assert.equal(context.generationRequests.accepts(token, context.project.value), true);
});

// 非法移动须在写入前拒绝，历史、重做、保存状态及工程均保持原样。
test("失败分类不产生历史记录也不改变工程", () => {
  const { context, commitCatalogOrganization } = editor();
  context.catalogIndex.value.addFolder("父目录", "", "parent");
  context.catalogIndex.value.addFolder("子目录", "parent", "child");
  const before = stringifyJSON(context.project.value);
  context.saveState.reset(before);
  const redo = captureSnapshot(context.project.value, context.treeID.value, "");
  context.redoStack.value = [redo];
  assert.equal(commitCatalogOrganization(() => context.catalogIndex.value.move({ kind: "folder", id: "parent" }, "child")), false);
  assert.equal(stringifyJSON(context.project.value), before);
  assert.equal(context.undoStack.value.length, 0);
  assert.equal(context.redoStack.value[0], redo);
  assert.equal(context.saveState.dirty, false);
  assert.equal(context.editRevision, 7);
  assert.equal(context.catalogRevision.value, 3);
  assert.match(context.message.value, /自身或后代/);
  assert.equal(context.error.value, true);
});

// 工程切换期间拒绝分类回调，避免修改即将被替换的工程。
test("工程切换期间分类操作不执行回调", () => {
  const { context, commitCatalogOrganization } = editor();
  context.workspaceChanging.value = true;
  let called = false;
  assert.equal(commitCatalogOrganization(() => { called = true; }), false);
  assert.equal(called, false);
  assert.equal(context.undoStack.value.length, 0);
});

// 文件输出覆盖整个业务库且保留声明原顺序，单条导出使用单独文件名。
test("单条和全部下载使用明确文件名且不修改工程", async () => {
  const { context, downloads, validated, transferCatalog } = editor();
  context.catalogIndex.value.addFolder("业务", "", "business");
  context.catalogIndex.value.move({ kind: "definition", id: "A" }, "business");
  const before = stringifyJSON(context.project.value);
  context.saveState.reset(before);
  await transferCatalog("download");
  await transferCatalog("download", context.project.value.catalog[1]);
  assert.deepEqual(downloads.map(item => item.name), ["business-definitions.json", "business-definition.json"]);
  assert.deepEqual(parseJSON<Definition[]>(downloads[0]!.content).map(item => item.id), ["Z", "A"]);
  assert.deepEqual(parseJSON<Definition[]>(downloads[1]!.content).map(item => item.id), ["A"]);
  assert.match(downloads[0]!.content, /18446744073709551615/);
  assert.doesNotMatch(downloads[0]!.content, /catalogOrganization|folderId/);
  assert.notEqual(validated[0], context.project.value.catalog);
  assert.equal(stringifyJSON(context.project.value), before);
  assert.equal(context.undoStack.value.length, 0);
  assert.equal(context.saveState.dirty, false);
  assert.equal(context.catalogTransferBusy.value, false);
});

// 写入剪贴板失败时仍保留经校验的文本用于手动复制，不触碰工程历史。
test("单条全部复制以及剪贴板失败的手动回退", async () => {
  const { context, copies, transferCatalog } = editor();
  const before = stringifyJSON(context.project.value);
  await transferCatalog("copy", context.project.value.catalog[0]);
  await transferCatalog("copy");
  assert.deepEqual(copies.map(content => parseJSON<Definition[]>(content).length), [1, 2]);
  assert.equal(context.catalogCopy.value, undefined);
  assert.match(context.message.value, /已复制 2 个业务定义 JSON/);
  context.navigator.clipboard.writeText = async () => { throw new Error("无剪贴板权限"); };
  await transferCatalog("copy");
  assert.equal(context.catalogCopy.value, copies[1]);
  assert.equal(stringifyJSON(context.project.value), before);
  assert.equal(context.undoStack.value.length, 0);
  assert.equal(context.saveState.dirty, false);
});

// 验证服务拒绝时不能下载或写剪贴板，也不能产生回退文本。
test("导出校验失败仅显示错误且不修改工程", async () => {
  const { context, copies, downloads, transferCatalog } = editor();
  const before = stringifyJSON(context.project.value);
  context.validatedCatalogJSON = async () => { throw new Error("无效默认值"); };
  await transferCatalog("download");
  assert.equal(downloads.length, 0);
  assert.equal(copies.length, 0);
  assert.equal(context.catalogCopy.value, undefined);
  assert.equal(stringifyJSON(context.project.value), before);
  assert.equal(context.undoStack.value.length, 0);
  assert.equal(context.error.value, true);
  assert.match(context.message.value, /无效默认值/);
  assert.equal(context.catalogTransferBusy.value, false);
});

// 校验期间切换工程后丢弃旧内容，不触发下载、复制或手动回退弹窗。
test("工程切换使迟到的导出响应失效", async () => {
  for (const operation of ["copy", "download"] as const) {
    const { context, copies, downloads, transferCatalog } = editor();
    let complete!: (content: string) => void;
    context.validatedCatalogJSON = () => new Promise<string>(resolve => { complete = resolve; });
    const pending = transferCatalog(operation);
    context.project.value = blankProject();
    complete("[]");
    await pending;
    assert.equal(downloads.length, 0);
    assert.equal(copies.length, 0);
    assert.equal(context.catalogCopy.value, undefined);
    assert.equal(context.catalogTransferBusy.value, false);
  }
});

// 空库、旧对象菜单目标和切换状态均不发起校验或触发外部输出。
test("导出拒绝空业务库和失效菜单对象", async () => {
  const { context, validated, transferCatalog } = editor();
  await transferCatalog("download", clone(context.project.value.catalog[0]!));
  context.workspaceChanging.value = true;
  await transferCatalog("copy");
  context.workspaceChanging.value = false;
  context.project.value.catalog = [];
  await transferCatalog("download");
  assert.equal(validated.length, 0);
});

// 导出校验失败发生在切换工程之后时，保留新工程提示而非显示旧请求错误。
test("工程切换后忽略迟到的导出错误提示", async () => {
  const { context, transferCatalog } = editor();
  let reject!: (cause: Error) => void;
  context.validatedCatalogJSON = () => new Promise<string>((_resolve, fail) => { reject = fail; });
  const pending = transferCatalog("download");
  context.project.value = blankProject();
  context.message.value = "已打开新工程";
  reject(new Error("旧工程校验失败"));
  await pending;
  assert.equal(context.message.value, "已打开新工程");
  assert.equal(context.error.value, false);
});

// 剪贴板请求发出后切换工程，成功或失败都不能覆盖新提示或打开旧内容弹窗。
test("切换工程后剪贴板完成不显示过期提示或回退", async () => {
  for (const failed of [false, true]) {
    const { context, transferCatalog } = editor();
    let finish!: () => void;
    let started!: () => void;
    const clipboardStarted = new Promise<void>(resolve => { started = resolve; });
    context.navigator.clipboard.writeText = () => new Promise<void>((resolve, reject) => {
      finish = () => failed ? reject(new Error("旧工程复制失败")) : resolve();
      started();
    });
    const pending = transferCatalog("copy");
    await clipboardStarted;
    context.project.value = blankProject();
    context.message.value = "已打开新工程";
    finish();
    await pending;
    assert.equal(context.message.value, "已打开新工程");
    assert.equal(context.catalogCopy.value, undefined);
    assert.equal(context.error.value, false);
    assert.equal(context.catalogTransferBusy.value, false);
  }
});
