package model

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const testEventID = "1"

// eventProject 构造带稳定事件引用与双层注释的测试工程。
func eventProject() Project {
	p := Example()
	p.EventEnumDescription = "整体第一行\r\n  第二行"
	p.Events = []EventDefinition{{ID: testEventID, Name: " 命令变化 ", CodeName: "CommandChanged", Description: "成员第一行\r第二行"}}
	p.NextEventID = "2"
	p.Catalog = []Definition{{ID: "check", Name: "检查", Kind: DefinitionCondition, GoName: "Check", Params: []Parameter{}, EventIDs: []string{testEventID}}}
	return p
}

// TestEventRoundTrip 验证身份、引用和双层注释往返，且编码不修改调用者。
func TestEventRoundTrip(t *testing.T) {
	p := eventProject()
	original := p.Events[0]
	data, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Events[0] != original || p.EventEnumDescription != "整体第一行\r\n  第二行" {
		t.Fatal("编码修改了输入工程")
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.EventEnumDescription != "整体第一行\n  第二行" || got.Events[0].Description != "成员第一行\n第二行" || got.Events[0].Name != "命令变化" || got.NextEventID != "2" || !reflect.DeepEqual(got.Catalog[0].EventIDs, []string{testEventID}) {
		t.Fatalf("事件往返发生变化: %+v", got)
	}
	got.Events[0].Description = " \t\r\n "
	got.EventEnumDescription = " \n "
	data, err = Encode(got)
	if err != nil || bytes.Contains(data, []byte(`"eventEnumDescription"`)) || bytes.Contains(data, []byte(`"description"`)) {
		t.Fatalf("空注释应统一省略: %v %s", err, data)
	}
}

// TestEventIDAndRegistryRejectInvalid 验证非法格式、重复及未知引用都在保存边界拒绝。
func TestEventIDAndRegistryRejectInvalid(t *testing.T) {
	for _, id := range []string{"", "0", "01", "-1", "+1", "0x1", "1a", "18446744073709551616"} {
		if _, err := ParseEventID(id); err == nil {
			t.Errorf("非法 ID 被接受: %q", id)
		}
	}
	if value, err := ParseEventID("18446744073709551615"); err != nil || value != ^uint64(0) {
		t.Fatalf("uint64 上限事件 ID 应可解析: %v", err)
	}
	for name, edit := range map[string]func(*Project){
		"重复身份": func(p *Project) { p.Events = append(p.Events, p.Events[0]) },
		"大小写重名": func(p *Project) {
			p.Events = append(p.Events, EventDefinition{ID: "3", Name: "其他", CodeName: "COMMANDCHANGED"})
		},
		"未知引用":    func(p *Project) { p.Catalog[0].EventIDs = []string{"3"} },
		"重复引用":    func(p *Project) { p.Catalog[0].EventIDs = []string{testEventID, testEventID} },
		"代码符号冲突":  func(p *Project) { p.Catalog[0].GoName = "EventCommandChanged" },
		"上下文类型冲突": func(p *Project) { p.Generation.ContextType = "EventCommandChanged" },
	} {
		t.Run(name, func(t *testing.T) {
			p := eventProject()
			edit(&p)
			if _, err := Encode(p); err == nil || len(ValidateEvents(p)) == 0 {
				t.Fatal("事件错误未在模型边界拒绝")
			}
		})
	}
}

// TestEventJSONStrictness 验证缺失、null、错误类型与旧事件字段被明确拒绝。
func TestEventJSONStrictness(t *testing.T) {
	data, err := Encode(eventProject())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, old, replacement, field string }{
		{"缺少注册表", `"events": [`, `"other": [`, "events"},
		{"空注册表", `"events": [`, `"events": null, "other": [`, "events"},
		{"整体注释 null", `"eventEnumDescription": "整体第一行\n  第二行"`, `"eventEnumDescription": null`, "eventEnumDescription"},
		{"成员注释 null", `"description": "成员第一行\n第二行"`, `"description": null`, "events[0].description"},
		{"成员注释数字", `"description": "成员第一行\n第二行"`, `"description": 1`, "events[0].description"},
		{"引用 null", `"eventIds": [`, `"eventIds": null, "other": [`, "eventIds"},
		{"游标 null", `"nextEventId": "2"`, `"nextEventId": null`, "nextEventId"},
		{"旧定义事件", `"eventIds": [`, `"events": [`, "events"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := strings.Replace(string(data), tc.old, tc.replacement, 1)
			if bad == string(data) {
				t.Fatal("测试替换未命中")
			}
			if _, err := Decode([]byte(bad)); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("错误定位不符: %v", err)
			}
		})
	}
	if _, err := Decode([]byte(strings.Replace(string(data), `"schemaVersion": 4`, `"schemaVersion": 3`, 1))); err == nil {
		t.Fatal("旧 Schema 被接受")
	}
}

