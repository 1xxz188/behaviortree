package editor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestEventDraftSaveBoundary 验证未完成树可保存，但删除事件后的旧引用不能覆盖已有工程。
func TestEventDraftSaveBoundary(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Trees[0].Root = "missing"
	p.EventEnumDescription = "整体说明\n第二行"
	p.Events = []model.EventDefinition{{ID: "1", Name: "命令变化", CodeName: "CommandChanged", Description: "字段说明"}}
	p.NextEventID = "2"
	p.Catalog = []model.Definition{{ID: "check", Name: "检查", Kind: model.DefinitionCondition, GoName: "Check", Params: []model.Parameter{}, EventIDs: []string{p.Events[0].ID}}}
	w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "draft.json", "project": p})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := model.Decode(baseline)
	if err != nil || got.EventEnumDescription != p.EventEnumDescription || got.Events[0].Description != p.Events[0].Description {
		t.Fatalf("草稿事件注释往返失败: %v", err)
	}
	// 模拟旧表单仍持有已删除事件 ID，后端拒绝悬空引用且原文件保持完整。
	stale := p
	stale.Events = []model.EventDefinition{}
	w = callEditor(t, s, "POST", "/api/project", map[string]any{"name": "draft.json", "project": stale})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "eventIds") {
		t.Fatalf("旧引用未拒绝: %d %s", w.Code, w.Body.String())
	}
	after, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil || !bytes.Equal(after, baseline) {
		t.Fatalf("非法草稿覆盖了原文件: %v", err)
	}
}

// TestCatalogExchangeHTTPBoundary 验证 HTTP 目录入口接受自包含对象并拒绝旧数组和未知事件。
func TestCatalogExchangeHTTPBoundary(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Events = []model.EventDefinition{{ID: "1", Name: "命令变化", CodeName: "CommandChanged", Description: "成员说明"}}
	p.NextEventID = "2"
	p.EventEnumDescription = "整体说明"
	p.Catalog = []model.Definition{{ID: "check", Name: "检查", Kind: model.DefinitionCondition, GoName: "Check", Params: []model.Parameter{}, EventIDs: []string{p.Events[0].ID}}}
	data, err := model.ExportCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	w := callEditor(t, s, "POST", "/api/catalog", json.RawMessage(data))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var exchange model.CatalogExchange
	if err := json.Unmarshal(w.Body.Bytes(), &exchange); err != nil || exchange.EventEnumDescription != p.EventEnumDescription || exchange.Events[0].Description != p.Events[0].Description {
		t.Fatalf("目录交换丢失注释: %v %s", err, w.Body.String())
	}
	exchange.Catalog[0].EventIDs = []string{"999"}
	unknown, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []json.RawMessage{json.RawMessage(`[]`), unknown} {
		w = callEditor(t, s, "POST", "/api/catalog", bad)
		if w.Code != 400 {
			t.Fatalf("错误目录被接受: %s", bad)
		}
	}
}
