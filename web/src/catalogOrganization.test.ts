import assert from "node:assert/strict";
import test from "node:test";
import { CatalogOrganizationIndex, validateCatalogOrganization } from "./catalogOrganization.ts";
import type { CatalogOrganization } from "./catalogOrganization.ts";
import { blankProject, clone } from "./project.ts";
import type { Definition } from "./project.ts";
import { semanticSignature } from "./generation.ts";
import { validateProjectTypes } from "./enums.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 测试定义携带固定身份，避免随机 ID 让断言不稳定。
function definition(id: string, name = id): Definition { return { id, name, goName: `Go${id}`, kind: "action" }; }
// 工程夹具仅提供定义和空行为树，分类不应访问或修改树数据。
function projectWith(...ids: string[]) { const project = blankProject(); project.catalog = ids.map((id) => definition(id)); return project; }

// 打开未组织的工程不能悄悄写分类，缺省定义须排在持久化根条目后面。
test("稀疏分类无副作用，根目录缺省定义按声明顺序追加", () => {
  const project = projectWith("a", "b", "c");
  project.catalogOrganization = { folders: [{ id: "f", name: "目录", parentId: "", order: 4 }], tags: [], assignments: { b: { folderId: "", tagIds: [], order: 3 } } };
  const before = stringifyJSON(project), index = new CatalogOrganizationIndex(project);
  assert.equal(stringifyJSON(project), before);
  assert.deepEqual(index.entries().map((entry) => entry.id), ["b", "f", "a", "c"]);
  assert.equal(index.definitionPath("a"), "根目录");
});

// 首次编辑必须固化缺省条目位置，不能因只保存部分关联而在重开时跳位。
test("稀疏根条目首次新增目录和标签后保存重开顺序不漂移", () => {
  const project = projectWith("a", "b"), index = new CatalogOrganizationIndex(project);
  index.addFolder("末尾目录", "", "f");
  assert.deepEqual(index.entries().map((entry) => entry.id), ["a", "b", "f"]);
  assert.deepEqual(new CatalogOrganizationIndex(clone(project)).entries().map((entry) => entry.id), ["a", "b", "f"]);
  index.addTag("标签", "t"); index.setTags("b", ["t"]);
  assert.deepEqual(new CatalogOrganizationIndex(clone(project)).entries().map((entry) => entry.id), ["a", "b", "f"]);
  // 既有目录在缺省定义之前时，设置其中一个定义标签也必须保持完整顺序。
  const sparse = projectWith("a", "b"); sparse.catalogOrganization = { folders: [{ id: "f", name: "目录", parentId: "", order: 0 }], tags: [{ id: "t", name: "标签" }], assignments: {} };
  const sparseIndex = new CatalogOrganizationIndex(sparse); sparseIndex.setTags("b", ["t"]);
  assert.deepEqual(new CatalogOrganizationIndex(clone(sparse)).entries().map((entry) => entry.id), ["f", "a", "b"]);
});

// 混合排序、目录迁移及防环失败都通过同一个操作 API 验证。
test("嵌套目录支持混合排序、移动，拒绝环、重名与非空删除", () => {
  const project = projectWith("a", "b"), index = new CatalogOrganizationIndex(project);
  index.addFolder("  战斗  ", "", "battle"); index.addFolder("追击", "battle", "chase");
  index.move({ kind: "definition", id: "b" }, "battle", { kind: "folder", id: "chase" });
  assert.deepEqual(index.entries("battle").map((entry) => entry.id), ["b", "chase"]);
  assert.equal(index.definitionPath("b"), "根目录 / 战斗");
  assert.throws(() => index.deleteFolder("battle"), /空目录/);
  const before = stringifyJSON(project);
  assert.throws(() => index.move({ kind: "folder", id: "battle" }, "chase"), /自身或后代/);
  assert.equal(stringifyJSON(project), before);
  assert.throws(() => index.renameFolder("", "其它根"), /固定根/);
  assert.throws(() => index.addFolder("战斗", "", "other"), /重复/);
  index.move({ kind: "definition", id: "a" }, "battle");
  index.move({ kind: "definition", id: "a" }, "battle", { kind: "definition", id: "b" });
  assert.deepEqual(index.entries("battle").map((entry) => entry.id), ["a", "b", "chase"]);
  index.move({ kind: "definition", id: "a" }, ""); index.move({ kind: "definition", id: "b" }, "");
  index.deleteFolder("chase"); index.deleteFolder("battle");
  assert.equal(index.folders.size, 0);
  assert.doesNotThrow(() => validateProjectTypes(project));
});

