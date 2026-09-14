package model

import (
	"encoding/json"
	"fmt"
)

// NodeType 用紧凑整数表示内建节点种类，JSON 保留可读名称。
type NodeType uint8

const (
	InvalidNodeType NodeType = iota // 未设置或非法节点类型。
	NodeSequence                    // 顺序执行。
	NodeSelector                    // 首个成功分支。
	NodePriority                    // 带守卫的优先级选择。
	NodeParallel                    // 并行执行。
	NodeCondition                   // 业务条件。
	NodeAction                      // 业务动作。
	NodeWait                        // 等待时长。
	NodeTimeout                     // 子节点超时。
	NodeRepeat                      // 有限重复。
	NodeRetry                       // 有限重试。
	NodeInverter                    // 反转结果。
	NodeSucceed                     // 强制成功。
	NodeFail                        // 强制失败。
	NodeSubtree                     // 引用子树。
)

// nodeTypeNames 与整数种类保持一一对应。
var nodeTypeNames = [...]string{"invalid", "sequence", "selector", "priority", "parallel", "condition", "action", "wait", "timeout", "repeat", "retry", "inverter", "succeed", "fail", "subtree"}

// Valid 判断类型是否属于支持的节点集合。
func (t NodeType) Valid() bool { return t > InvalidNodeType && int(t) < len(nodeTypeNames) }

// String 返回可读名称，非法类型返回 invalid。
func (t NodeType) String() string {
	if !t.Valid() {
		return "invalid"
	}
	return nodeTypeNames[t]
}

// ParseNodeType 解析稳定的工程格式名称。
func ParseNodeType(name string) (NodeType, error) {
	switch name {
	case "sequence":
		return NodeSequence, nil
	case "selector":
		return NodeSelector, nil
	case "priority":
		return NodePriority, nil
	case "parallel":
		return NodeParallel, nil
	case "condition":
		return NodeCondition, nil
	case "action":
		return NodeAction, nil
	case "wait":
		return NodeWait, nil
	case "timeout":
		return NodeTimeout, nil
	case "repeat":
		return NodeRepeat, nil
	case "retry":
		return NodeRetry, nil
	case "inverter":
		return NodeInverter, nil
	case "succeed":
		return NodeSucceed, nil
	case "fail":
		return NodeFail, nil
	case "subtree":
		return NodeSubtree, nil
	default:
		return InvalidNodeType, fmt.Errorf("未知节点类型 %q", name)
	}
}

// MarshalJSON 只导出有效节点名称，不泄漏内部序号。
func (t NodeType) MarshalJSON() ([]byte, error) {
	if !t.Valid() {
		return nil, fmt.Errorf("非法节点类型 %d", t)
	}
	return json.Marshal(t.String())
}

// UnmarshalJSON 拒绝空名称、null 和非字符串，错误时不改变原值。
func (t *NodeType) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("type 节点类型 %s: %w", data, err)
	}
	value, err := ParseNodeType(name)
	if err != nil {
		return fmt.Errorf("type 节点类型 %s: %w", data, err)
	}
	*t = value
	return nil
}

// DefinitionKind 仅表示业务目录项种类，不能与任意内建节点类型混用。
type DefinitionKind uint8

const (
	InvalidDefinitionKind DefinitionKind = iota // 未设置或非法目录种类。
	DefinitionAction                            // 业务动作。
	DefinitionCondition                         // 业务条件。
)

// Valid 判断目录种类是否有效。
func (k DefinitionKind) Valid() bool { return k == DefinitionAction || k == DefinitionCondition }

// String 返回目录格式使用的可读名称。
func (k DefinitionKind) String() string {
	switch k {
	case DefinitionAction:
		return "action"
	case DefinitionCondition:
		return "condition"
	default:
		return "invalid"
	}
}

// ParseDefinitionKind 解析业务动作或条件的稳定名称。
func ParseDefinitionKind(name string) (DefinitionKind, error) {
	switch name {
	case "action":
		return DefinitionAction, nil
	case "condition":
		return DefinitionCondition, nil
	default:
		return InvalidDefinitionKind, fmt.Errorf("未知目录种类 %q", name)
	}
}

// Matches 显式验证目录种类与行为树节点的绑定关系。
func (k DefinitionKind) Matches(t NodeType) bool {
	return k == DefinitionAction && t == NodeAction || k == DefinitionCondition && t == NodeCondition
}

// MarshalJSON 拒绝非法目录种类并导出可读名称。
func (k DefinitionKind) MarshalJSON() ([]byte, error) {
	if !k.Valid() {
		return nil, fmt.Errorf("非法目录种类 %d", k)
	}
	return json.Marshal(k.String())
}

// UnmarshalJSON 严格解析目录名称，失败时保留原值。
func (k *DefinitionKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("kind 目录种类 %s: %w", data, err)
	}
	value, err := ParseDefinitionKind(name)
	if err != nil {
		return fmt.Errorf("kind 目录种类 %s: %w", data, err)
	}
	*k = value
	return nil
}

// validateDecodedTypes 在输入边界拒绝缺失必填类型，但不限制草稿的未完成拓扑。
func validateDecodedTypes(p Project) error {
	for _, field := range p.Blackboard {
		if !field.Type.Valid() {
			return fmt.Errorf("黑板字段 %q 缺少有效 type", field.ID)
		}
	}
	for _, definition := range p.Catalog {
		if !definition.Kind.Valid() {
			return fmt.Errorf("目录 %q 缺少有效 kind", definition.ID)
		}
		for _, parameter := range definition.Params {
			if !parameter.Type.Valid() {
				return fmt.Errorf("目录 %q 参数 %q 缺少有效 type", definition.ID, parameter.Name)
			}
		}
	}
	for _, tree := range p.Trees {
		for _, node := range tree.Nodes {
			if !node.Type.Valid() {
				return fmt.Errorf("树 %q 节点 %q 缺少有效 type", tree.ID, node.ID)
			}
		}
	}
	return nil
}
