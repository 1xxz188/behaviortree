package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// BusinessNamingVersion 标识业务名称规则；缺省零值保留历史名称，不做隐式迁移。
type BusinessNamingVersion uint8

// PrefixedBusinessNaming 要求新业务名称使用动作 Action、条件 Is 的固定前缀。
const PrefixedBusinessNaming BusinessNamingVersion = 1

// UnmarshalJSON 拒绝未知规则和 null，避免新节点在输入边界退回历史命名规则。
func (v *BusinessNamingVersion) UnmarshalJSON(data []byte) error {
	var version uint8
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("namingVersion 必须为 0 或 1")
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return fmt.Errorf("namingVersion 必须为 0 或 1: %w", err)
	}
	if version > uint8(PrefixedBusinessNaming) {
		return fmt.Errorf("不支持 namingVersion %d", version)
	}
	*v = BusinessNamingVersion(version)
	return nil
}

// businessNamePrefix 常数时间返回业务种类的固定前缀，控制节点不使用该规则。
func businessNamePrefix(kind string) string {
	switch kind {
	case "action":
		return "Action"
	case "condition":
		return "Is"
	default:
		return ""
	}
}

// businessNameError 只校验显式启用的新规则，旧名称的标识符校验沿用原入口。
func businessNameError(version BusinessNamingVersion, kind, name string) string {
	if version == 0 {
		return ""
	}
	if version != PrefixedBusinessNaming {
		return fmt.Sprintf("不支持 namingVersion %d", version)
	}
	prefix := businessNamePrefix(kind)
	if prefix == "" {
		return "固定业务前缀仅适用于动作和条件节点"
	}
	if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
		return fmt.Sprintf("业务名称必须以 %s 开头，并在前缀后填写名称", prefix)
	}
	return ""
}

// ValidateBusinessNames 在线性输入边界校验新业务名称，不要求草稿已绑定或连线完整。
func ValidateBusinessNames(p Project) []Diagnostic {
	var diagnostics []Diagnostic
	for i, definition := range p.Catalog {
		if failure := businessNameError(definition.NamingVersion, definition.Kind.String(), definition.GoName); failure != "" {
			diagnostics = append(diagnostics, Diagnostic{Field: fmt.Sprintf("catalog[%d].goName", i), Message: failure})
		}
	}
	for _, tree := range p.Trees {
		for _, node := range tree.Nodes {
			name := node.CodeName
			if name == "" {
				// 缺省代码名仍由 WithCodeNames 补全；这里只验证规则版本和适用种类。
				name = businessNamePrefix(node.Type.String()) + "1"
			}
			if failure := businessNameError(node.NamingVersion, node.Type.String(), name); failure != "" {
				diagnostics = append(diagnostics, Diagnostic{TreeID: tree.ID, NodeID: node.ID, Field: "codeName", Message: failure})
			}
		}
	}
	return diagnostics
}
