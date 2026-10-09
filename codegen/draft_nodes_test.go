package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestDraftNodesDoNotChangeGeneratedProgram 验证不可达节点的绑定、参数和拓扑错误不会污染有效树的源码或映射。
func TestDraftNodesDoNotChangeGeneratedProgram(t *testing.T) {
	project := model.WithCodeNames(model.Example())
	project.Catalog = []model.Definition{{ID: "move", Name: "移动", Kind: model.DefinitionAction, GoName: "Move", Params: []model.Parameter{{Name: "Count", Type: bt.IntType}}}}
	before, err := Generate(project)
	if err != nil {
		t.Fatal(err)
	}
	project.Trees[0].Nodes = append(project.Trees[0].Nodes,
		model.Node{ID: "draft_unbound", Type: model.NodeAction},
		model.Node{ID: "draft_params", Type: model.NodeAction, Binding: "move"},
		model.Node{ID: "draft_name", Type: model.NodeWait, CodeName: "for"},
		model.Node{ID: "draft_missing_child", Type: model.NodeSequence, Children: []string{"missing"}},
		model.Node{ID: "draft_cycle_a", Type: model.NodeSequence, Children: []string{"draft_cycle_b"}},
		model.Node{ID: "draft_cycle_b", Type: model.NodeSequence, Children: []string{"draft_cycle_a"}},
		model.Node{ID: "draft_recursive", Type: model.NodeSubtree, Tree: "main"},
		model.Node{ID: "draft_missing_tree", Type: model.NodeSubtree, Tree: "missing"},
		model.Node{ID: "draft_parent", Type: model.NodeSequence, Children: []string{"wait"}},
	)
	input, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Generate(project)
	if err != nil {
		t.Fatalf("草稿节点阻断了代码生成: %v", err)
	}
	assertDraftWarnings(t, after.Diagnostics, map[string][]string{"main": {"draft_unbound", "draft_params", "draft_name", "draft_missing_child", "draft_cycle_a", "draft_cycle_b", "draft_recursive", "draft_missing_tree", "draft_parent"}})
	if before.Version != after.Version || !reflect.DeepEqual(before.Files, after.Files) || !reflect.DeepEqual(before.SourceMap, after.SourceMap) {
		t.Fatal("不可达草稿改变了运行源码、版本或节点映射")
	}
	unchanged, err := json.Marshal(project)
	if err != nil || !bytes.Equal(input, unchanged) {
		t.Fatal("代码生成修改了包含草稿的输入工程", err)
	}
}

// TestDraftNodeChangesKeepProjectVersion 验证新增、编辑和删除不可达草稿都不使已有运行源码过期。
func TestDraftNodeChangesKeepProjectVersion(t *testing.T) {
	project := model.WithCodeNames(model.Example())
	before, err := ProjectVersion(project)
	if err != nil {
		t.Fatal(err)
	}
	project.Trees[0].Nodes = append(project.Trees[0].Nodes, model.Node{ID: "draft", Type: model.NodeAction, CodeName: "Draft"})
	for _, name := range []string{"草稿", "尚未完成的动作"} {
		project.Trees[0].Nodes[3].Name = name
		project.Trees[0].Nodes[3].Comment = name
		version, err := ProjectVersion(project)
		if err != nil || version != before {
			t.Fatalf("修改草稿改变了运行版本: %s != %s, %v", version, before, err)
		}
	}
	project.Trees[0].Nodes = project.Trees[0].Nodes[:3]
	version, err := ProjectVersion(project)
	if err != nil || version != before {
		t.Fatal("删除草稿改变了运行版本", err)
	}
}

// TestDraftNodesDoNotCountTowardExpansionLimit 验证大量仅存在于草稿的重复子树引用不会触发展开上限。
func TestDraftNodesDoNotCountTowardExpansionLimit(t *testing.T) {
	project := model.Example()
	project.Trees = []model.Tree{{ID: "t0", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeWait}}}}
	for i := 1; i <= 17; i++ {
		project.Trees = append(project.Trees, model.Tree{ID: fmt.Sprintf("t%d", i), Root: "root", Nodes: []model.Node{
			{ID: "root", Type: model.NodeWait},
			{ID: "draft_a", Type: model.NodeSubtree, Tree: fmt.Sprintf("t%d", i-1)},
			{ID: "draft_b", Type: model.NodeSubtree, Tree: fmt.Sprintf("t%d", i-1)},
		}})
	}
	result, err := Generate(project)
	if err != nil {
		t.Fatalf("未执行的草稿被计入子树展开: %v", err)
	}
	if len(result.SourceMap) != len(project.Trees) {
		t.Fatalf("生成槽位包含草稿: %d != %d", len(result.SourceMap), len(project.Trees))
	}
	expected := make(map[string][]string, len(project.Trees)-1)
	for _, tree := range project.Trees[1:] {
		expected[tree.ID] = []string{"draft_a", "draft_b"}
	}
	assertDraftWarnings(t, result.Diagnostics, expected)
}

