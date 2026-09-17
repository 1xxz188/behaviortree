package editor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestPackagePathGeneration 验证 Web 预览、写盘和重新读取共享路径规则并保留手写文件。
func TestPackagePathGeneration(t *testing.T) {
	for _, raw := range []string{"brawl", "ai/brawl", ` game\ai\brawl `} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := model.Example()
			p.Generation.PackagePath = raw
			resolved, err := model.ResolveGoPackage(root, raw)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(resolved.OutputDir, 0755); err != nil {
				t.Fatal(err)
			}
			handwritten := []byte("package brawl\n// 手写业务必须原样保留。\n")
			file := filepath.Join(resolved.OutputDir, "actions.go")
			if err = os.WriteFile(file, handwritten, 0644); err != nil {
				t.Fatal(err)
			}
			preview := decodePreview(t, callEditor(t, s, "POST", "/api/preview", p))
			generated := decodePreview(t, callEditor(t, s, "POST", "/api/generate", p))
			if generated.Directory != resolved.OutputDir || generated.Version != preview.Version {
				t.Fatal("预览与写盘配置不一致")
			}
			for _, source := range generated.Files {
				if !strings.Contains(source.Source, "\npackage brawl\n") {
					t.Fatal("生成包名错误", source.Name)
				}
			}
			decodePreview(t, callEditor(t, s, "POST", "/api/generated", p))
			p.Trees[0].ID = "renamed"
			decodePreview(t, callEditor(t, s, "POST", "/api/generate", p))
			if _, err := os.Stat(filepath.Join(resolved.OutputDir, "tree_main.gen.go")); !os.IsNotExist(err) {
				t.Fatal("旧产物未按清单清理")
			}
			actual, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(actual, handwritten) {
				t.Fatal("手写文件被修改")
			}
			if _, err := os.Stat(filepath.Join(root, "generated")); !os.IsNotExist(err) {
				t.Fatal("自动创建了 generated 目录")
			}
		})
	}
}

// TestPackageConflictPreflight 验证普通 Go 文件包名冲突时，在发布前拒绝整批产物。
func TestPackageConflictPreflight(t *testing.T) {
	root := t.TempDir()
	p := model.Example()
	p.Generation.PackagePath = "ai/brawl"
	dir := filepath.Join(root, "ai", "brawl")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package npc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectGenerated(root, p.Generation.PackagePath, generatedForTest(t, p)); err == nil || !strings.Contains(err.Error(), `"npc"`) {
		t.Fatal("未报告已有包冲突", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "glue.gen.go")); !os.IsNotExist(err) {
		t.Fatal("冲突后仍发布源码")
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package brawl\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "external_test.go"), []byte("package brawl_test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProjectGenerated(root, p.Generation.PackagePath, generatedForTest(t, p)); err != nil {
		t.Fatal(err)
	}
}

// TestPackagePathWriteRejectsEscapingSymlink 验证写目录与已有文件的外部符号链接均不可访问。
func TestPackagePathWriteRejectsEscapingSymlink(t *testing.T) {
	for _, linkFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "file"}[linkFile], func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			p := model.Example()
			p.Generation.PackagePath = "ai/brawl"
			link, target := filepath.Join(root, "ai"), outside
			if linkFile {
				if err := os.MkdirAll(filepath.Join(root, "ai", "brawl"), 0755); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(outside, "helper.go")
				if err := os.WriteFile(target, []byte("package brawl\n"), 0644); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(root, "ai", "brawl", "helper.go")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("系统不允许创建符号链接: %v", err)
			}
			if err := WriteProjectGenerated(root, p.Generation.PackagePath, generatedForTest(t, p)); err == nil {
				t.Fatal("允许符号链接逃逸")
			}
			if _, err := os.Stat(filepath.Join(outside, "brawl")); !os.IsNotExist(err) {
				t.Fatal("工程外目录被创建")
			}
		})
	}
}

// TestPackagePathMovePreservesOldDirectory 验证新目录自动创建，改路径不会删除旧目录中的任何业务文件。
func TestPackagePathMovePreservesOldDirectory(t *testing.T) {
	root := t.TempDir()
	p := model.Example()
	p.Generation.PackagePath = "brawl"
	if err := WriteProjectGenerated(root, p.Generation.PackagePath, generatedForTest(t, p)); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(root, "brawl", "actions.go")
	handwritten := []byte("package brawl\n// 路径迁移时应保留的业务实现。\n")
	if err := os.WriteFile(oldFile, handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	p.Generation.PackagePath = "game/ai/brawl"
	if err := WriteProjectGenerated(root, p.Generation.PackagePath, generatedForTest(t, p)); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"brawl", filepath.Join("game", "ai", "brawl")} {
		if _, err := os.Stat(filepath.Join(root, dir, "glue.gen.go")); err != nil {
			t.Fatal("新目录生成失败或旧目录被误删", err)
		}
	}
	if got, err := os.ReadFile(oldFile); err != nil || !bytes.Equal(got, handwritten) {
		t.Fatal("迁移修改了手写文件", err)
	}
}
