// Package model 定义编辑器、命令行和代码生成器共享的行为树元数据。
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	bt "github.com/1xxz188/behaviortree"
)

// SchemaVersion 是当前支持的工程格式版本。
const SchemaVersion = 4

// Project 是可独立导入、导出的行为树工程；布局不参与运行语义。
type Project struct {
	SchemaVersion        int                  `json:"schemaVersion"`                  // 格式版本。
	Name                 string               `json:"name"`                           // 工程显示名称。
	EventEnumDescription string               `json:"eventEnumDescription,omitempty"` // 整个事件枚举的功能注释。
	Events               []EventDefinition    `json:"events"`                         // 稳定 ID 的事件枚举成员。
	NextEventID          string               `json:"nextEventId"`                    // 下一次分配的十进制事件 ID，耗尽时为 2^64。
	Blackboard           []Field              `json:"blackboard"`                     // 全工程共享的黑板布局。
	Catalog              []Definition         `json:"catalog"`                        // Go 业务节点目录。
	CatalogOrganization  *CatalogOrganization `json:"catalogOrganization,omitempty"`  // 仅用于编辑器分类的目录、标签与归属。
	Trees                []Tree               `json:"trees"`                          // 可独立启动或引用的树。
	Generation           Generation           `json:"generation"`                     // Go 生成目标。
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
	ID       string         `json:"id"`                 // 编辑器绑定使用的稳定 ID。
	Name     string         `json:"name"`               // 节点面板显示名称。
	Kind     DefinitionKind `json:"kind"`               // action 或 condition。
	GoName   string         `json:"goName"`             // 同包手写函数名称。
	Params   []Parameter    `json:"params"`             // 强类型参数声明。
	EventIDs []string       `json:"eventIds,omitempty"` // 需要唤醒或重新求值的事件 ID。
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
	Comment    string           `json:"comment,omitempty"`    // 节点实例的多行注释，供画布悬浮展示并生成到 Go 节点函数。
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
	if err := checkEventJSONShape(data); err != nil {
		return p, err
	}
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
	if p.SchemaVersion != SchemaVersion {
		return p, fmt.Errorf("不支持 schemaVersion %d，仅支持版本 %d", p.SchemaVersion, SchemaVersion)
	}
	if err := ValidateCatalogOrganization(p); err != nil {
		return p, err
	}
	if diagnostics := ValidateEvents(p); len(diagnostics) != 0 {
		return p, fmt.Errorf("%s: %s", diagnostics[0].Field, diagnostics[0].Message)
	}
	var err error
	p, err = normalizeEvents(p)
	if err != nil {
		return p, err
	}
	p.CatalogOrganization = NormalizedCatalogOrganization(p.CatalogOrganization)
	p.Generation.PackagePath = NormalizePackagePath(p.Generation.PackagePath)
	return WithCodeNames(p), nil
}

// Encode 保留稳定 ID、显式顺序以及编辑布局。
func Encode(p Project) ([]byte, error) {
	if p.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("不支持 schemaVersion %d，仅支持版本 %d", p.SchemaVersion, SchemaVersion)
	}
	if err := ValidateCatalogOrganization(p); err != nil {
		return nil, err
	}
	if diagnostics := ValidateEvents(p); len(diagnostics) != 0 {
		return nil, fmt.Errorf("%s: %s", diagnostics[0].Field, diagnostics[0].Message)
	}
	var err error
	p, err = normalizeEvents(p)
	if err != nil {
		return nil, err
	}
	p.CatalogOrganization = NormalizedCatalogOrganization(p.CatalogOrganization)
	p.Generation.PackagePath = NormalizePackagePath(p.Generation.PackagePath)
	return json.MarshalIndent(WithCodeNames(p), "", "  ")
}

