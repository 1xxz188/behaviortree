// Package codegen 将已校验元数据编译成原生 Go 控制流，运行时不解释 JSON。
package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// SourceLocation 关联节点的一次展开实例和生成 Go 源码位置。
type SourceLocation struct {
	TreeID       string `json:"treeId"`       // 原始树 ID。
	NodeID       string `json:"nodeId"`       // 原始节点 ID。
	File         string `json:"file"`         // 所属定义树的生成文件名。
	Index        int    `json:"index"`        // 本版本状态槽位；子树多次调用具有不同槽位。
	Line         int    `json:"line"`         // 生成文件中从 1 开始的行号。
	FunctionName string `json:"functionName"` // 实际生成函数名，包含展开实例的消歧后缀。
}

// GeneratedFile 是同一 Go 包中的一个生成文件。
type GeneratedFile struct {
	Name   string `json:"name"`   // 不含目录的安全文件名。
	TreeID string `json:"treeId"` // 定义树 ID；公共 glue 为空。
	Source []byte `json:"-"`      // 应写入文件的 Go 源码。
}

// Result 包含公共 glue、各定义树文件和可持久化的调试映射。
type Result struct {
	Files     []GeneratedFile  `json:"files"`     // glue 在首位，其余按定义树 ID 排序。
	SourceMap []SourceLocation `json:"sourceMap"` // 节点与源码映射。
	Version   string           `json:"version"`   // 不含画布布局的内容摘要。
}

// ValidationError 为 CLI 和 Web 保留全部可定位诊断。
type ValidationError struct{ Diagnostics []model.Diagnostic } // 生成前的全部校验错误。

// MaxExpandedNodes 限制子树复用后的总展开状态，避免很小的输入指数占用内存。
const MaxExpandedNodes = 100000

// Error 返回第一个诊断作为普通 Go 错误信息。
func (e *ValidationError) Error() string {
	return fmt.Sprintf("行为树校验失败 (%d): %s", len(e.Diagnostics), e.Diagnostics[0].Message)
}

// occurrence 是生成期间使用的一个独立节点展开实例。
type occurrence struct {
	node     model.Node // 原始节点。
	tree     string     // 定义树 ID。
	parent   int        // 父槽位。
	end      int        // 连续子树槽位终点（不含）。
	children []int      // 编译后的显式有序子槽位。
	symbol   string     // 由稳定身份生成的槽位常量名。
	function string     // 当前展开实例的静态节点函数名。
	identity string     // 入口树、引用链和原始节点身份的完整摘要。
	via      string     // 有界可读引用链；独立入口为空，深链使用固定长度摘要。
	chain    string     // 引用链完整摘要，用于检测短摘要冲突。
}

// generator 只存在于代码生成阶段，不进入游戏运行时。
type generator struct {
	buf            bytes.Buffer                // 生成文本。
	p              model.Project               // 规范化的工程。
	nodes          []occurrence                // 前序展开后的节点。
	roots          map[string]int              // 各独立树的入口。
	defs           map[string]model.Definition // 业务函数声明。
	fields         map[string]int              // 黑板字段槽位。
	context        string                      // 生成的上下文类型表达式。
	countSymbol    string                      // 节点总数及最后一个子树的排他上界常量。
	noParentSymbol string                      // 根节点无父槽位的哨兵常量。
}