// TestEventNextIDCursor 验证删除后保留游标、越界和耗尽哨兵的边界。
func TestEventNextIDCursor(t *testing.T) {
	for _, tc := range []struct {
		name, id, next string
		valid          bool
	}{
		{"初始空表", "", "1", true},
		{"删除后空表", "", "9", true},
		{"当前最大值", "7", "8", true},
		{"上限已用", "18446744073709551615", MaxNextEventID, true},
		{"缺失游标", "", "", false},
		{"游标为零", "1", "0", false},
		{"前导零", "1", "02", false},
		{"未超出现有值", "7", "7", false},
		{"游标越界", "1", "18446744073709551617", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Example()
			p.NextEventID = tc.next
			if tc.id != "" {
				p.Events = []EventDefinition{{ID: tc.id, Name: "事件", CodeName: "Event"}}
			}
			_, err := Encode(p)
			if (err == nil) != tc.valid {
				t.Fatalf("游标校验不符: %v", err)
			}
		})
	}
}

// TestEventDescriptionLimits 验证注释的 UTF-8 字节限额、NUL 与换行规则。
func TestEventDescriptionLimits(t *testing.T) {
	for _, description := range []string{strings.Repeat("中", 6000), "x\x00y"} {
		p := eventProject()
		p.Events[0].Description = description
		if _, err := Encode(p); err == nil {
			t.Fatal("非法成员注释被接受")
		}
		p = eventProject()
		p.EventEnumDescription = description
		if _, err := Encode(p); err == nil {
			t.Fatal("非法整体注释被接受")
		}
	}
}

// TestCatalogExchange 验证目录只携带被引用事件，并拒绝悬空与旧数组格式。
func TestCatalogExchange(t *testing.T) {
	p := eventProject()
	p.Events = append(p.Events, EventDefinition{ID: "3", Name: "未使用", CodeName: "Unused"})
	p.NextEventID = "4"
	p.Trees[0].Root = "missing" // 未完成画布不阻止目录交换。
	data, err := ExportCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	var exchange CatalogExchange
	if err := json.Unmarshal(data, &exchange); err != nil || len(exchange.Events) != 1 || exchange.Events[0].ID != testEventID || exchange.EventEnumDescription != "整体第一行\n  第二行" {
		t.Fatalf("目录交换缺失或附带未引用事件: %v %+v", err, exchange)
	}
	if _, err := DecodeCatalogExchange(data); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`[]`), []byte(`{"kind":"behaviortree.catalog","schemaVersion":4,"events":[],"catalog":[{"id":"check","kind":"condition","goName":"Check","eventIds":["1"]}]}`)} {
		if _, err := DecodeCatalogExchange(bad); err == nil {
			t.Fatalf("非法交换包被接受: %s", bad)
		}
	}
}

// TestEmptyEventEnumDescription 验证没有成员时整体注释仍随工程和目录交换保存。
func TestEmptyEventEnumDescription(t *testing.T) {
	p := Example()
	p.EventEnumDescription = "无成员时的整体说明"
	data, err := Encode(p)
	if err != nil || !bytes.Contains(data, []byte(`"events": []`)) {
		t.Fatalf("空事件表没有显式数组: %v %s", err, data)
	}
	got, err := Decode(data)
	if err != nil || got.EventEnumDescription != p.EventEnumDescription || len(got.Events) != 0 {
		t.Fatalf("空枚举工程往返丢失整体注释: %v", err)
	}
	catalog, err := ExportCatalog(got)
	if err != nil {
		t.Fatal(err)
	}
	exchange, err := DecodeCatalogExchange(catalog)
	if err != nil || exchange.EventEnumDescription != p.EventEnumDescription || exchange.Events == nil {
		t.Fatalf("空枚举目录往返丢失整体注释: %v %+v", err, exchange)
	}
}
