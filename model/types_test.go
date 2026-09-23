package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
)

// TestNodeTypeJSON 覆盖全部节点类型往返、非法输入与失败时不修改目标。
func TestNodeTypeJSON(t *testing.T) {
	for typ := NodeSequence; typ <= NodeSubtree; typ++ {
		raw, err := json.Marshal(typ)
		if err != nil || string(raw) != fmt.Sprintf("%q", typ.String()) {
			t.Fatalf("编码 %v: %s %v", typ, raw, err)
		}
		var decoded NodeType
		if err = json.Unmarshal(raw, &decoded); err != nil || decoded != typ {
			t.Fatalf("往返 %v: %v", typ, err)
		}
	}
	for _, raw := range []string{`"unknown"`, `""`, `null`, `1`, `true`, `{}`} {
		value := NodeWait
		if err := json.Unmarshal([]byte(raw), &value); err == nil || value != NodeWait || !strings.Contains(err.Error(), "type") || !strings.Contains(err.Error(), raw) {
			t.Fatalf("非法输入 %s 改变目标或被接受", raw)
		}
	}
	for _, value := range []NodeType{InvalidNodeType, 255} {
		if value.Valid() || value.String() != "invalid" {
			t.Fatalf("非法类型 %d", value)
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("非法类型被导出")
		}
	}
}

// TestDefinitionKindJSON 验证业务种类独立编码及与内建节点的显式匹配。
func TestDefinitionKindJSON(t *testing.T) {
	for _, kind := range []DefinitionKind{DefinitionAction, DefinitionCondition} {
		raw, err := json.Marshal(kind)
		var decoded DefinitionKind
		if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded != kind {
			t.Fatalf("种类往返失败: %v", kind)
		}
		for node := NodeSequence; node <= NodeSubtree; node++ {
			want := kind == DefinitionAction && node == NodeAction || kind == DefinitionCondition && node == NodeCondition
			if kind.Matches(node) != want {
				t.Fatalf("错误绑定: %v/%v", kind, node)
			}
		}
	}
	for _, raw := range []string{`"sequence"`, `"unknown"`, `""`, `null`, `1`} {
		value := DefinitionAction
		if err := json.Unmarshal([]byte(raw), &value); err == nil || value != DefinitionAction || !strings.Contains(err.Error(), "kind") || !strings.Contains(err.Error(), raw) {
			t.Fatalf("非法种类被接受: %s", raw)
		}
	}
	for _, kind := range []DefinitionKind{InvalidDefinitionKind, 255} {
		if kind.Valid() || kind.String() != "invalid" || kind.Matches(NodeAction) {
			t.Fatal("非法种类有效")
		}
		if _, err := json.Marshal(kind); err == nil {
			t.Fatal("非法种类被导出")
		}
	}
}

// TestDecodeRequiredTypes 验证缺失类型在输入边界被拒绝，而未完成拓扑仍可解码。
func TestDecodeRequiredTypes(t *testing.T) {
	for _, body := range []string{
		`{"schemaVersion":4,"nextEventId":"1","events":[],"blackboard":[{"id":"f"}]}`,
		`{"schemaVersion":4,"nextEventId":"1","events":[],"catalog":[{"id":"a"}]}`,
		`{"schemaVersion":4,"nextEventId":"1","events":[],"catalog":[{"id":"a","kind":"action","params":[{"name":"P"}]}]}`,
		`{"schemaVersion":4,"nextEventId":"1","events":[],"trees":[{"id":"t","nodes":[{"id":"n"}]}]}`,
	} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Fatalf("缺失类型未拒绝: %s", body)
		}
	}
	if _, err := Decode([]byte(`{"schemaVersion":4,"nextEventId":"1","events":[],"trees":[{"id":"t","root":"missing","nodes":[{"id":"n","type":"sequence"}]}]}`)); err != nil {
		t.Fatalf("未完成拓扑不能解码: %v", err)
	}
}

// TestValueTypeModelRoundTrip 验证全部共享值类型和旧 entity 拼写能在工程中往返。
func TestValueTypeModelRoundTrip(t *testing.T) {
	for _, typ := range []bt.ValueType{bt.BoolType, bt.IntType, bt.UIntType, bt.FloatType, bt.StringType, bt.EnumType, bt.EntityIDType, bt.DurationType} {
		p := Example()
		p.Blackboard = []Field{{ID: "value", Name: "Value", Type: typ, Enum: []string{"ready"}}}
		raw, err := Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(raw)
		if err != nil || decoded.Blackboard[0].Type != typ {
			t.Fatalf("值类型往返 %v: %v", typ, err)
		}
	}
}
