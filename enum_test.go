package behaviortree

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestValueTypeJSON 验证所有字段类型保留字符串协议，并严格拒绝无效输入及枚举值。
func TestValueTypeJSON(t *testing.T) {
	cases := []struct {
		value ValueType // value 是内部字段类型。
		name  string    // name 是持久化协议的规范名称。
	}{
		{BoolType, "bool"}, {IntType, "int64"}, {UIntType, "uint64"}, {FloatType, "float64"},
		{StringType, "string"}, {EnumType, "enum"}, {EntityIDType, "entity"}, {DurationType, "duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseValueType(tc.name)
			if err != nil || parsed != tc.value || !tc.value.Valid() || tc.value.String() != tc.name {
				t.Fatalf("invalid type mapping: parsed=%v err=%v", parsed, err)
			}
			data, err := json.Marshal(tc.value)
			if err != nil || string(data) != strconv.Quote(tc.name) {
				t.Fatalf("unexpected JSON: %s err=%v", data, err)
			}
			var decoded ValueType
			if err := json.Unmarshal(data, &decoded); err != nil || decoded != tc.value {
				t.Fatalf("type round trip failed: %v err=%v", decoded, err)
			}
		})
	}
	for _, input := range []string{`""`, `null`, `0`, `1`, `255`, `true`, `{}`, `[]`, `"entityID"`, `"Bool"`, `"unknown"`, `" bool "`, `"invalid"`} {
		value := IntType
		err := json.Unmarshal([]byte(input), &value)
		if err == nil || value != IntType {
			t.Errorf("invalid JSON %s accepted or changed original value to %v", input, value)
		} else if !strings.Contains(err.Error(), "type: invalid value "+input) {
			t.Errorf("error lost field or original JSON: %v", err)
		}
	}
	for _, input := range []string{"", "entityID", "Bool", "unknown", " bool ", "invalid"} {
		if value, err := ParseValueType(input); err == nil || value != InvalidValueType {
			t.Errorf("invalid name %q accepted: %v err=%v", input, value, err)
		}
	}
	for _, value := range []ValueType{InvalidValueType, DurationType + 1, 255} {
		if value.Valid() || value.String() != "invalid" {
			t.Errorf("invalid numeric type %d recognized as valid", value)
		}
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid numeric type %d serialized", value)
		}
	}
	var escaped ValueType
	if err := json.Unmarshal([]byte(` "\u0062ool" `), &escaped); err != nil || escaped != BoolType {
		t.Fatalf("valid JSON string escaping rejected: %v err=%v", escaped, err)
	}
}

// TestLogKindJSON 验证结构化日志种类按原名称往返，数字、空值和未知值不会进入枚举。
func TestLogKindJSON(t *testing.T) {
	cases := []struct {
		value LogKind // value 是内部日志种类。
		name  string  // name 是外部日志接收者看到的名称。
	}{
		{LogPublish, "publish"}, {LogSwitch, "switch"}, {LogError, "error"}, {LogNode, "node"}, {LogAbort, "abort"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseLogKind(tc.name)
			if err != nil || parsed != tc.value || !tc.value.Valid() || tc.value.String() != tc.name {
				t.Fatalf("invalid log kind mapping: parsed=%v err=%v", parsed, err)
			}
			data, err := json.Marshal(LogRecord{Kind: tc.value})
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil || string(fields["Kind"]) != strconv.Quote(tc.name) {
				t.Fatalf("log kind is not a canonical string: %s err=%v", data, err)
			}
			var decoded LogRecord
			if err := json.Unmarshal(data, &decoded); err != nil || decoded.Kind != tc.value {
				t.Fatalf("log kind round trip failed: %v err=%v", decoded.Kind, err)
			}
		})
	}
	for _, input := range []string{`""`, `null`, `0`, `1`, `255`, `true`, `{}`, `[]`, `"Error"`, `"unknown"`, `" error "`, `"invalid"`} {
		value := LogPublish
		err := json.Unmarshal([]byte(input), &value)
		if err == nil || value != LogPublish {
			t.Errorf("invalid JSON %s accepted or changed original value to %v", input, value)
		} else if !strings.Contains(err.Error(), "kind: invalid value "+input) {
			t.Errorf("error lost field or original JSON: %v", err)
		}
	}
	for _, input := range []string{"", "Error", "unknown", " error ", "invalid"} {
		if value, err := ParseLogKind(input); err == nil || value != InvalidLogKind {
			t.Errorf("invalid name %q accepted: %v err=%v", input, value, err)
		}
	}
	for _, value := range []LogKind{InvalidLogKind, LogAbort + 1, 255} {
		if value.Valid() || value.String() != "invalid" {
			t.Errorf("invalid numeric kind %d recognized as valid", value)
		}
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid numeric kind %d serialized", value)
		}
	}
}

// TestProgramRejectsInvalidValueType 验证直接用 Go 构造的非法字段类型也会在创建和发布边界被拒绝。
func TestProgramRejectsInvalidValueType(t *testing.T) {
	for _, value := range []ValueType{InvalidValueType, DurationType + 1, 255} {
		p := benchmarkProgram(2, false, true)
		p.Fields = []Field{{ID: "field", Name: "Field", Type: value}}
		if err := ValidateProgram(p); err == nil {
			t.Errorf("invalid numeric type %d passed program validation", value)
		}
	}
}
