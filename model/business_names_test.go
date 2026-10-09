package model

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// businessNamingFixture 构造同时含历史名称与新前缀名称的完整工程，验证两套规则独立持久化。
func businessNamingFixture() Project {
	p := Example()
	p.Catalog = []Definition{
		{ID: "old-move", Name: "历史动作", Kind: DefinitionAction, GoName: "Move", Params: []Parameter{}},
		{ID: "old-ready", Name: "历史条件", Kind: DefinitionCondition, GoName: "Ready", Params: []Parameter{}},
		{ID: "new-move", Name: "新动作", Kind: DefinitionAction, GoName: "ActionMove", NamingVersion: PrefixedBusinessNaming, Params: []Parameter{}},
		{ID: "new-ready", Name: "新条件", Kind: DefinitionCondition, GoName: "IsReady", NamingVersion: PrefixedBusinessNaming, Params: []Parameter{}},
	}
	p.Trees[0].Layout = nil
	p.Trees[0].Nodes = []Node{
		{ID: "root", Type: NodeSequence, CodeName: "Root", Children: []string{"old-move", "old-ready", "new-move", "new-ready"}},
		{ID: "old-move", Type: NodeAction, CodeName: "Patrol", Binding: "old-move"},
		{ID: "old-ready", Type: NodeCondition, CodeName: "CanStart", Binding: "old-ready"},
		{ID: "new-move", Type: NodeAction, CodeName: "ActionMove", NamingVersion: PrefixedBusinessNaming, Binding: "new-move"},
		{ID: "new-ready", Type: NodeCondition, CodeName: "IsReady", NamingVersion: PrefixedBusinessNaming, Binding: "new-ready"},
	}
	return p
}

// TestBusinessNamingMixedRoundTrip 验证工程和目录交换保留历史函数、旧实例名称及新规则标记。
func TestBusinessNamingMixedRoundTrip(t *testing.T) {
	p := businessNamingFixture()
	if diagnostics := Validate(p); len(diagnostics) != 0 {
		t.Fatalf("混合命名工程未通过验证: %+v", diagnostics)
	}
	data, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || !reflect.DeepEqual(decoded, p) {
		t.Fatalf("工程往返修改了业务名称或规则: %v\n%+v", err, decoded)
	}
	if bytes.Count(data, []byte(`"namingVersion": 1`)) != 4 {
		t.Fatal("新规则未持久化，或给历史节点隐式增加了规则")
	}
	exchangeData, err := ExportCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	exchange, err := DecodeCatalogExchange(exchangeData)
	if err != nil || !reflect.DeepEqual(exchange.Catalog, p.Catalog) {
		t.Fatalf("目录交换修改了业务函数名或规则: %v\n%+v", err, exchange.Catalog)
	}
}

// TestBusinessNamingPrefixBoundaries 验证新动作、条件的函数名与实例代码名都要求精确大小写及非空后缀。
func TestBusinessNamingPrefixBoundaries(t *testing.T) {
	cases := []struct {
		name  string         // 被检查的完整名称。
		kind  DefinitionKind // 对应的业务定义种类。
		valid bool           // 此名称是否符合固定前缀规则。
	}{
		{"ActionMove", DefinitionAction, true},
		{"Action_1", DefinitionAction, true},
		{"Move", DefinitionAction, false},
		{"actionMove", DefinitionAction, false},
		{"ACTIONMove", DefinitionAction, false},
		{"IsMove", DefinitionAction, false},
		{"Action", DefinitionAction, false},
		{"IsReady", DefinitionCondition, true},
		{"Is1", DefinitionCondition, true},
		{"Ready", DefinitionCondition, false},
		{"isReady", DefinitionCondition, false},
		{"ISReady", DefinitionCondition, false},
		{"ActionReady", DefinitionCondition, false},
		{"Is", DefinitionCondition, false},
	}
	for _, scope := range []string{"definition", "node"} {
		for _, tc := range cases {
			t.Run(scope+"/"+tc.kind.String()+"/"+tc.name, func(t *testing.T) {
				p := Example()
				goName, nodeType := "ActionCallable", NodeAction
				if tc.kind == DefinitionCondition {
					goName, nodeType = "IsCallable", NodeCondition
				}
				p.Catalog = []Definition{{ID: "callable", Name: "测试业务", Kind: tc.kind, GoName: goName, NamingVersion: PrefixedBusinessNaming, Params: []Parameter{}}}
				p.Trees[0].Nodes = []Node{{ID: "root", Type: nodeType, CodeName: goName, NamingVersion: PrefixedBusinessNaming, Binding: "callable"}}
				field := "codeName"
				if scope == "definition" {
					p.Catalog[0].GoName, field = tc.name, "catalog[0].goName"
				} else {
					p.Trees[0].Nodes[0].CodeName = tc.name
				}
				diagnostics := ValidateBusinessNames(p)
				if tc.valid {
					if len(diagnostics) != 0 {
						t.Fatalf("合法前缀被拒绝: %+v", diagnostics)
					}
				} else if len(diagnostics) != 1 || diagnostics[0].Field != field {
					t.Fatalf("错误前缀未定位: %+v", diagnostics)
				}
				if _, err := Encode(p); (err == nil) != tc.valid {
					t.Fatalf("保存边界与前缀规则不一致: %v", err)
				}
				raw, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Decode(raw); (err == nil) != tc.valid {
					t.Fatalf("读取边界与前缀规则不一致: %v", err)
				}
				if full := Validate(p); (len(full) == 0) != tc.valid {
					t.Fatalf("完整验证与前缀规则不一致: %+v", full)
				}
			})
		}
	}
}

