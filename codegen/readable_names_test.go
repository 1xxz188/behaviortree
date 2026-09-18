package codegen

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestReadableCodeNames 验证节点 UUID 只保留为身份，普通生成名称仅由树 ID 和代码名组成。
func TestReadableCodeNames(t *testing.T) {
	const uuid = "node_c76e896d9203449fbedff207f183462b"
	p := model.Project{SchemaVersion: model.SchemaVersion, Name: "可读命名", Generation: model.Generation{PackagePath: "generated", ContextType: "any"}, Trees: []model.Tree{
		{ID: "patrol", Root: uuid, Nodes: []model.Node{{ID: uuid, CodeName: "WaitMove", Type: model.NodeWait}}},
	}}
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 2 || r.Files[1].Name != "tree_patrol.gen.go" || r.SourceMap[0].FunctionName != "btNodePatrolWaitMove" {
		t.Fatalf("普通入口仍有冗余命名: %+v", r.SourceMap)
	}
	if !strings.Contains(string(r.Files[0].Source), "nodePatrolWaitMove = iota") {
		t.Fatal("槽位常量没有采用可读代码名")
	}
	compileNamedProject(t, p, r, "")
}

// TestReadableReferenceNames 验证重复子树入口与每个调用位置具有可读、独立且稳定的名称。
func TestReadableReferenceNames(t *testing.T) {
	p := model.Project{SchemaVersion: model.SchemaVersion, Name: "可读调用", Generation: model.Generation{PackagePath: "generated", ContextType: "any"}, Trees: []model.Tree{
		{ID: "combat", Root: "root", Nodes: []model.Node{{ID: "root", CodeName: "Attack", Type: model.NodeWait}}},
		{ID: "patrol", Root: "root", Nodes: []model.Node{
			{ID: "root", CodeName: "Root", Type: model.NodeSequence, Children: []string{"one", "two"}},
			{ID: "one", CodeName: "Engage", Type: model.NodeSubtree, Tree: "combat"},
			{ID: "two", CodeName: "Retry", Type: model.NodeSubtree, Tree: "combat"},
		}},
	}}
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, loc := range r.SourceMap {
		if loc.TreeID == "combat" {
			got[loc.FunctionName] = true
		}
	}
	want := map[string]bool{"btNodeCombatAttack": true, "btNodeCombatAttackViaPatrolEngage": true, "btNodeCombatAttackViaPatrolRetry": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("引用实例名 = %v，期望 %v", got, want)
	}
	compileNamedProject(t, p, r, "")
}

// TestDeepReferenceNamesStayBounded 验证深层引用只在超长路径使用短摘要，且生成包仍可编译。
func TestDeepReferenceNamesStayBounded(t *testing.T) {
	p := model.Project{SchemaVersion: model.SchemaVersion, Name: "深调用", Generation: model.Generation{PackagePath: "generated", ContextType: "any"}}
	for i := 0; i < 8; i++ {
		n := model.Node{ID: "root", CodeName: "CallWithLongButReadableStableCodeName", Type: model.NodeSubtree, Tree: fmt.Sprintf("tree%d", i+1)}
		if i == 7 {
			n.Type, n.Tree, n.CodeName = model.NodeWait, "", "Wait"
		}
		p.Trees = append(p.Trees, model.Tree{ID: fmt.Sprintf("tree%d", i), Root: "root", Nodes: []model.Node{n}})
	}
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	short := regexp.MustCompile(`Via[0-9A-Fa-f]{12}$`)
	seen, shortened := map[string]bool{}, false
	for _, loc := range r.SourceMap {
		if seen[loc.FunctionName] || len(loc.FunctionName) > 180 || strings.Contains(loc.FunctionName, "_") {
			t.Fatalf("深链名称不唯一或失去长度上界: %s", loc.FunctionName)
		}
		seen[loc.FunctionName] = true
		shortened = shortened || short.MatchString(loc.FunctionName)
	}
	if !shortened {
		t.Fatal("深链没有启用短摘要")
	}
	compileNamedProject(t, p, r, "")
}

// TestCodeNameVersionNormalization 验证缺失代码名的补全与持久化版本一致，且不会修改输入。
func TestCodeNameVersionNormalization(t *testing.T) {
	p := model.Project{SchemaVersion: model.SchemaVersion, Name: "版本补全", Generation: model.Generation{PackagePath: "generated", ContextType: "any"}, Trees: []model.Tree{
		{ID: "main", Root: "node_c76e896d9203449fbedff207f183462b", Nodes: []model.Node{{ID: "node_c76e896d9203449fbedff207f183462b", Type: model.NodeWait}}},
	}}
	r, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	version, err := ProjectVersion(model.WithCodeNames(p))
	if err != nil || version != r.Version || p.Trees[0].Nodes[0].CodeName != "" {
		t.Fatalf("补全版本不一致或修改了调用者: %s, %v", version, err)
	}
	if r.SourceMap[0].FunctionName != "btNodeMainWait1" {
		t.Fatalf("UUID 被带入默认代码名: %s", r.SourceMap[0].FunctionName)
	}
}

// TestGeneratedNameConflictFails 验证业务消歧若与另一实例重名会明确失败，不按遍历顺序改名。
func TestGeneratedNameConflictFails(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		nodes := []occurrence{
			{tree: "shared", node: model.Node{CodeName: "Root"}, identity: "one", via: "_ViaMain_Call"},
			{tree: "shared", node: model.Node{CodeName: "Root"}, identity: "two", via: "_ViaMain_Call_Generated"},
		}
		if reverse {
			nodes[0], nodes[1] = nodes[1], nodes[0]
		}
		g := generator{p: model.Project{Catalog: []model.Definition{{GoName: "nodeSharedRootViaMainCall"}}}, nodes: nodes}
		if err := g.assignSymbols(); err == nil || !strings.Contains(err.Error(), "代码名冲突") {
			t.Fatalf("实例名称冲突没有明确失败（逆序=%v）: %v", reverse, err)
		}
	}
}
