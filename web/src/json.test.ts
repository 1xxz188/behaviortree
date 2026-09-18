import assert from "node:assert/strict";
import test from "node:test";
import { parseJSON, stringifyJSON } from "./json.ts";

// 特殊属性应当是数据而非原型；嵌套对象和数组都必须保留同名 ID。
test("无损 JSON 保留 __proto__ 自身属性且不修改对象原型", () => {
  const text = '{"__proto__":{"polluted":true},"nested":[{"__proto__":{"id":"child"}}]}';
  const value = parseJSON<Record<string, unknown>>(text);
  assert.equal(Object.getPrototypeOf(value), Object.prototype);
  assert.equal(Object.hasOwn(value, "__proto__"), true);
  assert.equal(Object.hasOwn({}, "polluted"), false);
  assert.deepEqual(value.__proto__, { polluted: true });
  const nested = (value.nested as Record<string, unknown>[])[0];
  assert.equal(Object.getPrototypeOf(nested), Object.prototype);
  assert.deepEqual(nested.__proto__, { id: "child" });
  assert.deepEqual(parseJSON(stringifyJSON(value)), value);
});

// Unicode 转义与临时哨兵冲突不能绕过恢复，也不能误改字符串值。
test("转义对象键及哨兵冲突正确恢复，字符串内容保持原样", () => {
  const text = '{"\\u005f_proto__":{"count":18446744073709551615},"\\u005f_codex_json_proto_0__":"existing","text":"__proto__ and \\\"__proto__\\\": value"}';
  const value = parseJSON<Record<string, unknown>>(text);
  assert.equal(Object.hasOwn(value, "__proto__"), true);
  assert.equal(value.__codex_json_proto_0__, "existing");
  assert.equal(value.text, '__proto__ and "__proto__": value');
  assert.equal(stringifyJSON((value.__proto__ as Record<string, unknown>).count), "18446744073709551615");
  assert.equal(Object.keys(value).some((key) => key === "__codex_json_proto_1__"), false);
  assert.equal(parseJSON('"__proto__"'), "__proto__");
});

// 标量、null 和数组值也必须作为正常数据保留；重复与语法错误仍被拒绝。
test("危险键各种值均保留，非法 JSON 继续拒绝", () => {
  for (const scalar of ['null', '1', '"plain"', '[1,2]']) {
    const text = `{"__proto__":${scalar}}`, value = parseJSON<Record<string, unknown>>(text);
    assert.equal(Object.hasOwn(value, "__proto__"), true);
    assert.equal(stringifyJSON(value), text);
    assert.equal(Object.getPrototypeOf(value), Object.prototype);
  }
  for (const invalid of ['{"__proto__":1,"__proto__":2}', '{"__proto__":}', '{"__proto__":1,}', '{"__proto__" 1}', '{"__proto__":1} trailing']) {
    assert.throws(() => parseJSON(invalid));
  }
});
