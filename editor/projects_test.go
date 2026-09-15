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

	// 候选列表只按名称筛选，忽略隐藏文件、非 JSON 文件和目录，不读取工程内容。
	for _, name := range []string{"z.json", "a.json", ".hidden.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(wantWorkspace, name), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(wantWorkspace, "nested.json"), 0755); err != nil {
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
}
