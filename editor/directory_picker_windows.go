//go:build windows

package editor

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// 原生接口和槽位对应 Windows SDK 的 ShObjIdl_core.h；目录选择不启动外部进程。
const (
	nativeRelease        = 2          // IUnknown.Release。
	nativeShow           = 3          // IModalWindow.Show。
	nativeSetOptions     = 9          // IFileDialog.SetOptions。
	nativeGetOptions     = 10         // IFileDialog.GetOptions。
	nativeSetFolder      = 12         // IFileDialog.SetFolder。
	nativeSetTitle       = 17         // IFileDialog.SetTitle。
	nativeGetResult      = 20         // IFileDialog.GetResult。
	nativeGetDisplayName = 5          // IShellItem.GetDisplayName。
	nativeCancelled      = 0x800704c7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)。
	nativePickFolders    = 0x20       // FOS_PICKFOLDERS：选择文件夹。
	nativeFileSystem     = 0x40       // FOS_FORCEFILESYSTEM：只返回文件系统路径。
	nativePathMustExist  = 0x800      // FOS_PATHMUSTEXIST：目标目录必须存在。
	nativeNoChangeDir    = 0x8        // FOS_NOCHANGEDIR：不改变进程当前目录。
	nativeFileSystemName = 0x80058000 // SIGDN_FILESYSPATH：返回绝对文件系统路径。
)

var (
	nativeOle32        = syscall.NewLazyDLL("ole32.dll")                      // COM 运行库。
	nativeShell32      = syscall.NewLazyDLL("shell32.dll")                    // Shell 路径解析接口。
	nativeKernel32     = syscall.NewLazyDLL("kernel32.dll")                   // UTF-16 字符串长度。
	nativeUser32       = syscall.NewLazyDLL("user32.dll")                     // 选择窗口与点击来源窗口的所属关系。
	nativeForeground   = nativeUser32.NewProc("GetForegroundWindow")          // 请求到达时的前台窗口。
	nativeIsWindow     = nativeUser32.NewProc("IsWindow")                     // 打开前检查来源窗口是否仍存在。
	nativeIsVisible    = nativeUser32.NewProc("IsWindowVisible")              // 不将选择框附属到不可见窗口。
	nativeIsIconic     = nativeUser32.NewProc("IsIconic")                     // 最小化的来源窗口也不能作为显示依附对象。
	nativeCoInitialize = nativeOle32.NewProc("CoInitializeEx")                // 初始化当前线程 COM。
	nativeCoUninit     = nativeOle32.NewProc("CoUninitialize")                // 清理当前线程 COM。
	nativeCoCreate     = nativeOle32.NewProc("CoCreateInstance")              // 创建原生对话框。
	nativeCoFree       = nativeOle32.NewProc("CoTaskMemFree")                 // 释放 Shell 返回的路径。
	nativeShellItem    = nativeShell32.NewProc("SHCreateItemFromParsingName") // 路径转 Shell 项。
	nativeStringLength = nativeKernel32.NewProc("lstrlenW")                   // 获取系统返回路径的字符数。
)

// nativeCOMObject 只描述 COM 对象开头的虚函数表指针，实例内存由系统管理。
type nativeCOMObject struct {
	vtable *[29]uintptr // IFileOpenDialog 最多使用 29 个槽位；Shell 项仅访问其有效槽位。
}

// release 归还当前线程持有的一个 COM 引用。
func (object *nativeCOMObject) release() {
	syscall.SyscallN(object.vtable[nativeRelease], uintptr(unsafe.Pointer(object)))
}

// nativeResultError 按 HRESULT 的有符号低 32 位判断失败，不使用 Win32 last-error。
func nativeResultError(operation string, result uintptr) error {
	if int32(result) < 0 {
		return fmt.Errorf("%s失败（HRESULT 0x%08X）", operation, uint32(result))
	}
	return nil
}

// pickNativeDirectory 弹出资源管理器风格的目录选择框；取消返回空路径且不修改任何文件。
func pickNativeDirectory(initial string) (string, error) {
	// 在异步初始化 COM 之前捕获点击来源，避免延迟创建后误取另一个前台应用。
	owner, _, _ := nativeForeground.Call()
	// Shell 扩展可能保留线程状态，每次交互使用独立 STA 线程并在退出时销毁，避免污染 HTTP 线程。
	type selection struct {
		path string // 用户选择的目录，取消时为空。
		err  error  // 原生窗口错误。
	}
	done := make(chan selection, 1)
	go func() {
		runtime.LockOSThread()
		// 故意不调用 UnlockOSThread：goroutine 结束时由 Go 回收该系统线程。
		path, err := pickDirectoryOnThread(initial, owner)
		done <- selection{path: path, err: err}
	}()
	result := <-done
	return result.path, result.err
}

