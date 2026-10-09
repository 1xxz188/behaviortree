package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestDraftNodesSaveReopenAndGenerate 验证草稿完整保存重开，校验、预览与正式生成都返回警告且跳过草稿。
func TestDraftNodesSaveReopenAndGenerate(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	project := model.WithCodeNames(model.Example())
	draft := model.Node{ID: "draft", Name: "尚未绑定的动作", Type: model.NodeAction, CodeName: "ActionDraft", Comment: "开发阶段保留的草稿"}
	position := model.Position{X: -250, Y: 420}
	project.Trees[0].Nodes = append(project.Trees[0].Nodes, draft)
	project.Trees[0].Layout[draft.ID] = position
	if response := callEditor(t, server, "POST", "/api/project", map[string]any{"name": "draft.json", "project": project}); response.Code != 200 {
		t.Fatal("草稿保存失败", response.Code, response.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := model.Decode(saved)
	if err != nil || len(persisted.Trees[0].Nodes) != 4 || !reflect.DeepEqual(persisted.Trees[0].Nodes[3], draft) || persisted.Trees[0].Layout[draft.ID] != position {
		t.Fatal("工程编码或保存丢失了草稿内容和布局", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	server, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	response := callEditor(t, server, "GET", "/api/project?name=draft.json", nil)
	if response.Code != 200 {
		t.Fatal("保存后无法重开草稿", response.Code, response.Body.String())
	}
	reopened, err := model.Decode(response.Body.Bytes())
	if err != nil || !reflect.DeepEqual(reopened, persisted) {
		t.Fatal("重开修改了草稿或布局", err)
	}
	var preview, generated previewResponse
	for _, path := range []string{"/api/validate", "/api/preview", "/api/generate"} {
		t.Run(path, func(t *testing.T) {
			response := callEditor(t, server, "POST", path, reopened)
			if response.Code != 200 {
				t.Fatal("不可达草稿阻断接口", response.Code, response.Body.String())
			}
			var diagnostics struct {
				Diagnostics []model.Diagnostic `json:"diagnostics"` // 本次校验或生成返回的节点诊断。
			}
			if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
				t.Fatal(err)
			}
			if len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].Severity != "warning" || diagnostics.Diagnostics[0].TreeID != "main" || diagnostics.Diagnostics[0].NodeID != draft.ID {
				t.Fatalf("成功接口没有返回唯一可定位草稿警告: %#v", diagnostics.Diagnostics)
			}
			if path == "/api/validate" {
				return
			}
			result := decodePreview(t, response)
			for _, location := range result.SourceMap {
				if location.NodeID == draft.ID {
					t.Fatal("生成映射包含被跳过的草稿", location)
				}
			}
			for _, file := range result.Files {
				if strings.Contains(file.Source, "ActionDraft") || strings.Contains(file.Source, "尚未绑定的动作") {
					t.Fatal("生成文件包含被跳过的草稿", file.Name)
				}
			}
			if path == "/api/preview" {
				preview = result
			} else {
				generated = result
			}
		})
	}
	if generated.Version == "" || preview.Version == "" {
		return
	}
	if preview.Version != generated.Version || !reflect.DeepEqual(preview.Files, generated.Files) || !reflect.DeepEqual(preview.SourceMap, generated.SourceMap) {
		t.Fatal("带草稿的预览与正式生成结果不同")
	}
	// 仅编辑草稿后，磁盘源码仍与当前根可达运行语义匹配。
	reopened.Trees[0].Nodes[3].Name = "调整中的草稿"
	reopened.Trees[0].Nodes[3].Comment = "这段注释不参与运行源码"
	loaded := decodePreview(t, callEditor(t, server, "POST", "/api/generated", reopened))
	if !loaded.MatchesCurrent || loaded.Version != generated.Version || !reflect.DeepEqual(loaded.Files, generated.Files) {
		t.Fatal("草稿编辑错误地使磁盘运行源码过期")
	}
	after, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil || string(after) != string(saved) {
		t.Fatal("生成或预览改写了完整草稿工程", err)
	}
}

// TestDraftNodesReconnectBlocksGeneration 验证草稿接入根后，接口仍严格拒绝未绑定的业务动作。
func TestDraftNodesReconnectBlocksGeneration(t *testing.T) {
	server, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	project := model.Example()
	project.Trees[0].Nodes = append(project.Trees[0].Nodes, model.Node{ID: "draft", Type: model.NodeAction})
	project.Trees[0].Nodes[0].Children = append(project.Trees[0].Nodes[0].Children, "draft")
	for _, path := range []string{"/api/preview", "/api/generate"} {
		response := callEditor(t, server, "POST", path, project)
		if response.Code != 422 {
			t.Fatal("根可达的非法动作未阻断生成", path, response.Code, response.Body.String())
		}
		var result struct {
			Diagnostics []model.Diagnostic `json:"diagnostics"` // 失败响应保留真实的运行节点错误。
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(model.ValidationErrors(result.Diagnostics)) == 0 {
			t.Fatal("失败接口遗漏错误诊断", path, err)
		}
	}
}
