import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { computed, ref, shallowRef } from "vue";
import { blankProject } from "./project.ts";
import type { EventDefinition, Project } from "./project.ts";
import { EventRegistryIndex, normalizeEventDescription, validateEventRegistry } from "./eventRegistry.ts";
import { pruneHighlightedEvents, toggleHighlightedEvent } from "./eventHighlight.ts";

// 执行实际事件事务与悬停提交函数，验证目标身份和高亮边界。
const source = readFileSync(new URL("./App.vue", import.meta.url), "utf8")
  .split('<script setup lang="ts">')[1]!.split("</script>")[0]!;
const script = ts.createSourceFile("App.ts", source, ts.ScriptTarget.Latest, true);
const names = new Set(["commitEventChange", "commitEventComment", "toggleEventHighlight"]);
const handlers = script.statements.filter(statement => ts.isFunctionDeclaration(statement)
  && names.has(statement.name?.text ?? "")).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(handlers, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 事件与整体说明通过同一事务提交，旧工程和旧事件对象不得覆盖新工程。
test("事件悬停注释提交校验目标身份并保持单次事务", () => {
  const project = ref(blankProject());
  project.value.events = [{ id: "1", name: "移动", codeName: "Move", description: "旧注释" }];
  const original = project.value.events[0]!;
  const eventIndex = computed(() => new EventRegistryIndex(project.value));
  const highlightedEventIDs = shallowRef(new Set<string>());
  let transactions = 0;
  const context = {
    project, projectReady: ref(true), workspaceChanging: ref(false), eventIndex, highlightedEventIDs,
    normalizeEventDescription, validateEventRegistry, pruneHighlightedEvents, toggleHighlightedEvent,
    mutate(change: () => void) { transactions++; change(); }, notice() {},
  };
  const app = runInNewContext(`${js}\n({ commitEventComment, toggleEventHighlight });`, context) as {
    commitEventComment(sourceProject: Project, target: EventDefinition | null, value: string): boolean;
    toggleEventHighlight(id: string): void;
  };
  assert.equal(app.commitEventComment(project.value, original, "新注释"), true);
  assert.equal(original.description, "新注释");
  assert.equal(transactions, 1);
  assert.equal(app.commitEventComment(project.value, null, "整体说明"), true);
  assert.equal(project.value.eventEnumDescription, "整体说明");
  assert.equal(transactions, 2);
  app.toggleEventHighlight("1");
  assert.deepEqual([...highlightedEventIDs.value], ["1"]);
  app.toggleEventHighlight("1");
  assert.equal(highlightedEventIDs.value.size, 0);
  app.toggleEventHighlight("missing");
  assert.equal(highlightedEventIDs.value.size, 0);

  const stale = project.value;
  project.value = blankProject();
  assert.equal(app.commitEventComment(stale, original, "不得提交"), false);
  assert.equal(transactions, 2);
  project.value.events = [{ ...original }];
  assert.equal(app.commitEventComment(project.value, original, "过期对象"), false);
  assert.equal(transactions, 2);
});
