package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// businessNamingProject 构造既有业务符号和新前缀符号共同参与生成的完整工程。
func businessNamingProject() model.Project {
	p := model.Example()
	p.Catalog = []model.Definition{
		{ID: "old-move", Name: "历史动作", Kind: model.DefinitionAction, GoName: "Move", Params: []model.Parameter{}},
		{ID: "old-ready", Name: "历史条件", Kind: model.DefinitionCondition, GoName: "Ready", Params: []model.Parameter{}},
		{ID: "new-move", Name: "新动作", Kind: model.DefinitionAction, GoName: "ActionMove", NamingVersion: model.PrefixedBusinessNaming, Params: []model.Parameter{}},
		{ID: "new-ready", Name: "新条件", Kind: model.DefinitionCondition, GoName: "IsReady", NamingVersion: model.PrefixedBusinessNaming, Params: []model.Parameter{}},
	}
	p.Trees[0].Nodes = []model.Node{
		{ID: "root", Type: model.NodeSequence, CodeName: "Root", Children: []string{"old-move", "old-ready", "new-move", "new-ready"}},
		{ID: "old-move", Type: model.NodeAction, CodeName: "Patrol", Binding: "old-move"},
		{ID: "old-ready", Type: model.NodeCondition, CodeName: "CanStart", Binding: "old-ready"},
		{ID: "new-move", Type: model.NodeAction, CodeName: "ActionMove", NamingVersion: model.PrefixedBusinessNaming, Binding: "new-move"},
		{ID: "new-ready", Type: model.NodeCondition, CodeName: "IsReady", NamingVersion: model.PrefixedBusinessNaming, Binding: "new-ready"},
	}
	return p
}

// TestBusinessNamingVersionDoesNotChangeGeneration 验证仅增加新规则标记不会改变源码、调试映射、版本或业务骨架。
func TestBusinessNamingVersionDoesNotChangeGeneration(t *testing.T) {
	p := businessNamingProject()
	before, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	marked, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	markedScaffold, err := Scaffold(p)
	if err != nil {
		t.Fatal(err)
	}
	markedVersion, err := ProjectVersion(p)
	if err != nil {
		t.Fatal(err)
	}
	// 独立工程只移除标记，实际业务函数名、实例代码名和身份保持完全一致。
	legacy := businessNamingProject()
	for i := range legacy.Catalog {
		legacy.Catalog[i].NamingVersion = 0
	}
	for i := range legacy.Trees[0].Nodes {
		legacy.Trees[0].Nodes[i].NamingVersion = 0
	}
	unmarked, err := Generate(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(marked, unmarked) {
		t.Fatal("仅命名规则标记变化改变了生成源码、映射或版本")
	}
	unmarkedScaffold, err := Scaffold(legacy)
	if err != nil || !bytes.Equal(markedScaffold, unmarkedScaffold) {
		t.Fatalf("标记改变了业务骨架: %v", err)
	}
	unmarkedVersion, err := ProjectVersion(legacy)
	if err != nil || markedVersion != unmarkedVersion || markedVersion != marked.Version {
		t.Fatalf("生成版本与规则标记耦合: %v %s %s %s", err, markedVersion, unmarkedVersion, marked.Version)
	}
	after, err := json.Marshal(p)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("版本规范化修改了调用方的规则标记: %v", err)
	}
}

// TestBusinessNamingLegacySymbolsPreserved 验证旧业务函数、参数类型和实例符号保持原样，新前缀名称可与它们共同编译。
func TestBusinessNamingLegacySymbolsPreserved(t *testing.T) {
	p := businessNamingProject()
	result, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	for _, file := range result.Files {
		source.Write(file.Source)
	}
	for _, symbol := range []string{"type MoveParams struct", "type ReadyParams struct", "func btNodeMainPatrol(", "func btNodeMainCanStart(", "Move(f,", "Ready(f,", "func btNodeMainActionMove(", "func btNodeMainIsReady("} {
		if !strings.Contains(source.String(), symbol) {
			t.Fatalf("生成结果缺少原有或新前缀符号: %s", symbol)
		}
	}
	scaffold, err := Scaffold(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"func Move(", "func Ready(", "func ActionMove(", "func IsReady("} {
		if !bytes.Contains(scaffold, []byte(symbol)) {
			t.Fatalf("业务骨架修改了函数名: %s", symbol)
		}
	}
	compileNamedProject(t, p, result, "")
}

// TestBusinessNamingGenerationRejectsWrongPrefix 验证直接 Go 生成入口仍拒绝错误的新名称，不能被摘要去标记流程绕过。
func TestBusinessNamingGenerationRejectsWrongPrefix(t *testing.T) {
	for _, target := range []string{"definition", "node", "unknown-definition", "unknown-node"} {
		t.Run(target, func(t *testing.T) {
			p := businessNamingProject()
			switch target {
			case "definition":
				p.Catalog[2].GoName = "MoveAgain"
			case "node":
				p.Trees[0].Nodes[3].CodeName = "MoveAgain"
			case "unknown-definition":
				p.Catalog[2].NamingVersion = model.BusinessNamingVersion(2)
			case "unknown-node":
				p.Trees[0].Nodes[3].NamingVersion = model.BusinessNamingVersion(2)
			}
			_, err := Generate(p)
			var validation *ValidationError
			if !errors.As(err, &validation) || len(validation.Diagnostics) == 0 {
				t.Fatalf("直接生成入口接受错误新名称或规则: %v", err)
			}
			if target == "definition" || target == "unknown-definition" {
				if _, err := Scaffold(p); err == nil {
					t.Fatal("业务骨架接受错误的新函数名或规则")
				}
			}
		})
	}
}
