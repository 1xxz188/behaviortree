package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEditorTypeBoundaries 验证所有工程入口拒绝非法和缺失类型，且不会保存或生成文件。
func TestEditorTypeBoundaries(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, target := range []string{"node", "field", "parameter", "definition"} {
		for _, value := range []any{"unknown", "", nil, float64(1), false, "missing"} {
			item := map[string]any{"id": "item", "name": "Item"}
			key := "type"
			if target == "definition" {
				key = "kind"
			}
			if value != "missing" {
				item[key] = value
			}
			p := map[string]any{"schemaVersion": 1, "generation": map[string]any{"packagePath": "behavior", "contextType": "any"}, "trees": []any{map[string]any{"id": "t", "root": "root", "nodes": []any{map[string]any{"id": "root", "type": "wait"}}}}}
			switch target {
			case "node":
				p["trees"] = []any{map[string]any{"id": "t", "nodes": []any{item}}}
			case "field":
				p["blackboard"] = []any{item}
			case "parameter":
				p["catalog"] = []any{map[string]any{"id": "a", "kind": "action", "goName": "Act", "params": []any{item}}}
			case "definition":
				p["catalog"] = []any{item}
			}
			for _, endpoint := range []string{"/api/import", "/api/validate", "/api/generate", "/api/project"} {
				var body any = p
				if endpoint == "/api/project" {
					body = map[string]any{"name": "rejected.json", "project": p}
				}
				if w := callEditor(t, s, "POST", endpoint, body); w.Code != 400 {
					t.Fatalf("%s %s %v: %d %s", endpoint, target, value, w.Code, w.Body.String())
				}
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "invalid.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if w := callEditor(t, s, "GET", "/api/project?name=invalid.json", nil); w.Code != 400 {
				t.Fatalf("加载 %s %v: %d %s", target, value, w.Code, w.Body.String())
			}
			if target == "parameter" || target == "definition" {
				if w := callEditor(t, s, "POST", "/api/catalog", p["catalog"]); w.Code != 400 {
					t.Fatalf("目录 %s %v: %d %s", target, value, w.Code, w.Body.String())
				}
			}
		}
	}
	for _, path := range []string{"rejected.json", "generated"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Fatalf("非法类型写入了 %s: %v", path, err)
		}
	}
}
