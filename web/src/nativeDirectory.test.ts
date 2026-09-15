import assert from "node:assert/strict";
import test from "node:test";
import { selectNativeDirectory } from "./workspace.ts";

// 系统选择仅获取目录信息，不提前调用切换或保存接口，中文路径通过请求头安全传递。
test("原生目录选择发送初始路径并返回目标目录", async () => {
  const result = await selectNativeDirectory("E:\\工程", "D:\\保存目录", async (url, init) => {
    assert.equal(url, "/api/directory-picker");
    assert.equal(init?.method, "POST");
    assert.equal(new Headers(init?.headers).get("X-BT-Workspace"), encodeURIComponent("E:\\工程"));
    assert.deepEqual(JSON.parse(String(init?.body)), { directory: "D:\\保存目录" });
    return new Response(JSON.stringify({ workspace: "D:\\选择结果", files: ["copy.json"] }));
  });
  assert.deepEqual(result, { workspace: "D:\\选择结果", files: ["copy.json"] });
});

// 用户取消系统窗口与请求失败各自返回明确结果，调用方可保留原目录。
test("原生目录窗口取消返回空结果，失败保留错误原因", async () => {
  assert.equal(await selectNativeDirectory("E:\\工程", undefined, async () => new Response('{"cancelled":true}')), undefined);
  await assert.rejects(() => selectNativeDirectory("E:\\工程", undefined, async () => new Response('{"error":"目录窗口不可用"}', { status: 500 })), /目录窗口不可用/);
});
