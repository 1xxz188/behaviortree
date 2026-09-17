package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestCodeNamesPersistAcrossProjectBoundaries 验证默认代码名分配后保存重开，显示名编辑不会改变代码名或 UUID。
func TestCodeNamesPersistAcrossProjectBoundaries(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	project := model.Example()
	nodeID := "node_0123456789abcdef0123456789abcdef"
	project.Trees[0].Root = nodeID
	project.Trees[0].Nodes = []model.Node{{ID: nodeID, Type: model.NodeWait}}
	// 直接 JSON 编码模拟使用默认代码名分配的工程。
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	loadedResponse := callEditor(t, server, "GET", "/api/project?name=old.json", nil)
	if loadedResponse.Code != 200 {
		t.Fatal(loadedResponse.Body.String())
	}
	var loaded model.Project
	if err := json.Unmarshal(loadedResponse.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	node := &loaded.Trees[0].Nodes[0]
	if node.CodeName != "Wait1" || node.ID != nodeID {
		t.Fatalf("默认代码名错误: %+v", node)
	}
	node.CodeName, node.Name = "WaitMove", "等待移动完成"
	response := callEditor(t, server, "POST", "/api/project", map[string]any{"name": "named.json", "project": loaded})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(dir, "named.json"))
	if err != nil || !strings.Contains(string(saved), `"codeName": "WaitMove"`) {
		t.Fatalf("代码名未落盘: %s %v", saved, err)
	}
	preview := decodePreview(t, callEditor(t, server, "POST", "/api/preview", loaded))
	if preview.Files[1].Name != "tree_main.gen.go" || !strings.Contains(preview.Files[1].Source, "func btNodeMainWaitMove(") {
		t.Fatalf("生成名称仍不直观: %+v", preview.Files)
	}
	node.Name = "新的显示名称"
	if model.WithCodeNames(loaded).Trees[0].Nodes[0].CodeName != "WaitMove" {
		t.Fatal("显示名改变了代码名")
	}
	response = callEditor(t, server, "GET", "/api/project?name=named.json", nil)
	var reopened model.Project
	if err := json.Unmarshal(response.Body.Bytes(), &reopened); err != nil || reopened.Trees[0].Nodes[0].CodeName != "WaitMove" || reopened.Trees[0].Nodes[0].ID != nodeID {
		t.Fatalf("重开改变稳定身份: %v", err)
	}
}

// TestCodeNameBoundaryRejectsInvalidNames 验证导入和保存拒绝非法、关键字及同树重复代码名。
func TestCodeNameBoundaryRejectsInvalidNames(t *testing.T) {
	server, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, name := range []string{"1Bad", "中文", "for", "Root", strings.Repeat("A", 41)} {
		t.Run(name, func(t *testing.T) {
			project := model.WithCodeNames(model.Example())
			project.Trees[0].Nodes[1].CodeName = name
			for _, path := range []string{"/api/import", "/api/project"} {
				var body any = project
				if path == "/api/project" {
					body = map[string]any{"name": "bad.json", "project": project}
				}
				if response := callEditor(t, server, "POST", path, body); response.Code != 400 {
					t.Fatalf("接受非法代码名 %q: %s", name, response.Body.String())
				}
			}
		})
	}
}
