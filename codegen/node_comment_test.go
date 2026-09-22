package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestGeneratedNodeComments 验证所有节点类型及子树展开实例的注释、注入防护、版本和源码映射。
func TestGeneratedNodeComments(t *testing.T) {
	p := model.Example()
	p.Trees = nil
	p.Catalog = []model.Definition{
		{ID: "action", Name: "动作", Kind: model.DefinitionAction, GoName: "Act"},
		{ID: "condition", Name: "条件", Kind: model.DefinitionCondition, GoName: "Check"},
	}
	wants := make(map[string]string)
	// 每类节点使用独立的小树，子树指向等待树，同时覆盖独立与被引用的同一源节点。
	for typ := model.NodeSequence; typ <= model.NodeSubtree; typ++ {
		id := typ.String()
		comment := fmt.Sprintf("节点说明 %s 100%%\r\n*/\nvar Injected = 1\rgo:noinline\n//go:nosplit\n//line forged.go:999\n第二行 \"引用\"", id)
		node := model.Node{ID: "root", Type: typ, Comment: comment}
		nodes := []model.Node{node}
		switch typ {
		case model.NodeAction:
			nodes[0].Binding = "action"
		case model.NodeCondition:
			nodes[0].Binding = "condition"
		case model.NodeWait:
		case model.NodeSubtree:
			nodes[0].Tree = "wait"
		default:
			nodes[0].Children = []string{"leaf"}
			nodes = append(nodes, model.Node{ID: "leaf", Type: model.NodeWait})
			if typ == model.NodeRepeat || typ == model.NodeRetry {
				nodes[0].Count = 2
			}
		}
		p.Trees = append(p.Trees, model.Tree{ID: id, Root: "root", Nodes: nodes})
		wants[id] = strings.ReplaceAll(strings.ReplaceAll(comment, "\r\n", "\n"), "\r", "\n")
	}
	before, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, file := range result.Files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name, file.Source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				functions[function.Name.Name] = function
			}
			if general, ok := declaration.(*ast.GenDecl); ok && general.Tok == token.VAR {
				t.Fatalf("注释注入了变量声明：%s", file.Source)
			}
		}
	}
	counts := make(map[string]int)
	for _, location := range result.SourceMap {
		lines := strings.Split(string(generatedSource(t, result, location.File)), "\n")
		if location.Line < 1 || location.Line > len(lines) || !strings.HasPrefix(lines[location.Line-1], "func "+location.FunctionName+"(") {
			t.Fatalf("多行注释使函数映射错位：%+v", location)
		}
		function := functions[location.FunctionName]
		if function == nil || function.Doc == nil {
			t.Fatalf("节点缺少文档：%+v", location)
		}
		doc := function.Doc.Text()
		if !strings.Contains(doc, "执行树") {
			t.Fatal("丢失原有节点身份说明")
		}
		if location.NodeID != "root" {
			if strings.Contains(doc, "节点说明") {
				t.Fatal("未填写注释的节点被其他实例污染")
			}
			continue
		}
		counts[location.TreeID]++
		if !strings.Contains(doc, wants[location.TreeID]) {
			t.Fatalf("注释未附着于正确节点函数：%s\n%s", location.FunctionName, doc)
		}
		for _, comment := range function.Doc.List {
			if strings.HasPrefix(comment.Text, "//go:") || strings.HasPrefix(comment.Text, "//line ") {
				t.Fatalf("用户注释成为 Go 指令：%s", comment.Text)
			}
		}
	}
	for typ := model.NodeSequence; typ <= model.NodeSubtree; typ++ {
		if counts[typ.String()] == 0 {
			t.Fatalf("遗漏节点类型注释：%s", typ)
		}
	}
	if counts["wait"] != 2 {
		t.Fatalf("子树引用实例未保留源注释：%d", counts["wait"])
	}
	after, err := model.Encode(p)
	if err != nil || string(before) != string(after) {
		t.Fatal("生成改变了调用者的注释")
	}
	version, err := ProjectVersion(p)
	if err != nil || version != result.Version {
		t.Fatal("工程版本与生成版本不一致")
	}
	p.Trees[0].Nodes[0].Comment = "修改后的节点注释"
	updated, err := Generate(p)
	if err != nil || updated.Version == result.Version || reflect.DeepEqual(updated.Files, result.Files) {
		t.Fatalf("注释修改未更新生成源码与版本：%v", err)
	}
}

// TestEmptyNodeCommentVersion 验证省略注释与空字符串序列化后的版本及生成内容一致。
func TestEmptyNodeCommentVersion(t *testing.T) {
	p := model.Example()
	baseline, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Trees[0].Nodes[0].Comment = ""
	raw, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := model.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(reopened)
	if err != nil || !reflect.DeepEqual(result, baseline) {
		t.Fatalf("空注释重开后改变生成结果：%v", err)
	}
}