// 搜索跨目录，标签交集和名称、ID、函数名均应参与匹配。
test("标签 AND 检索和标签删除仅影响关联，不删除定义", () => {
  const project = projectWith("Move", "Attack", "Patrol"), index = new CatalogOrganizationIndex(project);
  index.addFolder("移动", "", "f"); index.move({ kind: "definition", id: "Move" }, "f");
  index.addTag("  常用  ", "common"); index.addTag("移动", "motion");
  index.setTags("Move", ["common", "motion"]); index.setTags("Patrol", ["motion"]);
  assert.deepEqual(index.search("gomove", ["common", "motion"]).map((item) => item.id), ["Move"]);
  assert.deepEqual(index.search("", ["common", "motion"]).map((item) => item.id), ["Move"]);
  assert.equal(index.search("", ["missing"]).length, 0);
  index.renameTag("common", "关键"); assert.equal(index.tags.get("common")?.name, "关键");
  index.deleteTag("motion"); assert.deepEqual(index.assignments.get("Move")?.tagIds, ["common"]);
  assert.deepEqual(index.assignments.get("Patrol")?.tagIds, []); assert.equal(project.catalog.length, 3);
  assert.throws(() => index.addTag("关键"), /重复/);
});

// 大整数默认值与特殊属性 ID 的组织信息须一起无损保存、恢复和撤销。
test("特殊 ID 安全保存，快照恢复与分类操作保持生成语义不变", () => {
  const project = projectWith("__proto__", "constructor"), before = clone(project);
  project.catalog[0].params = [{ name: "Target", type: "uint64", default: parseJSON("18446744073709551615") }];
  const signature = semanticSignature(project), index = new CatalogOrganizationIndex(project);
  index.addFolder("目录", "", "__proto__"); index.addTag("标签", "constructor");
  index.move({ kind: "definition", id: "__proto__" }, "__proto__"); index.setTags("__proto__", ["constructor"]);
  assert.equal(semanticSignature(project), signature);
  assert.equal(Object.hasOwn(project.catalogOrganization!.assignments, "__proto__"), true);
  const restored = clone(project), restoredIndex = new CatalogOrganizationIndex(restored);
  assert.equal(restoredIndex.definitionPath("__proto__"), "根目录 / 目录");
  assert.equal(stringifyJSON(restored.catalog[0].params![0].default), "18446744073709551615");
  assert.equal(new CatalogOrganizationIndex(before).folders.size, 0);
  restoredIndex.removeDefinition("__proto__"); restored.catalog = restored.catalog.filter((item) => item.id !== "__proto__");
  assert.equal(restoredIndex.tagMembers.get("constructor")?.size, 0);
  assert.doesNotThrow(() => validateProjectTypes(restored));
});

// 导入同 ID 更新对象时沿用组织，新声明追加到指定目录末尾。
test("新定义追加和定义替换保留已有分类", () => {
  const project = projectWith("a"), index = new CatalogOrganizationIndex(project);
  index.addFolder("目标", "", "f"); index.move({ kind: "definition", id: "a" }, "f"); index.addTag("常用", "t"); index.setTags("a", ["t"]);
  project.catalog = [definition("a", "新的名称"), definition("b")];
  index.assignNewDefinitions(["a", "b"], "f");
  assert.deepEqual(index.entries("f").map((entry) => entry.id), ["a", "b"]);
  assert.deepEqual(index.assignments.get("a")?.tagIds, ["t"]);
  assert.equal(index.search("新的名称")[0]?.id, "a");
  // 集成方也可以在更新 catalog 后重建索引，再为新增 ID 指定目录。
  project.catalog.push(definition("c")); const rebuilt = new CatalogOrganizationIndex(project); rebuilt.assignNewDefinitions(["c"], "f");
  assert.equal(rebuilt.assignments.get("c")?.folderId, "f");
});

