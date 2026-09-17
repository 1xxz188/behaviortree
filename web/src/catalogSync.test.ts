import assert from "node:assert/strict";
import test from "node:test";
import { synchronizeCatalog } from "./catalogSync.ts";
import { blankProject } from "./project.ts";
import type { Definition, Parameter, Project, Value } from "./project.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 创建具有多个引用节点的工程，验证同步不受当前树或当前选择限制。
function fixture(params: Parameter[], bindings: Record<string, Value>) {
  const project = blankProject();
  const definition: Definition = { id: "Check", name: "检查", kind: "condition", goName: "Check", params };
  project.catalog = [definition];
  project.trees = ["main", "other"].map(id => ({ id, name: id, root: "1", nodes: [
    { id: "1", type: "condition", binding: definition.id, params: parseJSON(stringifyJSON(bindings)) },
  ] }));
  return { project, definition };
}

// 用户报告的替换场景必须清除旧参数，补配后 JSON 重载保持完全一致。
test("跨树清除 Target/en，新 E/IsDeath 未绑定，配置后保存重载不复活旧键", () => {
  const { project, definition } = fixture([{ name: "Target", type: "int64" }, { name: "en", type: "entity" }],
    { Target: { value: 7 }, en: { value: 8 } });
  const updated: Definition = { ...definition, params: [{ name: "E", type: "entity" }, { name: "IsDeath", type: "bool" }] };
  assert.deepEqual(synchronizeCatalog(project, [updated]), []);
  for (const tree of project.trees) {
    assert.deepEqual(Object.keys(tree.nodes[0]!.params!), []);
    tree.nodes[0]!.params = { E: { value: 1 }, IsDeath: { value: true } };
  }
  const saved = stringifyJSON(project);
  assert.doesNotMatch(saved, /"Target"|"en"/);
  const loaded = parseJSON<Project>(saved);
  assert.deepEqual(loaded.trees[0]!.nodes[0]!.params, { E: { value: 1 }, IsDeath: { value: true } });
  synchronizeCatalog(loaded, loaded.catalog);
  assert.equal(stringifyJSON(loaded), saved);
});

// 未改名且兼容的字段/常量绑定保留，新增参数默认值保留 false、0 和 64 位整数。
test("保留兼容绑定并无损初始化新增默认值", () => {
  const { project, definition } = fixture([{ name: "E", type: "entity" }, { name: "IsDeath", type: "bool" }],
    { E: { field: "enemy" }, IsDeath: { value: false } });
  project.blackboard = [{ id: "enemy", name: "目标", type: "entity" }];
  const existing = project.trees[0]!.nodes[0]!.params!;
  const max = parseJSON("18446744073709551615");
  synchronizeCatalog(project, [{ ...definition, params: [...definition.params!,
    { name: "Max", type: "uint64", default: max }, { name: "Flag", type: "bool", default: false },
    { name: "Zero", type: "int64", default: 0 }, { name: "New", type: "string" },
  ] }]);
  const result = project.trees[0]!.nodes[0]!.params!;
  assert.equal(result.E, existing.E);
  assert.equal(result.IsDeath, existing.IsDeath);
  assert.equal(result.Flag!.value, false);
  assert.equal(result.Zero!.value, 0);
  assert.equal(Object.hasOwn(result, "New"), false);
  assert.match(stringifyJSON(result), /18446744073709551615/);
});

// 同名改型不能保留不兼容来源；错误明确定位树、节点、参数，可兼容常量继续保留。
test("类型变更重置不兼容值和字段，保留兼容的无损常量", () => {
  const { project, definition } = fixture([
    { name: "Flag", type: "entity" }, { name: "Bound", type: "entity" }, { name: "Count", type: "int64" },
    { name: "Negative", type: "int64" }, { name: "Large", type: "uint64" },
  ], { Flag: { value: 1 }, Bound: { field: "enemy" }, Count: { value: 3 }, Negative: { value: -1 }, Large: { value: parseJSON("18446744073709551615") } });
  project.blackboard = [{ id: "enemy", name: "目标", type: "entity" }];
  const messages = synchronizeCatalog(project, [{ ...definition, params: [
    { name: "Flag", type: "bool" }, { name: "Bound", type: "uint64", default: 0 },
    { name: "Count", type: "uint64" }, { name: "Negative", type: "uint64" }, { name: "Large", type: "int64" },
  ] }]);
  assert.equal(messages.length, 8);
  assert.equal(messages[0]!.treeId, "main");
  assert.equal(messages[0]!.nodeId, "1");
  assert.equal(messages[0]!.field, "params.Flag");
  assert.match(messages[0]!.message, /旧绑定不兼容.*未绑定参数/);
  assert.deepEqual(parseJSON(stringifyJSON(project.trees[0]!.nodes[0]!.params)), { Bound: { value: 0 }, Count: { value: 3 } });
});

// 收紧枚举时既检查常量，也检查黑板字段的整个取值域。
test("枚举约束收紧只重置失效绑定", () => {
  const { project, definition } = fixture(["Keep", "Drop", "Field"].map(name => ({ name, type: "enum", enum: ["A", "B"] })),
    { Keep: { value: "A" }, Drop: { value: "B" }, Field: { field: "mode" } });
  project.blackboard = [{ id: "mode", name: "模式", type: "enum", enum: ["A", "B"] }];
  const messages = synchronizeCatalog(project, [{ ...definition, params: definition.params!.map(param => ({ ...param, enum: ["A"] })) }]);
  assert.equal(messages.length, 4);
  assert.deepEqual(Object.keys(project.trees[0]!.nodes[0]!.params!), ["Keep"]);
});

// 已经存在隐藏旧数据时，即使再次应用同一目录，也会清理所有孤立参数。
test("再次应用同一目录修复历史残留且不改动无定义的节点", () => {
  const { project } = fixture([{ name: "E", type: "entity" }], { E: { value: 1 }, Target: { value: 3 } });
  project.trees[1]!.nodes[0]!.binding = "Missing";
  synchronizeCatalog(project, project.catalog);
  assert.deepEqual(Object.keys(project.trees[0]!.nodes[0]!.params!), ["E"]);
  assert.deepEqual(Object.keys(project.trees[1]!.nodes[0]!.params!), ["E", "Target"]);
});

// 显式空绑定仍应由校验器报告未绑定，不能因重新应用目录而悄悄转为默认值。
test("保留已有显式空绑定与默认值的区别", () => {
  const { project } = fixture([{ name: "E", type: "entity", default: 1 }], { E: {} });
  synchronizeCatalog(project, project.catalog);
  assert.deepEqual(project.trees[0]!.nodes[0]!.params!.E, {});
});

// 多树节点绑定相同枚举字段时，只检查一次字段取值域，后续使用兼容性缓存。
test("重复黑板枚举绑定复用兼容性检查", () => {
  const { project, definition } = fixture([{ name: "Mode", type: "enum", enum: ["A", "B"] }], { Mode: { field: "mode" } });
  let checks = 0;
  const values = ["A"];
  Object.defineProperty(values, "0", { get() { checks++; return "A"; } });
  project.blackboard = [{ id: "mode", name: "模式", type: "enum", enum: values }];
  synchronizeCatalog(project, [{ ...definition, params: [{ name: "Mode", type: "enum", enum: ["A"] }] }]);
  assert.equal(checks, 1);
});