// ExportCatalog 导出带实际引用事件的自包含目录，不要求草稿树已完成。
func ExportCatalog(p Project) ([]byte, error) {
	if err := validateCatalogExchangeProject(p); err != nil {
		return nil, err
	}
	used := make(map[string]bool)
	for _, definition := range p.Catalog {
		for _, id := range definition.EventIDs {
			used[id] = true
		}
	}
	exchange := CatalogExchange{Kind: "behaviortree.catalog", SchemaVersion: SchemaVersion, Events: []EventDefinition{}, Catalog: append([]Definition{}, p.Catalog...)}
	var err error
	exchange.EventEnumDescription, err = NormalizeEventDescription(p.EventEnumDescription)
	if err != nil {
		return nil, fmt.Errorf("eventEnumDescription: %w", err)
	}
	for _, event := range p.Events {
		if used[event.ID] {
			event.Name = strings.TrimSpace(event.Name)
			event.Description, _ = NormalizeEventDescription(event.Description)
			exchange.Events = append(exchange.Events, event)
		}
	}
	return json.MarshalIndent(exchange, "", "  ")
}

// validateCatalogExchangeProject 仅校验目录和事件，允许未完成的画布树。
func validateCatalogExchangeProject(p Project) error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持 schemaVersion %d", p.SchemaVersion)
	}
	if err := validateDecodedTypes(p); err != nil {
		return err
	}
	if diagnostics := ValidateEvents(p); len(diagnostics) != 0 {
		return fmt.Errorf("%s: %s", diagnostics[0].Field, diagnostics[0].Message)
	}
	probe := Example()
	probe.Events, probe.EventEnumDescription, probe.Catalog = p.Events, p.EventEnumDescription, p.Catalog
	probe.NextEventID = p.NextEventID
	if diagnostics := Validate(probe); len(diagnostics) != 0 {
		return fmt.Errorf("节点目录无效: %s: %s", diagnostics[0].Field, diagnostics[0].Message)
	}
	return nil
}

// Example 创建不依赖业务动作的最小可运行工程。
func Example() Project {
	return Project{SchemaVersion: SchemaVersion, Name: "行为树示例", Events: []EventDefinition{}, NextEventID: "1", Blackboard: []Field{}, Catalog: []Definition{}, Generation: Generation{PackagePath: "behavior", ContextType: "any"}, Trees: []Tree{{ID: "main", Name: "主行为树", Root: "root", Nodes: []Node{{ID: "root", Type: NodeSequence, Children: []string{"wait", "done"}}, {ID: "wait", Type: NodeWait, DurationMS: 100}, {ID: "done", Type: NodeWait}}, Layout: map[string]Position{"root": {X: 240, Y: 40}, "wait": {X: 120, Y: 180}, "done": {X: 360, Y: 180}}}}}
}

// CatalogOrganization 保存业务定义的编辑器分类，不参与运行语义。
type CatalogOrganization struct {
	Folders     []CatalogFolder              `json:"folders"`     // 可嵌套的目录集合，根目录不入表。
	Tags        []CatalogTag                 `json:"tags"`        // 工程共享的平铺标签集合。
	Assignments map[string]CatalogAssignment `json:"assignments"` // 以定义 ID 为键的稀疏归属记录。
}

// CatalogFolder 使用稳定 ID 表示目录，允许与定义混合同级排序。
type CatalogFolder struct {
	ID       string  `json:"id"`       // 稳定目录 ID，不能为空。
	Name     string  `json:"name"`     // 同级唯一的显示名称。
	ParentID string  `json:"parentId"` // 父目录 ID，空值表示根目录。
	Order    float64 `json:"order"`    // 有限的同级排序值。
}

// CatalogTag 是可供多个业务定义共享的标签。
type CatalogTag struct {
	ID   string `json:"id"`   // 稳定标签 ID。
	Name string `json:"name"` // 工程内唯一的标签名称。
}

// CatalogAssignment 描述单个定义的目录归属、标签及显示顺序。
type CatalogAssignment struct {
	FolderID string   `json:"folderId"` // 所属目录 ID，空值表示根目录。
	TagIDs   []string `json:"tagIds"`   // 不重复的标签 ID 集合。
	Order    float64  `json:"order"`    // 有限的同级排序值。
}

// NormalizedCatalogOrganization 复制名称所在切片后裁剪空白，不修改调用者或补齐稀疏归属。
func NormalizedCatalogOrganization(org *CatalogOrganization) *CatalogOrganization {
	if org == nil {
		return nil
	}
	result := *org
	result.Folders = append([]CatalogFolder{}, org.Folders...)
	result.Tags = append([]CatalogTag{}, org.Tags...)
	for i := range result.Folders {
		result.Folders[i].Name = strings.TrimSpace(result.Folders[i].Name)
	}
	for i := range result.Tags {
		result.Tags[i].Name = strings.TrimSpace(result.Tags[i].Name)
	}
	if result.Assignments == nil {
		result.Assignments = map[string]CatalogAssignment{}
	}
	return &result
}

