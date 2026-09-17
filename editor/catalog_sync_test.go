package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestCatalogParameterSyncRoundTrip 验证前端同步后的数据通过真实保存、重开、校验及生成接口，不再带入旧参数。
func TestCatalogParameterSyncRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Catalog = []model.Definition{{ID: "check", Name: "检查死亡", GoName: "Check", Kind: model.DefinitionCondition, Params: []model.Parameter{{Name: "Target", Type: bt.IntType}, {Name: "en", Type: bt.EntityIDType}}}}
	p.Trees[0].Root = "check"
	p.Trees[0].Nodes = []model.Node{{ID: "check", Type: model.NodeCondition, Binding: "check", Params: map[string]model.Value{"Target": {Value: json.RawMessage(`7`)}, "en": {Value: json.RawMessage(`1`)}}}}
	if w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "sync.json", "project": p}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.Catalog[0].Params = []model.Parameter{{Name: "E", Type: bt.EntityIDType}, {Name: "IsDeath", Type: bt.BoolType}}
	// 后端以本次提交的 Schema 校验，真实残留必须仍能报告未知参数，禁止在校验器内隐藏。
	w := callEditor(t, s, "POST", "/api/validate", p)
	if w.Code != 200 || strings.Count(w.Body.String(), "未知参数") != 2 || strings.Count(w.Body.String(), "未绑定参数") != 2 {
		t.Fatal("未按最新 Schema 区分未知与未绑定", w.Code, w.Body.String())
	}
	if w := callEditor(t, s, "POST", "/api/generate", p); w.Code != 422 {
		t.Fatal("残留参数不能通过生成校验", w.Code, w.Body.String())
	}
	// 模拟前端同步器已移除旧键；新参数尚未配置时只应存在未绑定诊断。
	p.Trees[0].Nodes[0].Params = nil
	w = callEditor(t, s, "POST", "/api/validate", p)
	if w.Code != 200 || strings.Contains(w.Body.String(), "未知参数") || strings.Count(w.Body.String(), "未绑定参数") != 2 {
		t.Fatal("同步后的新参数诊断错误", w.Code, w.Body.String())
	}
	p.Trees[0].Nodes[0].Params = map[string]model.Value{"E": {Value: json.RawMessage(`1`)}, "IsDeath": {Value: json.RawMessage(`true`)}}
	if w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "sync.json", "project": p}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sync.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"Target"`) || strings.Contains(string(raw), `"en"`) {
		t.Fatal("保存 JSON 仍含已删除参数", string(raw))
	}
	w = callEditor(t, s, "GET", "/api/project?name=sync.json", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	loaded, err := model.Decode(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	params := loaded.Trees[0].Nodes[0].Params
	if len(params) != 2 || string(params["E"].Value) != "1" || string(params["IsDeath"].Value) != "true" {
		t.Fatal("重开改变参数绑定", params)
	}
	w = callEditor(t, s, "POST", "/api/validate", loaded)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"diagnostics":[]`) {
		t.Fatal("E=1、IsDeath=true 未通过校验", w.Code, w.Body.String())
	}
	w = callEditor(t, s, "POST", "/api/generate", loaded)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, name := range []string{"glue.gen.go", "tree_main.gen.go"} {
		source, err := os.ReadFile(filepath.Join(dir, "generated", p.Generation.Package, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), "Target") || strings.Contains(string(source), "en:") {
			t.Fatal("生成源码仍含已删除参数", name, string(source))
		}
		if !strings.Contains(string(source), "IsDeath") {
			t.Fatal("生成源码遗漏最新参数", name)
		}
	}
}
