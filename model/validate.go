package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	bt "github.com/1xxz188/behaviortree"
)

// ValidTreeID 逐字节检查非空 ASCII 树 ID，允许字母、数字和下划线及数字开头。
func ValidTreeID(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// Identifier 判断名称是否为非保留的 Go 标识符。
func Identifier(name string) bool {
	return name != "_" && token.IsIdentifier(name) && !token.Lookup(name).IsKeyword()
}

// ExportedIdentifier 判断名称是否能作为导出参数或访问器使用。
func ExportedIdentifier(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return Identifier(name) && unicode.IsUpper(r)
}

// ValidType 检查首版支持的强类型集合。
func ValidType(t bt.ValueType) bool { return t.Valid() }

// Literal 校验并解码一个有精度约束的 JSON 常量。
func Literal(t bt.ValueType, raw json.RawMessage, enum []string) (any, error) {
	if !ValidType(t) {
		return nil, fmt.Errorf("未知值类型 %q", t)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	if string(raw) == "null" {
		return nil, fmt.Errorf("常量不能为 null")
	}
	var target any
	switch t {
	case bt.BoolType:
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		target = v
	case bt.IntType, bt.DurationType:
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		target = v
	case bt.UIntType, bt.EntityIDType:
		var v uint64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		target = v
	case bt.FloatType:
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, fmt.Errorf("浮点数必须有限")
		}
		target = v
	case bt.StringType, bt.EnumType:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if t == bt.EnumType && len(enum) > 0 {
			found := false
			for _, choice := range enum {
				found = found || v == choice
			}
			if !found {
				return nil, fmt.Errorf("枚举值 %q 不在允许列表中", v)
			}
		}
		target = v
	}
	return target, nil
}

// Zero 返回一种值类型的 JSON 零值；枚举优先采用首个允许值。
func Zero(t bt.ValueType, enum []string) json.RawMessage {
	if t == bt.BoolType {
		return json.RawMessage("false")
	}
	if t == bt.StringType || t == bt.EnumType {
		v := ""
		if t == bt.EnumType && len(enum) > 0 {
			v = enum[0]
		}
		data, _ := json.Marshal(v)
		return data
	}
	return json.RawMessage("0")
}

// Validate 检查拓扑、Go 绑定和所有参数，返回可直接展示的错误位置。
func Validate(p Project) []Diagnostic {
	var out []Diagnostic
	add := func(tree, node, field, message string) { out = append(out, Diagnostic{tree, node, field, message}) }
	if p.SchemaVersion != SchemaVersion {
		add("", "", "schemaVersion", "不支持的工程格式版本")
	}
	if !Identifier(p.Generation.Package) {
		add("", "", "generation.package", "目标包名必须为 Go 标识符")
	}
	contextType := strings.TrimPrefix(p.Generation.ContextType, "*")
	if !Identifier(contextType) {
		add("", "", "generation.contextType", "上下文类型必须为 any、类型名或 *类型名")
	}
	if obj := types.Universe.Lookup(contextType); obj != nil {
		if _, ok := obj.(*types.TypeName); !ok {
			add("", "", "generation.contextType", "上下文必须是类型，不能使用预声明常量或函数")
		}
	}
	if strings.ContainsAny(p.Generation.ContextImport, "\"\\\r\n\t ") || strings.HasPrefix(p.Generation.ContextImport, "/") || strings.Contains(p.Generation.ContextImport, "..") {
		add("", "", "generation.contextImport", "上下文导入路径无效")
	}
	if p.Generation.ContextImport != "" && contextType == "any" {
		add("", "", "generation.contextType", "导入上下文时需指定导出类型")
	}
	if p.Generation.ContextImport != "" && !ExportedIdentifier(contextType) {
		add("", "", "generation.contextType", "跨包上下文必须使用导出的 Go 类型")
	}
	symbols := map[string]bool{"NewProgram": true, "btStep": true, "btAbort": true, "btDuration": true, "bt": true, "time": true, "ctxpkg": true, "init": true, "f": true, "s": true, "phase": true, "node": true}
	if p.Generation.ContextImport == "" {
		symbols[contextType] = true
	}
	fields := map[string]Field{}
	names := map[string]bool{}
	for _, f := range p.Blackboard {
		if f.ID == "" {
			add("", "", "blackboard", "字段 ID 不能为空")
		}
		if _, ok := fields[f.ID]; ok {
			add("", "", "blackboard", "重复字段 ID: "+f.ID)
		}
		fields[f.ID] = f
		if !ExportedIdentifier(f.Name) || names[f.Name] {
			add("", "", "blackboard", "字段名称必须是唯一的导出 Go 标识符: "+f.Name)
		}
		names[f.Name] = true
		symbols["Get"+f.Name], symbols["Set"+f.Name] = true, true
		if f.Type == bt.EnumType && len(f.Enum) == 0 {
			add("", "", "blackboard", "枚举字段必须声明至少一个允许值: "+f.ID)
		}
		enumSeen := map[string]bool{}
		for _, value := range f.Enum {
			if enumSeen[value] {
				add("", "", "blackboard", "枚举允许值不能重复: "+f.ID)
			}
			enumSeen[value] = true
		}
		raw := f.Default
		if len(raw) == 0 {
			raw = Zero(f.Type, f.Enum)
		}
		if _, err := Literal(f.Type, raw, f.Enum); err != nil {
			add("", "", "blackboard", fmt.Sprintf("字段 %s: %v", f.ID, err))
		}
	}
	defs := map[string]Definition{}
	goNames := map[string]bool{}
	for _, d := range p.Catalog {
		if d.ID == "" {
			add("", "", "catalog", "节点目录 ID 不能为空")
		}
		if _, ok := defs[d.ID]; ok {
			add("", "", "catalog", "重复节点目录 ID: "+d.ID)
		}
		defs[d.ID] = d
		if !d.Kind.Valid() {
			add("", "", "catalog", "节点目录 kind 必须为 action 或 condition")
		}
		if !Identifier(d.GoName) || goNames[d.GoName] || symbols[d.GoName] || symbols[d.GoName+"Params"] || strings.HasPrefix(d.GoName, "btNode") || types.Universe.Lookup(d.GoName) != nil {
			add("", "", "catalog", "Go 函数名必须是唯一且非保留的标识符: "+d.GoName)
		}
		goNames[d.GoName] = true
		symbols[d.GoName], symbols[d.GoName+"Params"] = true, true
		paramNames := map[string]bool{}
		for _, param := range d.Params {
			if !ExportedIdentifier(param.Name) || paramNames[param.Name] {
				add("", "", "catalog", "参数名必须为唯一导出标识符: "+param.Name)
			}
			paramNames[param.Name] = true
			if !ValidType(param.Type) {
				add("", "", "catalog", "未知参数类型: "+param.Type.String())
			}
			if len(param.Default) > 0 {
				if _, err := Literal(param.Type, param.Default, param.Enum); err != nil {
					add("", "", "catalog", fmt.Sprintf("参数 %s: %v", param.Name, err))
				}
			}
		}
		for _, event := range d.Events {
			if event == "" {
				add("", "", "catalog", "事件名不能为空")
			}
		}
	}
	trees := map[string]Tree{}
	for _, tree := range p.Trees {
		if !ValidTreeID(tree.ID) {
			add(tree.ID, "", "id", "行为树 ID 不能为空，且只能包含英文字母、数字和下划线")
		}
		if _, ok := trees[tree.ID]; ok {
			add(tree.ID, "", "id", "重复树 ID")
		}
		trees[tree.ID] = tree
	}
	if len(p.Trees) == 0 {
		add("", "", "trees", "工程必须至少有一棵树")
	}
	for _, tree := range p.Trees {
		nodes := map[string]Node{}
		for _, n := range tree.Nodes {
			if n.ID == "" {
				add(tree.ID, n.ID, "id", "节点 ID 不能为空")
			}
			if _, ok := nodes[n.ID]; ok {
				add(tree.ID, n.ID, "id", "重复节点 ID")
			}
			nodes[n.ID] = n
		}
		if _, ok := nodes[tree.Root]; !ok {
			add(tree.ID, tree.Root, "root", "根节点不存在")
		}
		parents := map[string]int{}
		for _, n := range tree.Nodes {
			min, max := 0, 0
			switch n.Type {
			case NodeSequence, NodeSelector, NodeParallel, NodePriority:
				min, max = 1, -1
			case NodeRepeat, NodeRetry, NodeTimeout, NodeInverter, NodeSucceed, NodeFail:
				min, max = 1, 1
			case NodeAction, NodeCondition, NodeWait, NodeSubtree:
			default:
				add(tree.ID, n.ID, "type", "未知节点类型: "+n.Type.String())
			}
			if len(n.Children) < min || max >= 0 && len(n.Children) > max {
				add(tree.ID, n.ID, "children", "子节点数量不符合节点类型要求")
			}
			for _, id := range n.Children {
				parents[id]++
				if _, ok := nodes[id]; !ok {
					add(tree.ID, n.ID, "children", "子节点不存在: "+id)
				}
			}
			if n.Type == NodeRepeat || n.Type == NodeRetry {
				if n.Count <= 0 {
					add(tree.ID, n.ID, "count", "循环次数必须为正整数")
				}
			}
			if n.Type == NodeWait || n.Type == NodeTimeout {
				if n.DurationMS < 0 || n.DurationMS > math.MaxInt64/1000000 {
					add(tree.ID, n.ID, "durationMs", "时长必须为可表示的非负毫秒数")
				}
			}
			if n.Type == NodeSubtree {
				if _, ok := trees[n.Tree]; !ok {
					add(tree.ID, n.ID, "tree", "引用的子树不存在")
				}
			}
			if n.Type == NodePriority {
				for i, id := range n.Children {
					if i == len(n.Children)-1 {
						break
					}
					child, ok := nodes[id]
					if !ok {
						continue
					}
					if child.Type != NodeSequence || len(child.Children) == 0 || nodes[child.Children[0]].Type != NodeCondition {
						add(tree.ID, id, "children", "priority 除末尾候选外必须使用以 condition 开头的 sequence")
					}
				}
			}
			if n.Type == NodeAction || n.Type == NodeCondition {
				d, ok := defs[n.Binding]
				if !ok || !d.Kind.Matches(n.Type) {
					add(tree.ID, n.ID, "binding", "业务节点绑定不存在或种类不匹配")
					continue
				}
				params := map[string]Parameter{}
				for _, param := range d.Params {
					params[param.Name] = param
					v, exists := n.Params[param.Name]
					if !exists {
						if len(param.Default) == 0 {
							add(tree.ID, n.ID, "params."+param.Name, "缺少必填参数")
						}
						continue
					}
					if (v.Field != "") == (len(v.Value) > 0) {
						add(tree.ID, n.ID, "params."+param.Name, "参数必须选择字段绑定或常量中的一个")
						continue
					}
					if v.Field != "" {
						f, ok := fields[v.Field]
						if !ok || f.Type != param.Type {
							add(tree.ID, n.ID, "params."+param.Name, "黑板字段不存在或类型不匹配")
						}
						if ok && param.Type == bt.EnumType && len(param.Enum) > 0 {
							for _, choice := range f.Enum {
								found := false
								for _, allowed := range param.Enum {
									found = found || choice == allowed
								}
								if !found {
									add(tree.ID, n.ID, "params."+param.Name, "绑定枚举字段包含不允许的取值")
									break
								}
							}
							if len(f.Enum) == 0 {
								add(tree.ID, n.ID, "params."+param.Name, "受限枚举参数不能绑定不受限的枚举字段")
							}
						}
					} else if _, err := Literal(param.Type, v.Value, param.Enum); err != nil {
						add(tree.ID, n.ID, "params."+param.Name, err.Error())
					}
				}
				keys := make([]string, 0, len(n.Params))
				for name := range n.Params {
					keys = append(keys, name)
				}
				sort.Strings(keys)
				for _, name := range keys {
					if _, ok := params[name]; !ok {
						add(tree.ID, n.ID, "params."+name, "未知参数")
					}
				}
			} else if n.Binding != "" || len(n.Params) > 0 {
				add(tree.ID, n.ID, "binding", "只有 action/condition 可以绑定业务函数或参数")
			}
		}
		for _, n := range tree.Nodes {
			if parents[n.ID] > 1 {
				add(tree.ID, n.ID, "children", "节点不能有多个父节点")
			}
			if n.ID == tree.Root && parents[n.ID] > 0 {
				add(tree.ID, n.ID, "root", "根节点不能有父节点")
			}
		}
		color := map[string]int{}
		var visit func(string)
		visit = func(id string) {
			if color[id] == 1 {
				add(tree.ID, id, "children", "节点图存在环")
				return
			}
			if color[id] == 2 {
				return
			}
			n, ok := nodes[id]
			if !ok {
				return
			}
			color[id] = 1
			for _, child := range n.Children {
				visit(child)
			}
			color[id] = 2
		}
		visit(tree.Root)
		for _, n := range tree.Nodes {
			if color[n.ID] == 0 {
				add(tree.ID, n.ID, "children", "节点无法从根节点到达")
				visit(n.ID)
			}
		}
	}
	color := map[string]int{}
	var visitTree func(string)
	visitTree = func(id string) {
		if color[id] == 1 {
			add(id, "", "tree", "子树引用不能递归")
			return
		}
		if color[id] == 2 {
			return
		}
		color[id] = 1
		for _, n := range trees[id].Nodes {
			if n.Type == NodeSubtree {
				visitTree(n.Tree)
			}
		}
		color[id] = 2
	}
	for _, t := range p.Trees {
		visitTree(t.ID)
	}
	return out
}