// Generate 按定义树生成确定性的多个 Go 文件，不读写手写业务文件。
func Generate(project model.Project) (Result, error) {
	project = model.WithCodeNames(project)
	if d := model.Validate(project); len(d) > 0 {
		return Result{}, &ValidationError{d}
	}
	if err := checkExpandedSize(project); err != nil {
		return Result{}, err
	}
	p, version, err := normalizeProject(project)
	if err != nil {
		return Result{}, err
	}
	g := &generator{p: p, roots: map[string]int{}, defs: map[string]model.Definition{}, fields: map[string]int{}, context: p.Generation.ContextType}
	if p.Generation.ContextImport != "" {
		g.context = "ctxpkg." + strings.TrimPrefix(g.context, "*")
		if strings.HasPrefix(p.Generation.ContextType, "*") {
			g.context = "*" + g.context
		}
	}
	for _, d := range p.Catalog {
		g.defs[d.ID] = d
	}
	for i, f := range p.Blackboard {
		g.fields[f.ID] = i
	}
	trees := map[string]model.Tree{}
	indices := map[string]map[string]model.Node{}
	for _, t := range p.Trees {
		trees[t.ID] = t
		indices[t.ID] = map[string]model.Node{}
		for _, n := range t.Nodes {
			indices[t.ID][n.ID] = n
		}
	}
	var expand func(string, string, int, string, string, bool) int
	expand = func(tree, id string, parent int, chain, readable string, referenced bool) int {
		index := len(g.nodes)
		n := indices[tree][id]
		via := ""
		if referenced {
			via = "_Via" + readable
			if readable == "" {
				via = "_Via_" + chain[:12]
			}
		}
		g.nodes = append(g.nodes, occurrence{node: n, tree: tree, parent: parent, identity: identityHash(chain, tree, id), via: via, chain: chain})
		var children []int
		if n.Type == model.NodeSubtree {
			childTree := trees[n.Tree]
			// 引用链逐层摘要，避免深层展开反复复制整条路径。
			nextReadable := ""
			callName := symbolPart(n.CodeName)
			// 可读链一旦超过上限就只传摘要，后续层级不再复制或恢复长路径。
			if readable != "" && len(readable)+1+len(callName) <= 120 {
				nextReadable = readable + "_" + callName
			}
			children = append(children, expand(childTree.ID, childTree.Root, index, identityHash(chain, tree, id, childTree.ID), nextReadable, true))
		} else {
			for _, child := range n.Children {
				children = append(children, expand(tree, child, index, chain, readable, referenced))
			}
		}
		g.nodes[index].children = children
		g.nodes[index].end = len(g.nodes)
		return index
	}
	for _, t := range p.Trees {
		g.roots[t.ID] = expand(t.ID, t.Root, -1, identityHash(t.ID), treeSymbolPart(t.ID), false)
	}
	if err := g.assignSymbols(); err != nil {
		return Result{}, err
	}
	g.emitHeader(true)
	// 时长别名总是声明，保证没有计时节点的工程也不会产生未使用导入。
	g.line("type btDuration = time.Duration")
	g.line("// 编译槽位仅用于本版本运行时；稳定身份保留在节点元数据中。")
	g.line("const (")
	for i, n := range g.nodes {
		g.line("// %s 对应树 %q 的节点 %q（显示名 %q）。", n.symbol, n.tree, n.node.ID, n.node.Name)
		if i == 0 {
			g.line("%s = iota", n.symbol)
		} else {
			g.line("%s", n.symbol)
		}
	}
	g.line("// %s 是所有节点槽位的排他上界。", g.countSymbol)
	g.line("%s", g.countSymbol)
	g.line(")")
	g.line("// %s 表示独立树入口没有父节点。", g.noParentSymbol)
	g.line("const %s = -1", g.noParentSymbol)
	for _, d := range p.Catalog {
		g.line("// %sParams 是业务函数的强类型参数。", d.GoName)
		g.line("type %sParams struct {", d.GoName)
		for _, param := range d.Params {
			g.line("// %s 由常量或黑板字段绑定提供。", param.Name)
			g.line("%s %s", param.Name, goType(param.Type))
		}
		g.line("}")
	}
	for i, f := range p.Blackboard {
		g.line("// Get%s 按编译后槽位读取黑板字段。", f.Name)
		g.line("func Get%s(f *bt.Frame[%s]) %s { return f.Board.%s(%d) }", f.Name, g.context, goType(f.Type), boardMethod(f.Type), i)
		g.line("// Set%s 在值变化时通知依赖条件。", f.Name)
		if f.Type == bt.EnumType {
			// 允许值编译为静态分支，避免热路径遍历元数据或建立 map。
			g.line("func Set%s(f *bt.Frame[%s], value string) {switch value {", f.Name, g.context)
			choices := make([]string, len(f.Enum))
			for n, value := range f.Enum {
				choices[n] = strconv.Quote(value)
			}
			g.line("case %s:f.Board.SetEnum(%d,value)", strings.Join(choices, ","), i)
			g.line("default:panic(%q)}}", "invalid enum value for field "+f.ID)
		} else {
			g.line("func Set%s(f *bt.Frame[%s], value %s) { f.Board.Set%s(%d,value) }", f.Name, g.context, goType(f.Type), boardMethod(f.Type), i)
		}
	}
	g.line("// NewProgram 创建本版本共享的不可变程序，空版本名使用内容摘要。")
	g.line("func NewProgram(version string) *bt.Program[%s] {", g.context)
	g.line("if version == \"\" { version = %q }", version)
	g.line("return &bt.Program[%s]{Version:version, Roots:map[string]int{", g.context)
	for _, t := range p.Trees {
		g.line("%q:%s,", t.ID, g.slot(g.roots[t.ID]))
	}
	g.line("}, Nodes:[]bt.Node{")
	for _, n := range g.nodes {
		g.line("{ID:%q, TreeID:%q, Parent:%s, End:%s},", n.node.ID, n.tree, g.slot(n.parent), g.slot(n.end))
	}
	g.line("},Fields:[]bt.Field{")
	for _, f := range p.Blackboard {
		raw := f.Default
		if len(raw) == 0 {
			raw = model.Zero(f.Type, f.Enum)
		}
		g.line("{ID:%q,Name:%q,Type:bt.%s,Default:bt.Value{%s:%s},Enum:%s},", f.ID, f.Name, runtimeType(f.Type), valueMember(f.Type), literal(f.Type, raw), stringSlice(f.Enum))
	}
	g.line("},Dependencies:map[string][]int{")
	dependencies := map[string][]int{}
	for i, n := range g.nodes {
		if n.node.Type != model.NodeAction && n.node.Type != model.NodeCondition {
			continue
		}
		d := g.defs[n.node.Binding]
		seen := map[string]bool{}
		for _, event := range d.Events {
			seen["event:"+event] = true
		}
		for _, param := range n.node.Params {
			if param.Field != "" {
				seen["field:"+param.Field] = true
			}
		}
		for key := range seen {
			dependencies[key] = append(dependencies[key], i)
		}
	}
	keys := make([]string, 0, len(dependencies))
	for key := range dependencies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		names := make([]string, len(dependencies[key]))
		for i, index := range dependencies[key] {
			names[i] = g.slot(index)
		}
		g.line("%q:[]int{%s},", key, strings.Join(names, ","))
	}
	g.line("},Step:btStep,Abort:btAbort}")
	g.line("}")
	g.line("// btStep 通过整数槽位分派到编译后的节点函数。")
	g.line("func btStep(f *bt.Frame[%s], node int) bt.Status { switch node {", g.context)
	for i := range g.nodes {
		g.line("case %s:return %s(f)", g.slot(i), g.nodes[i].function)
	}
	g.line("};return bt.Failure }")
	g.line("// btAbort 仅清理当前动作；运行时负责活跃子树取消和状态失效。")
	g.line("func btAbort(f *bt.Frame[%s], node int) {switch node {", g.context)
	for i, n := range g.nodes {
		if n.node.Type == model.NodeAction {
			g.line("case %s: %s(f,%s,bt.Abort,%s)", g.slot(i), g.defs[n.node.Binding].GoName, g.slot(i), g.params(n.node))
		}
	}
	g.line("}}")
	source, err := format.Source(g.buf.Bytes())
	if err != nil {
		return Result{}, fmt.Errorf("格式化生成代码: %w", err)
	}
	files := []GeneratedFile{{Name: "glue.gen.go", Source: source}}
	// 一次分组全部展开实例，各定义树只处理自己的节点。
	groups := make(map[string][]int, len(p.Trees))
	for i, n := range g.nodes {
		groups[n.tree] = append(groups[n.tree], i)
	}
	mapping := make([]SourceLocation, len(g.nodes))
	fileNames := make(map[string]bool, len(p.Trees))
	for _, tree := range p.Trees {
		name := TreeFileName(tree.ID)
		if fileNames[strings.ToLower(name)] {
			return Result{}, fmt.Errorf("生成文件名冲突: %s", name)
		}
		fileNames[strings.ToLower(name)] = true
		indices := groups[tree.ID]
		sort.Slice(indices, func(i, j int) bool { return g.nodes[indices[i]].identity < g.nodes[indices[j]].identity })
		withTime := false
		for _, i := range indices {
			n := g.nodes[i].node
			if (n.Type == model.NodeWait || n.Type == model.NodeTimeout) && n.DurationMS != 0 {
				withTime = true
				break
			}
		}
		g.buf.Reset()
		g.emitHeader(withTime)
		for _, i := range indices {
			g.emitNode(i)
		}
		source, err := format.Source(g.buf.Bytes())
		if err != nil {
			return Result{}, fmt.Errorf("格式化生成文件 %s: %w", name, err)
		}
		files = append(files, GeneratedFile{Name: name, TreeID: tree.ID, Source: source})
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, name, source, parser.SkipObjectResolution)
		if err != nil {
			return Result{}, err
		}
		lines := make(map[string]int, len(indices))
		for _, d := range parsed.Decls {
			if f, ok := d.(*ast.FuncDecl); ok {
				lines[f.Name.Name] = fset.Position(f.Pos()).Line
			}
		}
		for _, i := range indices {
			n := g.nodes[i]
			mapping[i] = SourceLocation{TreeID: n.tree, NodeID: n.node.ID, File: name, Index: i, Line: lines[n.function], FunctionName: n.function}
		}
	}
	return Result{Files: files, SourceMap: mapping, Version: version}, nil
}

