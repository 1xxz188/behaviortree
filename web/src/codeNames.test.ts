import test from "node:test";
import assert from "node:assert/strict";
import { CodeNameIndex, defaultCodeName, normalizeCodeNames, validCodeName } from "./codeNames.ts";
import { blankProject, clone } from "./project.ts";
import type { BTNode, Tree } from "./project.ts";
import { captureSnapshot, restoreSnapshot } from "./treeIdentity.ts";
import { semanticSignature, GenerationRequests } from "./generation.ts";
import { stringifyJSON } from "./json.ts";
import { ProjectSaveState } from "./saveState.ts";

// 创建包含目标节点的独立树，避免拓扑影响代码名测试。
function makeTree(nodes: BTNode[]): Tree {
  return { id: "main", name: "主树", root: nodes[0]?.id ?? "", nodes };
}

// 输入不修剪、不接受 Go 关键字或尾部换行，大小写不同仍属于不同代码名。
test("代码名严格限制短ASCII标识符和Go关键字", () => {
  for (const value of ["", "_name", "1name", "名字", "a-b", "a\n", "a\r\n", "a ", "type", "for", "a".repeat(41), null, 12])
    assert.equal(validCodeName(value), false, String(value));
  for (const value of ["Root", "root", "A_12", "a".repeat(40), "Type"]) assert.equal(validCodeName(value), true);
  assert.deepEqual(defaultCodeName({ id: "root", type: "sequence" }), { base: "Root", forceNumber: false });
  assert.deepEqual(defaultCodeName({ id: `node_${"a".repeat(32)}`, type: "subtree" }), { base: "Subtree", forceNumber: true });
  assert.deepEqual(defaultCodeName({ id: "11223344-5566-7788-99aa-bbccddeeff00", type: "wait" }), { base: "Wait", forceNumber: true });
  assert.deepEqual(defaultCodeName({ id: "type", type: "action" }), { base: "Action", forceNumber: true });
});

// 先预留显式名称，按 ID 固定顺序补全默认代码名，结果不依赖节点数组顺序。
test("默认补全保留显式值并使用与Go一致的Unicode排序", () => {
  const nodes: BTNode[] = [
    { id: "\u{10000}", type: "wait" }, { id: "\ue000", type: "wait" },
    { id: "explicit", type: "wait", codeName: "Wait" },
    { id: "root", type: "sequence", codeName: "" },
  ];
  const tree = makeTree(clone(nodes));
  new CodeNameIndex(tree);
  assert.deepEqual(tree.nodes.map((node) => node.codeName), ["Wait2", "Wait1", "Wait", "Root"]);
  const reversed = makeTree(clone(nodes).reverse());
  new CodeNameIndex(reversed);
  assert.deepEqual(reversed.nodes.reverse(), tree.nodes);
  assert.throws(() => new CodeNameIndex(makeTree([
    { id: "a", type: "wait", codeName: "Wait" }, { id: "b", type: "wait", codeName: "Wait" },
  ])), /已存在/);
});

// 长名称共享截短前缀时仍只检查有限次候选，跨位数后继续保持 40 字符上限。
test("长代码名碰撞共享后缀且持续满足长度限制", () => {
  const stem = "A".repeat(39);
  const tree = makeTree([{ id: "a", type: "wait", codeName: `${stem}X` }, { id: "b", type: "wait", codeName: `${stem}Y` }]);
  const index = new CodeNameIndex(tree);
  for (let i = 1; i <= 1000; i++) {
    const codeName = index.allocate(`${stem}${i % 2 ? "X" : "Y"}`);
    assert.ok(validCodeName(codeName));
    assert.equal(codeName, `${stem.slice(0, 40 - String(i).length)}${i}`);
    index.add({ id: String(i), type: "wait", codeName });
  }
});

// 分配后的代码名写入节点；删除、排序、改显示名和改 ID 均不重新生成名称。
test("创建复制和删除保持已有代码名稳定，提交校验不扫描树", () => {
  const original: BTNode = { id: "random?", type: "wait" };
  const tree = makeTree([original]);
  const index = new CodeNameIndex(tree);
  assert.equal(original.codeName, "Wait1");
  const copied = { ...original, id: "copy", codeName: index.allocateCopy(original.codeName!) };
  index.add(copied);
  assert.equal(copied.codeName, "Wait2");
  tree.nodes.push(copied);
  tree.nodes.reverse();
  index.remove(original);
  tree.nodes.pop();
  copied.id = "changed";
  copied.name = "更易读的展示名";
  assert.equal(copied.codeName, "Wait2");
  Object.defineProperty(tree, "nodes", { get() { throw new Error("普通名称操作不应扫描节点"); } });
  index.rename(copied, "WaitMove");
  assert.equal(copied.codeName, "WaitMove");
  assert.ok(index.validateRename(copied, "var"));
  assert.throws(() => index.rename(copied, "bad-name"));
  assert.equal(copied.codeName, "WaitMove");
});

// 保存前补全后的工程和旧数据具有相同语义，修改代码名会淘汰在途请求。
test("补全代码名签名稳定且显式改名使源码失效", () => {
  const project = blankProject();
  delete project.trees[0]!.nodes[0]!.codeName;
  const signature = semanticSignature(project);
  normalizeCodeNames(project);
  assert.equal(semanticSignature(project), signature);
  const requests = new GenerationRequests();
  const token = requests.begin(project);
  const node = project.trees[0]!.nodes[0]!;
  node.codeName = "Run";
  assert.notEqual(semanticSignature(project), signature);
  assert.equal(requests.accepts(token, project), false);
});

// 显式应用在一次快照中保存，撤销/重做同时恢复代码名与保存状态，JSON 包含补全值。
test("代码名保存与撤销重做保留稳定身份和代码名", () => {
  const project = blankProject();
  const tree = project.trees[0]!;
  const node = tree.nodes[0]!;
  const index = new CodeNameIndex(tree);
  const before = captureSnapshot(project, tree.id, node.id);
  const state = new ProjectSaveState();
  state.reset(before.project);
  index.rename(node, "Run");
  state.changed();
  const after = captureSnapshot(project, tree.id, node.id);
  const undone = restoreSnapshot(before).project;
  state.restore(stringifyJSON(undone));
  assert.equal(undone.trees[0]!.nodes[0]!.codeName, "Root");
  assert.equal(state.dirty, false);
  const redone = restoreSnapshot(after).project;
  state.restore(stringifyJSON(redone));
  assert.equal(redone.trees[0]!.nodes[0]!.codeName, "Run");
  assert.equal(redone.trees[0]!.nodes[0]!.id, "root");
  assert.equal(state.dirty, true);
  assert.match(after.project, /"codeName":"Run"/);
});
