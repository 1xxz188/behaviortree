import test from "node:test";
import assert from "node:assert/strict";
import { blankProject } from "./project.ts";
import {
  projectFileKey,
  recentProjectKey,
  rememberProject,
  tabSessionKey,
  readTabSession,
  rememberTabSession,
  startupProject,
  validProjectFileName,
} from "./workspace.ts";
import type { SessionStorage } from "./workspace.ts";

// 用内存存储模拟浏览器历史，避免测试依赖浏览器或磁盘。
function memoryStorage(): SessionStorage {
  const entries = new Map<string, string>();
  return {
    getItem: (key) => entries.get(key) ?? null,
    setItem: (key, value) => { entries.set(key, value); },
  };
}

// 首次进入默认目录且没有最近工程时自动打开唯一工程。
test("首次进入单候选直接打开", () => {
  assert.equal(startupProject({ workspace: "E:\\workspace", files: ["bt_project.json"] }), "bt_project.json");
});

// 无页签身份时沿用每个目录的最近工程；已有页签身份优先于共享记录。
test("新页签按目录恢复最近工程且不覆盖已有页签", () => {
  const storage = memoryStorage();
  const first = { workspace: "E:\\first", files: ["a.json", "b.json"] };
  const second = { workspace: "E:\\second", files: ["a.json", "b.json"] };
  assert.notEqual(recentProjectKey(first.workspace), recentProjectKey(second.workspace));
  rememberProject(first.workspace, "a.json", storage);
  rememberProject(second.workspace, "b.json", storage);
  assert.equal(startupProject(first, undefined, storage), "a.json");
  assert.equal(startupProject(second, undefined, storage), "b.json");
  assert.equal(startupProject(first, { workspace: first.workspace, fileName: "b.json" }, storage), "b.json");
});

// 最近文件或页签文件消失时均不能偷偷选择剩余的唯一工程。
test("已记住文件缺失时不跳到其他单候选", () => {
  const storage = memoryStorage();
  const list = { workspace: "E:\\workspace", files: ["remaining.json"] };
  rememberProject(list.workspace, "removed.json", storage);
  assert.equal(startupProject(list, undefined, storage), undefined);
  assert.equal(startupProject(list, { workspace: list.workspace, fileName: "removed.json" }, storage), undefined);
});

// 页签恢复自身成功打开的文件，多候选没有记录时等待选择。
test("页签身份恢复当前工程", () => {
  const storage = memoryStorage();
  const list = { workspace: "E:\\workspace", files: ["a.json", "b.json"] };
  assert.equal(startupProject(list), undefined);
  rememberTabSession(list.workspace, "b.json", storage);
  assert.deepEqual(readTabSession(storage), { workspace: list.workspace, fileName: "b.json" });
  assert.equal(startupProject(list, readTabSession(storage)), "b.json");
});

// 记录的文件消失时不打开剩余的单候选，当前目录仍保留供重新选择。
test("缺失文件不自动打开其他工程", () => {
  const storage = memoryStorage();
  rememberTabSession("E:\\workspace", "removed.json", storage);
  assert.equal(startupProject({ workspace: "E:\\workspace", files: ["a.json"] }, readTabSession(storage)), undefined);
  assert.equal(startupProject({ workspace: "E:\\workspace", files: [] }, readTabSession(storage)), undefined);
});

// 两个页签即使目录相同，也分别恢复自己的工程文件。
test("同目录不同页签的文件身份相互隔离", () => {
  const firstStorage = memoryStorage(), secondStorage = memoryStorage();
  const first = "E:\\workspace-one";
  const list = { workspace: first, files: ["a.json", "b.json"] };
  rememberTabSession(first, "a.json", firstStorage);
  rememberTabSession(first, "b.json", secondStorage);
  assert.equal(startupProject(list, readTabSession(firstStorage)), "a.json");
  assert.equal(startupProject(list, readTabSession(secondStorage)), "b.json");
  assert.equal(readTabSession(firstStorage)?.workspace, readTabSession(secondStorage)?.workspace);
});

// 无效记录及存储异常只影响恢复功能，不能阻断手动操作。
test("存储读写异常降级为手动选择", () => {
  const storage: SessionStorage = {
    getItem: () => { throw new Error("存储不可读"); },
    setItem: () => { throw new Error("存储不可写"); },
  };
  assert.equal(readTabSession(storage), undefined);
  assert.equal(startupProject({ workspace: "E:\\workspace", files: ["a.json", "b.json"] }, readTabSession(storage)), undefined);
  assert.doesNotThrow(() => rememberTabSession("E:\\workspace", "a.json", storage));
  assert.doesNotThrow(() => rememberTabSession("E:\\workspace", "a.json"));
  const malformed = memoryStorage();
  malformed.setItem(tabSessionKey, "not json");
  assert.equal(readTabSession(malformed), undefined);
});

// 文件名只能指向工作目录顶层的非隐藏 JSON 文件，扩展名不区分大小写。
test("工程文件名接受普通 JSON 名称并拒绝路径和隐藏文件", () => {
  for (const name of ["project.json", "行为树工程.json", "My Project.JSON"]) {
    assert.equal(validProjectFileName(name), true, name);
  }
  for (const name of ["", ".json", ".hidden.json", "project.txt", "project.json.bak", "../project.json", "folder/project.json", "folder\\project.json", "E:project.json"]) {
    assert.equal(validProjectFileName(name), false, name);
  }
});

// Windows 盘符与网络共享目录中的大小写别名必须命中原文件，覆盖时保留磁盘实际名称。
test("Windows 工程文件键忽略大小写并可找回实际文件名", () => {
  for (const workspace of ["E:\\workspace", "e:/workspace", "\\\\server\\share\\workspace"]) {
    const actualName = "BT_Project.JSON";
    const existing = new Map([[projectFileKey(workspace, actualName), actualName]]);
    assert.equal(projectFileKey(workspace, "bt_project.json"), projectFileKey(workspace, actualName), workspace);
    assert.equal(existing.get(projectFileKey(workspace, "bt_project.json")), actualName, workspace);
  }
});

// Linux 工作目录允许不同大小写的工程并存，不能错误地把另存为视为覆盖。
test("Linux 工程文件键保留大小写区别", () => {
  const workspace = "/home/user/workspace";
  const upper = "BT_Project.JSON";
  const lower = "bt_project.json";
  assert.notEqual(projectFileKey(workspace, upper), projectFileKey(workspace, lower));
  const existing = new Map([[projectFileKey(workspace, upper), upper]]);
  assert.equal(existing.get(projectFileKey(workspace, lower)), undefined);
  assert.equal(existing.get(projectFileKey(workspace, upper)), upper);
});

// 新建工程只带可编辑根节点，不带巡逻示例、业务目录或示例黑板。
test("空白工程没有示例内容且不同新工程相互独立", () => {
  const project = blankProject();
  assert.deepEqual(project.catalog, []);
  assert.deepEqual(project.blackboard, []);
  assert.equal(project.trees.length, 1);
  const tree = project.trees[0]!;
  assert.equal(tree.nodes.length, 1);
  assert.equal(tree.nodes[0]!.id, tree.root);
  assert.equal(tree.nodes[0]!.type, "sequence");
  assert.equal(tree.nodes[0]!.children?.length, 0);
  tree.nodes[0]!.children!.push("user-node");
  assert.deepEqual(blankProject().trees[0]!.nodes[0]!.children, []);
});
