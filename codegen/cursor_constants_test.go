package codegen

import (
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestGeneratedCursorPositions 验证组合节点直接使用数字序号，不生成仅供本函数使用的游标常量。
func TestGeneratedCursorPositions(t *testing.T) {
	for _, kind := range []model.NodeType{model.NodeSequence, model.NodeSelector, model.NodeParallel} {
		t.Run(kind.String(), func(t *testing.T) {
			p := model.Project{SchemaVersion: model.SchemaVersion, NextEventID: "1", Name: "游标常量", Generation: model.Generation{PackagePath: "generated", ContextType: "any"}, Trees: []model.Tree{
				{ID: "main", Root: "root", Nodes: []model.Node{
					{ID: "root", CodeName: "Root", Type: kind, Children: []string{"first", "second"}},
					{ID: "first", CodeName: "First", Type: model.NodeSequence, Children: []string{"nested"}},
					{ID: "nested", CodeName: "Nested", Type: model.NodeWait},
					{ID: "second", CodeName: "Second", Type: model.NodeWait},
				}},
			}}
			r, err := Generate(p)
			if err != nil {
				t.Fatal(err)
			}
			source := string(generatedSource(t, r, "tree_main.gen.go"))
			if strings.Contains(source, "cursorChild") || strings.Contains(source, "cursorEnd") {
				t.Fatal("仍生成了多余的局部游标常量")
			}
			// 第二个孩子的全局槽位为 3，局部游标仍须为 1。
			for _, branch := range []string{"case 0:", "case 1:"} {
				if !strings.Contains(source, branch) {
					t.Fatalf("缺少直接孩子序号分支 %s", branch)
				}
			}
			compileNamedProject(t, p, r, "")
		})
	}
}
