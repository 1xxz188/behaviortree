package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestGeneratedBusinessComments 验证动作和条件显示名、参数说明均附着于正确声明，多行文本不能变成 Go 代码。
func TestGeneratedBusinessComments(t *testing.T) {
	p := model.Example()
	p.Catalog = []model.Definition{
		{ID: "move", Name: "移动到目标\n第二行", Kind: model.DefinitionAction, GoName: "MoveTo", Params: []model.Parameter{
			{Name: "Target", Type: bt.EntityIDType, Default: []byte("1"), Comment: "目标实体 ID（100%）\r\n*/\nvar Injected = 1\r仍是注释"},
			{Name: "Enabled", Type: bt.BoolType, Default: []byte("true")},
		}},
		{ID: "ready", Name: "检查就绪", Kind: model.DefinitionCondition, GoName: "Ready"},
	}
	p.Trees[0].Nodes = []model.Node{
		{ID: "root", Type: model.NodeSequence, Children: []string{"ready", "move"}},
		{ID: "ready", Type: model.NodeCondition, Binding: "ready"},
		{ID: "move", Name: "实例名称", Type: model.NodeAction, Binding: "move"},
	}
	result, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name, file.Source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "glue.gen.go" {
			found := false
			for _, declaration := range parsed.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, spec := range general.Specs {
					typ := spec.(*ast.TypeSpec)
					if typ.Name.Name != "MoveToParams" {
						continue
					}
					found = true
					fields := typ.Type.(*ast.StructType).Fields.List
					if len(fields) != 2 {
						t.Fatalf("参数注释改变结构体：%s", file.Source)
					}
					for _, field := range fields {
						want := "Enabled 由常量或黑板字段绑定提供。"
						if field.Names[0].Name == "Target" {
							want = "Target 目标实体 ID（100%）\n*/\nvar Injected = 1\n仍是注释"
						}
						if got := strings.TrimSpace(field.Doc.Text()); got != want {
							t.Fatalf("成员注释错误：%q，期望 %q", got, want)
						}
					}
				}
			}
			if !found {
				t.Fatal("缺少 MoveToParams")
			}
		} else {
			for _, definition := range p.Catalog {
				want := "业务函数 " + definition.GoName + "（显示名 " + strconv.Quote(definition.Name) + "）"
				if !strings.Contains(string(file.Source), want) {
					t.Fatalf("节点函数未包含业务显示名：%s", want)
				}
			}
		}
	}
	scaffold, err := Scaffold(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "actions.go", scaffold, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]string)
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function.Doc.Text()
		}
	}
	for _, definition := range p.Catalog {
		if !strings.Contains(functions[definition.GoName], strconv.Quote(definition.Name)) {
			t.Fatalf("业务骨架 %s 未包含显示名称", definition.GoName)
		}
	}
	// 只修改参数注释也应刷新生成结果的版本，避免继续展示旧源码。
	p.Catalog[0].Params[0].Comment = "新的目标说明"
	updated, err := Generate(p)
	if err != nil || updated.Version == result.Version {
		t.Fatalf("注释修改未更新生成版本：%v", err)
	}
}
