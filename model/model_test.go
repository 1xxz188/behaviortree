package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRoundTrip 验证导入导出保留节点 ID、顺序和布局。
func TestRoundTrip(t *testing.T) {
	p := Example()
	raw, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d := Validate(decoded); len(d) > 0 {
		t.Fatal(d)
	}
	again, _ := Encode(decoded)
	if string(raw) != string(again) {
		t.Fatal("JSON 往返改变工程")
	}
	if _, err := Decode(append(raw, []byte(" {}")...)); err == nil {
		t.Fatal("应拒绝多余 JSON")
	}
	if _, err := Decode([]byte(strings.Replace(string(raw), `"schemaVersion"`, `"schemaVerzion"`, 1))); err == nil {
		t.Fatal("应拒绝拼错属性")
	}
}

// TestValidation 验证拓扑错误均产生可定位诊断。
func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Project)
		part string
	}{
		{"重复ID", func(p *Project) { p.Trees[0].Nodes[2].ID = "wait" }, "重复节点"},
		{"节点环", func(p *Project) { p.Trees[0].Nodes[1] = Node{ID: "wait", Type: "sequence", Children: []string{"root"}} }, "存在环"},
		{"多个父节点", func(p *Project) { p.Trees[0].Nodes[0].Children = []string{"wait", "wait"} }, "多个父节点"},
		{"孤立节点", func(p *Project) { p.Trees[0].Nodes[0].Children = []string{"wait"} }, "无法从根"},
		{"参数缺失", func(p *Project) {
			p.Catalog = []Definition{{ID: "a", Name: "a", Kind: "action", GoName: "Move", Params: []Parameter{{Name: "Speed", Type: "int64"}}}}
			p.Trees[0].Nodes[1] = Node{ID: "wait", Type: "action", Binding: "a"}
		}, "缺少必填"},
		{"递归引用", func(p *Project) { p.Trees[0].Nodes[1] = Node{ID: "wait", Type: "subtree", Tree: "main"} }, "不能递归"},
		{"错误优先级守卫", func(p *Project) { p.Trees[0].Nodes[0].Type = "priority" }, "以 condition 开头"},
		{"无限重试", func(p *Project) { p.Trees[0].Nodes[0].Type = "retry" }, "循环次数"},
		{"时长溢出", func(p *Project) { p.Trees[0].Nodes[1].DurationMS = 9223372036854775807 }, "非负毫秒"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Example()
			tc.edit(&p)
			d := Validate(p)
			raw, _ := json.Marshal(d)
			if !strings.Contains(string(raw), tc.part) {
				t.Fatalf("期望 %s，得到 %s", tc.part, raw)
			}
		})
	}
}

// TestLiteralTypes 验证整型精度、空值和枚举约束不会被 JSON 宽松转换掩盖。
func TestLiteralTypes(t *testing.T) {
	for _, tc := range []struct {
		typ, raw string
		ok       bool
	}{{"int64", "9223372036854775807", true}, {"uint64", "18446744073709551615", true}, {"int64", "1.5", false}, {"uint64", "-1", false}, {"bool", " null ", false}, {"string", "null", false}, {"bool", "1", false}, {"float64", "1e309", false}, {"duration", "12", true}} {
		_, err := Literal(tc.typ, json.RawMessage(tc.raw), nil)
		if (err == nil) != tc.ok {
			t.Errorf("%s(%s): %v", tc.typ, tc.raw, err)
		}
	}
	if _, err := Literal("enum", json.RawMessage(`"missing"`), []string{"ready"}); err == nil {
		t.Fatal("应拒绝未知枚举值")
	}
}

// TestTypedCatalog 验证 Go 声明可导出并严格校验字段绑定类型。
func TestTypedCatalog(t *testing.T) {
	d := []Definition{{ID: "move", Name: "移动", Kind: "action", GoName: "Move", Params: []Parameter{{Name: "Speed", Type: "float64", Default: json.RawMessage("1.5")}}}}
	if _, err := ExportCatalog(d); err != nil {
		t.Fatal(err)
	}
	p := Example()
	p.Catalog = d
	p.Blackboard = []Field{{ID: "speed", Name: "Speed", Type: "int64"}}
	p.Trees[0].Nodes[1] = Node{ID: "wait", Type: "action", Binding: "move", Params: map[string]Value{"Speed": {Field: "speed"}}}
	if len(Validate(p)) == 0 {
		t.Fatal("不同类型字段绑定应被拒绝")
	}
}

// TestGeneratedSymbolConflicts 验证绑定名称不会与生成函数、参数类型或局部变量冲突。
func TestGeneratedSymbolConflicts(t *testing.T) {
	for _, name := range []string{"NewProgram", "btNode12", "init", "true", "f", "phase", "GetEnabled"} {
		t.Run(name, func(t *testing.T) {
			p := Example()
			p.Blackboard = []Field{{ID: "enabled", Name: "Enabled", Type: "bool"}}
			p.Catalog = []Definition{{ID: "a", Name: "a", Kind: "action", GoName: name}}
			if len(Validate(p)) == 0 {
				t.Fatalf("未拒绝冲突名称 %s", name)
			}
		})
	}
	p := Example()
	p.Catalog = []Definition{{ID: "a", Kind: "action", GoName: "Move"}, {ID: "b", Kind: "action", GoName: "MoveParams"}}
	if len(Validate(p)) == 0 {
		t.Fatal("未拒绝参数类型与函数名称冲突")
	}
}
