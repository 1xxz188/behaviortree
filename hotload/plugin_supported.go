//go:build (linux || darwin || freebsd) && cgo

package hotload

import "plugin"

// supported 仅在官方支持的平台且启用 CGO 时允许加载。
func supported() bool { return true }

// openPlugin 将平台 API 限制在编译标签文件内。
func openPlugin(path string) (func(string) (any, error), error) {
	p, err := plugin.Open(path)
	if err != nil {
		return nil, err
	}
	return func(name string) (any, error) { return p.Lookup(name) }, nil
}
