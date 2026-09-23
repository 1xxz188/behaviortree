package model

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
)

// organizationProject 构造有嵌套目录、共享标签及稀疏归属的合法工程。
func organizationProject() Project {
	p := Example()
	p.Catalog = []Definition{{ID: "move", Name: "移动", Kind: DefinitionAction, GoName: "Move", Params: []Parameter{}}, {ID: "idle", Name: "空闲", Kind: DefinitionAction, GoName: "Idle", Params: []Parameter{}}}
	p.CatalogOrganization = &CatalogOrganization{
		Folders:     []CatalogFolder{{ID: "parent", Name: "行为", Order: -1}, {ID: "child", Name: "移动", ParentID: "parent", Order: 0.5}},
		Tags:        []CatalogTag{{ID: "tag", Name: "常用"}},
		Assignments: map[string]CatalogAssignment{"move": {FolderID: "child", TagIDs: []string{"tag"}, Order: 0.25}},
	}
	return p
}

// TestCatalogOrganizationRoundTrip 验证完整工程保留分类、稀疏归属与无损默认值，独立导出不携带分类。
func TestCatalogOrganizationRoundTrip(t *testing.T) {
	p := organizationProject()
	p.Catalog[0].Params = []Parameter{{Name: "Target", Type: bt.UIntType, Default: json.RawMessage("18446744073709551615")}}
	raw, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.CatalogOrganization, p.CatalogOrganization) {
		t.Fatal("分类往返发生变化")
	}
	if _, exists := decoded.CatalogOrganization.Assignments["idle"]; exists {
		t.Fatal("不应擅自补齐缺省归属")
	}
	if diagnostics := Validate(decoded); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	catalog, err := ExportCatalog(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(catalog), "catalogOrganization") || !strings.Contains(string(catalog), "18446744073709551615") {
		t.Fatal("独立导出泄漏分类或丢失整数精度")
	}
	p.CatalogOrganization.Folders[0].Name = "  行为  "
	raw, err = Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = Decode(raw)
	if err != nil || decoded.CatalogOrganization.Folders[0].Name != "行为" || p.CatalogOrganization.Folders[0].Name != "  行为  " {
		t.Fatalf("名称规范化应裁剪空白且不修改调用者: %v", err)
	}
}

