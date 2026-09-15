import test from "node:test";
import assert from "node:assert/strict";
import { blankProject } from "./project.ts";
import {
  projectFileKey,
  recentProjectKey,
  rememberProject,
  startupProject,
  validProjectFileName,
} from "./workspace.ts";
import type { RecentStorage } from "./workspace.ts";

// 用内存存储模拟浏览器历史，避免测试依赖浏览器或磁盘。
function memoryStorage(): RecentStorage {
  const entries = new Map<string, string>();
  return {
    getItem: (key) => entries.get(key) ?? null,
    setItem: (key, value) => { entries.set(key, value); },
  };
}

// 唯一候选不依赖历史读取，浏览器禁用存储也应自动打开。
test("单候选直接打开且不读取存储", () => {
  const storage: RecentStorage = {
    getItem: () => { assert.fail("单候选不应读取历史"); },
    setItem: () => {},
  };
  assert.equal(startupProject({ workspace: "E:\\workspace", files: ["bt_project.json"] }, storage), "bt_project.json");
});

// 多候选只恢复用户成功打开过的有效文件，不默认取排序首项。
test("多候选恢复有效历史，无记录时等待选择", () => {
  const storage = memoryStorage();
  const list = { workspace: "E:\\workspace", files: ["a.json", "b.json"] };
  assert.equal(startupProject(list, storage), undefined);
  assert.equal(startupProject(list), undefined);
  rememberProject(list.workspace, "b.json", storage);
  assert.equal(startupProject(list, storage), "b.json");
});

// 文件被移除后不能把过期历史或剩余列表首项当作用户选择。
test("过期历史与空目录均不选择工程", () => {
  const storage = memoryStorage();
  rememberProject("E:\\workspace", "removed.json", storage);
  assert.equal(startupProject({ workspace: "E:\\workspace", files: ["a.json", "b.json"] }, storage), undefined);
  assert.equal(startupProject({ workspace: "E:\\workspace", files: [] }, storage), undefined);
});

// 同一浏览器连接不同工作目录时，最近文件记录必须相互隔离。
test("不同工作目录分别恢复最近工程", () => {
  const storage = memoryStorage();
  const first = "E:\\workspace-one";
  const second = "E:\\workspace-two";
  assert.notEqual(recentProjectKey(first), recentProjectKey(second));
  rememberProject(first, "a.json", storage);
  rememberProject(second, "b.json", storage);
  assert.equal(startupProject({ workspace: first, files: ["a.json", "b.json"] }, storage), "a.json");
  assert.equal(startupProject({ workspace: second, files: ["a.json", "b.json"] }, storage), "b.json");
});

// 隐私模式和存储配额异常只影响历史功能，不能阻断打开工程。
test("存储读写异常降级为手动选择", () => {
  const storage: RecentStorage = {
    getItem: () => { throw new Error("存储不可读"); },
    setItem: () => { throw new Error("存储不可写"); },
  };
  assert.equal(startupProject({ workspace: "E:\\workspace", files: ["a.json", "b.json"] }, storage), undefined);
  assert.doesNotThrow(() => rememberProject("E:\\workspace", "a.json", storage));
  assert.doesNotThrow(() => rememberProject("E:\\workspace", "a.json"));
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
