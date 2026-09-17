package model

import (
	"encoding/json"
	"testing"
	"time"

	bt "github.com/1xxz188/behaviortree"
)

// TestDurationLiteral 验证可读时长、旧纳秒整数和边界精度，并拒绝非法格式与溢出。
func TestDurationLiteral(t *testing.T) {
	for _, tc := range []struct {
		raw  string // 原始 JSON 输入。
		want int64  // 精确纳秒结果。
	}{
		{`"1s"`, int64(time.Second)}, {`"500ms"`, int64(500 * time.Millisecond)},
		{`"2.5s"`, int64(2500 * time.Millisecond)}, {`"1h30m"`, int64(90 * time.Minute)},
		{`"-1s"`, -int64(time.Second)}, {`"0"`, 0}, {`"1\u0073"`, int64(time.Second)},
		{`1000000000`, int64(time.Second)}, {`-1`, -1},
		{`9223372036854775807`, 9223372036854775807}, {`-9223372036854775808`, -9223372036854775808},
		{`"9223372036854775807ns"`, 9223372036854775807},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := Literal(bt.DurationType, json.RawMessage(tc.raw), nil)
			if err != nil || got != tc.want {
				t.Fatalf("时长未保留精确 int64 纳秒: got=%v (%T), want=%d, err=%v", got, got, tc.want, err)
			}
		})
	}
	for _, raw := range []string{`""`, `"1"`, `"1sec"`, `"1 s"`, `"1d"`, `"1e3s"`, `"9223372036854775808ns"`, `9223372036854775808`, `1.5`, `null`, `true`, `{}`, `[]`, `"1s" {}`} {
		if _, err := Literal(bt.DurationType, json.RawMessage(raw), nil); err == nil {
			t.Errorf("应拒绝非法时长 %s", raw)
		}
	}
	// 只扩展 duration，普通整数类型不得随之接受带单位字符串。
	if _, err := Literal(bt.IntType, json.RawMessage(`"1s"`), nil); err == nil {
		t.Fatal("int64 意外接受时长字符串")
	}
}

// TestDurationProjectRoundTrip 验证字段默认值、参数默认值和节点常量均支持时长且保存保留原表示。
func TestDurationProjectRoundTrip(t *testing.T) {
	for _, raw := range []string{`"500ms"`, `500000000`} {
		p := Example()
		p.Blackboard = []Field{{ID: "delay", Name: "Delay", Type: bt.DurationType, Default: json.RawMessage(raw)}}
		p.Catalog = []Definition{{ID: "pause", GoName: "Pause", Kind: DefinitionAction, Params: []Parameter{{Name: "Delay", Type: bt.DurationType, Default: json.RawMessage(raw)}}}}
		p.Trees[0].Nodes = []Node{{ID: "root", Type: NodeAction, Binding: "pause", Params: map[string]Value{"Delay": {Value: json.RawMessage(raw)}}}}
		encoded, err := Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if diagnostics := Validate(decoded); len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
		for _, value := range []json.RawMessage{decoded.Blackboard[0].Default, decoded.Catalog[0].Params[0].Default, decoded.Trees[0].Nodes[0].Params["Delay"].Value} {
			if string(value) != raw {
				t.Fatalf("保存改变时长表示: got=%s, want=%s", value, raw)
			}
		}
		catalog, err := ExportCatalog(decoded.Catalog)
		if err != nil {
			t.Fatal(err)
		}
		var definitions []Definition
		if err := json.Unmarshal(catalog, &definitions); err != nil {
			t.Fatal(err)
		}
		if string(definitions[0].Params[0].Default) != raw {
			t.Fatal("目录导出改变时长表示")
		}
		decoded.Catalog[0].Params[0].Default = json.RawMessage(`"invalid"`)
		if _, err := ExportCatalog(decoded.Catalog); err == nil {
			t.Fatal("目录导出未拒绝非法时长")
		}
	}
}
