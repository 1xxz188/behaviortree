package codegen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestReadableUnderscores 验证生成名称采用 MixedCaps，且保留显式大写缩写和持久身份。
func TestReadableUnderscores(t *testing.T) {
	p := model.Example()
	p.Trees[0].ID = "npc_troop"
	p.Trees[0].Nodes[0].CodeName = "NPC_Root"
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceMap[0].FunctionName != "btNodeNpcTroopNPCRoot" {
		t.Fatalf("生成名称未采用 MixedCaps: %s", r.SourceMap[0].FunctionName)
	}
	if r.SourceMap[0].TreeID != "npc_troop" || r.Files[1].Name != "tree_npc_troop.gen.go" || p.Trees[0].Nodes[0].CodeName != "NPC_Root" {
		t.Fatal("生成命名不应修改持久化身份、代码名或文件名")
	}
	compileNamedProject(t, p, r, "")
	p.Trees[0].ID = "NPCTroop"
	r, err = Generate(p)
	if err != nil || r.SourceMap[0].FunctionName != "btNodeNPCTroopNPCRoot" {
		t.Fatalf("显式大写缩写未保留: %+v, %v", r.SourceMap, err)
	}
}

// TestNormalizedNameCollisions 验证组件边界、连续下划线及引用链整理后的重名能稳定消歧。
func TestNormalizedNameCollisions(t *testing.T) {
	p := model.Project{SchemaVersion: 1, Generation: model.Generation{PackagePath: "generated", ContextType: "any"}, Trees: []model.Tree{
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
		if strings.Contains(loc.FunctionName, "_") || names[loc.FunctionName] {
			t.Fatalf("生成名称仍有下划线或重名: %s", loc.FunctionName)
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
