// Package definition 用普通 Go 声明节点元数据，GoLand 可直接导航和编辑。
package definition

import (
	"encoding/json"
	"github.com/1xxz188/behaviortree/model"
)

// Catalog 导出与 behavior 手写函数匹配的强类型目录。
func Catalog() []model.Definition {
	return []model.Definition{
		{ID: "gate", Name: "等待宿主异步完成", Kind: "action", GoName: "Gate", Params: []model.Parameter{}},
		{ID: "record", Name: "记录业务事件", Kind: "action", GoName: "Record", Params: []model.Parameter{{Name: "Message", Type: "string", Default: json.RawMessage(`"completed"`)}}},
	}
}

// Project 建立真实示例树；release 越大，生成的后置动作数量越多。
func Project(release int) model.Project {
	tree := model.Tree{ID: "main", Name: "异步任务", Root: "root", Nodes: []model.Node{{ID: "root", Type: "sequence", Children: []string{"gate", "record"}}, {ID: "gate", Type: "action", Binding: "gate"}, {ID: "record", Type: "action", Binding: "record", Params: map[string]model.Value{"Message": {Field: "message"}}}}, Layout: map[string]model.Position{"root": {X: 200, Y: 40}, "gate": {X: 80, Y: 180}, "record": {X: 320, Y: 180}}}
	for index := 0; index < release; index++ {
		id := string(rune('a' + index))
		tree.Nodes[0].Children = append(tree.Nodes[0].Children, "extra_"+id)
		tree.Nodes = append(tree.Nodes, model.Node{ID: "extra_" + id, Type: "action", Binding: "record", Params: map[string]model.Value{"Message": {Value: json.RawMessage(`"extra_` + id + `"`)}}})
	}
	return model.Project{SchemaVersion: model.SchemaVersion, Name: "Go 原生插件示例", Catalog: Catalog(), Blackboard: []model.Field{{ID: "message", Name: "Message", Type: "string", Default: json.RawMessage(`"completed"`)}}, Trees: []model.Tree{tree}, Generation: model.Generation{Package: "behavior", ContextImport: "github.com/1xxz188/behaviortree/examples/shared", ContextType: "*Context"}}
}
