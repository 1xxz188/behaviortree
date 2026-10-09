package model

import (
	"encoding/json"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
)

// TestValidateDraftNodes 验证孤立动作和整段未接入根的草稿只产生逐节点警告，不校验未完成的绑定和拓扑。
func TestValidateDraftNodes(t *testing.T) {
	cases := []struct {
		name  string // 草稿场景名称。
		nodes []Node // 根树之外的草稿节点。
	}{
		{"未绑定动作", []Node{{ID: "draft", Type: NodeAction}}},
		{"未完成分支", []Node{{ID: "draft", Type: NodeSequence, Children: []string{"draftAction", "missing"}}, {ID: "draftAction", Type: NodeAction}}},
		{"草稿节点环", []Node{{ID: "draft", Type: NodeSequence, Children: []string{"draftChild"}}, {ID: "draftChild", Type: NodeSequence, Children: []string{"draft"}}}},
		{"草稿子树递归", []Node{{ID: "draft", Type: NodeSubtree, Tree: "main"}}},
		{"草稿引用缺失子树", []Node{{ID: "draft", Type: NodeSubtree, Tree: "missing"}}},
		{"草稿指向有效树", []Node{{ID: "draft", Type: NodeSequence, Children: []string{"root", "wait"}}}},
		{"草稿代码名未完成", []Node{{ID: "draft", Type: NodeAction, CodeName: "!", NamingVersion: PrefixedBusinessNaming}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Example()
			p.Trees[0].Nodes = append(p.Trees[0].Nodes, tc.nodes...)
			diagnostics := Validate(p)
			if len(diagnostics) != len(tc.nodes) {
				t.Fatalf("每个草稿应只有一条跳过警告，得到 %+v", diagnostics)
			}
			for _, diagnostic := range diagnostics {
				raw, err := json.Marshal(diagnostic)
				if err != nil || !strings.Contains(string(raw), `"severity":"warning"`) || !strings.Contains(diagnostic.Message, "跳过") {
					t.Fatalf("草稿不应阻断校验，应说明生成时跳过: %+v, %v", diagnostic, err)
				}
			}
		})
	}
}

// TestConnectedDraftValidation 验证草稿一旦接入根树，未绑定业务、参数错误和非法拓扑仍会阻止生成。
func TestConnectedDraftValidation(t *testing.T) {
	cases := []struct {
		name string         // 接回根树后的错误场景。
		node Node           // 待接入的草稿节点。
		edit func(*Project) // 可选的业务目录配置。
		want string         // 必须保留的错误说明。
	}{
		{name: "业务未绑定", node: Node{ID: "draft", Type: NodeAction}, want: "绑定不存在"},
		{name: "参数未绑定", node: Node{ID: "draft", Type: NodeAction, Binding: "move"}, edit: func(p *Project) {
			p.Catalog = []Definition{{ID: "move", Kind: DefinitionAction, GoName: "Move", Params: []Parameter{{Name: "Speed", Type: bt.IntType}}}}
		}, want: "未绑定参数"},
		{name: "节点环", node: Node{ID: "draft", Type: NodeSequence, Children: []string{"root"}}, want: "存在环"},
		{name: "子树递归", node: Node{ID: "draft", Type: NodeSubtree, Tree: "main"}, want: "不能递归"},
		{name: "子树缺失", node: Node{ID: "draft", Type: NodeSubtree, Tree: "missing"}, want: "子树不存在"},
		{name: "代码名非法", node: Node{ID: "draft", Type: NodeWait, CodeName: "!"}, want: "代码名必须"},
		{name: "时长非法", node: Node{ID: "draft", Type: NodeWait, DurationMS: -1}, want: "非负毫秒"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Example()
			if tc.edit != nil {
				tc.edit(&p)
			}
			p.Trees[0].Nodes = append(p.Trees[0].Nodes, tc.node)
			p.Trees[0].Nodes[0].Children = append(p.Trees[0].Nodes[0].Children, tc.node.ID)
			diagnostics := Validate(p)
			found := false
			for _, diagnostic := range ValidationErrors(diagnostics) {
				found = found || strings.Contains(diagnostic.Message, tc.want)
			}
			if !found || len(ValidationErrors(diagnostics)) != len(diagnostics) {
				t.Fatalf("根可达节点应保留真实错误 %q，而非草稿警告: %+v", tc.want, diagnostics)
			}
		})
	}
}

// TestDraftIdentityAndMixedDiagnostics 验证草稿仍须保持身份唯一，警告不会隐藏根树的实际错误。
func TestDraftIdentityAndMixedDiagnostics(t *testing.T) {
	for _, nodes := range [][]Node{
		{{ID: "", Type: NodeAction}},
		{{ID: "draft", Type: NodeAction}, {ID: "draft", Type: NodeWait}},
		{{ID: "root", Type: NodeAction}},
	} {
		p := Example()
		p.Trees[0].Nodes = append(p.Trees[0].Nodes, nodes...)
		found := false
		for _, diagnostic := range ValidationErrors(Validate(p)) {
			found = found || diagnostic.Field == "id"
		}
		if !found {
			t.Fatalf("未拒绝草稿的空或重复节点 ID: %+v", nodes)
		}
	}
	p := Example()
	p.Trees[0].Nodes[1].DurationMS = -1
	p.Trees[0].Nodes = append(p.Trees[0].Nodes, Node{ID: "draft", Type: NodeAction})
	diagnostics := Validate(p)
	errors := ValidationErrors(diagnostics)
	if len(diagnostics) != 2 || len(errors) != 1 || errors[0].NodeID != "wait" {
		t.Fatalf("警告和错误必须共存，只有运行节点错误阻断生成: %+v", diagnostics)
	}
}

// TestDraftProjectRoundTrip 验证运行投影和校验不修改工程，完整保存与重开保留孤立节点、连线及布局。
func TestDraftProjectRoundTrip(t *testing.T) {
	p := Example()
	p.Trees[0].Nodes = append(p.Trees[0].Nodes, Node{ID: "draft", Type: NodeSequence, Children: []string{"draftAction"}}, Node{ID: "draftAction", Type: NodeAction})
	p.Trees[0].Layout["draft"] = Position{X: 800, Y: 500}
	p.Trees[0].Layout["draftAction"] = Position{X: 900, Y: 600}
	raw, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	active, diagnostics := ExecutionProject(reopened)
	if len(active.Trees[0].Nodes) != 3 || len(diagnostics) != 2 || len(ValidationErrors(Validate(reopened))) != 0 {
		t.Fatalf("运行投影应只保留根树，原工程草稿应仅警告: %+v", diagnostics)
	}
	after, err := Encode(reopened)
	if err != nil || string(raw) != string(after) || len(reopened.Trees[0].Nodes) != 5 || reopened.Trees[0].Layout["draft"].X != 800 {
		t.Fatalf("校验或运行投影丢失了草稿: %v", err)
	}
}