// 整批导入时首条写入会补齐隐式根分类，后续新定义仍必须全部移动到指定目录。
test("三条定义批量导入目标目录且同 ID 替换保持既有分类", () => {
  const project = projectWith("existing"), first = new CatalogOrganizationIndex(project);
  first.addFolder("已有目录", "", "old"); first.addFolder("导入目录", "", "target");
  first.move({ kind: "definition", id: "existing" }, "old"); first.addTag("保留标签", "tag"); first.setTags("existing", ["tag"]);
  project.catalog = [definition("existing", "更新声明"), definition("a"), definition("b"), definition("c")];
  const index = new CatalogOrganizationIndex(project);
  index.assignNewDefinitions(["existing", "a", "b", "c"], "target");
  assert.deepEqual(index.entries("target").map((entry) => entry.id), ["a", "b", "c"]);
  assert.equal(index.assignments.get("existing")?.folderId, "old");
  assert.deepEqual(index.assignments.get("existing")?.tagIds, ["tag"]);
  const restored = new CatalogOrganizationIndex(clone(project));
  assert.deepEqual(restored.entries("target").map((entry) => entry.id), ["a", "b", "c"]);
  assert.equal(restored.entries().filter((entry) => entry.kind === "definition").length, 0);
  // 在 catalog 更新前构造索引时也应能把同批定义全部追加。
  project.catalog.push(definition("d"), definition("e"), definition("f"));
  index.assignNewDefinitions(["d", "e", "f"], "target");
  assert.deepEqual(index.entries("target").map((entry) => entry.id), ["a", "b", "c", "d", "e", "f"]);
});

// 各种恶意或破损输入必须在工程替换前被边界校验拒绝。
test("组织校验拒绝重复身份、循环、悬空关联和非法字段", () => {
  const project = projectWith("a");
  const valid: CatalogOrganization = { folders: [{ id: "f", name: "目录", parentId: "", order: 0 }], tags: [{ id: "t", name: "标签" }], assignments: { a: { folderId: "f", tagIds: ["t"], order: 0 } } };
  const corruptions: ((organization: CatalogOrganization) => void)[] = [
    (organization) => organization.folders.push({ ...organization.folders[0] }),
    (organization) => organization.folders[0].parentId = "f",
    (organization) => organization.folders[0].parentId = "missing",
    (organization) => organization.assignments.a.folderId = "missing",
    (organization) => organization.assignments.a.tagIds.push("t"),
    (organization) => organization.assignments.a.tagIds.push("missing"),
    (organization) => organization.assignments.missing = { folderId: "", tagIds: [], order: 0 },
    (organization) => organization.tags.push({ id: "other", name: " 标签 " }),
    (organization) => organization.folders[0].order = Infinity,
    (organization) => organization.folders[0].name = " ",
  ];
  for (const corrupt of corruptions) { const organization = clone(valid); corrupt(organization); assert.throws(() => validateCatalogOrganization(project.catalog, organization)); }
  assert.throws(() => validateCatalogOrganization(project.catalog, []), /对象/);
  assert.throws(() => validateProjectTypes({ ...project, schemaVersion: 1 }), /版本 2/);
  assert.throws(() => validateProjectTypes({ ...project, schemaVersion: undefined }), /版本 2/);
});

// 两千定义常见负载与两万级深链验证不使用递归，也不为分类访问行为树。
test("2000 定义索引、筛选、移动和深链防环有界完成", () => {
  const project = projectWith(...Array.from({ length: 2000 }, (_, i) => `node${i}`));
  const started = performance.now(), index = new CatalogOrganizationIndex(project);
  index.addFolder("目标", "", "f"); index.addTag("匹配", "t");
  for (let i = 0; i < 2000; i += 10) index.setTags(`node${i}`, ["t"]);
  assert.equal(index.search("", ["t"]).length, 200);
  index.move({ kind: "definition", id: "node1999" }, "f");
  assert.equal(index.entries().length, 2000); assert.equal(index.entries("f").length, 1);
  assert.ok(performance.now() - started < 3000, "两千定义基础操作应在宽松时间上限内完成");
  const folders = Array.from({ length: 20000 }, (_, i) => ({ id: `f${i}`, name: `目录${i}`, parentId: i ? `f${i - 1}` : "", order: 0 }));
  const organization = { folders, tags: [], assignments: {} };
  assert.doesNotThrow(() => validateCatalogOrganization([], organization));
  folders[0].parentId = "f19999";
  assert.throws(() => validateCatalogOrganization([], organization), /目录环/);
});
