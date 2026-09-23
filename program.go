package behaviortree

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// preparedProgram 保存发布时校验并复制的不可变执行描述。
type preparedProgram[C any] struct {
	program      Program[C]                // program 是复制并校验后的不可变定义。
	eventByID    map[EventID]struct{}      // eventByID 包含全部合法事件，包括未被引用的事件。
	dependencies map[int]*rootDependencies // dependencies 只为每个入口保留自身观察者。
	generation   uint64                    // generation 是注册表内单调发布顺序。
}

// rootDependencies 是多个相同入口实例共享的 O(1) 通知分发表。
type rootDependencies struct {
	events map[EventID]int // events 将稳定事件 ID 映射到观察者组。
	groups [][]int         // groups 前 len(Fields) 个槽位固定为字段依赖，其后是事件依赖。
}

// ValidateProgram 在加载插件前后复用相同的程序描述校验。
func ValidateProgram[C any](p *Program[C]) error { _, err := prepareProgram(p); return err }

// prepareProgram 校验所有节点和字段，只在创建或发布版本时执行。
func prepareProgram[C any](p *Program[C]) (*preparedProgram[C], error) {
	if p == nil || p.Version == "" || p.Step == nil || len(p.Roots) == 0 || len(p.Nodes) == 0 {
		return nil, fmt.Errorf("program requires version, roots, nodes and Step")
	}
	cp := &preparedProgram[C]{program: *p}
	cp.program.Nodes = append([]Node(nil), p.Nodes...)
	cp.program.Fields = append([]Field(nil), p.Fields...)
	cp.program.Events = append([]EventDefinition(nil), p.Events...)
	cp.program.Roots = make(map[string]int, len(p.Roots))
	cp.program.FieldDependencies = make(map[string][]int, len(p.FieldDependencies))
	cp.program.EventDependencies = make(map[EventID][]int, len(p.EventDependencies))
	cp.eventByID = make(map[EventID]struct{}, len(p.Events))
	cp.dependencies = make(map[int]*rootDependencies, len(p.Roots))
	var err error
	cp.program.EventEnumDescription, err = normalizeEventDescription(p.EventEnumDescription)
	if err != nil {
		return nil, fmt.Errorf("eventEnumDescription: %w", err)
	}
	codeNames := make(map[string]struct{}, len(p.Events))
	for n, event := range p.Events {
		if event.ID == InvalidEventID {
			return nil, fmt.Errorf("events[%d]: invalid event ID 0", n)
		}
		if _, exists := cp.eventByID[event.ID]; exists {
			return nil, fmt.Errorf("events[%d]: duplicate event ID %d", n, event.ID)
		}
		if strings.TrimSpace(event.Name) == "" || !utf8.ValidString(event.Name) || strings.ContainsRune(event.Name, 0) {
			return nil, fmt.Errorf("events[%d].name: invalid event name", n)
		}
		if !validEventCodeName(event.CodeName) {
			return nil, fmt.Errorf("events[%d].codeName: invalid event code name %q", n, event.CodeName)
		}
		folded := strings.ToLower(event.CodeName)
		if _, exists := codeNames[folded]; exists {
			return nil, fmt.Errorf("events[%d].codeName: duplicate event code name %q", n, event.CodeName)
		}
		cp.program.Events[n].Name = strings.TrimSpace(event.Name)
		cp.program.Events[n].Description, err = normalizeEventDescription(event.Description)
		if err != nil {
			return nil, fmt.Errorf("events[%d].description: %w", n, err)
		}
		cp.eventByID[event.ID] = struct{}{}
		codeNames[folded] = struct{}{}
	}
	rootForNode := make([]int, len(p.Nodes))
	roots := make(map[int]bool, len(p.Roots))
	for id, root := range p.Roots {
		if id == "" || root < 0 || root >= len(p.Nodes) || p.Nodes[root].Parent != -1 || roots[root] {
			return nil, fmt.Errorf("invalid or duplicate root %q", id)
		}
		cp.program.Roots[id] = root
		roots[root] = true
		cp.dependencies[root] = &rootDependencies{events: make(map[EventID]int), groups: make([][]int, len(p.Fields))}
	}
	currentRoot := 0
	for n, node := range p.Nodes {
		if node.ID == "" || node.TreeID == "" || node.End <= n || node.End > len(p.Nodes) {
			return nil, fmt.Errorf("invalid node descriptor at %d", n)
		}
		key := node.TreeID + "/" + node.ID
		if node.Parent == -1 {
			currentRoot = n
			if !roots[n] {
				return nil, fmt.Errorf("node %q is an unlisted root", key)
			}
		} else if node.Parent < 0 || node.Parent >= n || p.Nodes[node.Parent].End < node.End || p.Nodes[node.Parent].End <= n {
			return nil, fmt.Errorf("node %q has invalid preorder parent", key)
		}
		rootForNode[n] = currentRoot
		// 先序树的下一个节点只能进入当前节点或位于其祖先链的下一个兄弟。
		if n+1 < node.End && p.Nodes[n+1].Parent != n {
			return nil, fmt.Errorf("node %q has inconsistent subtree bounds", key)
		}
		if node.End < len(p.Nodes) && p.Nodes[node.End].Parent >= n {
			return nil, fmt.Errorf("node %q has crossing subtree bounds", key)
		}
	}
	fieldIndex := make(map[string]int, len(p.Fields))
	for slot, field := range p.Fields {
		if field.ID == "" || field.Name == "" {
			return nil, fmt.Errorf("field %d requires ID and name", slot)
		}
		if _, exists := fieldIndex[field.ID]; exists {
			return nil, fmt.Errorf("duplicate field ID %q", field.ID)
		}
		fieldIndex[field.ID] = slot
		switch field.Type {
		case BoolType, IntType, UIntType, FloatType, StringType, EntityIDType, DurationType:
		case EnumType:
			allowed := make(map[string]bool, len(field.Enum))
			for _, value := range field.Enum {
				if allowed[value] {
					return nil, fmt.Errorf("field %q duplicates enum %q", field.ID, value)
				}
				allowed[value] = true
			}
			if !allowed[field.Default.String] {
				return nil, fmt.Errorf("field %q has invalid enum default", field.ID)
			}
		default:
			return nil, fmt.Errorf("field %q has unknown type %d", field.ID, field.Type)
		}
		cp.program.Fields[slot].Enum = append([]string(nil), field.Enum...)
	}
	for fieldID, deps := range p.FieldDependencies {
		slot, exists := fieldIndex[fieldID]
		if !exists {
			return nil, fmt.Errorf("dependency references unknown field %q", fieldID)
		}
		copied, err := copyDependencyNodes(deps, len(p.Nodes), "field "+fieldID)
		if err != nil {
			return nil, err
		}
		cp.program.FieldDependencies[fieldID] = copied
		// 依赖在发布阶段按入口分组，单个 AI 通知不会遍历其他树的节点。
		for first := 0; first < len(copied); {
			root := rootForNode[copied[first]]
			end := first + 1
			for end < len(copied) && rootForNode[copied[end]] == root {
				end++
			}
			cp.dependencies[root].groups[slot] = copied[first:end]
			first = end
		}
	}
	for eventID, deps := range p.EventDependencies {
		if _, exists := cp.eventByID[eventID]; !exists {
			return nil, fmt.Errorf("dependency references unknown event %d", eventID)
		}
		copied, err := copyDependencyNodes(deps, len(p.Nodes), fmt.Sprintf("event %d", eventID))
		if err != nil {
			return nil, err
		}
		cp.program.EventDependencies[eventID] = copied
		for first := 0; first < len(copied); {
			root := rootForNode[copied[first]]
			end := first + 1
			for end < len(copied) && rootForNode[copied[end]] == root {
				end++
			}
			d := cp.dependencies[root]
			d.events[eventID] = len(d.groups)
			d.groups = append(d.groups, copied[first:end])
			first = end
		}
	}
	return cp, nil
}

