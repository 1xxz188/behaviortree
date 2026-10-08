package model

import (
	"bytes"
	"testing"
)

// TestPriorityCompletionRoundTrip 验证 Schema 4 保留开启配置，旧工程及关闭配置仍使用缺省语义。
func TestPriorityCompletionRoundTrip(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		p := Example()
		p.Trees[0].Nodes[0] = Node{ID: "root", Type: NodePriority, Children: []string{"wait"}, ReselectOnCompletion: enabled}
		p.Trees[0].Nodes = p.Trees[0].Nodes[:2]
		if diagnostics := Validate(p); len(diagnostics) != 0 {
			t.Fatalf("有效优先级配置被拒绝：%+v", diagnostics)
		}
		raw, err := Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"reselectOnCompletion"`)) != enabled {
			t.Fatalf("可选配置省略规则错误：%s", raw)
		}
		decoded, err := Decode(raw)
		if err != nil || decoded.SchemaVersion != 4 || decoded.Trees[0].Nodes[0].ReselectOnCompletion != enabled {
			t.Fatalf("优先级配置往返失败：%+v，%v", decoded, err)
		}
	}
}

// TestPriorityCompletionValidation 验证开启配置只能放在 Priority，关闭配置不影响已有其他节点。
func TestPriorityCompletionValidation(t *testing.T) {
	for typ := NodeSequence; typ <= NodeSubtree; typ++ {
		if typ == NodePriority {
			continue
		}
		p := Example()
		p.Trees[0].Nodes[0].Type = typ
		p.Trees[0].Nodes[0].ReselectOnCompletion = true
		found := false
		for _, d := range Validate(p) {
			if d.TreeID == "main" && d.NodeID == "root" && d.Field == "reselectOnCompletion" {
				found = true
			}
		}
		if !found {
			t.Errorf("未拒绝 %s 的完成后重选配置", typ)
		}
	}
	p := Example()
	if diagnostics := Validate(p); len(diagnostics) != 0 {
		t.Fatalf("缺省配置改变旧工程校验：%+v", diagnostics)
	}
}

// TestPriorityCompletionRejectsWrongJSONType 验证输入边界不会把字符串或数值开关静默转换。
func TestPriorityCompletionRejectsWrongJSONType(t *testing.T) {
	p := Example()
	p.Trees[0].Nodes[0].ReselectOnCompletion = true
	raw, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`"true"`, "1", "[]", "{}"} {
		input := bytes.Replace(raw, []byte(`"reselectOnCompletion": true`), []byte(`"reselectOnCompletion": `+invalid), 1)
		if _, err := Decode(input); err == nil {
			t.Fatalf("接受非法开关类型：%s", invalid)
		}
	}
}

// TestPriorityCompletionReservedHelper 验证固定 helper 只在启用重选时占用命名，且同时保护本包上下文类型。
func TestPriorityCompletionReservedHelper(t *testing.T) {
	for _, tc := range []struct {
		name  string         // 冲突来源的测试名称。
		field string         // 应报告错误的工程字段。
		edit  func(*Project) // 设置同名业务函数或上下文类型。
	}{
		{"动作函数", "catalog", func(p *Project) {
			p.Catalog = []Definition{{ID: "action", Kind: DefinitionAction, GoName: "btRearmPriorityCandidate"}}
		}},
		{"条件函数", "catalog", func(p *Project) {
			p.Catalog = []Definition{{ID: "condition", Kind: DefinitionCondition, GoName: "btRearmPriorityCandidate"}}
		}},
		{"本包上下文", "generation.contextType", func(p *Project) {
			p.Generation.ContextType = "btRearmPriorityCandidate"
		}},
		{"本包上下文指针", "generation.contextType", func(p *Project) {
			p.Generation.ContextType = "*btRearmPriorityCandidate"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				p := Example()
				p.Trees[0].Nodes[0] = Node{ID: "root", Type: NodePriority, Children: []string{"wait"}, ReselectOnCompletion: enabled}
				p.Trees[0].Nodes = p.Trees[0].Nodes[:2]
				tc.edit(&p)
				diagnostics := Validate(p)
				if !enabled {
					if len(diagnostics) != 0 {
						t.Fatalf("未开启重选却改变旧命名规则：%+v", diagnostics)
					}
					continue
				}
				found := false
				for _, d := range diagnostics {
					if d.Field == tc.field {
						found = true
					}
				}
				if !found {
					t.Fatalf("未拒绝固定 helper 名称冲突：%+v", diagnostics)
				}
			}
		})
	}
}