// emitHeader 只导入当前文件实际使用的包，公共时长别名由 glue 提供。
func (g *generator) emitHeader(withTime bool) {
	g.line("// Code generated by behaviortree/codegen; DO NOT EDIT.")
	g.line("// 本文件使用原生 Go 分支与调用；业务函数请写在同包独立文件中。")
	g.line("package %s", g.p.Generation.Package)
	g.line("import (bt %q", "github.com/1xxz188/behaviortree")
	if withTime {
		g.line("\"time\"")
	}
	if g.p.Generation.ContextImport != "" {
		g.line("ctxpkg %q", g.p.Generation.ContextImport)
	}
	g.line(")")
}

// identityHash 使用长度前缀区分任意身份片段，返回固定大小的链式摘要。
func identityHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(h, "%d:", len(part))
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TreeFileName 使用已校验的树 ID 生成可读文件名，调用方须先检查 ID 和大小写冲突。
func TreeFileName(id string) string {
	return "tree_" + id + ".gen.go"
}

// line 追加一行可格式化的 Go 文本。
func (g *generator) line(format string, args ...any) { fmt.Fprintf(&g.buf, format+"\n", args...) }

// symbolPart 在内部引用链中转义下划线，最终显示名称由 readableSymbol 整理。
func symbolPart(id string) string {
	return strings.ReplaceAll(id, "_", "__")
}

