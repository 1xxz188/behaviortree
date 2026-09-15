package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestProjectListWorkspace 验证相对工作目录转换为绝对路径，并保留空数组、排序和顶层文件筛选契约。
func TestProjectListWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	s, err := New("workspace")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wantWorkspace, err := filepath.Abs("workspace")
	if err != nil {
		t.Fatal(err)
	}

	// 检查空目录也返回可显示的路径，且 files 仍为 JSON 数组。
	w := callEditor(t, s, "GET", "/api/projects", nil)
	var response struct {
		Workspace string   `json:"workspace"` // 服务端解析后的工作目录。
		Files     []string `json:"files"`     // 原有的候选工程文件列表。
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(response.Workspace) || response.Workspace != wantWorkspace {
		t.Fatalf("工作目录 = %q，期望 %q", response.Workspace, wantWorkspace)
	}
	if response.Files == nil || len(response.Files) != 0 {
		t.Fatalf("空目录文件列表 = %#v，期望非 nil 空数组", response.Files)
	}

	// 列表仅保留可读取的工程草稿，忽略无关配置、损坏内容、隐藏文件和目录。
	for _, name := range []string{"z.json", "a.json", ".hidden.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(wantWorkspace, name), []byte(`{"schemaVersion":1,"trees":[{"id":"main","nodes":[]}]}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"config.json": `{"exclude":[]}`, "broken.json": `{`, "empty.json": `{}`,
		"version.json":   `{"schemaVersion":99,"trees":[{"id":"main"}]}`,
		"duplicate.json": `{"schemaVersion":1,"trees":[{"id":"main"},{"id":"MAIN"}]}`,
		"type.json":      `{"schemaVersion":1,"trees":[{"id":"main","nodes":[{"id":"n","type":"invalid"}]}]}`,
		"trailing.json":  `{"schemaVersion":1,"trees":[{"id":"main"}]} {}`,
	} {
		if err := os.WriteFile(filepath.Join(wantWorkspace, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(wantWorkspace, "nested.json"), 0755); err != nil {
		t.Fatal(err)
	}
	// 超限文件先检查大小，不能进入候选或造成无界内容读取。
	large, err := os.Create(filepath.Join(wantWorkspace, "large.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = large.Truncate(MaxProjectBytes + 1)
	_ = large.Close()
	if err != nil {
		t.Fatal(err)
	}
	w = callEditor(t, s, "GET", "/api/projects", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Workspace != wantWorkspace || !slices.Equal(response.Files, []string{"a.json", "z.json"}) {
		t.Fatalf("工程列表响应不符合契约: %s", w.Body.String())
	}
	// 切换工作目录也必须使用同一过滤规则，同时保留无效同名文件供覆盖确认。
	listing := workspaceTestDecode(t, callEditor(t, s, "POST", "/api/workspace", map[string]string{"directory": wantWorkspace}))
	if !slices.Equal(listing.Files, response.Files) {
		t.Fatalf("切换目录未过滤无效工程: %+v", listing)
	}
	var names struct {
		AllFiles []string `json:"allFiles"` // 保存时用于提示同名覆盖的完整候选。
	}
	if err := json.Unmarshal(w.Body.Bytes(), &names); err != nil || !slices.Contains(names.AllFiles, "config.json") {
		t.Fatalf("覆盖确认丢失无效 JSON 文件: %s (%v)", w.Body.String(), err)
	}
	// 外部修改后刷新必须重新判断内容：损坏的工程消失，修好的工程重新出现。
	for name, content := range map[string]string{
		"a.json": `{`, "broken.json": `{"schemaVersion":1,"trees":[{"id":"main","nodes":[]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(wantWorkspace, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	refreshed := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if !slices.Equal(refreshed.Files, []string{"broken.json", "z.json"}) {
		t.Fatalf("刷新后工程有效性未更新: %+v", refreshed)
	}
}
