import test from "node:test";
import assert from "node:assert/strict";
import type { Definition } from "./project.ts";
import { catalogConflicts, mergeCatalog, parseCatalog } from "./catalog.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 构造最小有效动作声明，便于突出各测试的冲突条件。
function action(id: string, goName = id): Definition {
  return { id, name: id, kind: "action", goName, params: [] };
}

// 合并导入只追加新 ID，不会删除已有业务绑定所需的声明。
test("目录导入保留旧项和顺序并追加新项", () => {
  const current = [action("Old")];
  const merged = mergeCatalog(current, [action("New")]);
  assert.deepEqual(merged.map((item) => item.id), ["Old", "New"]);
  merged[0]!.name = "新名称";
  assert.equal(current[0]!.name, "Old");
});

// 同 ID 定义变化必须逐项明确确认，两种选择均能正确生效。
test("同 ID 冲突禁止隐式覆盖并支持保留或更新", () => {
  const current = [action("Move")];
  const incoming = [{ ...action("Move"), kind: "condition" as const }];
  assert.equal(catalogConflicts(current, incoming).length, 1);
  assert.throws(() => mergeCatalog(current, incoming), /请处理目录冲突/);
  assert.equal(mergeCatalog(current, incoming, new Map([["Move", "keep"]]))[0]!.kind, "action");
  assert.equal(mergeCatalog(current, incoming, new Map([["Move", "replace"]]))[0]!.kind, "condition");
  assert.equal(current[0]!.kind, "action");
});

// 不同 ID 共享 Go 名会破坏生成代码；失败必须不改变输入目录。
test("跨 ID 的 Go 名冲突拒绝整个合并", () => {
  const current = [action("Old", "Move")];
  const before = stringifyJSON(current);
  assert.throws(() => mergeCatalog(current, [action("New", "Move")]), /Go 函数名 Move/);
  assert.equal(stringifyJSON(current), before);
});

// 目录参数中的 64 位默认值必须在冲突处理和复制时保留全部精度。
test("合并保留大整数默认值且相同声明无需冲突选择", () => {
  const catalog = parseCatalog(parseJSON('[{"id":"Read","name":"读取","kind":"action","goName":"Read","params":[{"name":"Value","type":"uint64","default":18446744073709551615}]}]'));
  assert.equal(catalogConflicts(catalog, catalog).length, 0);
  assert.match(stringifyJSON(mergeCatalog(catalog, catalog)), /18446744073709551615/);
});

// 重复 ID、非法容器和非法类型在发起网络请求前报告具体原因。
test("拒绝重复 ID 和非法目录输入", () => {
  assert.throws(() => parseCatalog([action("A"), action("A")]), /重复 ID/);
  assert.throws(() => parseCatalog({ catalog: [] }), /JSON 数组/);
  assert.throws(() => parseCatalog([{ ...action("A"), kind: "wait" }]), /kind/);
});