// readableSymbol 单遍合并连续下划线并去掉首尾分隔符，保留可读名称的大小写。
func readableSymbol(name string) string {
	var result strings.Builder
	result.Grow(len(name))
	separator := false
	for i := 0; i < len(name); i++ {
		if name[i] == '_' {
			separator = result.Len() > 0
			continue
		}
		if separator {
			result.WriteByte('_')
			separator = false
		}
		result.WriteByte(name[i])
	}
	return result.String()
}

// treeSymbolPart 仅提升树 ID 首字母；树 ID 的大小写折叠唯一校验保证不会合并不同树。
func treeSymbolPart(id string) string {
	if len(id) > 0 && id[0] >= 'a' && id[0] <= 'z' {
		id = string(id[0]-'a'+'A') + id[1:]
	}
	return symbolPart(id)
}

// assignSymbols 在生成阶段一次分配全部名称，运行时只保留编译后的整数。
func (g *generator) assignSymbols() error {
	used := map[string]bool{"NewProgram": true, "btStep": true, "btAbort": true, "btDuration": true, "bt": true, "time": true, "ctxpkg": true}
	if g.p.Generation.ContextImport == "" {
		used[strings.TrimPrefix(g.context, "*")] = true
	}
	for _, d := range g.p.Catalog {
		used[d.GoName], used[d.GoName+"Params"] = true, true
	}
	for _, f := range g.p.Blackboard {
		used["Get"+f.Name], used["Set"+f.Name] = true, true
	}
	// 边界符号也避开现有上下文类型与业务声明，保证生成包的标识符唯一。
	claim := func(name string) string {
		for used[name] {
			name += "_Generated"
		}
		used[name] = true
		return name
	}
	g.countSymbol = claim("btNodeCount")
	g.noParentSymbol = claim("btNodeNoParent")
	identities := make(map[string]bool, len(g.nodes))
	shortChains := make(map[string]string)
	bases := make([]string, len(g.nodes))
	baseCounts := make(map[string]int, len(g.nodes))
	allocated := make(map[string]bool, len(g.nodes)*2)
	for i, n := range g.nodes {
		if identities[n.identity] {
			return fmt.Errorf("节点实例身份重复: %s", n.identity)
		}
		identities[n.identity] = true
		if strings.HasPrefix(n.via, "_Via_") && !strings.HasPrefix(n.via, "_Via__") {
			if prior, exists := shortChains[n.via]; exists && prior != n.chain {
				return fmt.Errorf("引用链摘要冲突 %s: %s 与 %s", n.via, prior, n.chain)
			}
			shortChains[n.via] = n.chain
		}
		base := readableSymbol(treeSymbolPart(n.tree) + "_" + symbolPart(n.node.CodeName) + n.via)
		bases[i] = base
		baseCounts[base]++
	}
	// 先收集再消歧，碰撞组统一使用稳定身份摘要，避免名称依赖节点遍历顺序。
	for i, n := range g.nodes {
		base := bases[i]
		if baseCounts[base] > 1 {
			base += "_ID" + identityHash(n.identity)[:12]
		}
		suffix := ""
		// 业务声明碰撞只增加固定后缀，不依赖全局槽位或展开次数。
		for used["node"+base+suffix] || used["btNode"+base+suffix] {
			suffix += "_Generated"
		}
		g.nodes[i].symbol, g.nodes[i].function = "node"+base+suffix, "btNode"+base+suffix
		if allocated[g.nodes[i].symbol] || allocated[g.nodes[i].function] {
			return fmt.Errorf("业务消歧后的节点代码名冲突: %s", base+suffix)
		}
		allocated[g.nodes[i].symbol], allocated[g.nodes[i].function] = true, true
	}
	return nil
}