// copyDependencyNodes 验证并复制观察者索引，排序后供入口分组复用。
func copyDependencyNodes(nodes []int, nodeCount int, label string) ([]int, error) {
	seen := make(map[int]struct{}, len(nodes))
	for _, n := range nodes {
		if n < 0 || n >= nodeCount {
			return nil, fmt.Errorf("invalid dependency node %d for %s", n, label)
		}
		if _, exists := seen[n]; exists {
			return nil, fmt.Errorf("duplicate dependency node %d for %s", n, label)
		}
		seen[n] = struct{}{}
	}
	copied := append([]int(nil), nodes...)
	sort.Ints(copied)
	return copied, nil
}

// validEventCodeName 检查事件成员的受限 Go 代码名。
func validEventCodeName(name string) bool {
	if len(name) == 0 || len(name) > 80 || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for n := 1; n < len(name); n++ {
		c := name[n]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// normalizeEventDescription 统一注释换行、空白与 UTF-8 字节长度合同。
func normalizeEventDescription(description string) (string, error) {
	description = strings.ReplaceAll(strings.ReplaceAll(description, "\r\n", "\n"), "\r", "\n")
	if strings.ContainsRune(description, 0) || !utf8.ValidString(description) {
		return "", fmt.Errorf("description contains NUL or invalid UTF-8")
	}
	if strings.Trim(description, " \t\n") == "" {
		return "", nil
	}
	if len(description) > 16*1024 {
		return "", fmt.Errorf("description exceeds 16 KiB UTF-8")
	}
	return description, nil
}
