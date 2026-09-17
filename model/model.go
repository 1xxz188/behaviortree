// Package model 定义编辑器、命令行和代码生成器共享的行为树元数据。
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	bt "github.com/1xxz188/behaviortree"
)

// SchemaVersion 是当前支持的工程格式版本。
const SchemaVersion = 1

// Project 是可独立导入、导出的行为树工程；布局不参与运行语义。
type Project struct {
	SchemaVersion int          `json:"schemaVersion"` // 格式版本。
	Name          string       `json:"name"`          // 工程显示名称。
	Blackboard    []Field      `json:"blackboard"`    // 全工程共享的黑板布局。
	Catalog       []Definition `json:"catalog"`       // Go 业务节点目录。
	Trees         []Tree       `json:"trees"`         // 可独立启动或引用的树。
	Generation    Generation   `json:"generation"`    // Go 生成目标。
}

// Generation 指定手写节点与生成代码所在的同一个 Go 包。
type Generation struct {
	PackagePath   string `json:"packagePath"`             // 相对工程目录的生成路径，末级目录决定 Go 包名。
	ContextImport string `json:"contextImport,omitempty"` // 稳定业务上下文的导入路径。
	ContextType   string `json:"contextType"`             // any、类型名或 *类型名；导入别名由生成器管理。
}

// Field 用稳定 ID 标识热更前后的黑板字段；Name 可以变更。
type Field struct {
	ID      string          `json:"id"`                // 跨版本稳定的字段 ID。
	Name    string          `json:"name"`              // Go 导出访问器名称。
	Type    bt.ValueType    `json:"type"`              // bool/int64/uint64/float64/string/enum/entity/duration。
	Default json.RawMessage `json:"default,omitempty"` // JSON 默认值；duration 支持 Go 时长字符串及旧纳秒整数。
	Enum    []string        `json:"enum,omitempty"`    // 枚举字段必须提供非空允许值。
}

// Definition 是由程序员用 Go 声明的业务节点目录项。
type Definition struct {
	ID     string         `json:"id"`               // 编辑器绑定使用的稳定 ID。
	Name   string         `json:"name"`             // 节点面板显示名称。
	Kind   DefinitionKind `json:"kind"`             // action 或 condition。
	GoName string         `json:"goName"`           // 同包手写函数名称。
	Params []Parameter    `json:"params"`           // 强类型参数声明。
	Events []string       `json:"events,omitempty"` // 需要唤醒或重新求值的宿主事件。
}

// Parameter 定义生成的 <GoName>Params 结构体成员。
type Parameter struct {
	Name    string          `json:"name"`              // Go 导出成员名称。
	Comment string          `json:"comment,omitempty"` // 生成到参数结构体成员上的业务注释，可留空。
	Type    bt.ValueType    `json:"type"`              // 与黑板字段相同的值类型。
	Default json.RawMessage `json:"default,omitempty"` // 未填写时使用的默认常量；duration 支持 Go 时长字符串及纳秒整数。
	Enum    []string        `json:"enum,omitempty"`    // 枚举的允许值。
}

// Value 恰好包含字段绑定或 JSON 常量中的一个。
type Value struct {
	Field string          `json:"field,omitempty"` // 黑板字段稳定 ID。
	Value json.RawMessage `json:"value,omitempty"` // 强类型 JSON 常量。
}

// Tree 以 ID 作为运行时主键，并使用显式 Children 顺序定义优先级。
type Tree struct {
	ID     string              `json:"id"`               // 稳定 ID；引用区分大小写，工程内也禁止仅大小写不同的 ID，修改需同步引用。
	Name   string              `json:"name"`             // 允许重复的显示名称，不参与运行时版本。
	Root   string              `json:"root"`             // 根节点 ID。
	Nodes  []Node              `json:"nodes"`            // 节点集合，数组顺序无语义。
	Layout map[string]Position `json:"layout,omitempty"` // 单独保存的画布坐标。
}

// Position 仅影响编辑器布局。
type Position struct {
	X float64 `json:"x"` // 横坐标。
	Y float64 `json:"y"` // 纵坐标。
}

// Node 是内建节点或 Go 业务节点的一次使用。
type Node struct {
	ID         string           `json:"id"`                   // 树内稳定节点 ID。
	CodeName   string           `json:"codeName,omitempty"`   // 树内唯一的持久化代码名，与显示名称和运行身份独立。
	Type       NodeType         `json:"type"`                 // 内建节点种类。
	Name       string           `json:"name,omitempty"`       // 可选显示名。
	Children   []string         `json:"children,omitempty"`   // 有序子节点 ID。
	Binding    string           `json:"binding,omitempty"`    // 业务节点目录 ID。
	Params     map[string]Value `json:"params,omitempty"`     // 参数常量或字段绑定。
	Tree       string           `json:"tree,omitempty"`       // subtree 引用的树 ID。
	Count      int              `json:"count,omitempty"`      // repeat/retry 的有限次数。
	DurationMS int64            `json:"durationMs,omitempty"` // wait/timeout 的非负毫秒数。
}

// Diagnostic 定位可在画布上显示的校验错误。
type Diagnostic struct {
	TreeID  string `json:"treeId,omitempty"` // 所属树。
	NodeID  string `json:"nodeId,omitempty"` // 所属节点。
	Field   string `json:"field,omitempty"`  // 错误属性。
	Message string `json:"message"`          // 中文错误说明。
}

// Decode 严格读取单个工程，防止拼错的属性被静默丢弃。
func Decode(data []byte) (Project, error) {
	var p Project
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, fmt.Errorf("读取行为树工程: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("工程后含多余 JSON 数据")
	}
	if err := validateDecodedTypes(p); err != nil {
		return p, err
	}
	p.Generation.PackagePath = NormalizePackagePath(p.Generation.PackagePath)
	return WithCodeNames(p), nil
}

// Encode 保留稳定 ID、显式顺序以及编辑布局。
func Encode(p Project) ([]byte, error) {
	p.Generation.PackagePath = NormalizePackagePath(p.Generation.PackagePath)
	return json.MarshalIndent(WithCodeNames(p), "", "  ")
}

// ExportCatalog 将手写 Go 声明导出为 Web 可读取的节点目录。
func ExportCatalog(definitions []Definition) ([]byte, error) {
	p := Example()
	p.Catalog = definitions
	if err := validateDecodedTypes(p); err != nil {
		return nil, err
	}
	if diagnostics := Validate(p); len(diagnostics) != 0 {
		return nil, fmt.Errorf("节点目录无效: %s", diagnostics[0].Message)
	}
	return json.MarshalIndent(definitions, "", "  ")
}

// Example 创建不依赖业务动作的最小可运行工程。
func Example() Project {
	return Project{SchemaVersion: SchemaVersion, Name: "行为树示例", Blackboard: []Field{}, Catalog: []Definition{}, Generation: Generation{PackagePath: "behavior", ContextType: "any"}, Trees: []Tree{{ID: "main", Name: "主行为树", Root: "root", Nodes: []Node{{ID: "root", Type: NodeSequence, Children: []string{"wait", "done"}}, {ID: "wait", Type: NodeWait, DurationMS: 100}, {ID: "done", Type: NodeWait}}, Layout: map[string]Position{"root": {X: 240, Y: 40}, "wait": {X: 120, Y: 180}, "done": {X: 360, Y: 180}}}}}
}