// slot 将已编译槽位和边界转换为语义常量，保留根哨兵及排他上界。
func (g *generator) slot(index int) string {
	if index < 0 {
		return g.noParentSymbol
	}
	if index == len(g.nodes) {
		return g.countSymbol
	}
	return g.nodes[index].symbol
}

// typeDescription 集中保存一种值类型的生成规则，避免多个分支映射漂移。
type typeDescription struct {
	goName      string // 生成的 Go 类型。
	boardMethod string // 黑板槽位访问器。
	runtimeName string // 公共运行时常量名称。
	member      string // 公共 Value 的存储成员。
}

// typeDescriptions 通过枚举索引直接查询全部支持的类型。
var typeDescriptions = [...]typeDescription{
	bt.BoolType:     {"bool", "Bool", "BoolType", "Bool"},
	bt.IntType:      {"int64", "Int", "IntType", "Int"},
	bt.UIntType:     {"uint64", "Uint", "UIntType", "Uint"},
	bt.FloatType:    {"float64", "Float", "FloatType", "Float"},
	bt.StringType:   {"string", "String", "StringType", "String"},
	bt.EnumType:     {"string", "Enum", "EnumType", "String"},
	bt.EntityIDType: {"uint64", "EntityID", "EntityIDType", "EntityID"},
	bt.DurationType: {"btDuration", "Duration", "DurationType", "Duration"},
}

// describeType 拒绝遗漏或非法类型；调用前工程已经完成类型校验。
func describeType(t bt.ValueType) typeDescription {
	if !t.Valid() || int(t) >= len(typeDescriptions) || typeDescriptions[t].goName == "" {
		panic(fmt.Sprintf("unsupported value type %v", t))
	}
	return typeDescriptions[t]
}

// goType 返回静态 Go 类型名称。
func goType(t bt.ValueType) string { return describeType(t).goName }

// boardMethod 返回直接槽位访问器名称。
func boardMethod(t bt.ValueType) string { return describeType(t).boardMethod }

// runtimeType 返回公共运行时类型常量名称。
func runtimeType(t bt.ValueType) string { return describeType(t).runtimeName }

