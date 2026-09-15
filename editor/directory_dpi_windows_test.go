//go:build windows

package editor

import (
	"runtime"
	"testing"
)

// TestNativeDialogDPI 验证真实 Windows 线程 DPI 的启用和恢复，全程不创建窗口。
func TestNativeDialogDPI(t *testing.T) {
	getContext := nativeUser32.NewProc("GetThreadDpiAwarenessContext")
	equalContexts := nativeUser32.NewProc("AreDpiAwarenessContextsEqual")
	validContext := nativeUser32.NewProc("IsValidDpiAwarenessContext")
	if getContext.Find() != nil || equalContexts.Find() != nil || validContext.Find() != nil {
		t.Skip("当前 Windows 不提供线程 DPI 检查接口")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	before, _, _ := getContext.Call()
	if before == 0 {
		t.Fatal("无法读取初始 DPI 上下文")
	}
	restore := nativeDialogDPI()
	defer restore() // 即使断言失败，也不会把测试线程留在修改后的状态。
	expected := ^uintptr(3)
	if valid, _, _ := validContext.Call(expected); valid == 0 {
		expected = ^uintptr(2)
	}
	current, _, _ := getContext.Call()
	if equal, _, _ := equalContexts.Call(current, expected); equal == 0 {
		t.Fatal("对话框线程未启用系统支持的逐屏 DPI 模式")
	}
	restore()
	after, _, _ := getContext.Call()
	if equal, _, _ := equalContexts.Call(after, before); equal == 0 {
		t.Fatal("对话框结束后未恢复原 DPI 上下文")
	}
}