// TestDraftNodesCompileAndRunSharedSubtree 验证共享子树各自保留独立入口，且真实编译和运行都跳过未绑定草稿。
func TestDraftNodesCompileAndRunSharedSubtree(t *testing.T) {
	project := model.Example()
	project.Trees = []model.Tree{
		{ID: "main", Root: "root", Nodes: []model.Node{
			{ID: "root", Type: model.NodeSequence, Children: []string{"call_a", "call_b"}},
			{ID: "call_a", Type: model.NodeSubtree, Tree: "shared"},
			{ID: "call_b", Type: model.NodeSubtree, Tree: "shared"},
			{ID: "draft_recursive", Type: model.NodeSubtree, Tree: "main"},
		}},
		{ID: "shared", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeWait}, {ID: "draft_unbound", Type: model.NodeAction}}},
	}
	result, err := Generate(project)
	if err != nil {
		t.Fatalf("共享子树中的草稿阻断生成: %v", err)
	}
	if len(result.SourceMap) != 6 {
		t.Fatalf("独立入口与两次子树调用的槽位数错误: %d", len(result.SourceMap))
	}
	assertDraftWarnings(t, result.Diagnostics, map[string][]string{"main": {"draft_recursive"}, "shared": {"draft_unbound"}})
	shared := 0
	for _, location := range result.SourceMap {
		if location.TreeID == "shared" {
			shared++
			if location.NodeID != "root" {
				t.Fatal("共享子树草稿进入了源码映射", location)
			}
		}
	}
	if shared != 3 {
		t.Fatalf("子树的独立入口或复用实例丢失: %d", shared)
	}
	dir := t.TempDir()
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedFiles(t, dir, result)
	gomod := "module bt.draft.generated.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(moduleRoot)) + "\n"
	for name, content := range map[string]string{"go.mod": gomod, "draft_runtime_test.go": draftRuntimeFixture} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("包含草稿的生成包编译或运行失败: %v\n%s", err, output)
	}
}

// TestDraftNodesBecomeErrorsWhenConnected 验证未绑定动作接入根节点后立即恢复严格校验。
func TestDraftNodesBecomeErrorsWhenConnected(t *testing.T) {
	project := model.Example()
	project.Trees[0].Nodes = append(project.Trees[0].Nodes, model.Node{ID: "draft", Type: model.NodeAction})
	if _, err := Generate(project); err != nil {
		t.Fatalf("未接入根的动作不应阻断生成: %v", err)
	}
	project.Trees[0].Nodes[0].Children = append(project.Trees[0].Nodes[0].Children, "draft")
	var validation *ValidationError
	if _, err := Generate(project); !errors.As(err, &validation) {
		t.Fatalf("接入根的未绑定动作没有产生校验错误: %v", err)
	}
}

// TestDraftNodeIdentityErrorsStillRejectGeneration 验证过滤草稿不会掩盖空节点身份和重复身份。
func TestDraftNodeIdentityErrorsStillRejectGeneration(t *testing.T) {
	for _, id := range []string{"", "root"} {
		t.Run(fmt.Sprintf("ID=%q", id), func(t *testing.T) {
			project := model.Example()
			project.Trees[0].Nodes = append(project.Trees[0].Nodes, model.Node{ID: id, Type: model.NodeWait})
			var validation *ValidationError
			if _, err := Generate(project); !errors.As(err, &validation) {
				t.Fatalf("无效节点身份未阻断生成: %v", err)
			}
		})
	}
}

// assertDraftWarnings 验证每个被跳过的原始节点只有一条可定位警告，复用展开不会重复报警。
func assertDraftWarnings(t *testing.T, diagnostics []model.Diagnostic, expected map[string][]string) {
	t.Helper()
	want := make(map[string]bool)
	for treeID, nodeIDs := range expected {
		for _, nodeID := range nodeIDs {
			want[treeID+"\x00"+nodeID] = true
		}
	}
	if len(diagnostics) != len(want) || len(model.ValidationErrors(diagnostics)) != 0 {
		t.Fatalf("跳过节点的警告数量或级别错误: %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		key := diagnostic.TreeID + "\x00" + diagnostic.NodeID
		if diagnostic.Severity != "warning" || diagnostic.Message == "" || !want[key] {
			t.Fatalf("草稿警告遗漏、重复或无法定位: %#v", diagnostic)
		}
		delete(want, key)
	}
}

// draftRuntimeFixture 编译进实际生成包，检查草稿不占运行槽位且两个树入口均可启动。
const draftRuntimeFixture = `package behavior

import (
	"testing"
	bt "github.com/1xxz188/behaviortree"
)

// TestDraftRuntime 验证生成程序只包含根可达节点，并保留共享树的独立入口。
func TestDraftRuntime(t *testing.T) {
	program := NewProgram("")
	if len(program.Nodes) != 6 || len(program.Roots) != 2 {
		t.Fatalf("运行节点或入口数量错误: %d, %d", len(program.Nodes), len(program.Roots))
	}
	for _, node := range program.Nodes {
		if node.ID == "draft_unbound" || node.ID == "draft_recursive" {
			t.Fatal("草稿进入运行程序", node)
		}
	}
	for _, treeID := range []string{"main", "shared"} {
		var queue []func()
		instance, err := bt.NewInstance(program, treeID, treeID, any(nil), bt.Options{Post: func(fn func()) { queue = append(queue, fn) }})
		if err != nil {
			t.Fatal(err)
		}
		if status := instance.Start(); status != bt.Success || len(queue) != 0 {
			t.Fatalf("入口 %s 未正常完成: %v, %d", treeID, status, len(queue))
		}
	}
}
`
