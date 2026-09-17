package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"

	"github.com/1xxz188/behaviortree/model"
)

// Scaffold 创建可复制到独立手写文件的业务骨架，不读写磁盘，也不要求草稿拓扑完整。
func Scaffold(project model.Project) ([]byte, error) {
	// 以最小有效树隔离拓扑检查，仍复用目录、黑板名称冲突和生成配置校验。
	validated := project
	validated.Trees = model.Example().Trees
	if diagnostics := model.Validate(validated); len(diagnostics) != 0 {
		return nil, &ValidationError{Diagnostics: diagnostics}
	}
	context := project.Generation.ContextType
	if project.Generation.ContextImport != "" {
		context = "ctxpkg." + strings.TrimPrefix(context, "*")
		if strings.HasPrefix(project.Generation.ContextType, "*") {
			context = "*" + context
		}
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "// 业务函数骨架：复制所需函数到同包独立文件并完成 TODO。\n// 参数结构体由 glue.gen.go 提供；本预览不会覆盖任何手写文件。\npackage %s\n", project.Generation.Package)
	if len(project.Catalog) != 0 {
		fmt.Fprintln(&out, "import (bt \"github.com/1xxz188/behaviortree\"")
		if project.Generation.ContextImport != "" {
			fmt.Fprintf(&out, "ctxpkg %q\n", project.Generation.ContextImport)
		}
		fmt.Fprintln(&out, ")")
	}
	definitions := append([]model.Definition(nil), project.Catalog...)
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	for _, definition := range definitions {
		fmt.Fprintf(&out, "// %s 的业务显示名称：%q。\n", definition.GoName, definition.Name)
		if definition.Kind == model.DefinitionCondition {
			fmt.Fprintf(&out, "// %s 判断业务条件；未实现时保守返回 false。\nfunc %s(f *bt.Frame[%s], node int, params %sParams) bool {\n// TODO：读取上下文和参数完成条件判断。\nreturn false\n}\n", definition.GoName, definition.GoName, context, definition.GoName)
			continue
		}
		fmt.Fprintf(&out, "// %s 处理动作生命周期；未实现时保守返回 Failure。\nfunc %s(f *bt.Frame[%s], node int, phase bt.Phase, params %sParams) bt.Status {\nswitch phase {\ncase bt.Start:\n// TODO：启动业务动作；异步动作应保存 f.Token(node)，等待宿主完成通知。\nreturn bt.Failure\ncase bt.Resume:\n// TODO：消费宿主完成结果并返回状态，避免轮询。\nreturn bt.Failure\ncase bt.Abort:\n// TODO：取消外部任务并释放动作资源。\nreturn bt.Failure\n}\nreturn bt.Failure\n}\n", definition.GoName, definition.GoName, context, definition.GoName)
	}
	return format.Source(out.Bytes())
}