// valueMember 返回公共 Value 的强类型存储成员。
func valueMember(t bt.ValueType) string { return describeType(t).member }

// literal 将已校验常量转换为无注入可能的 Go 字面量。
func literal(t bt.ValueType, raw json.RawMessage) string {
	value, _ := model.Literal(t, raw, nil)
	switch v := value.(type) {
	case string:
		return strconv.Quote(v)
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		panic("validated literal has unsupported type")
	}
}

// stringSlice 生成字符串常量切片。
func stringSlice(values []string) string {
	var out strings.Builder
	out.WriteString("[]string{")
	for _, v := range values {
		out.WriteString(strconv.Quote(v))
		out.WriteByte(',')
	}
	out.WriteByte('}')
	return out.String()
}

// params 构造直接传给业务函数的强类型参数，不经反射或动态字典。
func (g *generator) params(n model.Node) string {
	d := g.defs[n.Binding]
	var out strings.Builder
	out.WriteString(d.GoName + "Params{")
	for _, p := range d.Params {
		v, ok := n.Params[p.Name]
		out.WriteString(p.Name + ":")
		if ok && v.Field != "" {
			fmt.Fprintf(&out, "f.Board.%s(%d)", boardMethod(p.Type), g.fields[v.Field])
		} else {
			raw := v.Value
			if !ok {
				raw = p.Default
			}
			out.WriteString(literal(p.Type, raw))
		}
		out.WriteByte(',')
	}
	out.WriteByte('}')
	return out.String()
}

