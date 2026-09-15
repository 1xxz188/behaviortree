package codegen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestReadableUnderscores 验证树名和代码名中的下划线不会在生成名称中连续出现。
func TestReadableUnderscores(t *testing.T) {
	p := model.Example()
	p.Trees[0].ID = "npc_troop"
	p.Trees[0].Nodes[0].CodeName = "NPC_Root"
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceMap[0].FunctionName != "btNodeNpc_troop_NPC_Root" {
		t.Fatalf("生成名称仍包含转义下划线: %s", r.SourceMap[0].FunctionName)
	}
	compileNamedProject(t, p, r, "")
}

// TestNormalizedNameCollisions 验证组件边界、连续下划线及引用链整理后的重名能稳定消歧。
func TestNormalizedNameCollisions(t *testing.T) {
	p := model.Project{SchemaVersion: 1, Generation: model.Generation{Package: "generated", ContextType: "any"}, Trees: []model.Tree{
		{ID: "foo", Root: "root", Nodes: []model.Node{{ID: "root", CodeName: "Bar_Baz", Type: model.NodeWait}}},
		{ID: "foo_Bar", Root: "root", Nodes: []model.Node{{ID: "root", CodeName: "Baz", Type: model.NodeWait}}},
		{ID: "_", Root: "root", Nodes: []model.Node{
			{ID: "root", CodeName: "Root_", Type: model.NodeSequence, Children: []string{"end"}},
			// 纯下划线树 ID 中的子节点名 End 仍应生成合法且独立的符号。
			{ID: "end", CodeName: "End", Type: model.NodeWait},
		}},
		{ID: "__", Root: "root", Nodes: []model.Node{{ID: "root", CodeName: "Root__", Type: model.NodeWait}}},
		{ID: "main_", Root: "root", Nodes: []model.Node{
			{ID: "root", CodeName: "Root", Type: model.NodeSequence, Children: []string{"a", "b"}},
			{ID: "a", CodeName: "A_B", Type: model.NodeSubtree, Tree: "foo"},
			{ID: "b", CodeName: "A__B", Type: model.NodeSubtree, Tree: "foo"},
		}},
	}}
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, loc := range r.SourceMap {
		if strings.Contains(loc.FunctionName, "__") || names[loc.FunctionName] {
			t.Fatalf("生成名称仍有连续下划线或重名: %s", loc.FunctionName)
		}
		names[loc.FunctionName] = true
	}
	compileNamedProject(t, p, r, "")
	// 改变输入数组顺序不应影响名称分配或完整生成结果。
	for i, j := 0, len(p.Trees)-1; i < j; i, j = i+1, j-1 {
		p.Trees[i], p.Trees[j] = p.Trees[j], p.Trees[i]
	}
	again, err := Generate(p)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatalf("名称消歧依赖输入顺序: %v", err)
	}
}
