package editor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// workspaceTestListing 描述目录浏览响应，只使用公开接口验证工作目录身份。
type workspaceTestListing struct {
	Workspace   string   `json:"workspace"` // 实际绝对目录。
	Files       []string `json:"files"`     // 顶层 JSON 文件。
	Parent      string   `json:"parent"`    // 可返回的上一级目录。
	Directories []struct {
		Name string `json:"name"` // 子目录名称。
		Path string `json:"path"` // 子目录绝对路径。
	} `json:"directories"` // 当前层可选择的目录。
}

// workspaceTestCall 模拟浏览器携带百分号编码的目标目录发起请求。
func workspaceTestCall(t *testing.T, s *Server, method, path, workspace string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:8791"+path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	if workspace != "" {
		r.Header.Set("X-BT-Workspace", strings.ReplaceAll(url.QueryEscape(workspace), "+", "%20"))
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// workspaceTestDecode 检查接口成功后解析目录，防止错误响应被当成空列表。
func workspaceTestDecode(t *testing.T, w *httptest.ResponseRecorder) workspaceTestListing {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("目录请求失败：%d %s", w.Code, w.Body.String())
	}
	var listing workspaceTestListing
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	return listing
}

// workspaceTestServer 创建可清理的本地服务，保证目录句柄在临时目录删除前释放。
func workspaceTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestDirectoryBrowseDoesNotSwitchOrRecurse 验证浏览只返回当前层，不改变后续读写目录。
func TestDirectoryBrowseDoesNotSwitchOrRecurse(t *testing.T) {
	original, target := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	child := filepath.Join(target, "子目录 空格")
	if err := os.MkdirAll(filepath.Join(child, "grandchild"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(target, "top.json"), filepath.Join(child, "nested.json"), filepath.Join(target, "notes.txt")} {
		if err := os.WriteFile(path, []byte(`{"schemaVersion":4,"nextEventId":"1","events":[],"trees":[{"id":"main","nodes":[]}]}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	listing := workspaceTestDecode(t, workspaceTestCall(t, s, "GET", "/api/directories?path="+url.QueryEscape(target), original, nil))
	if listing.Workspace != target || listing.Parent != filepath.Dir(target) || !slices.Equal(listing.Files, []string{"top.json"}) {
		t.Fatalf("目录或顶层文件不正确：%+v", listing)
	}
	if len(listing.Directories) != 1 || listing.Directories[0].Name != filepath.Base(child) || listing.Directories[0].Path != child {
		t.Fatalf("浏览目录递归或子目录路径错误：%+v", listing.Directories)
	}
	current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if current.Workspace != original {
		t.Fatal("浏览目录改变了工作目录", current.Workspace)
	}
}

// TestWorkspaceSwitchMovesReadWriteAndGeneration 验证页签切换后读写生成都使用该页签的目标目录。
func TestWorkspaceSwitchMovesReadWriteAndGeneration(t *testing.T) {
	original := t.TempDir()
	target := filepath.Join(t.TempDir(), "目标 工作目录")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	s := workspaceTestServer(t, original)
	p := model.Example()
	data, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(target, "existing.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	listing := workspaceTestDecode(t, workspaceTestCall(t, s, "POST", "/api/workspace", original, map[string]any{"directory": target}))
	if listing.Workspace != target || !slices.Contains(listing.Files, "existing.json") {
		t.Fatalf("切换响应错误：%+v", listing)
	}
	if current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil)); current.Workspace != original {
		t.Fatal("切换修改了其他页签的默认目录", current.Workspace)
	}
	if w := workspaceTestCall(t, s, "GET", "/api/project?name=existing.json", target, nil); w.Code != 200 {
		t.Fatal("新目录工程不可读", w.Code, w.Body.String())
	}
	if w := workspaceTestCall(t, s, "POST", "/api/project", target, map[string]any{"name": "saved.json", "project": p}); w.Code != 200 {
		t.Fatal("新目录工程保存失败", w.Code, w.Body.String())
	}
	w := workspaceTestCall(t, s, "POST", "/api/generate", target, p)
	if w.Code != 200 {
		t.Fatal("新目录生成失败", w.Code, w.Body.String())
	}
	for _, relative := range []string{"saved.json", filepath.Join(p.Generation.PackagePath, "glue.gen.go")} {
		if _, err = os.Stat(filepath.Join(target, relative)); err != nil {
			t.Fatal("目标文件未写入新目录", relative, err)
		}
		if _, err = os.Stat(filepath.Join(original, relative)); !os.IsNotExist(err) {
			t.Fatal("切换后仍向旧目录写入", relative, err)
		}
	}
	w = workspaceTestCall(t, s, "POST", "/api/generated", target, p)
	if w.Code != 200 {
		t.Fatal("无法读取新目录生成结果", w.Code, w.Body.String())
	}
	var generated struct {
		Directory string `json:"directory"` // 生成产物实际目录。
	}
	if err = json.Unmarshal(w.Body.Bytes(), &generated); err != nil || generated.Directory != filepath.Join(target, p.Generation.PackagePath) {
		t.Fatal("生成产物目录错误", generated.Directory, err)
	}
	if w = workspaceTestCall(t, s, "POST", "/api/preview", target, p); w.Code != 200 {
		t.Fatal("新目录预览失败", w.Code, w.Body.String())
	}
	if w = workspaceTestCall(t, s, "POST", "/api/scaffold/save", target, map[string]any{"project": p}); w.Code != 200 {
		t.Fatal("新目录业务骨架保存失败", w.Code, w.Body.String())
	}
	if _, err = os.Stat(filepath.Join(target, p.Generation.PackagePath, "actions.go")); err != nil {
		t.Fatal("业务骨架未写入目标目录", err)
	}
	if _, err = os.Stat(filepath.Join(original, p.Generation.PackagePath, "actions.go")); !os.IsNotExist(err) {
		t.Fatal("业务骨架写入了默认目录", err)
	}
}

// TestSaveAsChangesWorkspaceAndPreservesOriginal 验证另存为只返回目标目录，后续请求明确使用该目录。
func TestSaveAsChangesWorkspaceAndPreservesOriginal(t *testing.T) {
	original, target := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	p := model.Example()
	if w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "source.json", "project": p}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before, err := os.ReadFile(filepath.Join(original, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "另存为内容"
	w := workspaceTestCall(t, s, "POST", "/api/project", original, map[string]any{"name": "copy.json", "directory": target, "project": p})
	listing := workspaceTestDecode(t, w)
	if listing.Workspace != target {
		t.Fatal("另存为未返回新工作目录", w.Body.String())
	}
	current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if current.Workspace != original || !slices.Contains(current.Files, "source.json") {
		t.Fatalf("另存为修改了默认目录：%+v", current)
	}
	if targetListing := workspaceTestDecode(t, workspaceTestCall(t, s, "GET", "/api/projects", target, nil)); !slices.Contains(targetListing.Files, "copy.json") {
		t.Fatalf("目标目录未列出副本：%+v", targetListing)
	}
	p.Name = "后续保存"
	if w = workspaceTestCall(t, s, "POST", "/api/project", target, map[string]any{"name": "copy.json", "project": p}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	after, err := os.ReadFile(filepath.Join(original, "source.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("另存为或后续保存修改了原工程", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "copy.json"))
	if err != nil || !bytes.Contains(data, []byte("后续保存")) {
		t.Fatal("后续保存未更新另存为目标", err)
	}
}

// TestSaveAsOverwriteAndFailureKeepWorkspace 验证覆盖须明确确认，所有失败均保留原目录和文件。
func TestSaveAsOverwriteAndFailureKeepWorkspace(t *testing.T) {
	original, target := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	existing := []byte("已有文件，不能隐式覆盖")
	if err := os.WriteFile(filepath.Join(target, "existing.json"), existing, 0644); err != nil {
		t.Fatal(err)
	}
	p := model.Example()
	w := workspaceTestCall(t, s, "POST", "/api/project", original, map[string]any{"name": "existing.json", "directory": target, "project": p})
	if w.Code != 409 {
		t.Fatal("未拒绝未经确认的覆盖", w.Code, w.Body.String())
	}
	// 无效工程、缺失目录和非目录路径都不能改变默认目录或覆盖已有文件。
	for _, test := range []struct {
		Path string // 待调用接口。
		Body any    // 必须失败的请求。
	}{
		{"/api/project", map[string]any{"name": "invalid.json", "directory": target, "project": map[string]any{"schemaVersion": 2, "trees": []any{}}}},
		{"/api/workspace", map[string]any{"directory": filepath.Join(target, "missing")}},
		{"/api/workspace", map[string]any{"directory": filepath.Join(target, "existing.json")}},
		{"/api/project", map[string]any{"name": "invalid.json", "directory": filepath.Join(target, "existing.json"), "project": p}},
	} {
		w = workspaceTestCall(t, s, "POST", test.Path, original, test.Body)
		if w.Code < 400 {
			t.Fatal("错误请求意外成功", test.Path, w.Code, w.Body.String())
		}
		current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
		if current.Workspace != original {
			t.Fatal("失败操作改变了工作目录", test.Path, current.Workspace)
		}
	}
	after, err := os.ReadFile(filepath.Join(target, "existing.json"))
	if err != nil || !bytes.Equal(existing, after) {
		t.Fatal("失败请求覆盖了已有文件", err)
	}
	if _, err = os.Stat(filepath.Join(target, "invalid.json")); !os.IsNotExist(err) {
		t.Fatal("非法工程仍写入文件", err)
	}
	w = workspaceTestCall(t, s, "POST", "/api/project", original, map[string]any{"name": "existing.json", "directory": target, "overwrite": true, "project": p})
	if listing := workspaceTestDecode(t, w); listing.Workspace != target {
		t.Fatal("确认覆盖后未返回目标目录", w.Body.String())
	}
	if after, err = os.ReadFile(filepath.Join(target, "existing.json")); err != nil || bytes.Equal(existing, after) {
		t.Fatal("确认覆盖后文件未更新", err)
	}
}

// TestWorkspaceTabsInterleave 验证两个页签切换、保存和读取时始终使用各自目录。
func TestWorkspaceTabsInterleave(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, first)
	p := model.Example()
	for _, tc := range []struct {
		workspace string // 模拟页签发送的目录。
		name      string // 该页签独有的工程文件。
		label     string // 工程内容标记，用于识别交叉写入。
	}{{first, "first.json", "第一页签"}, {second, "second.json", "第二页签"}} {
		workspaceTestDecode(t, workspaceTestCall(t, s, "POST", "/api/workspace", tc.workspace, map[string]string{"directory": tc.workspace}))
		p.Name = tc.label
		if w := workspaceTestCall(t, s, "POST", "/api/project", tc.workspace, map[string]any{"name": tc.name, "project": p}); w.Code != 200 {
			t.Fatal("页签保存失败", tc.name, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		workspace string // 模拟页签发送的目录。
		name      string // 应读取的工程文件。
		other     string // 不应出现在此目录的工程文件。
		label     string // 应读取的工程内容标记。
	}{{first, "first.json", "second.json", "第一页签"}, {second, "second.json", "first.json", "第二页签"}} {
		listing := workspaceTestDecode(t, workspaceTestCall(t, s, "GET", "/api/projects", tc.workspace, nil))
		if listing.Workspace != tc.workspace || !slices.Contains(listing.Files, tc.name) || slices.Contains(listing.Files, tc.other) {
			t.Fatalf("页签目录列表交叉：%+v", listing)
		}
		w := workspaceTestCall(t, s, "GET", "/api/project?name="+tc.name, tc.workspace, nil)
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(tc.label)) {
			t.Fatal("页签读取错误", tc.name, w.Code, w.Body.String())
		}
		if w = workspaceTestCall(t, s, "GET", "/api/project?name="+tc.other, tc.workspace, nil); w.Code != 404 {
			t.Fatal("跨目录读到了其他页签工程", tc.other, w.Code)
		}
		p.Name = tc.label
		if w = workspaceTestCall(t, s, "POST", "/api/generate", tc.workspace, p); w.Code != 200 {
			t.Fatal("页签生成失败", tc.workspace, w.Code, w.Body.String())
		}
		if w = workspaceTestCall(t, s, "POST", "/api/generated", tc.workspace, p); w.Code != 200 {
			t.Fatal("页签无法读取自身产物", tc.workspace, w.Code, w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(tc.workspace, p.Generation.PackagePath, "glue.gen.go")); err != nil {
			t.Fatal("页签产物未写入自身目录", tc.workspace, err)
		}
	}
}

// TestWorkspaceHeaderRejectsInvalidDirectory 验证非法、缺失和非目录目标不会退回默认目录写入。
func TestWorkspaceHeaderRejectsInvalidDirectory(t *testing.T) {
	defaultDir := t.TempDir()
	s := workspaceTestServer(t, defaultDir)
	file := filepath.Join(defaultDir, "not-directory")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"relative", filepath.Join(defaultDir, "missing"), file, "%ZZ"} {
		w := workspaceTestCall(t, s, "POST", "/api/project", target, map[string]any{"name": "invalid.json", "project": model.Example()})
		if w.Code != 400 {
			t.Fatal("无效目录未拒绝", target, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(defaultDir, "invalid.json")); !os.IsNotExist(err) {
		t.Fatal("无效目录请求写入了默认目录", err)
	}
}
