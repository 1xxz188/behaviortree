//go:build !windows

package editor

import "fmt"

// pickNativeDirectory 明确提示当前平台尚未提供原生目录选择窗口。
func pickNativeDirectory(initial string) (string, error) {
	return "", fmt.Errorf("原生目录选择器目前仅支持 Windows，请在 Windows 本机运行编辑器服务")
}
