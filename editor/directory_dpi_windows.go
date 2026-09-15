//go:build windows

package editor

// nativeDialogDPI 为已锁定的对话框线程启用逐屏 DPI，并返回同线程调用的恢复函数。
func nativeDialogDPI() func() {
	setContext := nativeUser32.NewProc("SetThreadDpiAwarenessContext")
	if err := setContext.Find(); err != nil {
		return func() {} // 旧版 Windows 不提供线程 DPI 接口，保留原有选择能力。
	}
	// PMv2 自动适配对话框控件和非客户区；Windows 10 1607 仅支持 PMv1。
	previous, _, _ := setContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)。
	if previous == 0 {
		previous, _, _ = setContext.Call(^uintptr(2)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE (-3)。
	}
	return func() {
		if previous != 0 {
			setContext.Call(previous)
		}
	}
}
