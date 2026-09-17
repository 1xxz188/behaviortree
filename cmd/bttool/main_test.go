package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestGeneratePackagePath 验证默认配置、显式覆盖及 Windows 分隔符都以工作目录为根解析。
func TestGeneratePackagePath(t *testing.T) {
	for _, test := range []struct {
		name string   // 用例名称。
		out  []string // 显式命令行覆盖。
		want string   // 预期的规范化目录。
	}{
		{name: "配置路径", want: "game/ai/npc"},
		{name: "覆盖路径", out: []string{"--out", "ai/brawl"}, want: "ai/brawl"},
		{name: "Windows路径", out: []string{"--out", `ai\brawl`}, want: "ai/brawl"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			project := model.Example()
			project.Generation.PackagePath = "game/ai/npc"
			data, err := model.Encode(project)
			if err != nil {
				t.Fatal(err)
			}
			// JSON 放在子目录，确保生成基准不会误变成 JSON 所在目录。
			if err := os.Mkdir("projects", 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join("projects", "tree.json")
			if err := os.WriteFile(file, data, 0644); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"generate", "--file", file}, test.out...)
			if err := run(args); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(test.want), "glue.gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(source), "package "+filepath.Base(test.want)+"\n") {
				t.Fatalf("生成包名与目录不一致: %s", source)
			}
			if _, err := os.Stat(filepath.Join(root, "generated")); !os.IsNotExist(err) {
				t.Fatalf("不应创建固定 generated 层: %v", err)
			}
			after, err := os.ReadFile(file)
			if err != nil || string(after) != string(data) {
				t.Fatalf("CLI 不应改写输入工程: %v", err)
			}
		})
	}
}

// TestGenerateRejectsInvalidPath 验证显式空值、目录穿越和绝对路径在写盘前拒绝。
func TestGenerateRejectsInvalidPath(t *testing.T) {
	t.Chdir(t.TempDir())
	data, err := model.Encode(model.Example())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project.json", data, 0644); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{"", "../brawl", "ai/../brawl", "/tmp/brawl", "C:/temp/brawl", "ai/type", "ai//brawl"} {
		if err := run([]string{"generate", "--out", out}); err == nil {
			t.Errorf("非法路径 %q 未被拒绝", out)
		}
	}
}

// TestGeneratePreservesConflictingPackage 验证目标手写包冲突时原文件不变且不发布生成文件。
func TestGeneratePreservesConflictingPackage(t *testing.T) {
	t.Chdir(t.TempDir())
	data, err := model.Encode(model.Example())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project.json", data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("ai/brawl", 0755); err != nil {
		t.Fatal(err)
	}
	source := "package npc\n"
	if err := os.WriteFile("ai/brawl/actions.go", []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"generate", "--out", "ai/brawl"}); err == nil {
		t.Fatal("目标包冲突未拒绝")
	}
	after, err := os.ReadFile("ai/brawl/actions.go")
	if err != nil || string(after) != source {
		t.Fatalf("手写文件被修改: %v", err)
	}
	if _, err := os.Stat("ai/brawl/glue.gen.go"); !os.IsNotExist(err) {
		t.Fatalf("冲突时不应发布源码: %v", err)
	}
}
