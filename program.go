package behaviortree

import (
	"fmt"
	"sort"
	"strings"
)

// preparedProgram 保存发布时校验并复制的不可变执行描述。
type preparedProgram[C any] struct {
	program      Program[C]                // program 是复制并校验后的不可变定义。
	dependencies map[int]*rootDependencies // dependencies 只为每个入口保留自身观察者。
	generation   uint64                    // generation 是注册表内单调发布顺序。
}

// rootDependencies 是多个相同入口实例共享的 O(1) 通知分发表。
type rootDependencies struct {
	events map[string]int // events 将宿主事件映射到观察者组。
	groups [][]int        // groups 前 len(Fields) 个槽位固定为字段依赖，其后是事件依赖。
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
	cp.program.Roots = make(map[string]int, len(p.Roots))
	cp.program.Dependencies = make(map[string][]int, len(p.Dependencies))
	cp.dependencies = make(map[int]*rootDependencies, len(p.Roots))
	rootForNode := make([]int, len(p.Nodes))
	roots := make(map[int]bool, len(p.Roots))
	for id, root := range p.Roots {
		if id == "" || root < 0 || root >= len(p.Nodes) || p.Nodes[root].Parent != -1 || roots[root] {
			return nil, fmt.Errorf("invalid or duplicate root %q", id)
		}
		cp.program.Roots[id] = root
		roots[root] = true
		cp.dependencies[root] = &rootDependencies{events: make(map[string]int), groups: make([][]int, len(p.Fields))}
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
			return nil, fmt.Errorf("field %q has unknown type %q", field.ID, field.Type)
		}
		cp.program.Fields[slot].Enum = append([]string(nil), field.Enum...)
	}
	for key, deps := range p.Dependencies {
		if !strings.HasPrefix(key, "field:") && !strings.HasPrefix(key, "event:") {
			return nil, fmt.Errorf("invalid dependency %q", key)
		}
		seen := make(map[int]bool, len(deps))
		for _, n := range deps {
			if n < 0 || n >= len(p.Nodes) || seen[n] {
				return nil, fmt.Errorf("invalid or duplicate dependency node for %q", key)
			}
			seen[n] = true
		}
		copied := append([]int(nil), deps...)
		sort.Ints(copied)
		cp.program.Dependencies[key] = copied
		fieldSlot := -1
		if strings.HasPrefix(key, "field:") {
			slot, exists := fieldIndex[strings.TrimPrefix(key, "field:")]
			if !exists {
				return nil, fmt.Errorf("dependency references unknown field %q", key)
			}
			fieldSlot = slot
		}
		// 依赖在发布阶段按入口分组，单个 AI 通知不会遍历其他树的节点。
		for first := 0; first < len(copied); {
			root := rootForNode[copied[first]]
			end := first + 1
			for end < len(copied) && rootForNode[copied[end]] == root {
				end++
			}
			d := cp.dependencies[root]
			if fieldSlot >= 0 {
				d.groups[fieldSlot] = copied[first:end]
			} else {
				d.events[key] = len(d.groups)
				d.groups = append(d.groups, copied[first:end])
			}
			first = end
		}
	}
	return cp, nil
}