// pickDirectoryOnThread 在当前 STA 线程内完成全部 COM 操作，Show 自带模态消息循环。
func pickDirectoryOnThread(initial string, owner uintptr) (string, error) {
	// 在 Shell 创建任何窗口前启用逐屏 DPI，避免系统把低分辨率窗口整体放大。
	restoreDPI := nativeDialogDPI()
	defer restoreDPI() // 最后恢复，确保 COM 对象释放期间仍使用同一 DPI 上下文。

	result, _, _ := nativeCoInitialize.Call(0, 0x2|0x4) // COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE。
	if err := nativeResultError("初始化目录选择器", result); err != nil {
		return "", err
	}
	defer nativeCoUninit.Call() // S_OK 与 S_FALSE 均须配对清理，初始化失败则不会调用。

	classID := syscall.GUID{Data1: 0xdc1c5a9c, Data2: 0xe88a, Data3: 0x4dde, Data4: [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	dialogID := syscall.GUID{Data1: 0xd57c7288, Data2: 0xd4ad, Data3: 0x4768, Data4: [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	itemID := syscall.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	var dialog *nativeCOMObject
	result, _, _ = nativeCoCreate.Call(uintptr(unsafe.Pointer(&classID)), 0, 1, uintptr(unsafe.Pointer(&dialogID)), uintptr(unsafe.Pointer(&dialog)))
	if err := nativeResultError("创建目录选择器", result); err != nil {
		return "", err
	}
	defer dialog.release()

	// 在系统默认选项上启用目录和文件系统限制，保留系统自带的有效路径校验。
	var options uint32
	result, _, _ = syscall.SyscallN(dialog.vtable[nativeGetOptions], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(&options)))
	if err := nativeResultError("读取目录选择器选项", result); err != nil {
		return "", err
	}
	options |= nativePickFolders | nativeFileSystem | nativePathMustExist | nativeNoChangeDir
	result, _, _ = syscall.SyscallN(dialog.vtable[nativeSetOptions], uintptr(unsafe.Pointer(dialog)), uintptr(options))
	if err := nativeResultError("设置目录选择器选项", result); err != nil {
		return "", err
	}
	title, _ := syscall.UTF16PtrFromString("选择工作目录")
	result, _, _ = syscall.SyscallN(dialog.vtable[nativeSetTitle], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(title)))
	if err := nativeResultError("设置目录选择器标题", result); err != nil {
		return "", err
	}

	if initial != "" {
		path, err := syscall.UTF16PtrFromString(initial)
		if err != nil {
			return "", fmt.Errorf("初始目录无效：%w", err)
		}
		var folder *nativeCOMObject
		result, _, _ = nativeShellItem.Call(uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&itemID)), uintptr(unsafe.Pointer(&folder)))
		if err := nativeResultError("读取初始目录", result); err != nil {
			return "", err
		}
		defer folder.release()
		result, _, _ = syscall.SyscallN(dialog.vtable[nativeSetFolder], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(folder)))
		if err := nativeResultError("设置初始目录", result); err != nil {
			return "", err
		}
	}

	// 模态选择框始终位于来源窗口上方，避免被浏览器遮住后只能点击任务栏才能看到。
	// 初始化期间来源窗口可能关闭或隐藏；此时退回独立窗口，不向失效句柄创建对话框。
	if owner != 0 {
		valid, _, _ := nativeIsWindow.Call(owner)
		visible, _, _ := nativeIsVisible.Call(owner)
		minimized, _, _ := nativeIsIconic.Call(owner)
		if valid == 0 || visible == 0 || minimized != 0 {
			owner = 0
		}
	}
	result, _, _ = syscall.SyscallN(dialog.vtable[nativeShow], uintptr(unsafe.Pointer(dialog)), owner)
	if uint32(result) == nativeCancelled {
		return "", nil
	}
	if err := nativeResultError("打开目录选择器", result); err != nil {
		return "", err
	}
	var selected *nativeCOMObject
	result, _, _ = syscall.SyscallN(dialog.vtable[nativeGetResult], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(&selected)))
	if err := nativeResultError("获取所选目录", result); err != nil {
		return "", err
	}
	defer selected.release()
	var path *uint16
	result, _, _ = syscall.SyscallN(selected.vtable[nativeGetDisplayName], uintptr(unsafe.Pointer(selected)), nativeFileSystemName, uintptr(unsafe.Pointer(&path)))
	if err := nativeResultError("获取目录路径", result); err != nil {
		return "", err
	}
	defer nativeCoFree.Call(uintptr(unsafe.Pointer(path)))
	length, _, _ := nativeStringLength.Call(uintptr(unsafe.Pointer(path)))
	return syscall.UTF16ToString(unsafe.Slice(path, int(length))), nil
}
