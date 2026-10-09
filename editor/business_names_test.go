package editor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// businessNamingDraft 构造未完成拓扑的混合命名草稿，用于检验 HTTP 输入和保存边界。
func businessNamingDraft() model.Project {
	p := model.Example()
	p.Catalog = []model.Definition{
		{ID: "old-ready", Name: "历史条件", Kind: model.DefinitionCondition, GoName: "Ready", Params: []model.Parameter{}},
		{ID: "new-move", Name: "新动作", Kind: model.DefinitionAction, GoName: "ActionMove", NamingVersion: model.PrefixedBusinessNaming, Params: []model.Parameter{}},
	}
	p.Trees[0].Root, p.Trees[0].Layout = "missing", nil
	p.Trees[0].Nodes = []model.Node{
		{ID: "old-ready", Type: model.NodeCondition, CodeName: "CanStart", Binding: "old-ready"},
		{ID: "new-move", Type: model.NodeAction, CodeName: "ActionMove", NamingVersion: model.PrefixedBusinessNaming, Binding: "new-move"},
	}
	return p
}

// TestBusinessNamingHTTPDraftRoundTrip 验证 HTTP 保存、重开、工程导入及目录交换保留新旧命名与标记。
func TestBusinessNamingHTTPDraftRoundTrip(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	p := businessNamingDraft()
	response := callEditor(t, server, "POST", "/api/project", map[string]any{"name": "draft.json", "project": p})
	if response.Code != 200 {
		t.Fatalf("混合命名草稿保存失败: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/project?name=draft.json", "/api/import"} {
		method, body := "GET", any(nil)
		if path == "/api/import" {
			method, body = "POST", p
		}
		response = callEditor(t, server, method, path, body)
		var decoded model.Project
		if response.Code != 200 {
			t.Fatalf("工程边界拒绝草稿: %s %d %s", path, response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || !reflect.DeepEqual(decoded, p) {
			t.Fatalf("工程边界修改了名称、身份或命名规则: %s %v %+v", path, err, decoded)
		}
	}
	data, err := model.ExportCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	response = callEditor(t, server, "POST", "/api/catalog", json.RawMessage(data))
	var exchange model.CatalogExchange
	if response.Code != 200 {
		t.Fatalf("目录交换失败: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &exchange); err != nil || !reflect.DeepEqual(exchange.Catalog, p.Catalog) {
		t.Fatalf("目录交换修改了函数名或规则: %v %+v", err, exchange.Catalog)
	}
	data, err = os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil || bytes.Count(data, []byte(`"namingVersion": 1`)) != 2 {
		t.Fatalf("新标记未落盘，或历史节点被隐式迁移: %v %s", err, data)
	}
}

// TestBusinessNamingHTTPRejectsInvalidPrefixes 验证错误新名称和未知版本不能保存、导入、预览或进入目录交换。
func TestBusinessNamingHTTPRejectsInvalidPrefixes(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	response := callEditor(t, server, "POST", "/api/project", map[string]any{"name": "draft.json", "project": businessNamingDraft()})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string               // 用中文说明本次非法输入的功能场景。
		change  func(*model.Project) // 仅修改当前测试独立工程的命名字段。
		catalog bool                 // 此变更是否也影响目录交换内容。
	}{
		{"动作函数缺前缀", func(p *model.Project) { p.Catalog[1].GoName = "Move" }, true},
		{"动作函数前缀大小写错误", func(p *model.Project) { p.Catalog[1].GoName = "actionMove" }, true},
		{"动作函数缺少后缀", func(p *model.Project) { p.Catalog[1].GoName = "Action" }, true},
		{"动作实例缺前缀", func(p *model.Project) { p.Trees[0].Nodes[1].CodeName = "Move" }, false},
		{"动作实例前缀大小写错误", func(p *model.Project) { p.Trees[0].Nodes[1].CodeName = "actionMove" }, false},
		{"条件函数前缀错误", func(p *model.Project) { p.Catalog[0].NamingVersion = model.PrefixedBusinessNaming }, true},
		{"条件实例前缀错误", func(p *model.Project) { p.Trees[0].Nodes[0].NamingVersion = model.PrefixedBusinessNaming }, false},
		{"未知函数规则版本", func(p *model.Project) { p.Catalog[1].NamingVersion = model.BusinessNamingVersion(2) }, true},
		{"未知实例规则版本", func(p *model.Project) { p.Trees[0].Nodes[1].NamingVersion = model.BusinessNamingVersion(2) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := businessNamingDraft()
			tc.change(&p)
			for _, path := range []string{"/api/project", "/api/import", "/api/preview", "/api/generate"} {
				var body any = p
				if path == "/api/project" {
					body = map[string]any{"name": "draft.json", "project": p}
				}
				response := callEditor(t, server, "POST", path, body)
				if response.Code != 400 || !strings.Contains(response.Body.String(), "error") {
					t.Fatalf("HTTP 边界接受错误命名: %s %d %s", path, response.Code, response.Body.String())
				}
			}
			if tc.catalog {
				exchange := model.CatalogExchange{Kind: "behaviortree.catalog", SchemaVersion: model.SchemaVersion, Events: p.Events, Catalog: p.Catalog}
				response := callEditor(t, server, "POST", "/api/catalog", exchange)
				if response.Code != 400 {
					t.Fatalf("目录交换接受错误函数名称或版本: %d %s", response.Code, response.Body.String())
				}
			}
		})
	}
	after, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil || !bytes.Equal(after, baseline) {
		t.Fatalf("非法请求覆盖了已有草稿: %v", err)
	}
}

// TestBusinessNamingHTTPRejectsNullVersion 验证显式 null 不能将新规则退回旧规则，包括目录交换入口。
func TestBusinessNamingHTTPRejectsNullVersion(t *testing.T) {
	server, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	p := businessNamingDraft()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte(`"namingVersion":1`), []byte(`"namingVersion":null`))
	for _, path := range []string{"/api/import", "/api/project"} {
		var body any = json.RawMessage(data)
		if path == "/api/project" {
			body = map[string]any{"name": "bad.json", "project": json.RawMessage(data)}
		}
		response := callEditor(t, server, "POST", path, body)
		if response.Code != 400 || !strings.Contains(response.Body.String(), "namingVersion") {
			t.Fatalf("null 规则未拒绝: %s %d %s", path, response.Code, response.Body.String())
		}
	}
	exchange := model.CatalogExchange{Kind: "behaviortree.catalog", SchemaVersion: model.SchemaVersion, Events: p.Events, Catalog: p.Catalog}
	data, err = json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte(`"namingVersion":1`), []byte(`"namingVersion":null`))
	response := callEditor(t, server, "POST", "/api/catalog", json.RawMessage(data))
	if response.Code != 400 || !strings.Contains(response.Body.String(), "namingVersion") {
		t.Fatalf("目录交换接受 null 规则: %d %s", response.Code, response.Body.String())
	}
}
