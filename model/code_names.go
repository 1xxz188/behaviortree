package model

import (
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ValidCodeName 检查可直接用于生成符号的树内代码名，不接受空值、Go 关键字或非 ASCII 字符。
func ValidCodeName(name string) bool {
	if len(name) == 0 || len(name) > 40 || !asciiLetter(name[0]) || token.IsKeyword(name) {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !asciiLetter(c) && c != '_' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// asciiLetter 判断单字节是否为英文字母。
func asciiLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// opaqueNodeID 识别编辑器生成的随机身份，避免将 UUID 再复制进可读代码名。
func opaqueNodeID(id string) bool {
	if len(id) == 37 && strings.EqualFold(id[:5], "node_") {
		id = id[5:]
	} else if len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' {
		id = strings.ReplaceAll(id, "-", "")
	} else {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// defaultCodeName 优先复用简短语义 ID；随机或非法 ID 则返回类型基名，并要求追加数字。
func defaultCodeName(n Node) (string, bool) {
	name := n.ID
	forceNumber := !ValidCodeName(name) || opaqueNodeID(name)
	if forceNumber {
		name = "Node"
		if n.Type.Valid() {
			name = n.Type.String()
		}
	}
	if name[0] >= 'a' && name[0] <= 'z' {
		name = string(name[0]-'a'+'A') + name[1:]
	}
	return name, forceNumber
}

// availableCodeName 为碰撞名称分配数字后缀，同一截短前缀共享游标，避免反复扫描已占用名称。
// 类型回退通过 forceNumber 从 1 开始分配，不占用无后缀的语义名称。
func availableCodeName(base string, forceNumber bool, used map[string]struct{}, next map[string]int) string {
	if _, exists := used[base]; !exists && !forceNumber {
		return base
	}
	for digits, lower := 1, 1; ; digits, lower = digits+1, lower*10 {
		prefix := base[:min(len(base), 40-digits)]
		key := strconv.Itoa(digits) + ":" + prefix
		start := max(lower, next[key])
		for value := start; value < lower*10; value++ {
			next[key] = value + 1
			candidate := prefix + strconv.Itoa(value)
			if _, exists := used[candidate]; !exists {
				return candidate
			}
		}
	}
}

// WithCodeNames 复制树与节点切片并只补齐缺失代码名，保留显式名称供 Validate 报告错误。
// 按节点 ID 排序分配默认名，因此节点数组重排不会影响首次补全结果。
func WithCodeNames(p Project) Project {
	p.Trees = slices.Clone(p.Trees)
	for treeIndex := range p.Trees {
		tree := &p.Trees[treeIndex]
		tree.Nodes = slices.Clone(tree.Nodes)
		used := make(map[string]struct{}, len(tree.Nodes))
		missing := make([]int, 0)
		for i, node := range tree.Nodes {
			if node.CodeName == "" {
				missing = append(missing, i)
			} else {
				used[node.CodeName] = struct{}{}
			}
		}
		sort.Slice(missing, func(i, j int) bool { return tree.Nodes[missing[i]].ID < tree.Nodes[missing[j]].ID })
		next := make(map[string]int)
		for _, index := range missing {
			node := &tree.Nodes[index]
			base, forceNumber := defaultCodeName(*node)
			node.CodeName = availableCodeName(base, forceNumber, used, next)
			used[node.CodeName] = struct{}{}
		}
	}
	return p
}
