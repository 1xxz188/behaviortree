package editor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// TestGeneratedCaseOnlyTreeRename 验证只修改树 ID 大小写后，新文件在 Windows 上不会被旧文件清理误删。
func TestGeneratedCaseOnlyTreeRename(t *testing.T) {
	dir := t.TempDir()
	project := model.Example()
	if err := WriteGenerated(dir, generatedForTest(t, project)); err != nil {
		t.Fatal(err)
	}
	project.Trees[0].ID = "MAIN"
	result := generatedForTest(t, project)
	if err := WriteGenerated(dir, result); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, _, err := readGeneratedFiles(root, ".", "generated"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tree_MAIN.gen.go"))
	if err != nil || !bytes.Equal(data, result.Files[1].Source) {
		t.Fatalf("改号丢失新文件: %v", err)
	}
}

// generatedForTest 使用正式生成器准备写盘测试，避免手工模拟源码格式。
func generatedForTest(t *testing.T, project model.Project) codegen.Result {
	t.Helper()
	result, err := codegen.Generate(project)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestWriteGeneratedStableFiles 验证重复生成和新增无关树均保留原树内容与修改时间。
func TestWriteGeneratedStableFiles(t *testing.T) {
	dir := t.TempDir()
	project := model.Example()
	result := generatedForTest(t, project)
	if err := WriteGenerated(dir, result); err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(1700000000, 0)
	for _, file := range result.Files {
		if err := os.Chtimes(filepath.Join(dir, file.Name), fixed, fixed); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteGenerated(dir, result); err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		info, err := os.Stat(filepath.Join(dir, file.Name))
		if err != nil || !info.ModTime().Equal(fixed) {
			t.Fatalf("未变化文件被重写 %s: %v", file.Name, err)
		}
	}
	project.Trees = append(project.Trees, model.Tree{ID: "AAA", Name: "无关树", Root: "new", Nodes: []model.Node{{ID: "new", Type: model.NodeWait}}})
	changed := generatedForTest(t, project)
	if err := WriteGenerated(dir, changed); err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files[1:] {
		actual, err := os.ReadFile(filepath.Join(dir, file.Name))
		if err != nil || !bytes.Equal(actual, file.Source) {
			t.Fatalf("无关树文件发生变化 %s", file.Name)
		}
		info, err := os.Stat(filepath.Join(dir, file.Name))
		if err != nil || !info.ModTime().Equal(fixed) {
			t.Fatalf("无关树文件时间变化 %s", file.Name)
		}
	}
}

// TestWriteGeneratedPreflightAndCleanup 验证后置目标冲突不会提前覆盖 glue，且只清理清单中的生成文件。
func TestWriteGeneratedPreflightAndCleanup(t *testing.T) {
	dir := t.TempDir()
	project := model.Example()
	original := generatedForTest(t, project)
	if err := WriteGenerated(dir, original); err != nil {
		t.Fatal(err)
	}
	project.Trees = append(project.Trees, model.Tree{ID: "unused", Name: "独立树", Root: "new", Nodes: []model.Node{{ID: "new", Type: model.NodeWait}}})
	changed := generatedForTest(t, project)
	var extra codegen.GeneratedFile
	for _, file := range changed.Files {
		if file.TreeID == "unused" {
			extra = file
		}
	}
	handwritten := []byte("package generated\n// 手写业务。\n")
	if err := os.WriteFile(filepath.Join(dir, extra.Name), handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(dir, changed); err == nil {
		t.Fatal("未拒绝后置文件冲突")
	}
	glue, err := os.ReadFile(filepath.Join(dir, "glue.gen.go"))
	if err != nil || !bytes.Equal(glue, original.Files[0].Source) {
		t.Fatal("预检完成前修改了 glue")
	}
	if err := os.Remove(filepath.Join(dir, extra.Name)); err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(dir, changed); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(dir, "tree_untracked.gen.go")
	if err := os.WriteFile(unknown, extra.Source, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(dir, original); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, extra.Name)); !os.IsNotExist(err) {
		t.Fatal("未清理已删除树")
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("清理了未列入清单的文件")
	}
}

// TestGeneratedInterruptedPublishRecovery 模拟源码发布后清单尚未提交的中断，并验证重新生成可修复缺失或损坏源码。
func TestGeneratedInterruptedPublishRecovery(t *testing.T) {
	dir := t.TempDir()
	server, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	project := model.Example()
	generated := decodePreview(t, callEditor(t, server, "POST", "/api/generate", project))
	path := filepath.Join(generated.Directory, generated.Files[1].Name)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 409 {
		t.Fatal("缺失源码被接受", response.Code)
	}
	decodePreview(t, callEditor(t, server, "POST", "/api/generate", project))
	if err := os.WriteFile(path, []byte(generated.Files[1].Source+"// 未提交的新源码\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if response := callEditor(t, server, "POST", "/api/generated", project); response.Code != 409 {
		t.Fatal("未提交源码被接受", response.Code)
	}
	decodePreview(t, callEditor(t, server, "POST", "/api/generate", project))
	decodePreview(t, callEditor(t, server, "POST", "/api/generated", project))
}

// TestWriteGeneratedRejectsPathsAndBatchSize 验证目标路径预检和整批体积上限在发布前生效。
func TestWriteGeneratedRejectsPathsAndBatchSize(t *testing.T) {
	for _, name := range []string{"../outside.go", `a\b.gen.go`, "C:escape.gen.go", "glue.gen.go"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			result := generatedForTest(t, model.Example())
			result.Files[1].Name = name
			if err := WriteGenerated(dir, result); err == nil {
				t.Fatal("接受无效目标路径", name)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("路径预检失败后仍发布了产物")
			}
		})
	}
	dir := t.TempDir()
	result := generatedForTest(t, model.Example())
	// 两个文件各自未达上限，但合计超过上限，必须按整批拒绝。
	large := make([]byte, MaxGeneratedBytes/2+1)
	copy(large, result.Files[0].Source)
	result.Files[0].Source = large
	result.Files[1].Source = large
	if err := WriteGenerated(dir, result); err == nil {
		t.Fatal("未应用整批体积限制")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("超限后仍发布了文件")
	}
}

// TestGeneratedProtectsStaleHandwritten 验证待删除文件变为手写内容时整批发布停止且旧 glue 不变。
func TestGeneratedProtectsStaleHandwritten(t *testing.T) {
	dir := t.TempDir()
	project := model.Example()
	original := generatedForTest(t, project)
	if err := WriteGenerated(dir, original); err != nil {
		t.Fatal(err)
	}
	project.Trees[0].ID = "renamed"
	changed := generatedForTest(t, project)
	path := filepath.Join(dir, original.Files[1].Name)
	handwritten := []byte("package generated\n// 手写实现。\n")
	if err := os.WriteFile(path, handwritten, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(dir, changed); err == nil {
		t.Fatal("清理了手写文件")
	}
	glue, err := os.ReadFile(filepath.Join(dir, "glue.gen.go"))
	if err != nil || !bytes.Equal(glue, original.Files[0].Source) {
		t.Fatal("清理预检完成前修改了 glue")
	}
	if err := os.WriteFile(path, original.Files[1].Source, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(dir, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("树重命名后旧文件未移除")
	}
}
