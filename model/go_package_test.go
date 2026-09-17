package model

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveGoPackage 验证各级目录、跨平台分隔符以及严格拒绝路径穿越。
func TestResolveGoPackage(t *testing.T) {
	root := t.TempDir()
	for _, raw := range []string{"brawl", "ai/brawl", "game/ai/npc", ` ai\brawl `} {
		t.Run(raw, func(t *testing.T) {
			got, err := ResolveGoPackage(root, raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.PackagePath != NormalizePackagePath(raw) || got.PackageName != filepath.Base(got.OutputDir) || got.OutputDir != filepath.Join(root, filepath.FromSlash(got.PackagePath)) {
				t.Fatalf("路径与包名不一致: %+v", got)
			}
		})
	}
	for _, raw := range []string{"", " ", "/", "./brawl", "../brawl", "ai/../brawl", "ai/./brawl", "ai//brawl", "ai/brawl/", "C:/temp/brawl", `C:\temp\brawl`, "/tmp/brawl", `\\server\brawl`, "ai/bt-brawl", "ai/123brawl", "ai/brawl.test", "ai/brawl test", "ai/type", "ai/_", "CON/brawl", "ai/NUL", "LPT9/brawl", "a\x00/brawl"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ResolveGoPackage(root, raw); err == nil {
				t.Fatalf("接受非法路径 %q", raw)
			}
		})
	}
}

// TestPackagePathJSON 验证保存路径规范化、字段拼写校验和旧格式明确拒绝。
func TestPackagePathJSON(t *testing.T) {
	p := Example()
	p.Generation.PackagePath = ` ai\brawl `
	raw, err := Encode(p)
	if err != nil || !strings.Contains(string(raw), `"packagePath": "ai/brawl"`) || strings.Contains(string(raw), `"package":`) {
		t.Fatalf("生成配置未规范保存: %s, %v", raw, err)
	}
	decoded, err := Decode(raw)
	if err != nil || decoded.Generation.PackagePath != "ai/brawl" {
		t.Fatalf("读取生成路径错误: %+v, %v", decoded.Generation, err)
	}
	legacy := strings.Replace(string(raw), `"packagePath"`, `"package"`, 1)
	if _, err := Decode([]byte(legacy)); err == nil {
		t.Fatal("不应继续兼容旧 package 字段")
	}
}
