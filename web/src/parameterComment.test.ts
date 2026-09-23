import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { mergeCatalog, parseCatalog } from "./catalog.ts";
import { validateProjectTypes } from "./enums.ts";
import { GenerationRequests, semanticSignature } from "./generation.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";
import type { Definition } from "./project.ts";

// 执行目录面板实际草稿读写函数，覆盖新建、编辑回显与清空的完整数据路径。
const source = readFileSync(new URL("./CatalogManager.vue", import.meta.url), "utf8").split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("CatalogManager.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["changeMode", "editDefinition", "addParameter", "lines", "draftDefinition"]);
const workflow = script.statements.filter(statement => ts.isFunctionDeclaration(statement) && names.has(statement.name?.text ?? ""))
  .map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(workflow, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 用可序列化上下文复用真实表单函数，无需复制参数转换实现。
function form() {
  const context = {
    parseJSON, stringifyJSON, props: { projectRevision: 0 }, busy: { value: false }, mode: { value: "create" }, draftRevision: { value: 0 },
    incoming: { value: [] }, incomingPackage: { value: null }, choices: { value: {} }, eventChoices: { value: {} }, eventMappings: { value: {} }, eventRenames: { value: {} }, importEnumDescription: { value: false }, acknowledged: { value: false },
    error: { value: "" }, editingID: { value: "" }, fileLabel: { value: "" }, bindNew: { value: false },
    importSource: { value: "paste" }, importText: { value: "" }, previewReady: { value: false }, previewValidated: { value: false },
    draft: { value: { id: "Move", name: "移动", kind: "action", goName: "Move", eventIds: [] } },
    parameters: { value: [] as { name: string; comment: string }[] },
  };
  const actions = runInNewContext(`let parameterKey = 0;\n${js}\n({ editDefinition, addParameter, draftDefinition });`, context) as {
    editDefinition: (definition: Definition) => void;
    addParameter: () => void;
    draftDefinition: () => Definition;
  };
  return { context, ...actions };
}

// 注释与大整数默认值一起导入和合并时不能丢失中文、换行或数值精度。
test("参数注释随目录导入合并无损往返，旧目录无需补字段", () => {
  const raw = '[{"id":"Move","name":"移动","kind":"action","goName":"Move","params":[{"name":"Target","type":"entity","comment":"目标实体\\n用于移动","default":18446744073709551615}]}]';
  const catalog = parseCatalog(parseJSON(raw));
  assert.equal(stringifyJSON(mergeCatalog([], catalog)), raw);
  delete catalog[0]!.params![0]!.comment;
  assert.doesNotThrow(() => parseCatalog(catalog));
});

// 工程与单独目录共用参数边界，非法注释必须定位到具体字段而不是在编辑时崩溃。
test("目录与工程导入拒绝非字符串参数注释", () => {
  for (const comment of [null, false, 123, {}, []]) {
    const catalog = [{ id: "Move", name: "移动", kind: "action", goName: "Move", params: [{ name: "Target", type: "entity", comment }] }];
    assert.throws(() => parseCatalog(catalog), /catalog\[0\].params\[0\].comment.*字符串/);
    assert.throws(() => validateProjectTypes({ schemaVersion: 4, nextEventId: "1", events: [], catalog }), /catalog\[0\].params\[0\].comment.*字符串/);
  }
});

// 实际面板保存后再次编辑必须回显多行注释，清空后应省略可选 JSON 字段。
test("参数表单支持填写、回显和清空注释，并兼容旧参数", () => {
  const editor = form();
  editor.addParameter();
  editor.context.parameters.value[0]!.name = "Target";
  editor.context.parameters.value[0]!.comment = "  目标实体\n用于移动  ";
  const saved = editor.draftDefinition();
  assert.equal(saved.params![0]!.comment, "目标实体\n用于移动");
  editor.editDefinition(saved);
  assert.equal(editor.context.mode.value, "edit", "编辑已有定义必须进入编辑页面，不能激活新建业务定义");
  assert.equal(editor.context.parameters.value[0]!.comment, "目标实体\n用于移动");
  editor.context.parameters.value[0]!.comment = " \n ";
  const cleared = editor.draftDefinition();
  assert.equal(Object.hasOwn(cleared.params![0]!, "comment"), false);
  editor.editDefinition(cleared);
  assert.equal(editor.context.parameters.value[0]!.comment, "");
});

// 注释和显示名称影响生成内容，必须淘汰旧预览；空注释的 omitempty 保存应保持签名稳定。
test("参数注释与业务显示名更新使旧代码失效，空注释保存保持签名", () => {
  const project = blankProject();
  project.catalog = [{ id: "Move", name: "移动", kind: "action", goName: "Move", params: [{ name: "Target", type: "entity" }] }];
  const baseline = semanticSignature(project);
  project.catalog[0]!.params![0]!.comment = "";
  assert.equal(semanticSignature(project), baseline);
  const requests = new GenerationRequests();
  const token = requests.begin(project);
  project.catalog[0]!.params![0]!.comment = "移动目标";
  assert.equal(requests.accepts(token, project), false);
  const next = requests.begin(project);
  project.catalog[0]!.name = "移动到目标";
  assert.equal(requests.accepts(next, project), false);
});
