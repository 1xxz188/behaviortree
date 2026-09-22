package model

import (
	"bytes"
	"testing"
)

// TestNodeCommentRoundTrip 验证节点多行注释无损往返，旧工程和清空后的工程省略可选字段。
func TestNodeCommentRoundTrip(t *testing.T) {
	for _, comment := range []string{"", "中文注释 \"引号\" 100%\n第二行\r\n第三行"} {
		p := Example()
		p.Trees[0].Nodes[0].Comment = comment
		raw, err := Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"comment"`)) != (comment != "") {
			t.Fatalf("注释字段省略规则错误：%s", raw)
		}
		decoded, err := Decode(raw)
		if err != nil || decoded.Trees[0].Nodes[0].Comment != comment {
			t.Fatalf("节点注释往返失败：%+v，%v", decoded, err)
		}
	}
}

// TestNodeCommentRejectsWrongType 验证非字符串注释不能在工程输入边界被静默丢失。
func TestNodeCommentRejectsWrongType(t *testing.T) {
	p := Example()
	p.Trees[0].Nodes[0].Comment = "placeholder"
	raw, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"123", "false", "[]", "{}"} {
		input := bytes.Replace(raw, []byte(`"placeholder"`), []byte(invalid), 1)
		if _, err := Decode(input); err == nil {
			t.Fatalf("接受非法注释：%s", invalid)
		}
	}
}
