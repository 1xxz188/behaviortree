import assert from "node:assert/strict";
import test from "node:test";
import { BUSINESS_NAMING_VERSION, businessNameError, businessNamePrefix, prefixedBusinessName } from "./businessNames.ts";
import { parseCatalog } from "./catalog.ts";
import { validateProjectTypes } from "./enums.ts";
import { blankProject } from "./project.ts";

// 固定前缀只适用于两类业务名称；新实例转换不重复叠加已有前缀，也不更改名称大小写。
test("业务前缀按种类固定且已带前缀的名称只保留一次", () => {
  assert.equal(BUSINESS_NAMING_VERSION, 1);
  assert.equal(businessNamePrefix("action"), "Action");
  assert.equal(businessNamePrefix("condition"), "Is");
  for (const kind of ["sequence", "wait", "", "Action"]) assert.equal(businessNamePrefix(kind), "");
  for (const [kind, original, expected] of [
    ["action", "MoveTo", "ActionMoveTo"],
    ["action", "ActionMoveTo", "ActionMoveTo"],
    ["condition", "Ready", "IsReady"],
    ["condition", "IsReady", "IsReady"],
    ["action", "actionMove", "ActionactionMove"],
    ["condition", "ActionMove", "IsActionMove"],
    ["action", "", "Action"],
    ["wait", "Wait", "Wait"],
  ]) assert.equal(prefixedBusinessName(kind!, original!), expected);
});

// 历史数据无需符合新前缀；新规则要求大小写精确的前缀和非空后缀，并拒绝未知版本。
test("业务命名规则兼容历史版本并严格检查新名称的前缀边界", () => {
  for (const version of [undefined, 0]) {
    assert.equal(businessNameError("action", "MoveTo", version), null);
    assert.equal(businessNameError("condition", "Ready", version), null);
    assert.equal(businessNameError("wait", "Wait", version), null);
  }
  for (const [kind, prefix] of [["action", "Action"], ["condition", "Is"]] as const) {
    for (const valid of [prefix + "A", prefix + "_1", prefix + "1"])
      assert.equal(businessNameError(kind, valid, BUSINESS_NAMING_VERSION), null);
    for (const invalid of [undefined, null, 12, {}, "", prefix, prefix.toLowerCase() + "Ready", "Ready", " " + prefix + "Ready"])
      assert.match(businessNameError(kind, invalid, BUSINESS_NAMING_VERSION)!, new RegExp(prefix));
  }
  assert.match(businessNameError("wait", "Wait", BUSINESS_NAMING_VERSION)!, /仅适用于动作和条件/);
  for (const invalid of [null, 2, -1, 0.5, "0", "1", false, true, {}, []])
    assert.match(businessNameError("action", "ActionMove", invalid)!, /namingVersion/);
});

// 目录输入边界识别新规则元数据；旧函数可以继续导入，新声明无法用错误前缀或版本绕过检查。
test("目录解析保留旧函数与新元数据并拒绝错误的新业务名称", () => {
  const catalog = [
    { id: "old", name: "旧移动", kind: "action", goName: "MoveTo" },
    { id: "new", name: "新移动", kind: "action", goName: "ActionMoveTo", namingVersion: BUSINESS_NAMING_VERSION },
    { id: "ready", name: "就绪", kind: "condition", goName: "IsReady", namingVersion: BUSINESS_NAMING_VERSION },
  ];
  assert.deepEqual(parseCatalog(catalog), catalog);
  for (const invalid of [
    { ...catalog[1], goName: "MoveTo" },
    { ...catalog[1], goName: "Action" },
    { ...catalog[2], goName: "ActionReady" },
    { ...catalog[2], goName: "Is" },
    { ...catalog[1], namingVersion: null },
    { ...catalog[1], namingVersion: "1" },
    { ...catalog[1], namingVersion: 2 },
  ]) assert.throws(() => parseCatalog([invalid]), /catalog\[0\]\.goName/);
});

// 工程加载允许缺省代码名留给分配器，但显式的新规则名称和元数据必须在进入编辑器前有效。
test("工程输入兼容旧节点及新缺省代码名并拒绝新规则的错误前缀", () => {
  const project = blankProject();
  project.trees[0]!.nodes = [
    { id: "old", type: "condition", codeName: "LegacyCheck" },
    { id: "missing-old", type: "condition" },
    { id: "missing-new", type: "condition", namingVersion: BUSINESS_NAMING_VERSION },
    { id: "new", type: "action", codeName: "ActionMove", namingVersion: BUSINESS_NAMING_VERSION },
  ];
  assert.doesNotThrow(() => validateProjectTypes(project));
  for (const codeName of ["Move", "IsReady", "Action", "actionMove"])
    assert.throws(() => validateProjectTypes({ ...project, trees: [{ ...project.trees[0], nodes: [
      { id: "wrong", type: "action", codeName, namingVersion: BUSINESS_NAMING_VERSION },
    ] }] }), /trees\[0\]\.nodes\[0\]\.codeName.*Action/);
  for (const namingVersion of [null, 2, "1", false])
    assert.throws(() => validateProjectTypes({ ...project, trees: [{ ...project.trees[0], nodes: [
      { id: "wrong", type: "action", codeName: "ActionMove", namingVersion },
    ] }] }), /namingVersion/);
  assert.throws(() => validateProjectTypes({ ...project, trees: [{ ...project.trees[0], nodes: [
    { id: "wrong", type: "wait", codeName: "Wait1", namingVersion: BUSINESS_NAMING_VERSION },
  ] }] }), /仅适用于动作和条件/);
});
