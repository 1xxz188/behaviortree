package editor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestTreeIDBoundaries 验证读取、导入、保存共用 ID 格式及唯一性规则，拒绝时不覆盖已有文件。
func TestTreeIDBoundaries(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"", " ", "main ", "main\n", "中文", "main-tree", "duplicate", "9_Main", "MAIN", "_"} {
		t.Run(id, func(t *testing.T) {
			p := model.Example()
			p.Trees[0].ID = id
			if id == "duplicate" {
				p.Trees = append(p.Trees, p.Trees[0])
			} else if id == "MAIN" {
				second := p.Trees[0]
				second.ID = "main"
				p.Trees = append(p.Trees, second)
			}
			want := 400
			if model.ValidTreeID(id) && id != "duplicate" && id != "MAIN" {
				want = 200
			}
			raw, err := model.Encode(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "input.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			baseline := []byte("已有内容")
			if err := os.WriteFile(filepath.Join(dir, "saved.json"), baseline, 0600); err != nil {
				t.Fatal(err)
			}
			read := callEditor(t, s, "GET", "/api/project?name=input.json", nil)
			imported := callEditor(t, s, "POST", "/api/import", p)
			saved := callEditor(t, s, "POST", "/api/project", map[string]any{"name": "saved.json", "project": p})
			if read.Code != want || imported.Code != want || saved.Code != want {
				t.Fatalf("边界结果不一致: read=%d import=%d save=%d want=%d", read.Code, imported.Code, saved.Code, want)
			}
			if want == 400 {
				persisted, err := os.ReadFile(filepath.Join(dir, "saved.json"))
				if err != nil || !bytes.Equal(persisted, baseline) {
					t.Fatalf("拒绝的保存覆盖了旧文件: %v", err)
				}
			}
		})
	}
}

// TestTreeIdentityRoundTrip 验证名称改动保留源码定位，改号后保存重开及导入仍保留新引用和布局。
func TestTreeIdentityRoundTrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := model.Example()
	p.Trees = append(p.Trees, model.Tree{ID: "caller", Name: p.Trees[0].Name, Root: "call", Nodes: []model.Node{{ID: "call", Type: model.NodeSubtree, Tree: "main"}}})
	w := callEditor(t, s, "POST", "/api/generate", p)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.Trees[0].Name = "新展示名称"
	w = callEditor(t, s, "POST", "/api/generated", p)
	var generated struct {
		MatchesCurrent bool `json:"matchesCurrent"` // 已写盘源码是否仍与当前工程语义一致。
	}
	if err := json.Unmarshal(w.Body.Bytes(), &generated); err != nil || w.Code != 200 || !generated.MatchesCurrent {
		t.Fatalf("改名称使已有产物失效: %s, %v", w.Body.String(), err)
	}
	p.Trees[0].ID = "9_Main"
	p.Trees[1].Nodes[0].Tree = "9_Main"
	w = callEditor(t, s, "POST", "/api/generated", p)
	if err := json.Unmarshal(w.Body.Bytes(), &generated); err != nil || w.Code != 200 || generated.MatchesCurrent {
		t.Fatalf("改 ID 后已有产物未失效: %s, %v", w.Body.String(), err)
	}
	w = callEditor(t, s, "POST", "/api/project", map[string]any{"name": "renamed.json", "project": p})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = callEditor(t, s, "GET", "/api/project?name=renamed.json", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	loaded, err := model.Decode(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	w = callEditor(t, s, "POST", "/api/import", loaded)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	imported, err := model.Decode(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := model.Encode(p)
	got, _ := model.Encode(imported)
	if !bytes.Equal(want, got) {
		t.Fatal("保存重开或导入改变了树名称、ID、引用、顺序或布局")
	}
	w = callEditor(t, s, "POST", "/api/generate", imported)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
