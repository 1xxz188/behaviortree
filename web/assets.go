// Package web 提供内嵌的离线 Vue 编辑器资源。
package web

import (
	"embed"
	"io/fs"
)

// assets 在发布构建时打包 Vite 生成的全部文件；用户工程不写入此文件系统。
//
//go:embed all:dist
var assets embed.FS

// Assets 返回可由 HTTP 服务直接使用的前端文件系统。
func Assets() fs.FS {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err) // 编译期已经保证 dist 存在。
	}
	return root
}
