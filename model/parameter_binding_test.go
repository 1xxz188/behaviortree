package model

import (
	"encoding/json"
	"testing"

	bt "github.com/1xxz188/behaviortree"
)

// TestParameterBindingDiagnostics 区分已删除的未知参数、缺失来源和重复来源，保留默认值的既有语义。
func TestParameterBindingDiagnostics(t *testing.T) {
	cases := []struct {
		name       string           // 子用例名称。
		params     map[string]Value // 节点保存的参数绑定。
		defaultRaw json.RawMessage  // 定义默认值，仅在参数键缺失时生效。
		want       string           // 期望诊断，空串表示通过。
	}{
		{name: "缺少参数", want: "未绑定参数：请选择黑板字段或常量"},
		{name: "空绑定", params: map[string]Value{"E": {}}, want: "未绑定参数：请选择黑板字段或常量"},
		{name: "空字段", params: map[string]Value{"E": {Field: ""}}, want: "未绑定参数：请选择黑板字段或常量"},
		{name: "缺少参数使用默认", defaultRaw: json.RawMessage(`1`)},
		{name: "显式空绑定不隐式采用默认", params: map[string]Value{"E": {}}, defaultRaw: json.RawMessage(`1`), want: "未绑定参数：请选择黑板字段或常量"},
		{name: "常量", params: map[string]Value{"E": {Value: json.RawMessage(`1`)}}},
		{name: "字段", params: map[string]Value{"E": {Field: "entity"}}},
		{name: "来源冲突", params: map[string]Value{"E": {Field: "entity", Value: json.RawMessage(`1`)}}, want: "参数不能同时配置黑板字段和常量"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Example()
			p.Blackboard = []Field{{ID: "entity", Name: "Entity", Type: bt.EntityIDType}}
			p.Catalog = []Definition{{ID: "check", GoName: "Check", Kind: DefinitionCondition, Params: []Parameter{{Name: "E", Type: bt.EntityIDType, Default: tc.defaultRaw}}}}
			p.Trees[0].Root = "check"
			p.Trees[0].Nodes = []Node{{ID: "check", Type: NodeCondition, Binding: "check", Params: tc.params}}
			diagnostics := Validate(p)
			if tc.want == "" {
				if len(diagnostics) != 0 {
					t.Fatalf("有效绑定被拒绝：%+v", diagnostics)
				}
				return
			}
			if len(diagnostics) != 1 || diagnostics[0].Field != "params.E" || diagnostics[0].Message != tc.want {
				t.Fatalf("参数诊断不符：%+v，期望 %q", diagnostics, tc.want)
			}
		})
	}
}

// TestParameterUnknownRemainsVisible 确保未知参数不会被校验器隐藏，且当前定义未绑定参数得到独立定位。
func TestParameterUnknownRemainsVisible(t *testing.T) {
	p := Example()
	p.Catalog = []Definition{{ID: "check", GoName: "Check", Kind: DefinitionCondition, Params: []Parameter{{Name: "E", Type: bt.EntityIDType}, {Name: "IsDeath", Type: bt.BoolType}}}}
	p.Trees[0].Root = "check"
	p.Trees[0].Nodes = []Node{{ID: "check", Type: NodeCondition, Binding: "check", Params: map[string]Value{"Target": {Value: json.RawMessage(`7`)}, "en": {Value: json.RawMessage(`1`)}}}}
	diagnostics := Validate(p)
	want := map[string]string{"params.Target": "未知参数", "params.en": "未知参数", "params.E": "未绑定参数：请选择黑板字段或常量", "params.IsDeath": "未绑定参数：请选择黑板字段或常量"}
	if len(diagnostics) != len(want) {
		t.Fatalf("诊断数量不符：%+v", diagnostics)
	}
	for _, d := range diagnostics {
		if want[d.Field] != d.Message {
			t.Fatalf("未知参数与未绑定参数混淆：%+v", d)
		}
	}
}
