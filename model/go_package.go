package model

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// ResolvedGoPackage 是所有生成入口共享的路径解析结果。
type ResolvedGoPackage struct {
	PackagePath string // 以 / 分隔的工程内相对路径。
	PackageName string // 从最后一级目录推导的 Go 包名。
	OutputDir   string // 使用当前操作系统分隔符的完整目标路径。
}

// NormalizePackagePath 只统一分隔符和首尾空白，不清除必须拒绝的点段或空段。
func NormalizePackagePath(raw string) string {
	return strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
}

// ResolveGoPackage 以 O(路径长度) 校验并解析生成路径，不执行磁盘 IO。
// 文件系统入口仍须通过工程目录的 os.Root 写入，防止符号链接逃逸。
func ResolveGoPackage(projectDir, raw string) (ResolvedGoPackage, error) {
	p := NormalizePackagePath(raw)
	for _, segment := range strings.Split(p, "/") {
		if segment == "" {
			return ResolvedGoPackage{}, fmt.Errorf("生成包路径不能为空、绝对路径或含空目录段")
		}
		// 使用跨平台一致的 ASCII 目录名，拒绝点段、盘符和 Windows 设备名。
		for i := 0; i < len(segment); i++ {
			c := segment[i]
			if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
				return ResolvedGoPackage{}, fmt.Errorf("生成包路径目录段 %q 必须以字母或下划线开头，且仅含 ASCII 字母、数字和下划线", segment)
			}
		}
		upper := strings.ToUpper(segment)
		if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
			return ResolvedGoPackage{}, fmt.Errorf("生成包路径不能包含系统保留目录名 %q", segment)
		}
	}
	name := path.Base(p)
	if !Identifier(name) {
		return ResolvedGoPackage{}, fmt.Errorf("生成包路径末级 %q 必须为非关键字的 Go 包名", name)
	}
	dir := filepath.Join(projectDir, filepath.FromSlash(p))
	rel, err := filepath.Rel(filepath.Clean(projectDir), dir)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return ResolvedGoPackage{}, fmt.Errorf("生成包路径必须位于工程目录内")
	}
	return ResolvedGoPackage{PackagePath: p, PackageName: name, OutputDir: dir}, nil
}
