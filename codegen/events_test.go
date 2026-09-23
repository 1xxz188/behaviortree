package codegen

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// eventFixture 验证大数事件身份、双层说明和稳定排序共用同一份工程数据。
func eventFixture() model.Project {
	p := model.Example()
	p.EventEnumDescription = "宿主先更新状态。\r\n//go:nosplit\n仍是说明"
	p.NextEventID = "18446744073709551615"
	p.Events = []model.EventDefinition{
		{ID: "18446744073709551614", Name: "第二事件", CodeName: "Second", Description: "第二项说明\n*/\nvar Injected = 1"},
		{ID: "1", Name: "第一事件", CodeName: "First", Description: "第一项说明\r\n  保留缩进"},
	}
	p.Catalog = []model.Definition{{ID: "listener", Name: "监听者", Kind: model.DefinitionAction, GoName: "Listener", EventIDs: []string{"18446744073709551614", "1"}}}
	return p
}

// TestEventCommentsAndVersion 验证常量注释归属、安全文本以及排序和注释对产物版本的影响。
func TestEventCommentsAndVersion(t *testing.T) {
	p := eventFixture()
	first, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	source := generatedSource(t, first, "glue.gen.go")
	file, err := parser.ParseFile(token.NewFileSet(), "glue.gen.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST || group.Doc == nil || !strings.Contains(group.Doc.Text(), "工程事件枚举") {
			continue
		}
		if !strings.Contains(group.Doc.Text(), "宿主先更新状态") || !strings.Contains(group.Doc.Text(), "//go:nosplit") {
			t.Fatalf("整体注释缺失或移位: %q", group.Doc.Text())
		}
		for _, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			found[value.Names[0].Name] = value.Doc.Text()
			// 常量值保持十进制，调试时可直接与工程事件 ID 对照。
			literal := value.Values[0].(*ast.BasicLit)
			if value.Names[0].Name == "EventFirst" && literal.Value != "1" {
				t.Fatalf("首个事件常量不是十进制 1: %s", literal.Value)
			}
		}
	}
	if !strings.Contains(found["EventFirst"], "第一项说明") || !strings.Contains(found["EventFirst"], "保留缩进") || !strings.Contains(found["EventSecond"], "var Injected = 1") {
		t.Fatalf("成员注释未附着到各自常量: %#v", found)
	}
	if bytes.Contains(source, []byte("\n//go:nosplit")) || bytes.Contains(source, []byte("\nvar Injected = 1")) {
		t.Fatal("用户注释被当成编译指令或代码")
	}
	p.Events[0], p.Events[1] = p.Events[1], p.Events[0]
	p.Catalog[0].EventIDs[0], p.Catalog[0].EventIDs[1] = p.Catalog[0].EventIDs[1], p.Catalog[0].EventIDs[0]
	p.EventEnumDescription = strings.ReplaceAll(p.EventEnumDescription, "\r\n", "\n")
	p.Events[0].Description = strings.ReplaceAll(p.Events[0].Description, "\r\n", "\n")
	reordered, err := Generate(p)
	if err != nil || first.Version != reordered.Version || !bytes.Equal(source, generatedSource(t, reordered, "glue.gen.go")) {
		t.Fatalf("等价顺序或换行改变产物: %v", err)
	}
	p.NextEventID = model.MaxNextEventID
	advanced, err := Generate(p)
	if err != nil || advanced.Version != first.Version {
		t.Fatalf("仅推进分配游标不应改变产物版本: %v", err)
	}
	p.Events[0].Description += "\n新增说明"
	changed, err := Generate(p)
	if err != nil || changed.Version == first.Version {
		t.Fatalf("成员注释未改变产物版本: %v", err)
	}
}

// TestTypedGeneratedNotifyCompiles 验证生成常量可直接通知，而字符串实参在真实 Go 编译中失败。
func TestTypedGeneratedNotifyCompiles(t *testing.T) {
	result, err := Generate(eventFixture())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeGeneratedFiles(t, dir, result)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module bt.events.compile\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(root)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	good := `package behavior
import (
	"testing"
	bt "github.com/1xxz188/behaviortree"
)
// notifyTyped 验证生成常量能作为强类型通知实参。
func notifyTyped(i *bt.Instance[any]) { _ = i.Notify(EventFirst) }
// TestEventMetadata 验证已声明但未引用的事件仍保留完整说明。
func TestEventMetadata(t *testing.T) {
	p := NewProgram("")
	if len(p.Events) != 2 || p.Events[1].ID != EventSecond || p.Events[1].Description == "" || p.EventEnumDescription == "" { t.Fatal(p.Events, p.EventEnumDescription) }
}
`
	if err := os.WriteFile(filepath.Join(dir, "event_test.go"), []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	run := func() ([]byte, error) {
		cmd := exec.Command("go", "test", "-count=1", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		return cmd.CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("生成枚举常量编译失败: %v\n%s", err, output)
	}
	bad := `package behavior
import bt "github.com/1xxz188/behaviortree"
// badNotify 用旧字符串接口触发预期的编译错误。
func badNotify(i *bt.Instance[any]) { i.Notify("command.changed") }
`
	if err := os.WriteFile(filepath.Join(dir, "bad_test.go"), []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := run()
	if err == nil || !bytes.Contains(output, []byte("cannot use \"command.changed\"")) {
		t.Fatalf("字符串 Notify 未按预期编译失败: %v\n%s", err, output)
	}
}
