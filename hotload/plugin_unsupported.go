//go:build (!linux && !darwin && !freebsd) || !cgo

package hotload

// supported 为编辑器及普通运行保留跨平台构建能力。
func supported() bool { return false }

// openPlugin 在不支持的平台返回明确错误。
func openPlugin(string) (func(string) (any, error), error) { return nil, ErrUnsupported }
