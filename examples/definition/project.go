// Package definition 用普通 Go 声明节点元数据，GoLand 可直接导航和编辑。
package definition

import (
	"encoding/json"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// Catalog 导出与 behavior 手写函数匹配的强类型目录。
func Catalog() []model.Definition {
	return []model.Definition{
		{ID: "gate", Name: "等待宿主异步完成", Kind: model.DefinitionAction, GoName: "Gate", Params: []model.Parameter{}},
		{ID: "record", Name: "记录业务事件", Kind: model.DefinitionAction, GoName: "Record", Params: []model.Parameter{{Name: "Message", Type: bt.StringType, Default: json.RawMessage(`"completed"`)}}},
	}
}

// Project 建立真实示例树；release 越大，生成的后置动作数量越多。
func Project(release int) model.Project {
	tree := model.Tree{ID: "main", Name: "异步任务", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeSequence, Children: []string{"gate", "record"}}, {ID: "gate", Type: model.NodeAction, Binding: "gate"}, {ID: "record", Type: model.NodeAction, Binding: "record", Params: map[string]model.Value{"Message": {Field: "message"}}}}, Layout: map[string]model.Position{"root": {X: 200, Y: 40}, "gate": {X: 80, Y: 180}, "record": {X: 320, Y: 180}}}
	for index := 0; index < release; index++ {
		id := string(rune('a' + index))
		tree.Nodes[0].Children = append(tree.Nodes[0].Children, "extra_"+id)
		tree.Nodes = append(tree.Nodes, model.Node{ID: "extra_" + id, Type: model.NodeAction, Binding: "record", Params: map[string]model.Value{"Message": {Value: json.RawMessage(`"extra_` + id + `"`)}}})
	}
	return model.Project{SchemaVersion: model.SchemaVersion, Name: "Go 原生插件示例", NextEventID: "2", EventEnumDescription: "宿主先更新权威业务状态，再用枚举通知需要重新评估的节点。", Events: []model.EventDefinition{{ID: "1", Name: "宿主状态变化", CodeName: "HostStateChanged", Description: "宿主状态更新后通知；示例未配置监听节点，用于展示合法的无引用事件。"}}, Catalog: Catalog(), Blackboard: []model.Field{{ID: "message", Name: "Message", Type: bt.StringType, Default: json.RawMessage(`"completed"`)}}, Trees: []model.Tree{tree}, Generation: model.Generation{PackagePath: "behavior", ContextImport: "github.com/1xxz188/behaviortree/examples/shared", ContextType: "*Context"}}
}
