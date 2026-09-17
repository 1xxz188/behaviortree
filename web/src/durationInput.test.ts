import assert from "node:assert/strict";
import test from "node:test";
import { parseInput, parseJSON, stringifyJSON } from "./json.ts";
import { parseCatalog } from "./catalog.ts";

// 节点常量和黑板默认值共享输入路径，可读时长在保存后保留原始单位与精度。
test("可读 duration 输入作为 JSON 字符串无损往返", () => {
  for (const text of ["1s", "500ms", "2.5s", "1m30s", "1ns", "-1.5s", "2562047h47m16.854775807s"]) {
    const value = parseInput(text, "duration");
    assert.equal(value, text);
    assert.equal(parseJSON<{ value: string }>(stringifyJSON({ value })).value, text);
  }
});

// 已有工程的纳秒整数继续保持数字及大整数精度，不被新输入分支转为字符串。
test("旧 duration 纳秒整数保持原表示", () => {
  for (const text of ["0", "1000000000", "-500000000", "9223372036854775807", "-9223372036854775808"]) {
    assert.equal(stringifyJSON(parseInput(text, "duration")), text);
  }
  assert.throws(() => parseInput("", "duration"), /时间长度/);
  assert.throws(() => parseInput("  ", "duration"), /时间长度/);
});

// 参数声明经过 JSON 和目录导入边界后，两种默认值格式均不被转换或丢失。
test("目录 duration 默认值兼容可读字符串和旧纳秒整数", () => {
  const source = '[{"id":"wait_for","name":"等待","kind":"action","goName":"WaitFor","params":[{"name":"Readable","type":"duration","default":"2.5s"},{"name":"Legacy","type":"duration","default":9223372036854775807}]}]';
  const catalog = parseCatalog(parseJSON(source));
  assert.equal(catalog[0]!.params![0]!.default, "2.5s");
  assert.equal(stringifyJSON(catalog[0]!.params![1]!.default), "9223372036854775807");
  assert.deepEqual(parseCatalog(parseJSON(stringifyJSON(catalog))), catalog);
});
