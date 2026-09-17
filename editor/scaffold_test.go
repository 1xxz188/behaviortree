package editor

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// TestSaveScaffoldConfirmation 验证默认路径、同名保护、确认期间变化和明确确认后的覆盖。
func TestSaveScaffoldConfirmation(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Generation.PackagePath = "game/ai/behavior"
	request := map[string]any{"project": p}
	response := callEditor(t, s, "POST", "/api/scaffold/save", request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var saved map[string]string
	if err = json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "game", "ai", "behavior", "actions.go")
	if saved["path"] != path {
		t.Fatal("未使用生成包路径", saved)
	}
	want, err := codegen.Scaffold(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("未保存后端生成的骨架", err)
	}
	handwritten := []byte("package behavior\n// 手写业务实现必须保留。\n")
	if err = os.WriteFile(path, handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	response = callEditor(t, s, "POST", "/api/scaffold/save", request)
	if response.Code != 409 {
		t.Fatal("同名文件未经确认被覆盖", response.Code)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved["existingHash"] != sourceHash(handwritten) || saved["path"] != path {
		t.Fatal("缺少确认目标信息", saved)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, handwritten) {
		t.Fatal("确认前修改了业务实现", err)
	}
	request["overwrite"] = true
	response = callEditor(t, s, "POST", "/api/scaffold/save", request)
	if response.Code != 409 {
		t.Fatal("无摘要确认不应覆盖")
	}
	request["expectedHash"] = saved["existingHash"]
	changed := append(handwritten, []byte("// 确认期间继续编辑。\n")...)
	if err = os.WriteFile(path, changed, 0644); err != nil {
		t.Fatal(err)
	}
	response = callEditor(t, s, "POST", "/api/scaffold/save", request)
	if response.Code != 409 {
		t.Fatal("未拒绝过期确认")
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, changed) {
		t.Fatal("覆盖了确认期间的新修改", err)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	request["expectedHash"] = saved["existingHash"]
	response = callEditor(t, s, "POST", "/api/scaffold/save", request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("确认后未写入业务骨架", err)
	}
}

// TestSaveScaffoldRejectsUnsafeTargets 验证非法路径、目录和符号链接不会被覆盖。
func TestSaveScaffoldRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{"escape", "directory", "symlink"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := model.Example()
			p.Generation.PackagePath = "behavior"
			if err = os.Mkdir(filepath.Join(root, "behavior"), 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "behavior", "actions.go")
			switch target {
			case "escape":
				p.Generation.PackagePath = "../outside"
			case "directory":
				if err = os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err = os.WriteFile(filepath.Join(root, "handwritten.go"), []byte("package behavior\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(filepath.Join("..", "handwritten.go"), path); err != nil {
					t.Skipf("系统不允许符号链接: %v", err)
				}
			}
			for _, overwrite := range []bool{false, true} {
				response := callEditor(t, s, "POST", "/api/scaffold/save", map[string]any{"project": p, "overwrite": overwrite, "expectedHash": sourceHash([]byte("package behavior\n"))})
				if response.Code == 200 {
					t.Fatal("允许不安全目标", target, overwrite)
				}
				var result map[string]any
				if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["existingHash"] != nil {
					t.Fatal("不安全目标不应提供覆盖确认")
				}
			}
		})
	}
}

// TestSaveScaffoldRejectsStaleWorkspace 验证旧页面不能将下载目标切换到其他工作目录。
func TestSaveScaffoldRejectsStaleWorkspace(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	data, err := json.Marshal(map[string]any{"project": p})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8791/api/scaffold/save", bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-BT-Workspace", url.PathEscape(t.TempDir()))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal("未拒绝旧工作目录", w.Code)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("旧页面写入了新目录", err)
	}
}
