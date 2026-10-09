package model

import "slices"

// ValidationErrors 保留会阻断生成的诊断；未声明严重级别的旧诊断仍按错误处理。
func ValidationErrors(diagnostics []Diagnostic) []Diagnostic {
	var errors []Diagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != "warning" {
			errors = append(errors, diagnostic)
		}
	}
	return errors
}

// ExecutionProject 返回各树根节点可达的运行副本及逐节点草稿警告，不修改工程节点或布局。
// 每棵树一次建立 ID 索引并遍历可达边，整体为 O(节点数+连线数)，查找使用 O(1) 映射。
func ExecutionProject(p Project) (Project, []Diagnostic) {
	p.Trees = slices.Clone(p.Trees)
	var diagnostics []Diagnostic
	for i := range p.Trees {
		tree := &p.Trees[i]
		nodes := make(map[string]Node, len(tree.Nodes))
		for _, node := range tree.Nodes {
			// 草稿仍需保持唯一非空身份，避免连线、选区和生成映射产生歧义。
			if node.ID == "" {
				diagnostics = append(diagnostics, Diagnostic{TreeID: tree.ID, NodeID: node.ID, Field: "id", Message: "节点 ID 不能为空"})
			}
			if _, exists := nodes[node.ID]; exists {
				diagnostics = append(diagnostics, Diagnostic{TreeID: tree.ID, NodeID: node.ID, Field: "id", Message: "重复节点 ID"})
			}
			nodes[node.ID] = node
		}
		reachable := make(map[string]bool, len(nodes))
		pending := []string{tree.Root}
		// 显式栈和访问集合允许未完成草稿含环，不递归访问任何不可达组件。
		for len(pending) > 0 {
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if reachable[id] {
				continue
			}
			node, exists := nodes[id]
			if !exists {
				continue
			}
			reachable[id] = true
			pending = append(pending, node.Children...)
		}
		active := make([]Node, 0, len(reachable))
		for _, node := range tree.Nodes {
			if reachable[node.ID] {
				active = append(active, node)
			} else {
				diagnostics = append(diagnostics, Diagnostic{TreeID: tree.ID, NodeID: node.ID, Field: "children", Message: "节点无法从根节点到达，作为草稿保留，生成和代码导出时将跳过", Severity: "warning"})
			}
		}
		tree.Nodes = active
	}
	return p, diagnostics
}
