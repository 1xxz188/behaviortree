import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { ref, shallowRef } from "vue";
import { clone, blankProject } from "./project.ts";
import { allocateEventID, EventRegistryIndex, removeEvent, validateEventRegistry, validateNextEventID } from "./eventRegistry.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import type { EventDefinition } from "./project.ts";

// 从实际组件提取管理表单函数，验证事件新增、编辑和失效草稿的事务行为。
const component = readFileSync(new URL("./EventManager.vue", import.meta.url), "utf8");
const source = component.split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("EventManager.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["sameEvent", "openForm", "dismissForm", "saveMember", "confirmDelete", "dismissDelete", "deleteMember"]);
const handlers = script.statements.filter(statement => ts.isFunctionDeclaration(statement)
  && names.has(statement.name?.text ?? "")).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 管理入口只保留事件属性表单；整体说明和单项注释由悬停入口处理。
test("管理窗口不再展示注释编辑框或枚举文案", () => {
  const template = component.split("<template>")[1]!.split("</template>")[0]!;
  assert.match(template, /事件管理/);
  assert.match(template, /事件代码名/);
  assert.doesNotMatch(template, /枚举|注释|textarea|form\.description|saveEnum/);
});

// 新增和编辑表单共享独立弹窗，管理列表在弹窗打开时不可操作。
test("新增事件表单显示在独立对话框", () => {
  const template = component.split("<template>")[1]!.split("</template>")[0]!;
  assert.match(template, /v-if="form" class="event-form-backdrop"/);
  assert.match(template, /class="event-form-dialog" role="dialog" aria-modal="true"/);
  assert.match(template, /:inert="!!form \|\| !!deleteTarget"/);
  assert.match(template, /ref="formNameInput"/);
  assert.match(component, /function keydownForm\(/);
  // 两个输入框应处于同一行，第二列下方不再出现额外说明。
  assert.doesNotMatch(template, /生成常量/);
  assert.match(template, /显示名称<input[^>]+\/><\/label><label>事件代码名<input[^>]+\/><\/label>/);
});

// 删除确认是独立弹窗，管理层在弹窗显示时不能接受点击和键盘焦点。
test("删除事件使用独立确认弹窗", () => {
  const template = component.split("<template>")[1]!.split("</template>")[0]!;
  assert.match(template, /v-if="deleteTarget" class="event-form-backdrop"/);
  assert.match(template, /class="event-form-dialog event-delete-dialog" role="alertdialog" aria-modal="true"/);
  assert.match(template, /:inert="!!form \|\| !!deleteTarget"/);
  assert.match(template, /ref="deleteCancelButton"/);
  assert.match(component, /function keydownDelete\(/);
});

// 删除时再次确认成员仍是打开弹窗时的目标，避免覆盖外部事务。
test("删除弹窗拒绝过期目标并支持确认删除", () => {
  const project = blankProject();
  const first: EventDefinition = { id: "1", name: "采集", codeName: "Loot" };
  project.events = [first];
  project.nextEventId = "2";
  const props = { project, index: new EventRegistryIndex(project), commit(change: () => void) { change(); } };
  const deleteTarget = shallowRef<EventDefinition | null>(null);
  const context = {
    props, deleteTarget, deleteEvents: shallowRef(project.events), deleteOriginal: shallowRef<EventDefinition | null>(null),
    deleteTrigger: null, deleteCancelButton: ref<HTMLButtonElement>(), manager: ref<HTMLElement>(),
    document: { activeElement: null }, nextTick: async (task: () => void) => task(),
    error: ref(""), busy: ref(false), editing: shallowRef<EventDefinition | null>(null),
    removeEvent,
  };
  const manager = runInNewContext(`${js}\n({ confirmDelete, deleteMember });`, context) as {
    confirmDelete(target: EventDefinition): void; // 打开指定事件的删除确认。
    deleteMember(): void; // 确认删除当前事件。
  };
  manager.confirmDelete(first);
  assert.equal(deleteTarget.value, first);
  first.name = "外部修改";
  manager.deleteMember();
  assert.equal(project.events.length, 1);
  assert.match(context.error.value, /事件表已变化/);
  assert.equal(deleteTarget.value, null);

  props.index = new EventRegistryIndex(project);
  manager.confirmDelete(first);
  manager.deleteMember();
  assert.equal(project.events.length, 0);
  assert.equal(deleteTarget.value, null);
});

// 事务提交模拟父组件的同步修订，服务端候选校验只返回成功。
test("新增事件并编辑名称时保留悬停入口保存的注释", async () => {
  const project = blankProject();
  project.eventEnumDescription = "整体说明";
  const props = {
    project,
    index: new EventRegistryIndex(project),
    revision: 0,
    commit(change: () => void) { change(); props.revision++; },
  };
  let validations = 0;
  let validatedProject = "";
  const context = {
    props, form: ref<{ name: string; codeName: string } | null>(null),
    editing: shallowRef<EventDefinition | null>(null), formEvents: shallowRef(project.events), formOriginal: shallowRef<EventDefinition | null>(null),
    error: ref(""), busy: ref(false), active: true,
    formTrigger: null, formNameInput: ref<HTMLInputElement>(),
    document: { activeElement: null }, nextTick: async (task: () => void) => task(),
    allocateEventID, validateEventRegistry, validateNextEventID,
    clone, parseJSON, stringifyJSON,
    fetch: async (_url: string, init: { body: string }) => { validations++; validatedProject = init.body; return { ok: true, status: 200 }; },
  };
  const manager = runInNewContext(`${js}\n({ openForm, saveMember });`, context) as {
    openForm(target?: EventDefinition): void; // 打开新增或编辑事件草稿。
    saveMember(): Promise<void>; // 提交当前事件草稿。
  };
  manager.openForm();
  context.form.value = { name: "采集", codeName: "PickUp" };
  await manager.saveMember();
  assert.equal(validations, 1);
  assert.equal(context.error.value, "");
  assert.equal(project.eventEnumDescription, "整体说明");
  assert.equal(project.events[0]?.id, "1");
  assert.equal(project.events[0]?.description, undefined);
  assert.equal(project.nextEventId, "2");
  assert.equal(parseJSON<{ nextEventId: string }>(validatedProject).nextEventId, "2");

  // 注释由独立入口更新后，管理表单只提交名称与代码名。
  project.events[0]!.description = "采集中";
  props.index = new EventRegistryIndex(project);
  manager.openForm(project.events[0]);
  context.form.value = { name: "采集资源", codeName: "PickUp" };
  await manager.saveMember();
  assert.equal(validations, 2);
  assert.equal(project.events[0]?.name, "采集资源");
  assert.equal(project.events[0]?.description, "采集中");

  // 成员表替换后拒绝旧草稿，避免覆盖外部新增事件。
  manager.openForm();
  context.form.value = { name: "巡逻", codeName: "Patrol" };
  project.events = [...project.events, { id: "2", name: "移动", codeName: "Move" }];
  props.revision++;
  await manager.saveMember();
  assert.equal(context.error.value, "事件表已变化，请重新打开表单");
  assert.equal(validations, 2);

  // 编辑中的事件注释被外部修改时，也要让旧草稿失效。
  props.index = new EventRegistryIndex(project);
  manager.openForm(project.events[0]);
  project.events[0]!.description = "外部更新";
  await manager.saveMember();
  assert.equal(context.error.value, "事件表已变化，请重新打开表单");
  assert.equal(validations, 2);
});
