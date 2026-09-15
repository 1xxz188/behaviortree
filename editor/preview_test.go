package editor

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// previewResponse 解码实际接口产物用于端到端比较。
type previewResponse struct {
	Source         string                   `json:"source"`         // 返回的实际源码。
	SourceMap      []codegen.SourceLocation `json:"sourceMap"`      // 节点映射。
	Version        string                   `json:"version"`        // 语义版本。
	Path           string                   `json:"path"`           // 写盘路径。
	MatchesCurrent bool                     `json:"matchesCurrent"` // 持久产物与请求工程是否一致。
}

// decodePreview 检查接口成功后解析产物。
func decodePreview(t *testing.T, response *httptest.ResponseRecorder) previewResponse {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("接口失败 %d: %s", response.Code, response.Body.String())
	}
	var result previewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestPreviewGenerateAndReopen 验证无写盘预览、正式生成、重开读取与草稿版本判断完整闭环。
func TestPreviewGenerateAndReopen(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	project := model.Example()
	preview := decodePreview(t, callEditor(t, server, "POST", "/api/preview", project))
	for _, location := range preview.SourceMap {
		if location.FunctionName == "" || !strings.Contains(preview.Source, "func "+location.FunctionName+"(") {
			t.Fatal("预览缺少实际语义函数名", location)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("预览写入了磁盘", err)
	}
	if preview.Path != "" {
		t.Fatal("预览不应声称已经写盘")
	}
	generated := decodePreview(t, callEditor(t, server, "POST", "/api/generate", project))
	if preview.Source != generated.Source || preview.Version != generated.Version || !reflect.DeepEqual(preview.SourceMap, generated.SourceMap) {
		t.Fatal("预览与正式生成不一致")
	}
	if generated.Path != filepath.Join(dir, "generated", project.Generation.Package, "tree_gen.go") {
		t.Fatal("生成路径不正确")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	server, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	loaded := decodePreview(t, callEditor(t, server, "POST", "/api/generated", project))
	if loaded.Source != generated.Source || !loaded.MatchesCurrent || !reflect.DeepEqual(loaded.SourceMap, generated.SourceMap) {
		t.Fatal("重开未读回同一版本")
	}
	project.Trees[0].Layout["root"] = model.Position{X: 77, Y: 88}
	if !decodePreview(t, callEditor(t, server, "POST", "/api/generated", project)).MatchesCurrent {
		t.Fatal("移动节点改变了源码版本")
	}
	project.Trees[0].Root = "unfinished"
	loaded = decodePreview(t, callEditor(t, server, "POST", "/api/generated", project))
	if loaded.MatchesCurrent || loaded.Source != generated.Source {
		t.Fatal("未完成草稿应仍可查看旧源码并标记过期")
	}
	if response := callEditor(t, server, "POST", "/api/preview", project); response.Code != 422 || !strings.Contains(response.Body.String(), "diagnostics") {
		t.Fatal("预览未提供可定位校验问题", response.Body.String())
	}
}

// TestGeneratedRejectsCorruptionAndPaths 验证损坏源码、错误映射、超限文件与越界包名不能被当作有效产物。
func TestGeneratedRejectsCorruptionAndPaths(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	project := model.Example()
	if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 404 {
		t.Fatal("无产物应返回404", response.Code)
	}
	for _, pkg := range []string{"../outside", `a\b`, "C:outside", "_", "for"} {
		invalid := project
		invalid.Generation.Package = pkg
		if response := callEditor(t, server, "POST", "/api/generated", invalid); response.Code != 400 {
			t.Fatal(pkg, response.Code)
		}
	}
	generated := decodePreview(t, callEditor(t, server, "POST", "/api/generate", project))
	mapPath := filepath.Join(filepath.Dir(generated.Path), "tree_gen.map.json")
	original, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hash", "missing-hash", "version", "line", "node", "index", "count", "function", "empty-function", "missing-function", "swapped-functions"} {
		t.Run(name, func(t *testing.T) {
			var mapping generatedMapping
			if err := json.Unmarshal(original, &mapping); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "hash":
				mapping.SourceHash = "wrong"
			case "missing-hash":
				mapping.SourceHash = ""
			case "version":
				mapping.Version = "wrong"
			case "line":
				mapping.Locations[0].Line++
			case "node":
				mapping.Locations[0].NodeID = "other"
			case "index":
				mapping.Locations[0].Index++
			case "count":
				mapping.Locations = mapping.Locations[1:]
			case "function":
				mapping.Locations[0].FunctionName = "NewProgram"
			case "empty-function":
				mapping.Locations[0].FunctionName = ""
			case "swapped-functions":
				// 同时调换函数名和行号，仍必须通过真实 switch 分派检出错联。
				first, second := &mapping.Locations[0], &mapping.Locations[1]
				first.FunctionName, second.FunctionName = second.FunctionName, first.FunctionName
				first.Line, second.Line = second.Line, first.Line
			}
			data, _ := json.Marshal(mapping)
			if name == "missing-function" {
				// 真正删除 JSON 字段，与显式空字符串分别验证必填约束。
				var object map[string]json.RawMessage
				if err := json.Unmarshal(data, &object); err != nil {
					t.Fatal(err)
				}
				var locations []map[string]json.RawMessage
				if err := json.Unmarshal(object["locations"], &locations); err != nil {
					t.Fatal(err)
				}
				delete(locations[0], "functionName")
				object["locations"], _ = json.Marshal(locations)
				data, _ = json.Marshal(object)
			}
			if err := os.WriteFile(mapPath, data, 0644); err != nil {
				t.Fatal(err)
			}
			if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 409 {
				t.Fatal("损坏映射被接受", response.Code)
			}
		})
	}
	if err := os.WriteFile(mapPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated.Path, []byte(generated.Source+"// 修改\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 409 {
		t.Fatal("修改的源码被接受", response.Code)
	}
	file, err := os.OpenFile(generated.Path, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(MaxGeneratedBytes + 1)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 409 {
		t.Fatal("超限源码被接受", response.Code)
	}
}

// TestScaffoldPreservesHandwritten 验证草稿骨架预览接受未完成拓扑且不会覆盖手写业务。
func TestScaffoldPreservesHandwritten(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	handwritten := []byte("package generated\n// 已实现的业务逻辑。\n")
	path := filepath.Join(dir, "actions.go")
	if err := os.WriteFile(path, handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	project := model.Example()
	project.Trees[0].Root = "missing"
	project.Catalog = []model.Definition{{ID: "move", GoName: "Move", Kind: model.DefinitionAction}, {ID: "ready", GoName: "Ready", Kind: model.DefinitionCondition}}
	scaffold := decodePreview(t, callEditor(t, server, "POST", "/api/scaffold", project))
	if !strings.Contains(scaffold.Source, "case bt.Abort:") || !strings.Contains(scaffold.Source, "return false") {
		t.Fatal("骨架缺少生命周期或条件")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, handwritten) {
		t.Fatal("覆盖了手写业务", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("骨架预览生成了额外文件", err)
	}
	project.Catalog[0].GoName = "NewProgram"
	if response := callEditor(t, server, "POST", "/api/scaffold", project); response.Code != 422 {
		t.Fatal("骨架接受保留函数名", response.Code)
	}
}

// TestGeneratedRejectsEscapingSymlink 验证即使产物内容有效，越出工作目录的符号链接也不可读取。
func TestGeneratedRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	project := model.Example()
	result, err := codegen.Generate(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(outside, result); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "generated"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "generated", project.Generation.Package)); err != nil {
		t.Skipf("当前系统不允许创建符号链接：%v", err)
	}
	if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 409 {
		t.Fatal("越界符号链接被接受", response.Code, response.Body.String())
	}
}
