import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import type { TestContext } from "node:test";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import * as Vue from "vue";
import { compile } from "@vue/compiler-dom";
import { renderToString } from "vue/server-renderer";

// 执行真实提示组件脚本和模板，验证操作结束后用户实际可读的内容。
const file = readFileSync(new URL("./OperationNotice.vue", import.meta.url), "utf8");
const script = ts.createSourceFile("Notice.ts", file.split('<script setup lang="ts">')[1]!.split("</script>")[0]!, ts.ScriptTarget.Latest, true);
const code = script.statements.filter(statement => !ts.isImportDeclaration(statement)).map(statement => statement.getText(script)).join("\n");
const js = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const template = file.split("<template>")[1]!.split("</template>")[0]!;
const render = runInNewContext(`(() => { ${compile(template, { mode: "function", prefixIdentifiers: true }).code} })()`, { Vue });
// 使用应用实际的结果发布函数，确保同文案操作也有独立版本，取消则没有。
const appFile = readFileSync(new URL("./App.vue", import.meta.url), "utf8");
const appScript = ts.createSourceFile("App.ts", appFile.split('<script setup lang="ts">')[1]!.split("</script>")[0]!, ts.ScriptTarget.Latest, true);
const publishSource = appScript.statements.find(statement => ts.isFunctionDeclaration(statement) && statement.name?.text === "notice")!.getText(appScript);
const publishJS = ts.transpileModule(publishSource, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

// 用 Vue 响应式刷新模拟异步请求完成，无需计时等待。
function notice(t: TestContext) {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const props = Vue.reactive({ message: "初始状态", failed: false, busy: false, revision: 0 });
  const publish = runInNewContext(`${publishJS}\nnotice;`, {
    message: Vue.toRef(props, "message"), error: Vue.toRef(props, "failed"), noticeRevision: Vue.toRef(props, "revision"),
  }) as (message: string, failed?: boolean) => void;
  const scope = Vue.effectScope();
  const state = scope.run(() => runInNewContext(`${js}\n({ visible, dismiss });`, { ...Vue, setTimeout, clearTimeout, defineProps: () => props })) as {
    visible: Vue.Ref<boolean>; // 真实组件的显示状态。
    dismiss: () => void; // 真实关闭事件处理函数。
  };
  const html = () => renderToString(Vue.createSSRApp({ setup: () => ({ ...Vue.toRefs(props), ...state }), render }));
  return { props, state, html, scope, publish };
}

// 下载结果完整展示实际文件路径，初始状态不冒充成功。
test("下载成功展示完整路径及可关闭提示", async t => {
  const n = notice(t);
  try {
    assert.equal(n.state.visible.value, false);
    n.props.busy = true;
    await Vue.nextTick();
    n.publish("下载成功，业务骨架已保存到 E:/workspace/behavior/actions.go");
    await Vue.nextTick();
    assert.equal(n.state.visible.value, false);
    n.props.busy = false;
    await Vue.nextTick();
    const html = await n.html();
    assert.match(html, /role="status"/);
    assert.match(html, /下载成功/);
    assert.match(html, /E:\/workspace\/behavior\/actions.go/);
    assert.match(html, /关闭操作提示/);
  } finally { n.scope.stop(); }
});

// 保存窗口取消时没有产生新结果，不应重新弹出上一次打开工程的成功提示。
test("取消保存不重新展示已打开工程的旧提示", async t => {
  const n = notice(t);
  try {
    n.publish("已打开 room_final_audited.json");
    await Vue.nextTick();
    n.state.dismiss();
    n.props.busy = true;
    await Vue.nextTick();
    n.props.busy = false;
    await Vue.nextTick();
    assert.equal(n.state.visible.value, false);
    assert.doesNotMatch(await n.html(), /已打开/);
  } finally { n.scope.stop(); }
});

// 重复点击校验得到相同结果时仍应再次提示，错误使用 alert。
test("重复操作相同结果重新显示，失败使用错误提示", async t => {
  const n = notice(t);
  try {
    n.publish("校验通过，可以生成 Go 代码");
    await Vue.nextTick();
    n.state.dismiss();
    n.props.busy = true;
    await Vue.nextTick();
    n.publish("校验通过，可以生成 Go 代码");
    n.props.busy = false;
    await Vue.nextTick();
    assert.match(await n.html(), /校验通过/);
    n.publish("校验请求失败", true);
    await Vue.nextTick();
    assert.match(await n.html(), /role="alert"/);
    assert.match(await n.html(), /校验请求失败/);
  } finally { n.scope.stop(); }
});

// 精确推进虚拟时钟，验证三秒消失和新消息重置计时，不进行真实等待。
test("提示三秒自动消失，新提示不会被旧计时器提前关闭", async t => {
  const n = notice(t);
  try {
    n.publish("已保存");
    await Vue.nextTick();
    t.mock.timers.tick(2999);
    assert.equal(n.state.visible.value, true);
    n.publish("校验请求失败", true);
    await Vue.nextTick();
    t.mock.timers.tick(1);
    assert.equal(n.state.visible.value, true);
    t.mock.timers.tick(2998);
    assert.equal(n.state.visible.value, true);
    t.mock.timers.tick(1);
    assert.equal(n.state.visible.value, false);
    assert.doesNotMatch(await n.html(), /校验请求失败/);
  } finally { n.scope.stop(); }
});

// 手动关闭和卸载均清理计时器，关闭后新操作仍能正常显示。
test("手动关闭及卸载清理提示生命周期", async t => {
  const n = notice(t);
  n.publish("下载成功");
  await Vue.nextTick();
  n.state.dismiss();
  assert.equal(n.state.visible.value, false);
  n.props.busy = true;
  await Vue.nextTick();
  n.publish("下载成功");
  n.props.busy = false;
  await Vue.nextTick();
  assert.equal(n.state.visible.value, true);
  n.scope.stop();
  t.mock.timers.tick(3000);
  assert.equal(n.state.visible.value, false);
});
