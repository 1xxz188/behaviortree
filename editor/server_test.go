package editor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// TestCatalogOrganizationBoundaries 验证分类保存重开、导入和非法保存的原子拒绝行为。
func TestCatalogOrganizationBoundaries(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Catalog = []model.Definition{{ID: "move", Name: "移动", Kind: model.DefinitionAction, GoName: "Move", Params: []model.Parameter{}}}
	p.CatalogOrganization = &model.CatalogOrganization{Folders: []model.CatalogFolder{{ID: "folder", Name: "移动"}}, Tags: []model.CatalogTag{{ID: "tag", Name: "常用"}}, Assignments: map[string]model.CatalogAssignment{"move": {FolderID: "folder", TagIDs: []string{"tag"}, Order: 3}}}
	w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "organized.json", "project": p})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "organized.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"read", "import"} {
		if endpoint == "read" {
			w = callEditor(t, s, "GET", "/api/project?name=organized.json", nil)
		} else {
			w = callEditor(t, s, "POST", "/api/import", p)
		}
		loaded, err := model.Decode(w.Body.Bytes())
		if err != nil || w.Code != 200 || loaded.CatalogOrganization.Assignments["move"].FolderID != "folder" {
			t.Fatalf("分类往返失败: %s %v", w.Body.String(), err)
		}
	}
	// 不合法目录引用和旧格式都不能覆盖已有文件。
	for _, oldVersion := range []bool{false, true} {
		invalid := p
		if oldVersion {
			invalid.SchemaVersion = 1
		} else {
			invalid.CatalogOrganization = &model.CatalogOrganization{Folders: []model.CatalogFolder{{ID: "bad", Name: "非法", ParentID: "missing"}}}
		}
		if err := normalizeDraft(&invalid); err == nil {
			t.Fatal("草稿边界未拒绝非法分类或版本")
		}
		w = callEditor(t, s, "POST", "/api/import", invalid)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		w = callEditor(t, s, "POST", "/api/project", map[string]any{"name": "organized.json", "project": invalid})
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		after, err := os.ReadFile(filepath.Join(dir, "organized.json"))
		if err != nil || !bytes.Equal(baseline, after) {
			t.Fatal("失败保存覆盖了原工程")
		}
	}
}

// TestCatalogImportRequiresObjectAndRoundTrips 验证目录对象保持精度并拒绝旧数组载体。
func TestCatalogImportRequiresObjectAndRoundTrips(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	definitions := []model.Definition{{ID: "move", Name: "移动", Kind: model.DefinitionAction, GoName: "Move", Params: []model.Parameter{{Name: "Target", Type: bt.UIntType, Default: json.RawMessage("18446744073709551615")}}}}
	p := model.Example()
	p.Catalog = definitions
	raw, err := model.ExportCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	w := callEditor(t, s, "POST", "/api/catalog", json.RawMessage(raw))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "18446744073709551615") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, input := range []any{nil, model.Example(), definitions[0], json.RawMessage("null"), json.RawMessage(`[{"id":"move","name":"移动","kind":"action","goName":"Move","params":[]},{"id":"move","name":"重复","kind":"action","goName":"Other","params":[]}]`)} {
		w = callEditor(t, s, "POST", "/api/catalog", input)
		if w.Code != 400 {
			t.Fatal("应拒绝旧数组或错误目录对象", w.Code, w.Body.String())
		}
	}
}

// callEditor 模拟浏览器从同源发起的本地 JSON 请求。
func callEditor(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:8791"+path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// TestEditorRoundTrip 验证保存、重开、校验、生成及内嵌资源完整闭环。
func TestEditorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Blackboard = []model.Field{{ID: "target", Name: "TargetID", Type: bt.UIntType, Default: json.RawMessage("18446744073709551615")}}
	w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "patrol.json", "project": p})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = callEditor(t, s, "GET", "/api/project?name=patrol.json", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "18446744073709551615") {
		t.Fatal(w.Code, w.Body.String())
	}
	loaded, err := model.Decode(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Trees[0].Root != p.Trees[0].Root || loaded.Trees[0].Layout["root"] != p.Trees[0].Layout["root"] {
		t.Fatal("导入导出改变树身份或布局")
	}
	w = callEditor(t, s, "POST", "/api/validate", loaded)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"diagnostics":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = callEditor(t, s, "POST", "/api/generate", loaded)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	generated, err := os.ReadFile(filepath.Join(dir, p.Generation.PackagePath, "glue.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "18446744073709551615") || !strings.Contains(string(generated), "func btStep") {
		t.Fatal("未生成实际控制流或整数精度丢失")
	}
	w = callEditor(t, s, "GET", "/", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/index-") {
		t.Fatal("发行包前端资源缺失", w.Code)
	}
	w = callEditor(t, s, "GET", "/index.html", nil)
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "./" {
		t.Fatal("首页未规范重定向", w.Code)
	}
	w = callEditor(t, s, "GET", "/api/projects", nil)
	if !strings.Contains(w.Body.String(), "patrol.json") {
		t.Fatal(w.Body.String())
	}
}

// TestEditorDraftAndProtection 验证草稿可保存，非法结构不可生成，手写实现不会被覆盖。
func TestEditorDraftAndProtection(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Trees[0].Root = "missing"
	if w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "draft.json", "project": p}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := callEditor(t, s, "POST", "/api/generate", p); w.Code != 422 || !strings.Contains(w.Body.String(), "diagnostics") {
		t.Fatal(w.Code, w.Body.String())
	}
	p = model.Example()
	generatedDir := filepath.Join(dir, p.Generation.PackagePath)
	if err = os.MkdirAll(generatedDir, 0755); err != nil {
		t.Fatal(err)
	}
	handwritten := []byte("package behavior\n// 手写逻辑不能被自动生成覆盖。\n")
	file := filepath.Join(generatedDir, "glue.gen.go")
	if err = os.WriteFile(file, handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	if w := callEditor(t, s, "POST", "/api/generate", p); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := os.ReadFile(file)
	if !bytes.Equal(got, handwritten) {
		t.Fatal("覆盖了手写文件")
	}
	result, err := codegen.Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteGenerated(generatedDir, result); err == nil {
		t.Fatal("CLI未保护手写文件")
	}
}

// TestEditorRejectsExternalWrites 验证跨站、越界路径与未知接口不会修改本地工程。
func TestEditorRejectsExternalWrites(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"../outside.json", "a/b.json", `a\b.json`, "C:outside.json", ".hidden.json"} {
		w := callEditor(t, s, "POST", "/api/project", map[string]any{"name": name, "project": model.Example()})
		if w.Code != 400 {
			t.Fatal(name, w.Code)
		}
	}
	for _, url := range []string{"http://evil.example/api/projects", "http://192.168.1.1/api/projects"} {
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(url, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8791/api/generate", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = callEditor(t, s, "GET", "/api/missing", nil); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
	for _, addr := range []string{"0.0.0.0:8791", ":8791", "example.com:8791"} {
		if ValidListenAddr(addr) {
			t.Fatal(addr)
		}
	}
}

// TestEditorStrictImport 验证未知字段、拼接 JSON 和空工程不会破坏当前编辑状态。
func TestEditorStrictImport(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, raw := range []string{`{"schemaVersion":2,"unknown":true}`, `{} {}`, `{"schemaVersion":2,"trees":[]}`} {
		r := httptest.NewRequest("POST", "http://localhost:8791/api/import", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(raw, w.Code)
		}
	}
}
