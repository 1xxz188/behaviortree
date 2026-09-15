package model

import (
	"strings"
	"testing"
)

// TestTreeIDValidation 验证树主键严格采用 ASCII 格式，且共享校验器返回树级定位。
func TestTreeIDValidation(t *testing.T) {
	for _, id := range []string{"main", "MAIN", "0", "9_Main", "_", "aZ09_", strings.Repeat("a", 80)} {
		t.Run("合法_"+id, func(t *testing.T) {
			p := Example()
			p.Trees[0].ID = id
			if !ValidTreeID(id) || len(Validate(p)) != 0 {
				t.Fatalf("合法树 ID 被拒绝: %q", id)
			}
		})
	}
	for _, id := range []string{"", " ", " main", "main ", "main\n", "main\t", "中文", "main-tree", "a.b", "a/b", "é", "Ａ", strings.Repeat("a", 81)} {
		t.Run("非法_"+id, func(t *testing.T) {
			p := Example()
			p.Trees[0].ID = id
			d := Validate(p)
			if ValidTreeID(id) || len(d) != 1 || d[0].TreeID != id || d[0].NodeID != "" || d[0].Field != "id" {
				t.Fatalf("非法树 ID 未正确定位: %q, %+v", id, d)
			}
		})
	}
}

// TestTreeIdentityUniqueness 验证名称可重复，完全重复或仅大小写不同的 ID 均被拒绝。
func TestTreeIdentityUniqueness(t *testing.T) {
	p := Example()
	second := p.Trees[0]
	second.ID = "other"
	p.Trees = append(p.Trees, second)
	if d := Validate(p); len(d) != 0 {
		t.Fatalf("同名但主键不同的树被拒绝: %+v", d)
	}
	p.Trees[1].ID = "MAIN"
	if d := Validate(p); len(d) != 1 || d[0].Field != "id" || !strings.Contains(d[0].Message, "大小写") {
		t.Fatalf("大小写文件名冲突未被拒绝: %+v", d)
	}
	p.Trees[1].ID = p.Trees[0].ID
	if d := Validate(p); len(d) != 1 || d[0].Field != "id" {
		t.Fatalf("重复树 ID 未被拒绝: %+v", d)
	}
}