// ValidateCatalogOrganization 在线性时间内校验分类引用、名称与目录环，允许缺省归属。
func ValidateCatalogOrganization(p Project) error {
	definitions := make(map[string]bool, len(p.Catalog))
	for _, definition := range p.Catalog {
		if definition.ID == "" || definitions[definition.ID] {
			return fmt.Errorf("业务定义 ID 为空或重复: %s", definition.ID)
		}
		definitions[definition.ID] = true
	}
	org := p.CatalogOrganization
	if org == nil {
		return nil
	}
	folders := make(map[string]CatalogFolder, len(org.Folders))
	// 二元键避免名称或 ID 包含分隔符时出现组合键碰撞。
	folderNames := make(map[[2]string]bool, len(org.Folders))
	for _, folder := range org.Folders {
		if strings.TrimSpace(folder.ID) == "" {
			return fmt.Errorf("目录 ID 不能为空")
		}
		if _, exists := folders[folder.ID]; exists {
			return fmt.Errorf("目录 ID 重复: %s", folder.ID)
		}
		name := strings.ToLower(strings.TrimSpace(folder.Name))
		key := [2]string{folder.ParentID, name}
		if name == "" || folderNames[key] {
			return fmt.Errorf("同级目录名称为空或重复: %s", folder.Name)
		}
		if math.IsInf(folder.Order, 0) || math.IsNaN(folder.Order) {
			return fmt.Errorf("目录排序值必须有限: %s", folder.ID)
		}
		folders[folder.ID], folderNames[key] = folder, true
	}
	for _, folder := range org.Folders {
		if _, exists := folders[folder.ParentID]; folder.ParentID != "" && !exists {
			return fmt.Errorf("目录 %s 的父目录不存在: %s", folder.ID, folder.ParentID)
		}
	}
	// 迭代三色遍历，每条父边最多访问两次，避免深目录递归及逐目录重复回溯。
	colors := make(map[string]uint8, len(folders))
	for _, folder := range org.Folders {
		id := folder.ID
		for id != "" && colors[id] == 0 {
			colors[id] = 1
			id = folders[id].ParentID
		}
		if id != "" && colors[id] == 1 {
			return fmt.Errorf("目录存在环: %s", id)
		}
		id = folder.ID
		for id != "" && colors[id] == 1 {
			colors[id] = 2
			id = folders[id].ParentID
		}
	}
	tags := make(map[string]bool, len(org.Tags))
	tagNames := make(map[string]bool, len(org.Tags))
	for _, tag := range org.Tags {
		if strings.TrimSpace(tag.ID) == "" || tags[tag.ID] {
			return fmt.Errorf("标签 ID 为空或重复: %s", tag.ID)
		}
		name := strings.ToLower(strings.TrimSpace(tag.Name))
		if name == "" || tagNames[name] {
			return fmt.Errorf("标签名称为空或重复: %s", tag.Name)
		}
		tags[tag.ID], tagNames[name] = true, true
	}
	for id, assignment := range org.Assignments {
		if !definitions[id] {
			return fmt.Errorf("分类引用不存在的业务定义: %s", id)
		}
		if _, exists := folders[assignment.FolderID]; assignment.FolderID != "" && !exists {
			return fmt.Errorf("业务定义 %s 的目录不存在: %s", id, assignment.FolderID)
		}
		if math.IsInf(assignment.Order, 0) || math.IsNaN(assignment.Order) {
			return fmt.Errorf("业务定义排序值必须有限: %s", id)
		}
		seen := make(map[string]bool, len(assignment.TagIDs))
		for _, tagID := range assignment.TagIDs {
			if !tags[tagID] || seen[tagID] {
				return fmt.Errorf("业务定义 %s 的标签不存在或重复: %s", id, tagID)
			}
			seen[tagID] = true
		}
	}
	return nil
}
