package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestGenerateContext 验证命令行与编辑器一致地补全短路径并首次创建上下文。
func TestGenerateContext(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module bt_test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := model.Example()
	p.Generation.ContextImport = "bt_context"
	p.Generation.ContextType = "*Context"
	data, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project.json", data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"generate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("bt_context", "context.go")); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("behavior", "glue.gen.go"))
	if err != nil || !strings.Contains(string(source), `ctxpkg "bt_test/bt_context"`) {
		t.Fatal("CLI 未补全上下文导入", err)
	}
}
