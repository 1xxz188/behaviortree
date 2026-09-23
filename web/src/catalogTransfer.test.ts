import assert from "node:assert/strict";
import test from "node:test";
import { exportCatalogPackage, mergeCatalogPackage, parseCatalogPackage } from "./catalog.ts";
import { validatedCatalogJSON } from "./catalogTransfer.ts";
import { parseJSON, stringifyJSON } from "./json.ts";
import { blankProject } from "./project.ts";

const event = { id: "1", name: "命令变化", codeName: "CommandChanged", description: "先更新状态\n再通知" };
const definition = { id: "Move", name: "移动", kind: "action" as const, goName: "Move", eventIds: [event.id] };

// 目录交换只携带实际依赖的事件，保留两类注释与十进制 ID。
test("Schema 4 目录交换保留事件和双层注释", async (t) => {
  t.mock.method(globalThis, "fetch", async (_input: unknown, options?: RequestInit) => new Response(String(options?.body), { status: 200 }));
  const project = blankProject();
  project.events = [event, { id: "2", name: "其他", codeName: "Other" }];
  project.nextEventId = "3";
  project.catalog = [definition];
  project.eventEnumDescription = "工程约定\r\n先更新状态";
  const content = await validatedCatalogJSON(project, project.catalog);
  const parsed = parseCatalogPackage(parseJSON(content));
  assert.deepEqual(parsed.events.map(item => item.id), [event.id]);
  assert.equal(parsed.eventEnumDescription, "工程约定\n先更新状态");
  assert.equal(parsed.events[0]!.description, event.description);
  assert.match(content, /"id": "1"/);
});

// 旧数组、旧字符串字段与悬空 ID 必须在网络请求前拒绝。
test("目录导入严格拒绝旧格式和未知事件", () => {
  assert.throws(() => parseCatalogPackage([definition]), /旧数组/);
  assert.throws(() => parseCatalogPackage({ kind: "behaviortree.catalog", schemaVersion: 4, events: [], catalog: [{ ...definition, events: ["command"] }] }), /旧字符串事件/);
  assert.throws(() => parseCatalogPackage({ kind: "behaviortree.catalog", schemaVersion: 4, events: [], catalog: [definition] }), /未定义事件/);
});

// 同号但不同的导入事件自动分配新 ID，并同步业务定义引用。
test("成员 ID 冲突自动重映射且保留源工程", () => {
  const project = blankProject();
  project.events = [event];
  project.nextEventId = "2";
  const imported = { ...event, name: "采集", codeName: "PickUp" };
  const importedDefinition = { ...definition, id: "PickUp", eventIds: [imported.id] };
  const incoming = parseCatalogPackage({ kind: "behaviortree.catalog", schemaVersion: 4, events: [imported], catalog: [importedDefinition] });
  const merged = mergeCatalogPackage(project, incoming, new Map());
  assert.deepEqual(merged.events.map(item => item.id), ["1", "2"]);
  assert.deepEqual(merged.catalog[0]!.eventIds, ["2"]);
  assert.equal(merged.nextEventId, "3");
  assert.deepEqual(project.events.map(item => item.id), ["1"]);
  assert.deepEqual(incoming.catalog[0]!.eventIds, ["1"]);
});

// 不同 ID 的相同代码名必须显式映射或重命名。
test("目录事件代码名冲突可映射或重命名", () => {
  const project = blankProject();
  project.events = [event];
  project.nextEventId = "2";
  const incomingEvent = { ...event, id: "2" };
  const incoming = parseCatalogPackage({ kind: "behaviortree.catalog", schemaVersion: 4, events: [incomingEvent], catalog: [{ ...definition, eventIds: [incomingEvent.id] }] });
  assert.throws(() => mergeCatalogPackage(project, incoming, new Map()), /重复代码名|冲突/);
  const mapped = mergeCatalogPackage(project, incoming, new Map(), new Map([[incomingEvent.id, event.id]]));
  assert.equal(mapped.events.length, 1);
  assert.deepEqual(mapped.catalog[0]!.eventIds, [event.id]);
  const renamed = mergeCatalogPackage(project, incoming, new Map(), new Map(), new Map([[incomingEvent.id, "OtherChanged"]]));
  assert.equal(renamed.events[1]!.codeName, "OtherChanged");
});

// 空注释可省略，导出过程不改变传入工程。
test("目录导出不变更原模型", () => {
  const project = blankProject();
  project.events = [{ ...event, description: " \n " }];
  project.nextEventId = "2";
  project.catalog = [definition];
  const before = stringifyJSON(project);
  const exported = exportCatalogPackage(project, project.catalog);
  assert.equal(exported.events[0]!.description, "");
  assert.equal(stringifyJSON(project), before);
});
