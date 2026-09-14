package codegen

import (
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestGenerateRejectsInvalidTypes 验证手写 Go 模型中的非法枚举也不能进入生成映射。
func TestGenerateRejectsInvalidTypes(t *testing.T) {
	for _, edit := range []func(*model.Project){
		func(p *model.Project) { p.Trees[0].Nodes[0].Type = model.NodeType(255) },
		func(p *model.Project) {
			p.Blackboard = []model.Field{{ID: "f", Name: "Value", Type: bt.ValueType(255)}}
		},
		func(p *model.Project) {
			p.Catalog = []model.Definition{{ID: "a", GoName: "Act", Kind: model.DefinitionKind(255)}}
		},
		func(p *model.Project) {
			p.Catalog = []model.Definition{{ID: "a", GoName: "Act", Kind: model.DefinitionAction, Params: []model.Parameter{{Name: "Value", Type: bt.ValueType(255)}}}}
		},
	} {
		p := model.Example()
		edit(&p)
		if _, err := Generate(p); err == nil {
			t.Fatal("非法类型未在生成前被拒绝")
		}
	}
}

// TestTypeDescriptionRejectsInvalid 验证类型映射绝不将非法类型兜底为时长。
func TestTypeDescriptionRejectsInvalid(t *testing.T) {
	for _, typ := range []bt.ValueType{bt.InvalidValueType, 255} {
		t.Run(typ.String(), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("非法类型未明确失败")
				}
			}()
			describeType(typ)
		})
	}
}
