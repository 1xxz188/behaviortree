package codegen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestReadableDurationGeneratedRuntime 编译并执行可读时长，验证默认值、常量和黑板绑定均保持纳秒语义。
func TestReadableDurationGeneratedRuntime(t *testing.T) {
	p := model.Example()
	p.Blackboard = []model.Field{{ID: "delay", Name: "Delay", Type: bt.DurationType, Default: json.RawMessage(`"500ms"`)}}
	p.Catalog = []model.Definition{{ID: "check", GoName: "CheckDuration", Kind: model.DefinitionAction, Params: []model.Parameter{
		{Name: "Default", Type: bt.DurationType, Default: json.RawMessage(`"1s"`)},
		{Name: "Constant", Type: bt.DurationType}, {Name: "Bound", Type: bt.DurationType}, {Name: "Legacy", Type: bt.DurationType},
	}}}
	p.Trees[0].Nodes = []model.Node{{ID: "root", Type: model.NodeAction, Binding: "check", Params: map[string]model.Value{
		"Constant": {Value: json.RawMessage(`"2.5s"`)}, "Bound": {Field: "delay"}, "Legacy": {Value: json.RawMessage(`250000000`)},
	}}}
	result, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(allGeneratedSource(result)), "ParseDuration") {
		t.Fatal("时长解析不应进入运行时生成代码")
	}
	dir := t.TempDir()
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedFiles(t, dir, result)
	gomod := "module duration.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(moduleRoot)) + "\n"
	const source = `package generated
import ("testing"; "time"; bt "github.com/1xxz188/behaviortree")
// CheckDuration 验证实际业务函数收到精确的 time.Duration 参数。
func CheckDuration(f *bt.Frame[any], node int, phase bt.Phase, p CheckDurationParams) bt.Status {
 if p.Default != time.Second || p.Constant != 2500*time.Millisecond || p.Bound != 500*time.Millisecond || p.Legacy != 250*time.Millisecond { return bt.Failure }
 return bt.Success
}
// TestDurationValues 验证生成程序的字段默认值和业务参数在执行时一致。
func TestDurationValues(t *testing.T) {
 program := NewProgram("test")
 if program.Fields[0].Default.Duration != 500*time.Millisecond { t.Fatal("黑板默认时长错误") }
 // 测试宿主提供真实入队回调；同步动作不应调度任何异步续跑。
 var queue []func()
 instance, err := bt.NewInstance(program,"main","main",nil,bt.Options{Post:func(fn func()){queue=append(queue,fn)}})
 if err != nil { t.Fatal(err) }; defer instance.Close()
 if status := instance.Start(); status != bt.Success { t.Fatalf("业务时长不正确: %v", status) }
 if len(queue) != 0 { t.Fatal("同步时长参数读取产生了异步续跑") }
}
`
	for name, data := range map[string]string{"go.mod": gomod, "duration_test.go": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-mod=mod", "-count=1", ".")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("可读时长生成代码编译或执行失败: %v\n%s", err, output)
	}
}
