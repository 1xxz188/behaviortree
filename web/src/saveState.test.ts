import test from "node:test";
import assert from "node:assert/strict";
import { blankProject } from "./project.ts";
import type { Project } from "./project.ts";
import { parseInput } from "./json.ts";
import { captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import type { EditorSnapshot } from "./treeIdentity.ts";
import { ProjectSaveState } from "./saveState.ts";

// 沿用编辑器快照入口，包含布局和无损整数，选择信息不参与保存比较。
function snapshot(project: Project): EditorSnapshot {
  return captureSnapshot(project, project.trees[0]!.id, project.trees[0]!.root);
}

// 先恢复真实工程，再用恢复内容更新状态，模拟撤销和重做调用链。
function restore(state: ProjectSaveState, history: EditorSnapshot): Project {
  const restored = restoreSnapshot(history).project;
  state.restore(snapshot(restored).project);
  return restored;
}

// 撤销回到磁盘版本必须取消未保存提示，重做编辑后应再次提示。
test("编辑后撤销回已保存内容，再重做会正确切换保存状态", () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  const opened = snapshot(project);
  state.reset(opened.project);
  assert.equal(state.dirty, false);
  project.name = "修改后的名称";
  state.changed();
  const edited = snapshot(project);
  assert.equal(state.dirty, true);
  project = restore(state, opened);
  assert.equal(project.name, "未命名工程");
  assert.equal(state.dirty, false);
  project = restore(state, edited);
  assert.equal(project.name, "修改后的名称");
  assert.equal(state.dirty, true);
});

// 保存后历史仍保留，但只有最新写入的版本可被识别为已保存。
test("中途保存后，撤销跨过保存点变脏，重做回保存点变干净", () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  const original = snapshot(project);
  state.reset(original.project);
  project.name = "第二版本";
  state.changed();
  const saved = snapshot(project);
  state.saved(saved.project, saved.project);
  assert.equal(state.dirty, false);
  project.name = "第三版本";
  state.changed();
  const latest = snapshot(project);
  project = restore(state, saved);
  assert.equal(state.dirty, false);
  project = restore(state, original);
  assert.equal(state.dirty, true);
  project = restore(state, saved);
  assert.equal(state.dirty, false);
  project = restore(state, latest);
  assert.equal(project.name, "第三版本");
  assert.equal(state.dirty, true);
});

// 保存响应只能确认发出请求时的内容，期间新增的编辑不能被误标为已保存。
test("保存期间继续编辑保持未保存，撤销回请求快照后恢复已保存", () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  state.reset(snapshot(project).project);
  project.name = "请求中的版本";
  state.changed();
  const requested = snapshot(project);
  project.name = "请求发出后的编辑";
  state.changed();
  state.saved(requested.project, snapshot(project).project);
  assert.equal(state.dirty, true);
  project = restore(state, requested);
  assert.equal(project.name, "请求中的版本");
  assert.equal(state.dirty, false);
});

// 请求期间编辑后又撤销回请求内容，成功响应应按内容确认已保存。
test("保存期间编辑又撤销后，成功响应仍能确认相同内容已保存", () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  state.reset(snapshot(project).project);
  project.name = "待保存版本";
  state.changed();
  const requested = snapshot(project);
  project.name = "临时编辑";
  state.changed();
  project = restore(state, requested);
  assert.equal(state.dirty, true);
  state.saved(requested.project, snapshot(project).project);
  assert.equal(state.dirty, false);
});

// 保存失败不会调用成功入口，因此仍以旧磁盘内容判断撤销后的状态。
test("保存失败保留旧基准，失败版本未保存而原版本仍已保存", async () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  const original = snapshot(project);
  state.reset(original.project);
  project.name = "写入失败的版本";
  state.changed();
  const requested = snapshot(project);
  await assert.rejects(async () => {
    await Promise.reject(new Error("磁盘写入失败"));
    state.saved(requested.project, snapshot(project).project);
  }, /磁盘写入失败/);
  assert.equal(state.dirty, true);
  project = restore(state, original);
  assert.equal(state.dirty, false);
  project = restore(state, requested);
  assert.equal(project.name, "写入失败的版本");
  assert.equal(state.dirty, true);
});

// 未落盘的新建和导入工程没有保存基准，撤销到初始内容也仍须保存。
test("没有磁盘基准的草稿撤销后仍未保存，首次保存才建立基准", () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  const initial = snapshot(project);
  state.reset();
  assert.equal(state.dirty, true);
  project.name = "草稿编辑";
  state.changed();
  project = restore(state, initial);
  assert.equal(state.dirty, true);
  const firstSave = snapshot(project).project;
  state.saved(firstSave, firstSave);
  assert.equal(state.dirty, false);
  state.reset();
  project = restore(state, initial);
  assert.equal(state.dirty, true);
});

// 保存比较必须覆盖布局，并区别超出安全整数范围且仅末位不同的参数。
test("布局与相邻64位整数变化均参与保存状态判断", () => {
  const state = new ProjectSaveState();
  let project = blankProject();
  project.blackboard.push({ id: "id", name: "实体编号", type: "uint64", default: parseInput("18446744073709551614", "uint64") });
  const saved = snapshot(project);
  state.reset(saved.project);
  project.trees[0]!.layout![project.trees[0]!.root]!.x += 1;
  state.changed();
  const moved = snapshot(project);
  project = restore(state, moved);
  assert.equal(state.dirty, true);
  project = restore(state, saved);
  assert.equal(state.dirty, false);
  project.blackboard[0]!.default = parseInput("18446744073709551615", "uint64");
  state.changed();
  const changedInteger = snapshot(project);
  assert.notEqual(changedInteger.project, saved.project);
  project = restore(state, changedInteger);
  assert.equal(state.dirty, true);
  assert.match(snapshot(project).project, /18446744073709551615/);
  project = restore(state, saved);
  assert.equal(state.dirty, false);
  assert.match(snapshot(project).project, /18446744073709551614/);
});