// TestCatalogOrganizationInvalid 验证输入、输出及公共校验均拒绝非法分类且不限制草稿拓扑。
func TestCatalogOrganizationInvalid(t *testing.T) {
	cases := []struct {
		name string         // 校验场景。
		edit func(*Project) // 注入非法分类。
	}{
		{"重复定义", func(p *Project) { p.Catalog = append(p.Catalog, p.Catalog[0]) }},
		{"重复目录", func(p *Project) {
			p.CatalogOrganization.Folders = append(p.CatalogOrganization.Folders, p.CatalogOrganization.Folders[0])
		}},
		{"同级重名", func(p *Project) {
			p.CatalogOrganization.Folders = append(p.CatalogOrganization.Folders, CatalogFolder{ID: "other", Name: "  行为 "})
		}},
		{"大小写重名", func(p *Project) {
			p.CatalogOrganization.Folders[0].Name = "AI"
			p.CatalogOrganization.Folders = append(p.CatalogOrganization.Folders, CatalogFolder{ID: "other", Name: "ai"})
		}},
		{"空目录名", func(p *Project) { p.CatalogOrganization.Folders[0].Name = " " }},
		{"悬空父目录", func(p *Project) { p.CatalogOrganization.Folders[0].ParentID = "missing" }},
		{"自身环", func(p *Project) { p.CatalogOrganization.Folders[0].ParentID = "parent" }},
		{"后代环", func(p *Project) { p.CatalogOrganization.Folders[0].ParentID = "child" }},
		{"重复标签", func(p *Project) {
			p.CatalogOrganization.Tags = append(p.CatalogOrganization.Tags, p.CatalogOrganization.Tags[0])
		}},
		{"标签重名", func(p *Project) {
			p.CatalogOrganization.Tags = append(p.CatalogOrganization.Tags, CatalogTag{ID: "other", Name: " 常用 "})
		}},
		{"空标签名", func(p *Project) { p.CatalogOrganization.Tags[0].Name = " " }},
		{"悬空定义", func(p *Project) { p.CatalogOrganization.Assignments["missing"] = CatalogAssignment{} }},
		{"悬空归属", func(p *Project) { p.CatalogOrganization.Assignments["move"] = CatalogAssignment{FolderID: "missing"} }},
		{"悬空标签", func(p *Project) {
			p.CatalogOrganization.Assignments["move"] = CatalogAssignment{TagIDs: []string{"missing"}}
		}},
		{"重复关联", func(p *Project) {
			p.CatalogOrganization.Assignments["move"] = CatalogAssignment{TagIDs: []string{"tag", "tag"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := organizationProject()
			tc.edit(&p)
			if err := ValidateCatalogOrganization(p); err == nil {
				t.Fatal("公共分类校验未拒绝非法输入")
			}
			if _, err := Encode(p); err == nil {
				t.Fatal("编码未拒绝非法分类")
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Decode(raw); err == nil {
				t.Fatal("解码未拒绝非法分类")
			}
			found := false
			for _, diagnostic := range Validate(p) {
				found = found || diagnostic.Field == "catalogOrganization"
			}
			if !found {
				t.Fatal("未返回分类诊断")
			}
		})
	}
	for _, value := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		p := organizationProject()
		p.CatalogOrganization.Folders[0].Order = value
		if ValidateCatalogOrganization(p) == nil {
			t.Fatal("未拒绝非有限目录排序")
		}
		p = organizationProject()
		p.CatalogOrganization.Assignments["move"] = CatalogAssignment{Order: value}
		if ValidateCatalogOrganization(p) == nil {
			t.Fatal("未拒绝非有限定义排序")
		}
	}
}

// TestSchemaVersionTwoOnly 验证新版输入拒绝旧工程，不执行隐式迁移。
func TestSchemaVersionTwoOnly(t *testing.T) {
	p := Example()
	p.SchemaVersion = 1
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(raw); err == nil {
		t.Fatal("旧版工程被接受")
	}
	if _, err = Encode(p); err == nil {
		t.Fatal("旧版工程被写出")
	}
}

// TestRoundTrip 验证导入导出保留节点 ID、顺序和布局。
func TestRoundTrip(t *testing.T) {
	p := Example()
	raw, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d := Validate(decoded); len(d) > 0 {
		t.Fatal(d)
	}
	again, _ := Encode(decoded)
	if string(raw) != string(again) {
		t.Fatal("JSON 往返改变工程")
	}
	if _, err := Decode(append(raw, []byte(" {}")...)); err == nil {
		t.Fatal("应拒绝多余 JSON")
	}
	if _, err := Decode([]byte(strings.Replace(string(raw), `"schemaVersion"`, `"schemaVerzion"`, 1))); err == nil {
		t.Fatal("应拒绝拼错属性")
	}
}

// TestValidation 验证拓扑错误均产生可定位诊断。
func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Project)
		part string
	}{
		{"重复ID", func(p *Project) { p.Trees[0].Nodes[2].ID = "wait" }, "重复节点"},
		{"节点环", func(p *Project) {
			p.Trees[0].Nodes[1] = Node{ID: "wait", Type: NodeSequence, Children: []string{"root"}}
		}, "存在环"},
		{"多个父节点", func(p *Project) { p.Trees[0].Nodes[0].Children = []string{"wait", "wait"} }, "多个父节点"},
		{"孤立节点", func(p *Project) { p.Trees[0].Nodes[0].Children = []string{"wait"} }, "无法从根"},
		{"参数缺失", func(p *Project) {
			p.Catalog = []Definition{{ID: "a", Name: "a", Kind: DefinitionAction, GoName: "Move", Params: []Parameter{{Name: "Speed", Type: bt.IntType}}}}
			p.Trees[0].Nodes[1] = Node{ID: "wait", Type: NodeAction, Binding: "a"}
		}, "未绑定参数"},
		{"递归引用", func(p *Project) { p.Trees[0].Nodes[1] = Node{ID: "wait", Type: NodeSubtree, Tree: "main"} }, "不能递归"},
		{"错误优先级守卫", func(p *Project) { p.Trees[0].Nodes[0].Type = NodePriority }, "以 condition 开头"},
		{"无限重试", func(p *Project) { p.Trees[0].Nodes[0].Type = NodeRetry }, "循环次数"},
		{"时长溢出", func(p *Project) { p.Trees[0].Nodes[1].DurationMS = 9223372036854775807 }, "非负毫秒"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Example()
			tc.edit(&p)
			d := Validate(p)
			raw, _ := json.Marshal(d)
			if !strings.Contains(string(raw), tc.part) {
				t.Fatalf("期望 %s，得到 %s", tc.part, raw)
			}
		})
	}
}

