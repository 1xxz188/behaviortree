package codegen

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestSemanticNamesCompile 验证特殊身份、转换冲突和重复子树的名字稳定且真实可编译。
func TestSemanticNamesCompile(t *testing.T) {
	p := model.Project{SchemaVersion: 1, Name: "命名测试", Generation: model.Generation{PackagePath: "generated", ContextType: "any"}}
	leaf := func(tree, id string) model.Tree {
		return model.Tree{ID: tree, Name: "显示树", Root: id, Nodes: []model.Node{{ID: id, Name: "攻击目标\nfunc injected() {}\r\u2028", Type: model.NodeWait}}}
	}
	p.Trees = []model.Tree{
		leaf("patrol", "walk"), leaf("Patrol2", "walk"),
		leaf("foo", "bar_baz"), leaf("foo_bar", "baz"),
		// 连续下划线保留符号转换冲突覆盖，同时避免文件名规范化后重名。
		leaf("foo__bar", "baz"),
		leaf("12", "攻击/目标"), leaf("0", "--"),
		leaf("shared", "root"),
		{ID: "main", Name: "主树", Root: "root", Nodes: []model.Node{
			{ID: "root", Type: model.NodeParallel, Children: []string{"one", "two"}},
			{ID: "one", Type: model.NodeSubtree, Tree: "shared"},
			{ID: "two", Type: model.NodeSubtree, Tree: "shared"},
		}},
	}
	initial, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	reserved := ""
	for _, loc := range initial.SourceMap {
		if loc.TreeID == "main" && loc.NodeID == "root" {
			reserved = "node" + strings.TrimPrefix(loc.FunctionName, "btNode")
		}
	}
	// 同包合法业务名占用可读槽位常量及第一次消歧结果，生成器必须继续消歧。
	p.Catalog = []model.Definition{
		{ID: "reserved", Name: "冲突声明", Kind: model.DefinitionAction, GoName: reserved},
		{ID: "reservedSuffix", Name: "冲突后缀", Kind: model.DefinitionAction, GoName: reserved + "Generated"},
	}
	a, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	shared := 0
	for _, loc := range a.SourceMap {
		if !token.IsIdentifier(loc.FunctionName) || seen[loc.FunctionName] {
			t.Fatalf("无效或重复函数名: %+v", loc)
		}
		seen[loc.FunctionName] = true
		if loc.TreeID == "shared" {
			shared++
		}
		if strings.Contains(loc.FunctionName, "_") {
			t.Fatalf("函数名仍包含下划线: %+v", loc)
		}
		if loc.TreeID == "main" && loc.NodeID == "root" && !strings.HasSuffix(loc.FunctionName, "GeneratedGenerated") {
			t.Fatalf("业务后缀冲突未继续消歧: %+v", loc)
		}
	}
	if shared != 3 {
		t.Fatalf("子树展开次数 = %d", shared)
	}
	for i, j := 0, len(p.Trees)-1; i < j; i, j = i+1, j-1 {
		p.Trees[i], p.Trees[j] = p.Trees[j], p.Trees[i]
	}
	for i := range p.Trees {
		nodes := p.Trees[i].Nodes
		for a, b := 0, len(nodes)-1; a < b; a, b = a+1, b-1 {
			nodes[a], nodes[b] = nodes[b], nodes[a]
		}
	}
	p.Catalog[0], p.Catalog[1] = p.Catalog[1], p.Catalog[0]
	b, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Files, b.Files) || !reflect.DeepEqual(a.SourceMap, b.SourceMap) || a.Version != b.Version {
		t.Fatal("无语义集合重排改变了生成产物")
	}
	compileNamedProject(t, p, a, "")
}

// TestContextNameCollisions 验证上下文类型占用节点函数、边界或槽位名时仍可编译。
func TestContextNameCollisions(t *testing.T) {
	for _, name := range []string{"btNodeMainWait1", "nodeMainWait1", "btNodeCount", "btNodeNoParent"} {
		t.Run(name, func(t *testing.T) {
			p := model.Project{SchemaVersion: 1, Name: "上下文冲突", Generation: model.Generation{PackagePath: "generated", ContextType: "*" + name}, Trees: []model.Tree{{ID: "main", Name: "主树", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeWait}}}}}
			r, err := Generate(p)
			if err != nil {
				t.Fatal(err)
			}
			compileNamedProject(t, p, r, "type "+name+" struct{}\n")
		})
	}
}

// compileNamedProject 将完整产物与业务骨架放入临时模块，交由 Go 编译器验证名称解析。
func compileNamedProject(t *testing.T, p model.Project, r Result, extra string) {
	t.Helper()
	scaffold, err := Scaffold(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module bt.names.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(root)) + "\n"
	writeGeneratedFiles(t, dir, r)
	resolved, err := model.ResolveGoPackage("", p.Generation.PackagePath)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"go.mod": []byte(mod), "actions.go": scaffold, "context.go": []byte("package " + resolved.PackageName + "\n" + extra)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("命名产物编译失败: %v\n%s", err, output)
	}
}

// TestNodeSlotsAreSymbolic 验证全部节点类型的 Frame 槽位操作与元数据均没有裸整数。
func TestNodeSlotsAreSymbolic(t *testing.T) {
	r, err := Generate(fixtureProject())
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{"Enter": true, "State": true, "Exit": true, "Abort": true, "AbortChildren": true, "Reset": true, "Consume": true, "After": true, "OnlyDirtyChild": true, "IsDirty": true, "PopDirtyChild": true}
	for _, file := range r.Files {
		f, err := parser.ParseFile(token.NewFileSet(), file.Name, file.Source, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if selector, ok := n.Fun.(*ast.SelectorExpr); ok && methods[selector.Sel.Name] && len(n.Args) > 0 {
					if _, ok := n.Args[0].(*ast.BasicLit); ok {
						t.Errorf("Frame.%s 使用裸槽位", selector.Sel.Name)
					}
				}
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok && (key.Name == "Parent" || key.Name == "End") {
					if _, ok := n.Value.(*ast.Ident); !ok {
						t.Errorf("元数据 %s 没有使用具名常量", key.Name)
					}
				}
			}
			return true
		})
	}
	for _, loc := range r.SourceMap {
		lines := strings.Split(string(generatedSource(t, r, loc.File)), "\n")
		line := lines[loc.Line-1]
		if !strings.HasPrefix(line, "func "+loc.FunctionName+"(") {
			t.Fatalf("映射没有指向实际函数: %+v", loc)
		}
	}
	data, err := json.Marshal(r.SourceMap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []SourceLocation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.SourceMap, decoded) {
		t.Fatal("映射持久化丢失函数身份")
	}
}
