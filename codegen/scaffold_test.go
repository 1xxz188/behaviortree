package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestScaffoldCompilesWithGeneratedParameters 验证默认、同包和跨包上下文骨架能与生成参数共同编译并保守返回失败。
func TestScaffoldCompilesWithGeneratedParameters(t *testing.T) {
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, context := range []string{"any", "*LocalContext", "*Buffer"} {
		t.Run(context, func(t *testing.T) {
			project := model.Example()
			project.Generation.ContextType = context
			if context == "*Buffer" {
				project.Generation.ContextImport = "bytes"
			}
			project.Catalog = []model.Definition{
				{ID: "move", GoName: "Move", Kind: model.DefinitionAction, Params: []model.Parameter{{Name: "Delay", Type: bt.DurationType, Default: []byte("0")}}},
				{ID: "ready", GoName: "Ready", Kind: model.DefinitionCondition},
			}
			project.Trees[0].Nodes = []model.Node{{ID: "root", Type: model.NodeSequence, Children: []string{"ready", "move"}}, {ID: "ready", Type: model.NodeCondition, Binding: "ready"}, {ID: "move", Type: model.NodeAction, Binding: "move"}}
			generated, err := Generate(project)
			if err != nil {
				t.Fatal(err)
			}
			project.Trees[0].Root = "unfinished"
			scaffold, err := Scaffold(project)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			gomod := "module scaffold.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(moduleRoot)) + "\n"
			// 编译之外直接执行所有生命周期分支，保证未完成骨架不会误报业务成功。
			testSource := "package behavior\nimport (\"testing\"; bt \"github.com/1xxz188/behaviortree\")\n// TestDefaults 验证骨架未实现时保守失败。\nfunc TestDefaults(t *testing.T) {for _, phase := range []bt.Phase{bt.Start,bt.Resume,bt.Abort}{if Move(nil,0,phase,MoveParams{})!=bt.Failure{t.Fatal(\"动作误报成功\")}};if Ready(nil,0,ReadyParams{}){t.Fatal(\"条件误报成立\")}}\n"
			if context == "*LocalContext" {
				testSource += "// LocalContext 是同包上下文测试替身。\ntype LocalContext struct{}\n"
			}
			writeGeneratedFiles(t, dir, generated)
			for name, source := range map[string][]byte{"go.mod": []byte(gomod), "actions.go": scaffold, "actions_test.go": []byte(testSource)} {
				if err := os.WriteFile(filepath.Join(dir, name), source, 0644); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("go", "test", "-mod=mod", "-count=1", ".")
			command.Dir = dir
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("骨架无法编译或默认状态错误: %v\n%s", err, output)
			}
		})
	}
}

// TestScaffoldValidation 验证草稿目录与生成配置仍严格校验，空目录不产生未使用导入。
func TestScaffoldValidation(t *testing.T) {
	project := model.Example()
	project.Trees = nil
	source, err := Scaffold(project)
	if err != nil || strings.Contains(string(source), "import") {
		t.Fatal("空目录骨架失败", err)
	}
	for _, invalid := range []string{"for", "btNode1", "MoveParams", "any"} {
		project.Catalog = []model.Definition{{ID: "move", GoName: "Move", Kind: model.DefinitionAction}, {ID: "invalid", GoName: invalid, Kind: model.DefinitionAction}}
		if _, err := Scaffold(project); err == nil {
			t.Fatal("接受无效或冲突函数名", invalid)
		}
	}
	project.Catalog = nil
	project.Generation.ContextImport = "../escape"
	if _, err := Scaffold(project); err == nil {
		t.Fatal("接受无效上下文导入")
	}
}

// TestProjectVersionMatchesGenerated 验证版本计算忽略布局、集合顺序且不会修改输入，草稿变化仍改变版本。
func TestProjectVersionMatchesGenerated(t *testing.T) {
	project := model.Example()
	result, err := Generate(project)
	if err != nil {
		t.Fatal(err)
	}
	project.Trees[0].Layout["root"] = model.Position{X: 1000, Y: 2000}
	project.Trees[0].Nodes[0], project.Trees[0].Nodes[2] = project.Trees[0].Nodes[2], project.Trees[0].Nodes[0]
	version, err := ProjectVersion(project)
	if err != nil || version != result.Version {
		t.Fatal("版本与生成结果不同", err)
	}
	if project.Trees[0].Nodes[0].ID != "done" || len(project.Trees[0].Layout) == 0 {
		t.Fatal("版本计算修改了调用者工程")
	}
	project.Trees[0].Root = "missing"
	version, err = ProjectVersion(project)
	if err != nil || version == result.Version {
		t.Fatal("草稿内容变更未更新版本", err)
	}
}
