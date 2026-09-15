package codegen

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestTreeDisplayNameDoesNotChangeGeneratedIdentity 验证改展示名不改变版本、源码或映射，也不修改输入。
func TestTreeDisplayNameDoesNotChangeGeneratedIdentity(t *testing.T) {
	p := model.Example()
	before, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Trees[0].Name = "重命名后的展示名称"
	input, err := model.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	version, err := ProjectVersion(p)
	if err != nil {
		t.Fatal(err)
	}
	if version != before.Version || !reflect.DeepEqual(before, after) {
		t.Fatal("改展示名改变了源码、映射或运行版本")
	}
	output, err := model.Encode(p)
	if err != nil || !bytes.Equal(input, output) {
		t.Fatalf("生成或版本计算修改了调用者输入: %v", err)
	}
}

// TestRenamedTreeIDGeneratesNewReferences 验证同步改号后的入口、展开节点归属和映射均使用新主键。
func TestRenamedTreeIDGeneratesNewReferences(t *testing.T) {
	p := model.Example()
	p.Trees = append(p.Trees, model.Tree{ID: "caller", Name: p.Trees[0].Name, Root: "call", Nodes: []model.Node{{ID: "call", Type: model.NodeSubtree, Tree: "main"}}})
	before, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Trees[0].ID = "9_Main"
	p.Trees[1].Nodes[0].Tree = "9_Main"
	after, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if before.Version == after.Version || bytes.Contains(allGeneratedSource(after), []byte(`"main"`)) || !strings.Contains(string(allGeneratedSource(after)), `"9_Main":`) {
		t.Fatal("改号未更新运行版本或生成入口")
	}
	count := 0
	for _, location := range after.SourceMap {
		if location.TreeID == "main" {
			t.Fatal("源码映射残留旧树 ID")
		}
		if location.TreeID == "9_Main" {
			count++
		}
	}
	if count != len(p.Trees[0].Nodes)*2 {
		t.Fatal("独立入口与子树展开没有统一采用新树 ID")
	}
}
