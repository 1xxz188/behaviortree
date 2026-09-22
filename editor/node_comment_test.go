package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestNodeCommentProjectBoundaries 验证注释通过导入、保存、重开和清空完整链路，磁盘不保留空字段。
func TestNodeCommentProjectBoundaries(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	project := model.Example()
	for _, comment := range []string{"中文注释\n再次打开仍保留 \"引号\"", ""} {
		project.Trees[0].Nodes[0].Comment = comment
		response := callEditor(t, server, "POST", "/api/import", project)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var imported model.Project
		if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil || imported.Trees[0].Nodes[0].Comment != comment {
			t.Fatalf("导入丢失节点注释：%v", err)
		}
		response = callEditor(t, server, "POST", "/api/project", map[string]any{"name": "comments.json", "project": imported})
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(dir, "comments.json"))
		if err != nil || strings.Contains(string(raw), `"comment"`) != (comment != "") {
			t.Fatalf("注释落盘错误：%s，%v", raw, err)
		}
		response = callEditor(t, server, "GET", "/api/project?name=comments.json", nil)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var reopened model.Project
		if err := json.Unmarshal(response.Body.Bytes(), &reopened); err != nil || reopened.Trees[0].Nodes[0].Comment != comment {
			t.Fatalf("重开丢失节点注释：%v", err)
		}
	}
}