// emitNode 生成一个节点的真实分支、续跑游标和静态子函数调用。
func (g *generator) emitNode(i int) {
	n := g.nodes[i]
	g.line("// %s 执行树 %q 中的节点 %q（显示名 %q）。", n.function, n.tree, n.node.ID, n.node.Name)
	g.line("func %s(f *bt.Frame[%s]) bt.Status {", n.function, g.context)
	g.line("const node = %s", n.symbol)
	g.line("if cached,run:=f.Enter(node);!run{return cached}")
	g.line("s:=f.State(node); _=s")
	exit := func(status string) { g.line("return f.Exit(node,%s)", status) }
	switch n.node.Type {
	case model.NodeAction:
		g.line("phase:=bt.Start;if s.Started{phase=bt.Resume};s.Started=true")
		exit(fmt.Sprintf("%s(f,node,phase,%s)", g.defs[n.node.Binding].GoName, g.params(n.node)))
	case model.NodeCondition:
		g.line("if %s(f,node,%s) { return f.Exit(node,bt.Success) }", g.defs[n.node.Binding].GoName, g.params(n.node))
		exit("bt.Failure")
	case model.NodeSequence, model.NodeSelector:
		// 游标是函数内部的直接孩子序号，使用数字即可；全局节点槽位仍使用具名常量。
		g.line("for { switch s.Cursor {")
		for j, child := range n.children {
			g.line("case %d: status:=%s(f);if status==bt.Running{return f.Exit(node,status)}", j, g.nodes[child].function)
			terminal := "bt.Failure"
			if n.node.Type == model.NodeSelector {
				terminal = "bt.Success"
			}
			g.line("if status==%s{s.Cursor=0;return f.Exit(node,status)};s.Cursor++", terminal)
		}
		g.line("default:s.Cursor=0;")
		if n.node.Type == model.NodeSequence {
			exit("bt.Success")
		} else {
			exit("bt.Failure")
		}
		g.line("}}")
	case model.NodePriority:
		// 只有当前分支被唤醒且其首守卫未变时，直接续跑，避免探测所有高优先守卫。
		g.line("if s.Count!=0&&f.State(s.Cursor).Status==bt.Running&&f.OnlyDirtyChild(node,s.Cursor){guardDirty:=false;switch s.Cursor{")
		for _, child := range n.children {
			candidate := g.nodes[child]
			if candidate.node.Type == model.NodeSequence && len(candidate.children) > 0 && g.nodes[candidate.children[0]].node.Type == model.NodeCondition {
				g.line("case %s:guardDirty=f.IsDirty(%s)", g.slot(child), g.slot(candidate.children[0]))
			}
		}
		g.line("};if !guardDirty{status:=btStep(f,s.Cursor);if status!=bt.Failure{return f.Exit(node,status)};s.Count=0}}")
		// 先仅求值 guard，确定候选后再取消旧分支，避免新旧动作交叠。
		for j, child := range n.children {
			candidate := g.nodes[child]
			guard := -1
			if candidate.node.Type == model.NodeSequence && len(candidate.children) > 0 && g.nodes[candidate.children[0]].node.Type == model.NodeCondition {
				guard = candidate.children[0]
			}
			if guard >= 0 {
				g.line("previousGuard%d:=f.State(%s).Status;guard%d:=%s(f);if guard%d==bt.Running{return f.Exit(node,bt.Running)}", j, g.slot(guard), j, g.nodes[guard].function, j)
				// 失败候选只在守卫从真变假后重新待命，不因低优先动作事件反复重试。
				g.line("if previousGuard%d==bt.Success&&guard%d==bt.Failure&&f.State(%s).Status==bt.Failure{f.Reset(%s)}", j, j, g.slot(child), g.slot(child))
				g.line("if guard%d==bt.Success {", j)
			} else {
				g.line("{")
			}
			g.line("if s.Count!=0&&s.Cursor!=%s {f.Abort(s.Cursor,\"priority preempted\")};s.Count=1;s.Cursor=%s", g.slot(child), g.slot(child))
			g.line("status:=%s(f);if status!=bt.Failure {return f.Exit(node,status)};s.Count=0", g.nodes[child].function)
			g.line("}")
		}
		g.line("if s.Count!=0 {f.Abort(s.Cursor,\"priority guard failed\")};s.Count=0")
		exit("bt.Failure")
	case model.NodeParallel:
		// 首次按显式顺序启动全部分支；预算不足时保留尚未启动的位置。
		g.line("for s.Cursor<%d {var child int;var status bt.Status;switch s.Cursor {", len(n.children))
		for j, child := range n.children {
			g.line("case %d:child=%s;status=%s(f)", j, g.slot(child), g.nodes[child].function)
		}
		g.line("};if status==bt.Running&&f.State(child).Status==bt.Invalid{return f.Exit(node,bt.Running)}")
		g.line("if status==bt.Failure{f.AbortChildren(node,\"parallel failed\");return f.Exit(node,bt.Failure)};if status==bt.Success{s.Count++};s.Cursor++}")
		// 后续仅取实际收到通知的直接分支，完成分支在本轮保持终态。
		g.line("for child:=f.PopDirtyChild(node);child>=0;child=f.PopDirtyChild(node){if f.State(child).Status==bt.Success{continue};var status bt.Status;switch child{")
		for _, child := range n.children {
			g.line("case %s:status=%s(f)", g.slot(child), g.nodes[child].function)
		}
		g.line("};if status==bt.Failure{f.AbortChildren(node,\"parallel failed\");return f.Exit(node,bt.Failure)};if status==bt.Success{s.Count++}}")
		g.line("if s.Count==%d{return f.Exit(node,bt.Success)}", len(n.children))
		exit("bt.Running")
	case model.NodeRepeat, model.NodeRetry:
		child := n.children[0]
		g.line("for s.Count<%d {status:=%s(f);if status==bt.Running{return f.Exit(node,status)}", n.node.Count, g.nodes[child].function)
		terminal := "bt.Failure"
		finish := "bt.Success"
		if n.node.Type == model.NodeRetry {
			terminal, finish = "bt.Success", "bt.Failure"
		}
		g.line("if status==%s{return f.Exit(node,status)};s.Count++;if s.Count<%d{f.Reset(%s)}", terminal, n.node.Count, g.slot(child))
		g.line("}")
		exit(finish)
	case model.NodeWait:
		if n.node.DurationMS == 0 {
			exit("bt.Success")
		} else {
			g.line("if s.Ready {f.Consume(node);return f.Exit(node,bt.Success)}")
			g.line("if !s.Started{s.Started=true;f.After(node,time.Duration(%d)*time.Millisecond)}", n.node.DurationMS)
			exit("bt.Running")
		}
	case model.NodeTimeout:
		child := n.children[0]
		if n.node.DurationMS == 0 {
			g.line("f.Abort(%s,\"timeout\")", g.slot(child))
			exit("bt.Failure")
		} else {
			g.line("if s.Ready {f.Consume(node);f.Abort(%s,\"timeout\");return f.Exit(node,bt.Failure)}", g.slot(child))
			g.line("if !s.Started{s.Started=true;f.After(node,time.Duration(%d)*time.Millisecond)}", n.node.DurationMS)
			exit(fmt.Sprintf("%s(f)", g.nodes[child].function))
		}
	case model.NodeSubtree:
		exit(fmt.Sprintf("%s(f)", g.nodes[n.children[0]].function))
	case model.NodeInverter, model.NodeSucceed, model.NodeFail:
		g.line("status:=%s(f);if status==bt.Running{return f.Exit(node,status)}", g.nodes[n.children[0]].function)
		switch n.node.Type {
		case model.NodeInverter:
			g.line("if status==bt.Success{return f.Exit(node,bt.Failure)}")
			exit("bt.Success")
		case model.NodeSucceed:
			exit("bt.Success")
		case model.NodeFail:
			exit("bt.Failure")
		}
	}
	g.line("}")
}

