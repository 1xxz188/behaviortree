package editor

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestDirectoryPickerSelectsWithoutMutation 验证原生选择只返回目标信息，不切换目录或改写源文件。
func TestDirectoryPickerSelectsWithoutMutation(t *testing.T) {
	original, target := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	source := filepath.Join(target, "工程.json")
	data := []byte(`{"schemaVersion":4,"nextEventId":"1","events":[],"trees":[{"id":"main","nodes":[]}]}`)
	if err := os.WriteFile(source, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("说明"), 0644); err != nil {
		t.Fatal(err)
	}
	s.directoryPicker = func(initial string) (string, error) {
		if initial != original {
			t.Errorf("初始目录 = %q，期望 %q", initial, original)
		}
		return target, nil
	}
	listing := workspaceTestDecode(t, workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{}))
	if listing.Workspace != target || !slices.Equal(listing.Files, []string{"工程.json"}) {
		t.Fatalf("选择结果错误：%+v", listing)
	}
	current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if current.Workspace != original {
		t.Fatalf("选择目录意外切换工作区：%s", current.Workspace)
	}
	actual, err := os.ReadFile(source)
	if err != nil || string(actual) != string(data) {
		t.Fatalf("选择目录改写源文件：%q，错误：%v", actual, err)
	}
}

// TestDirectoryPickerCancellationKeepsWorkspace 验证取消原生窗口返回明确标志，保留原工作目录。
func TestDirectoryPickerCancellationKeepsWorkspace(t *testing.T) {
	original, initial := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	s.directoryPicker = func(path string) (string, error) {
		if path != initial {
			t.Errorf("未使用指定初始目录：%q", path)
		}
		return "", nil
	}
	w := workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{"directory": initial})
	var response map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || !response["cancelled"] {
		t.Fatalf("取消响应错误：%d %s", w.Code, w.Body.String())
	}
	current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if current.Workspace != original {
		t.Fatalf("取消后工作目录改变：%s", current.Workspace)
	}
}

// TestDirectoryPickerErrorsKeepWorkspace 验证原生错误和无效路径都不会切换或创建工作目录。
func TestDirectoryPickerErrorsKeepWorkspace(t *testing.T) {
	original := t.TempDir()
	missing := filepath.Join(t.TempDir(), "不存在")
	for _, tc := range []struct {
		name     string // 用例验证的失败原因。
		initial  string // 请求传入的初始目录。
		selected string // 模拟用户选择的路径。
		err      error  // 模拟系统窗口错误。
		status   int    // 预期 HTTP 状态。
		called   bool   // 是否应调用原生窗口。
	}{
		{name: "系统窗口失败", err: errors.New("系统窗口不可用"), status: http.StatusInternalServerError, called: true},
		{name: "目标不存在", selected: missing, status: http.StatusBadRequest, called: true},
		{name: "目标是相对路径", selected: "relative", status: http.StatusBadRequest, called: true},
		{name: "初始目录是相对路径", initial: "relative", status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workspaceTestServer(t, original)
			called := false
			s.directoryPicker = func(string) (string, error) {
				called = true
				return tc.selected, tc.err
			}
			w := workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{"directory": tc.initial})
			if w.Code != tc.status || called != tc.called {
				t.Fatalf("错误处理不符：状态 %d，调用窗口 %t，响应 %s", w.Code, called, w.Body.String())
			}
			current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
			if current.Workspace != original {
				t.Fatalf("失败后工作目录改变：%s", current.Workspace)
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatalf("失败请求意外创建目录：%v", err)
			}
		})
	}
}

// TestDirectoryPickerUsesRequestWorkspace 验证不同页签各自的目录可以作为选择器初始位置。
func TestDirectoryPickerUsesRequestWorkspace(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, first)
	s.directoryPicker = func(initial string) (string, error) {
		if initial != second {
			t.Errorf("初始目录 = %q，期望 %q", initial, second)
		}
		return "", nil
	}
	w := workspaceTestCall(t, s, "POST", "/api/directory-picker", second, map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("第二页签目录选择器失败：%d %s", w.Code, w.Body.String())
	}
}

// TestDirectoryPickerMissingWorkspaceFallsBack 验证恢复目录已被删除时仍能从启动目录重新选择。
func TestDirectoryPickerMissingWorkspaceFallsBack(t *testing.T) {
	defaultDir, selected := t.TempDir(), t.TempDir()
	missing := filepath.Join(t.TempDir(), "已删除目录")
	s := workspaceTestServer(t, defaultDir)
	s.directoryPicker = func(initial string) (string, error) {
		if initial != defaultDir {
			t.Errorf("失效初始目录未回退：%q", initial)
		}
		return selected, nil
	}
	w := workspaceTestCall(t, s, "POST", "/api/directory-picker", missing, map[string]string{"directory": missing})
	if listing := workspaceTestDecode(t, w); listing.Workspace != selected {
		t.Fatalf("无法从失效目录重新选择：%+v", listing)
	}
}

// TestDirectoryPickerAllowsWorkspaceSwitchWhileWaiting 验证窗口等待时其他页签仍能选择目录。
func TestDirectoryPickerAllowsWorkspaceSwitchWhileWaiting(t *testing.T) {
	original, target, selected := t.TempDir(), t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	opened := make(chan struct{}, 1)
	resume := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	s.directoryPicker = func(string) (string, error) {
		opened <- struct{}{}
		<-resume
		return selected, nil
	}
	go func() {
		done <- workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{})
	}()
	<-opened
	// 使用窗口已打开的信号安排切换，避免依赖延时或定时轮询。
	switched := workspaceTestCall(t, s, "POST", "/api/workspace", original, map[string]string{"directory": target})
	close(resume)
	w := <-done
	if switched.Code != http.StatusOK {
		t.Fatalf("窗口等待期间无法切换目录：%d %s", switched.Code, switched.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("目录选择不应被另一页签干扰：%d %s", w.Code, w.Body.String())
	}
	current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if current.Workspace != original {
		t.Fatalf("其他页签切换改变了默认目录：%s", current.Workspace)
	}
	if listing := workspaceTestDecode(t, w); listing.Workspace != selected {
		t.Fatalf("窗口结果被其他页签切换：%s", listing.Workspace)
	}
}

// TestDirectoryPickerAllowsConcurrentWindows 验证多个页签可以同时发起目录选择请求。
func TestDirectoryPickerAllowsConcurrentWindows(t *testing.T) {
	original := t.TempDir()
	s := workspaceTestServer(t, original)
	calls := 0
	var duplicate *httptest.ResponseRecorder
	s.directoryPicker = func(string) (string, error) {
		calls++
		if calls > 1 {
			return "", nil
		}
		// 在首个窗口回调返回前重入请求，确定性覆盖并发入口。
		duplicate = workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{})
		return "", nil
	}
	w := workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{})
	if w.Code != http.StatusOK || duplicate == nil || duplicate.Code != http.StatusOK || calls != 2 {
		t.Fatalf("并发窗口未独立完成：首请求 %d，第二请求 %+v，窗口次数 %d", w.Code, duplicate, calls)
	}
}
