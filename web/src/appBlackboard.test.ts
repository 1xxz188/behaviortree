import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { compile, createSSRApp } from "vue";
import { renderToString } from "@vue/server-renderer";

// 渲染真实黑板标题和说明模板，确保职责边界直接显示在页面中。
test("黑板顶部说明决策状态、参数绑定、稳定 ID 和 Go Context 职责", async () => {
  const app = readFileSync(new URL("./App.vue", import.meta.url), "utf8");
  const fields = app.indexOf('v-for="(field, i) in project.blackboard"');
  const start = app.lastIndexOf('<div class="panel-heading small-heading">', fields);
  const end = app.indexOf("<section", start);
  assert.ok(start >= 0 && end > start, "应找到黑板顶部模板");
  const html = await renderToString(createSSRApp({
    setup: () => ({ addField: () => {} }),
    render: compile(app.slice(start, end)),
  }));
  assert.match(html, />\s*AI 黑板字段\s*<button/);
  assert.match(html, />＋ 字段<\/button>/);
  const note = html.match(/<p class="muted empty-note">([^<]+)<\/p>/)?.[1];
  assert.ok(note, "说明应沿用辅助文字样式，直接呈现为正文");
  for (const sentence of [
    "保存行为树运行期间的决策状态，并可绑定到节点参数。",
    "字段值会在兼容热更时按稳定 ID 保留。",
    "玩家、NPC、房间、服务等真实业务对象请放在 Go Context 中。",
  ]) {
    assert.ok(note.includes(sentence), `页面缺少说明：${sentence}`);
  }
});