// checkExpandedSize 先在无环树引用图上累计大小，超限前不创建任何展开节点。
func checkExpandedSize(p model.Project) error {
	trees := make(map[string]model.Tree, len(p.Trees))
	sizes := make(map[string]int, len(p.Trees))
	for _, t := range p.Trees {
		trees[t.ID] = t
	}
	var size func(string) int
	size = func(id string) int {
		if n, ok := sizes[id]; ok {
			return n
		}
		n := len(trees[id].Nodes)
		for _, node := range trees[id].Nodes {
			if node.Type == model.NodeSubtree {
				child := size(node.Tree)
				if n > MaxExpandedNodes-child {
					sizes[id] = MaxExpandedNodes + 1
					return MaxExpandedNodes + 1
				}
				n += child
			}
		}
		sizes[id] = n
		return n
	}
	total := 0
	for _, tree := range p.Trees {
		n := size(tree.ID)
		if n > MaxExpandedNodes-total {
			return &ValidationError{[]model.Diagnostic{{TreeID: tree.ID, Field: "trees", Message: fmt.Sprintf("子树展开超过 %d 个节点，请拆分工程或减少重复引用", MaxExpandedNodes)}}}
		}
		total += n
	}
	return nil
}

// ProjectVersion 计算与生成器一致的语义版本，忽略树显示名称和布局且允许未连完的草稿。
func ProjectVersion(project model.Project) (string, error) {
	_, version, err := normalizeProject(project)
	return version, err
}

// normalizeProject 深拷贝并规范化集合，统一预览、产物和草稿的版本判定。
func normalizeProject(project model.Project) (model.Project, string, error) {
	project = model.WithCodeNames(project)
	// 经 JSON 深拷贝后规范化集合，避免修改调用者的工程或画布。
	raw, err := json.Marshal(project)
	if err != nil {
		return model.Project{}, "", err
	}
	var p model.Project
	if err = json.Unmarshal(raw, &p); err != nil {
		return model.Project{}, "", err
	}
	sort.Slice(p.Trees, func(i, j int) bool { return p.Trees[i].ID < p.Trees[j].ID })
	sort.Slice(p.Blackboard, func(i, j int) bool { return p.Blackboard[i].ID < p.Blackboard[j].ID })
	sort.Slice(p.Catalog, func(i, j int) bool { return p.Catalog[i].ID < p.Catalog[j].ID })
	for i := range p.Trees {
		// 名称仅用于展示；只清除深拷贝中的名称，持久化仍保留原值。
		p.Trees[i].Name = ""
		p.Trees[i].Layout = nil
		sort.Slice(p.Trees[i].Nodes, func(a, b int) bool { return p.Trees[i].Nodes[a].ID < p.Trees[i].Nodes[b].ID })
	}
	for i := range p.Catalog {
		sort.Slice(p.Catalog[i].Params, func(a, b int) bool { return p.Catalog[i].Params[a].Name < p.Catalog[i].Params[b].Name })
		sort.Strings(p.Catalog[i].Events)
	}
	raw, _ = json.Marshal(p)
	sum := sha256.Sum256(raw)
	version := hex.EncodeToString(sum[:16])
	return p, version, nil
}
