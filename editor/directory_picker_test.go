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
	data := []byte(`{"schemaVersion":2,"trees":[{"id":"main","nodes":[]}]}`)
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

// TestDirectoryPickerRejectsStaleWorkspace 验证旧页面被拒绝时不会弹出系统窗口。
func TestDirectoryPickerRejectsStaleWorkspace(t *testing.T) {
	s := workspaceTestServer(t, t.TempDir())
	called := false
	s.directoryPicker = func(string) (string, error) {
		called = true
		return "", nil
	}
	w := workspaceTestCall(t, s, "POST", "/api/directory-picker", t.TempDir(), map[string]string{})
	if w.Code != http.StatusConflict || called {
		t.Fatalf("旧工作目录请求未被提前拒绝：%d，调用窗口 %t", w.Code, called)
	}
}

// TestDirectoryPickerAllowsWorkspaceSwitchWhileWaiting 验证窗口等待不持有工作区锁，期间切换会使选择结果失效。
func TestDirectoryPickerAllowsWorkspaceSwitchWhileWaiting(t *testing.T) {
	original, target, selected := t.TempDir(), t.TempDir(), t.TempDir()
	s := workspaceTestServer(t, original)
	opened := make(chan bool, 1)
	resume := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	s.directoryPicker = func(string) (string, error) {
		// 先检查锁边界，锁回归时直接返回，避免测试陷入互相等待。
		unlocked := s.workspaceMu.TryLock()
		if unlocked {
			s.workspaceMu.Unlock()
		}
		opened <- unlocked
		<-resume
		return selected, nil
	}
	go func() {
		done <- workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{})
	}()
	if !<-opened {
		close(resume)
		<-done
		t.Fatal("系统窗口等待期间仍持有工作区锁")
	}
	// 使用窗口已打开的信号安排切换，避免依赖延时或定时轮询。
	switched := workspaceTestCall(t, s, "POST", "/api/workspace", original, map[string]string{"directory": target})
	close(resume)
	w := <-done
	if switched.Code != http.StatusOK {
		t.Fatalf("窗口等待期间无法切换目录：%d %s", switched.Code, switched.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("工作目录改变后仍接受旧选择结果：%d %s", w.Code, w.Body.String())
	}
	current := workspaceTestDecode(t, callEditor(t, s, "GET", "/api/projects", nil))
	if current.Workspace != target {
		t.Fatalf("选择结果覆盖了并发切换：%s", current.Workspace)
	}
}

// TestDirectoryPickerRejectsDuplicateWindow 验证首个窗口未关闭时重复请求返回冲突，只调用一次原生窗口。
func TestDirectoryPickerRejectsDuplicateWindow(t *testing.T) {
	original := t.TempDir()
	s := workspaceTestServer(t, original)
	calls := 0
	var duplicate *httptest.ResponseRecorder
	s.directoryPicker = func(string) (string, error) {
		calls++
		if calls > 1 {
			return "", errors.New("重复调用原生窗口")
		}
		// 在首个窗口回调返回前重入请求，确定性覆盖选择器忙碌状态。
		duplicate = workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{})
		return "", nil
	}
	w := workspaceTestCall(t, s, "POST", "/api/directory-picker", original, map[string]string{})
	if w.Code != http.StatusOK || duplicate == nil || duplicate.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("重复窗口未被拒绝：首请求 %d，重复请求 %+v，窗口次数 %d", w.Code, duplicate, calls)
	}
}