// TestLiteralTypes 验证整型精度、空值和枚举约束不会被 JSON 宽松转换掩盖。
func TestLiteralTypes(t *testing.T) {
	for _, tc := range []struct {
		typ bt.ValueType
		raw string
		ok  bool
	}{{bt.IntType, "9223372036854775807", true}, {bt.UIntType, "18446744073709551615", true}, {bt.IntType, "1.5", false}, {bt.UIntType, "-1", false}, {bt.BoolType, " null ", false}, {bt.StringType, "null", false}, {bt.BoolType, "1", false}, {bt.FloatType, "1e309", false}, {bt.DurationType, "12", true}} {
		_, err := Literal(tc.typ, json.RawMessage(tc.raw), nil)
		if (err == nil) != tc.ok {
			t.Errorf("%s(%s): %v", tc.typ, tc.raw, err)
		}
	}
	if _, err := Literal(bt.EnumType, json.RawMessage(`"missing"`), []string{"ready"}); err == nil {
		t.Fatal("应拒绝未知枚举值")
	}
}

// TestTypedCatalog 验证 Go 声明可导出并严格校验字段绑定类型。
func TestTypedCatalog(t *testing.T) {
	d := []Definition{{ID: "move", Name: "移动", Kind: DefinitionAction, GoName: "Move", Params: []Parameter{{Name: "Speed", Type: bt.FloatType, Default: json.RawMessage("1.5")}}}}
	p := Example()
	p.Catalog = d
	if _, err := ExportCatalog(p); err != nil {
		t.Fatal(err)
	}
	p.Blackboard = []Field{{ID: "speed", Name: "Speed", Type: bt.IntType}}
	p.Trees[0].Nodes[1] = Node{ID: "wait", Type: NodeAction, Binding: "move", Params: map[string]Value{"Speed": {Field: "speed"}}}
	if len(Validate(p)) == 0 {
		t.Fatal("不同类型字段绑定应被拒绝")
	}
}

// TestGeneratedSymbolConflicts 验证绑定名称不会与生成函数、参数类型或局部变量冲突。
func TestGeneratedSymbolConflicts(t *testing.T) {
	for _, name := range []string{"NewProgram", "btNode12", "init", "true", "f", "phase", "GetEnabled"} {
		t.Run(name, func(t *testing.T) {
			p := Example()
			p.Blackboard = []Field{{ID: "enabled", Name: "Enabled", Type: bt.BoolType}}
			p.Catalog = []Definition{{ID: "a", Name: "a", Kind: DefinitionAction, GoName: name}}
			if len(Validate(p)) == 0 {
				t.Fatalf("未拒绝冲突名称 %s", name)
			}
		})
	}
	p := Example()
	p.Catalog = []Definition{{ID: "a", Kind: DefinitionAction, GoName: "Move"}, {ID: "b", Kind: DefinitionAction, GoName: "MoveParams"}}
	if len(Validate(p)) == 0 {
		t.Fatal("未拒绝参数类型与函数名称冲突")
	}
}