// TestBusinessNamingVersionJSONShape 验证定义和实例均拒绝 null、未知规则及非整数值，显式旧规则仍可读取。
func TestBusinessNamingVersionJSONShape(t *testing.T) {
	for _, scope := range []string{"definition", "node"} {
		p := Example()
		p.Catalog = []Definition{{ID: "move", Name: "移动", Kind: DefinitionAction, GoName: "ActionMove", Params: []Parameter{}}}
		p.Trees[0].Nodes = []Node{{ID: "root", Type: NodeAction, CodeName: "ActionMove", Binding: "move"}}
		if scope == "definition" {
			p.Catalog[0].NamingVersion = PrefixedBusinessNaming
		} else {
			p.Trees[0].Nodes[0].NamingVersion = PrefixedBusinessNaming
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(raw, []byte(`"namingVersion":1`)) != 1 {
			t.Fatal("测试工程必须只启用一个规则标记")
		}
		for _, version := range []string{"0", "1", "null", "2", "-1", "256", `"1"`, "1.5", "true", "{}", "[]"} {
			t.Run(scope+"/"+version, func(t *testing.T) {
				modified := bytes.Replace(raw, []byte(`"namingVersion":1`), []byte(`"namingVersion":`+version), 1)
				_, err := Decode(modified)
				if version == "0" || version == "1" {
					if err != nil {
						t.Fatalf("合法规则版本被拒绝: %v", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "namingVersion") {
					t.Fatalf("错误规则版本未拒绝或未定位: %v", err)
				}
			})
		}
	}
	// 非 JSON 调用也必须拒绝未知版本和错误的节点种类。
	p := businessNamingFixture()
	p.Catalog[2].NamingVersion = BusinessNamingVersion(2)
	p.Trees[0].Nodes[0].NamingVersion = PrefixedBusinessNaming
	if diagnostics := ValidateBusinessNames(p); len(diagnostics) != 2 {
		t.Fatalf("直接 Go 对象绕过版本或适用种类校验: %+v", diagnostics)
	}
	if _, err := Encode(p); err == nil {
		t.Fatal("未知命名规则被保存")
	}
}

// TestBusinessNamingDefaultsAndDraft 验证未绑定、未连线草稿可保存，新默认名使用前缀且不改变历史名和调用方。
func TestBusinessNamingDefaultsAndDraft(t *testing.T) {
	p := Example()
	p.Trees[0].Root = "missing"
	p.Trees[0].Nodes = []Node{
		{ID: "old", Type: NodeCondition, CodeName: "Is1"},
		{ID: "legacyCheck", Type: NodeCondition},
		{ID: "new-check", Type: NodeCondition, NamingVersion: PrefixedBusinessNaming},
		{ID: "new-move", Type: NodeAction, NamingVersion: PrefixedBusinessNaming},
	}
	filled := WithCodeNames(p)
	want := []string{"Is1", "LegacyCheck", "Is2", "Action1"}
	for i, node := range filled.Trees[0].Nodes {
		if node.CodeName != want[i] {
			t.Fatalf("默认名未保留历史值或前缀: %q，期望 %q", node.CodeName, want[i])
		}
	}
	if p.Trees[0].Nodes[2].CodeName != "" {
		t.Fatal("默认分配修改了调用方草稿")
	}
	reordered := p
	reordered.Trees = slices.Clone(p.Trees)
	reordered.Trees[0].Nodes = slices.Clone(p.Trees[0].Nodes)
	slices.Reverse(reordered.Trees[0].Nodes)
	reordered = WithCodeNames(reordered)
	slices.Reverse(reordered.Trees[0].Nodes)
	if !reflect.DeepEqual(filled, reordered) {
		t.Fatal("节点数组重排改变了默认前缀名")
	}
	data, err := Encode(p)
	if err != nil {
		t.Fatalf("未完成草稿不能保存: %v", err)
	}
	decoded, err := Decode(data)
	if err != nil || !reflect.DeepEqual(decoded, filled) {
		t.Fatalf("默认前缀名在草稿往返时漂移: %v", err)
	}
	if len(Validate(decoded)) == 0 {
		t.Fatal("测试必须覆盖尚未完成绑定和拓扑的草稿")
	}
}
