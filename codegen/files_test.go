package codegen

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// allGeneratedSource 拼接全部文件，仅供测试搜索产物文本，不用于编译或行号定位。
func allGeneratedSource(result Result) []byte {
	var out bytes.Buffer
	for _, file := range result.Files {
		out.Write(file.Source)
	}
	return out.Bytes()
}

// generatedSource 从测试产物中找到指定文件，缺失时立即使测试失败。
func generatedSource(t *testing.T, result Result, name string) []byte {
	t.Helper()
	for _, file := range result.Files {
		if file.Name == name {
			return file.Source
		}
	}
	t.Fatalf("生成文件缺失: %s", name)
	return nil
}

// writeGeneratedFiles 将全部生成文件写入临时模块，验证真实多文件 Go 包。
func writeGeneratedFiles(t *testing.T, dir string, result Result) {
	t.Helper()
	for _, file := range result.Files {
		if err := os.WriteFile(filepath.Join(dir, file.Name), file.Source, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTreeFilesContainDefinitionInstances 验证每棵定义树只有一个文件且包含全部独立展开实例。
func TestTreeFilesContainDefinitionInstances(t *testing.T) {
	p := fixtureProject()
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != len(p.Trees)+1 || r.Files[0].Name != "glue.gen.go" || r.Files[0].TreeID != "" {
		t.Fatal("生成文件数量或 glue 位置错误")
	}
	locations := make(map[string]SourceLocation, len(r.SourceMap))
	for _, loc := range r.SourceMap {
		locations[loc.FunctionName] = loc
	}
	seen := make(map[string]bool, len(r.Files))
	for i, file := range r.Files {
		if seen[strings.ToLower(file.Name)] {
			t.Fatalf("大小写不敏感文件名重复: %s", file.Name)
		}
		seen[strings.ToLower(file.Name)] = true
		if i > 1 && r.Files[i-1].TreeID >= file.TreeID {
			t.Fatal("树文件未按 ID 排序")
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name, file.Source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			if file.TreeID == "" {
				if fn, ok := declaration.(*ast.FuncDecl); ok && locations[fn.Name.Name].FunctionName != "" {
					t.Fatal("glue 包含节点执行函数")
				}
				continue
			}
			if gen, ok := declaration.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
				continue
			}
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok {
				t.Fatalf("树文件含公共声明: %s", file.Name)
			}
			loc, ok := locations[fn.Name.Name]
			if !ok || loc.TreeID != file.TreeID || loc.File != file.Name {
				t.Fatalf("函数不属于定义树或映射丢失: %s", fn.Name.Name)
			}
			delete(locations, fn.Name.Name)
		}
		if file.TreeID != "" && bytes.Contains(file.Source, []byte(r.Version)) {
			t.Fatal("树文件嵌入工程版本")
		}
	}
	if len(locations) != 0 {
		t.Fatal("源码映射引用了不存在的函数")
	}
}

// TestUnrelatedTreeChangesKeepSourcesStable 验证排序靠前的无关树增删和结构变化不会改写既有树内容。
func TestUnrelatedTreeChangesKeepSourcesStable(t *testing.T) {
	p := model.Example()
	before, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	changes := []model.Tree{
		{ID: "0", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeWait}}},
		{ID: "0", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeSequence, Children: []string{"wait", "end"}}, {ID: "wait", Type: model.NodeWait, DurationMS: 5}, {ID: "end", Type: model.NodeWait}}},
	}
	for _, changed := range changes {
		p.Trees = append(p.Trees[:1], changed)
		after, err := Generate(p)
		if err != nil {
			t.Fatal(err)
		}
		if before.Version == after.Version {
			t.Fatal("无关树改变未更新工程版本")
		}
		for _, file := range before.Files[1:] {
			if !bytes.Equal(file.Source, generatedSource(t, after, file.Name)) {
				t.Fatalf("无关树变化影响 %s", file.Name)
			}
		}
	}
	p.Trees = p.Trees[:1]
	after, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("移除无关树后未恢复原始产物")
	}
}

// TestCallerStructureDoesNotRenameSharedInstances 验证调用方普通结构调整保留引用链身份，增加引用只增加新实例。
func TestCallerStructureDoesNotRenameSharedInstances(t *testing.T) {
	p := model.Project{SchemaVersion: 1, Name: "引用链", Generation: model.Generation{Package: "generated", ContextType: "any"}, Trees: []model.Tree{
		{ID: "caller", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeSequence, Children: []string{"call", "wait"}}, {ID: "call", Type: model.NodeSubtree, Tree: "shared"}, {ID: "wait", Type: model.NodeWait}}},
		{ID: "shared", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeWait}}},
	}}
	before, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Trees[0].Nodes[0].Children = []string{"wrapper"}
	p.Trees[0].Nodes = append(p.Trees[0].Nodes, model.Node{ID: "wrapper", Type: model.NodeSequence, Children: []string{"wait", "call"}})
	reshaped, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	name := TreeFileName("shared")
	if !bytes.Equal(generatedSource(t, before, name), generatedSource(t, reshaped, name)) {
		t.Fatal("普通祖先结构调整改变被引用树源码")
	}
	p.Trees[0].Nodes[0].Children = append(p.Trees[0].Nodes[0].Children, "callAgain")
	p.Trees[0].Nodes = append(p.Trees[0].Nodes, model.Node{ID: "callAgain", Type: model.NodeSubtree, Tree: "shared"})
	expanded, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(generatedSource(t, before, name), generatedSource(t, expanded, name)) {
		t.Fatal("新增引用未新增展开函数")
	}
	names := make(map[string]bool)
	for _, loc := range expanded.SourceMap {
		names[loc.FunctionName] = true
	}
	for _, loc := range before.SourceMap {
		if loc.TreeID == "shared" && !names[loc.FunctionName] {
			t.Fatal("新增引用改变既有展开实例名称")
		}
	}
}

// TestTreeFileNamesArePortable 验证合法树 ID 原样进入文件名，不添加摘要或截断。
func TestTreeFileNamesArePortable(t *testing.T) {
	seen := make(map[string]bool)
	for _, id := range []string{"patrol", "xxz1", "CON", "9_Main", "_", strings.Repeat("X", 80)} {
		name := TreeFileName(id)
		if filepath.Base(name) != name || name != "tree_"+id+".gen.go" {
			t.Fatalf("不安全或错误文件名: %q", name)
		}
		if seen[strings.ToLower(name)] {
			t.Fatalf("树文件名冲突: %s", name)
		}
		seen[strings.ToLower(name)] = true
	}
}

// TestInstanceDigestCollisionFails 验证深引用链短摘要相同的不同身份明确失败，不借用槽位消歧。
func TestInstanceDigestCollisionFails(t *testing.T) {
	g := generator{nodes: []occurrence{
		{tree: "main", node: model.Node{CodeName: "One"}, identity: "one", via: "_Via_aaaaaaaaaaaa", chain: strings.Repeat("a", 64)},
		{tree: "main", node: model.Node{CodeName: "Two"}, identity: "two", via: "_Via_aaaaaaaaaaaa", chain: strings.Repeat("a", 12) + strings.Repeat("b", 52)},
	}}
	if err := g.assignSymbols(); err == nil || !strings.Contains(err.Error(), "摘要冲突") {
		t.Fatalf("未拒绝摘要冲突: %v", err)
	}
}
