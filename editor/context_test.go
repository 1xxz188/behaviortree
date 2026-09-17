package editor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestContextAutoGeneration 验证截图配置通过实际生成接口创建上下文，并使用完整模块导入路径。
func TestContextAutoGeneration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/game\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Generation.ContextImport = "bt_context"
	p.Generation.ContextType = "*Context"
	preview := decodePreview(t, callEditor(t, s, "POST", "/api/preview", p))
	decodePreview(t, callEditor(t, s, "POST", "/api/scaffold", p))
	if _, err := os.Stat(filepath.Join(root, "bt_context")); !os.IsNotExist(err) {
		t.Fatal("预览创建了上下文目录", err)
	}
	response := callEditor(t, s, "POST", "/api/generate", p)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	source, err := os.ReadFile(filepath.Join(root, "bt_context", "context.go"))
	if err != nil {
		t.Fatal("Context 文件未自动生成:", err)
	}
	if !strings.Contains(string(source), "type Context struct") {
		t.Fatal(string(source))
	}
	glue, err := os.ReadFile(filepath.Join(root, "behavior", "glue.gen.go"))
	if err != nil || !strings.Contains(string(glue), `ctxpkg "example.com/game/bt_context"`) {
		t.Fatal("上下文导入路径不正确", err, string(glue))
	}
	saved := decodePreview(t, callEditor(t, s, "POST", "/api/generated", p))
	if !saved.MatchesCurrent || saved.Version != preview.Version {
		t.Fatal("预览、生成和重开版本不一致")
	}
	// 编译真实生成包，避免只校验字符串却仍存在不可解析的 import。
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	mod := fmt.Sprintf("module example.com/game\n\ngo 1.26.7\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => %s\n", filepath.ToSlash(repo))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "./behavior", "./bt_context")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("生成包不能编译: %v\n%s", err, output)
	}
	// 后续生成必须保留用户添加的业务字段。
	handwritten := []byte("package bt_context\n// Context 保存业务数据。\ntype Context struct { EntityID int64 }\n")
	if err := os.WriteFile(filepath.Join(root, "bt_context", "context.go"), handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	decodePreview(t, callEditor(t, s, "POST", "/api/generate", p))
	after, err := os.ReadFile(filepath.Join(root, "bt_context", "context.go"))
	if err != nil || !bytes.Equal(after, handwritten) {
		t.Fatal("重新生成覆盖了业务上下文", err)
	}
}

// TestContextGenerationVariants 覆盖同包、完整导入、外部包及骨架下载的实际入口。
func TestContextGenerationVariants(t *testing.T) {
	for _, test := range []struct {
		name       string // 用例的中文说明。
		importPath string // 用户填写的上下文导入。
		typeName   string // 上下文类型。
		wantDir    string // 应首次创建上下文的目录，空值表示不创建。
	}{
		{"同包", "", "*Context", "behavior"},
		{"完整路径", "example.com/game/shared", "Context", "shared"},
		{"指向生成包", "behavior", "*Context", "behavior"},
		{"标准库", "bytes", "*Buffer", ""},
		{"外部包", "example.org/shared", "*Context", ""},
		{"默认类型", "", "any", ""},
	} {
		for _, endpoint := range []string{"/api/generate", "/api/scaffold/save"} {
			t.Run(test.name+endpoint, func(t *testing.T) {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/game\n"), 0644); err != nil {
					t.Fatal(err)
				}
				s, err := New(root)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				p := model.Example()
				p.Generation.ContextImport = test.importPath
				p.Generation.ContextType = test.typeName
				var request any = p
				if endpoint == "/api/scaffold/save" {
					request = map[string]any{"project": p}
				}
				response := callEditor(t, s, "POST", endpoint, request)
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
				if test.wantDir != "" {
					if _, err := os.Stat(filepath.Join(root, test.wantDir, "context.go")); err != nil {
						t.Fatal("缺少上下文", err)
					}
				} else {
					if _, err := os.Stat(filepath.Join(root, "behavior", "context.go")); !os.IsNotExist(err) {
						t.Fatal("不应创建上下文", err)
					}
					if _, err := os.Stat(filepath.Join(root, "bytes")); !os.IsNotExist(err) {
						t.Fatal("标准库被当成本地目录", err)
					}
				}
			})
		}
	}
}

// TestContextPreservesExistingTypes 验证任意业务文件内的已有类型、不同包名和同名文件保护。
func TestContextPreservesExistingTypes(t *testing.T) {
	for _, hasType := range []bool{true, false} {
		t.Run(fmt.Sprint(hasType), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/game\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "shared"), 0755); err != nil {
				t.Fatal(err)
			}
			name := "context.go"
			source := []byte("package domain\n// 已有业务内容。\ntype Other struct{}\n")
			if hasType {
				name = "domain.go"
				source = []byte("package domain\n// Context 为已有业务类型。\ntype Context struct{ID int64}\n")
			}
			path := filepath.Join(root, "shared", name)
			if err := os.WriteFile(path, source, 0644); err != nil {
				t.Fatal(err)
			}
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := model.Example()
			p.Generation.ContextImport = "example.com/game/shared"
			p.Generation.ContextType = "*Context"
			response := callEditor(t, s, "POST", "/api/generate", p)
			if hasType && response.Code != 200 || !hasType && response.Code != 409 {
				t.Fatal(response.Code, response.Body.String())
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, source) {
				t.Fatal("已有文件被改写", err)
			}
			if hasType {
				if _, err := os.Stat(filepath.Join(root, "shared", "context.go")); !os.IsNotExist(err) {
					t.Fatal("重复生成已有类型", err)
				}
			}
		})
	}
}
