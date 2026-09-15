package model

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestCodeNameValidation 验证 ASCII、长度、关键字及树内重复规则，并允许草稿暂缺代码名。
func TestCodeNameValidation(t *testing.T) {
	for _, name := range []string{"Root", "wait", "Wait_1", strings.Repeat("A", 40)} {
		if !ValidCodeName(name) {
			t.Errorf("合法代码名被拒绝: %q", name)
		}
	}
	for _, name := range []string{"", "_Wait", "1Wait", "中文", "Wait-Move", "func", "wait\n", strings.Repeat("A", 41)} {
		if ValidCodeName(name) {
			t.Errorf("非法代码名未被拒绝: %q", name)
		}
		if name == "" {
			continue
		}
		p := Example()
		p.Trees[0].Nodes[0].CodeName = name
		if diagnostics := Validate(p); len(diagnostics) != 1 || diagnostics[0].Field != "codeName" || diagnostics[0].NodeID != "root" {
			t.Errorf("未正确定位非法代码名 %q: %+v", name, diagnostics)
		}
	}
	p := Example()
	p.Trees[0].Nodes[0].CodeName = "Wait"
	p.Trees[0].Nodes[1].CodeName = "Wait"
	if diagnostics := Validate(p); len(diagnostics) != 1 || diagnostics[0].Field != "codeName" {
		t.Fatalf("未拒绝树内重复: %+v", diagnostics)
	}
	p.Trees[0].Nodes[1].CodeName = "wait"
	if diagnostics := Validate(p); len(diagnostics) != 0 {
		t.Fatalf("应允许大小写不同的代码名及缺失代码名: %+v", diagnostics)
	}
}

// TestWithCodeNamesDeterministic 验证随机身份采用类型名、显式名称优先且重排不影响分配，也不修改调用方。
func TestWithCodeNamesDeterministic(t *testing.T) {
	p := Project{Trees: []Tree{{Nodes: []Node{
		{ID: "node_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Type: NodeWait},
		{ID: "root", Type: NodeSequence},
		{ID: "node_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: NodeWait},
		{ID: "custom", Type: NodeWait, CodeName: "Wait"},
		{ID: "550e8400-e29b-41d4-a716-446655440000", Type: NodeSubtree},
		{ID: "func", Type: NodeAction},
		{ID: "wrong", Type: NodeWait, CodeName: "bad-name"},
		{ID: "", Type: InvalidNodeType},
	}}}}
	filled := WithCodeNames(p)
	want := []string{"Wait2", "Root", "Wait1", "Wait", "Subtree1", "Action1", "bad-name", "Node1"}
	for i, node := range filled.Trees[0].Nodes {
		if node.CodeName != want[i] {
			t.Errorf("节点 %q 得到 %q，期望 %q", node.ID, node.CodeName, want[i])
		}
		if p.Trees[0].Nodes[i].CodeName == "" && node.CodeName == "" {
			t.Errorf("节点 %q 未补全", node.ID)
		}
	}
	if p.Trees[0].Nodes[0].CodeName != "" {
		t.Fatal("补全修改了调用方节点")
	}
	reordered := p
	reordered.Trees = slices.Clone(p.Trees)
	reordered.Trees[0].Nodes = slices.Clone(p.Trees[0].Nodes)
	slices.Reverse(reordered.Trees[0].Nodes)
	reordered = WithCodeNames(reordered)
	slices.Reverse(reordered.Trees[0].Nodes)
	if !reflect.DeepEqual(filled, reordered) {
		t.Fatal("重排改变了默认代码名")
	}
	if !reflect.DeepEqual(filled, WithCodeNames(filled)) {
		t.Fatal("重复补全改变了已保存代码名")
	}
}

// TestCodeNamePersistence 验证未指定代码名的工程在导入和导出边界使用默认分配规则，保存后增删节点不会重命名已有节点。
func TestCodeNamePersistence(t *testing.T) {
	p := Example()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Trees[0].Nodes[0].CodeName != "Root" {
		t.Fatal("导入没有分配默认代码名")
	}
	encoded, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"codeName": "Root"`) || p.Trees[0].Nodes[0].CodeName != "" {
		t.Fatal("导出没有保存代码名，或修改了调用方")
	}
	decoded.Trees[0].Nodes = append(decoded.Trees[0].Nodes[1:], Node{ID: "a", Type: NodeWait})
	filled := WithCodeNames(decoded)
	if filled.Trees[0].Nodes[0].CodeName != "Wait" || filled.Trees[0].Nodes[1].CodeName != "Done" {
		t.Fatal("增删节点重命名了已有代码名")
	}
}

// TestCodeNameLongCollision 验证截短的长名称共享后缀游标且始终满足长度限制。
func TestCodeNameLongCollision(t *testing.T) {
	base := strings.Repeat("A", 40)
	used := map[string]struct{}{base: {}}
	next := make(map[string]int)
	for i := 0; i < 120; i++ {
		name := availableCodeName(base, false, used, next)
		if !ValidCodeName(name) {
			t.Fatalf("产生非法代码名: %q", name)
		}
		if _, exists := used[name]; exists {
			t.Fatalf("分配重复代码名: %q", name)
		}
		used[name] = struct{}{}
	}
	other := strings.Repeat("A", 39) + "B"
	used[other] = struct{}{}
	if name := availableCodeName(other, false, used, next); name != strings.Repeat("A", 37)+"121" {
		t.Fatalf("同截短前缀未共享分配游标: %q", name)
	}
}

// TestFallbackCodeNameNumbering 验证类型默认名从 1 开始、冲突继续计数，并不抢占短语义 ID 的无后缀名称。
func TestFallbackCodeNameNumbering(t *testing.T) {
	p := Project{Trees: []Tree{{Nodes: []Node{
		{ID: "node_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: NodeWait},
		{ID: "node_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Type: NodeWait},
		{ID: "wait", Type: NodeWait},
		{ID: "custom", Type: NodeWait, CodeName: "Wait1"},
	}}}}
	filled := WithCodeNames(p)
	for i, want := range []string{"Wait2", "Wait3", "Wait", "Wait1"} {
		if got := filled.Trees[0].Nodes[i].CodeName; got != want {
			t.Errorf("节点 %d 代码名为 %q，期望 %q", i, got, want)
		}
	}
}

// TestOpaqueCodeNameCase 验证随机 ID 的前缀和十六进制大小写均不会泄漏到默认代码名，与 Web 分配规则一致。
func TestOpaqueCodeNameCase(t *testing.T) {
	for _, id := range []string{"node_abcdefabcdefabcdefabcdefabcdefab", "NODE_ABCDEFABCDEFABCDEFABCDEFABCDEFAB", "550E8400-E29B-41D4-A716-446655440000"} {
		name, numbered := defaultCodeName(Node{ID: id, Type: NodeWait})
		if name != "Wait" || !numbered {
			t.Errorf("随机 ID %q 未使用类型默认名: %q, %v", id, name, numbered)
		}
	}
}
