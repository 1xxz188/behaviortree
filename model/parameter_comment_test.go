package model

import (
	"bytes"
	"testing"

	bt "github.com/1xxz188/behaviortree"
)

// TestParameterCommentRoundTrip 验证参数注释随工程和目录导出保留，旧工程无需填写注释。
func TestParameterCommentRoundTrip(t *testing.T) {
	for _, comment := range []string{"", "移动目标实体 ID\n用于寻路，保留 100% 精度。"} {
		t.Run(comment, func(t *testing.T) {
			p := Example()
			p.Catalog = []Definition{{ID: "move", Name: "移动到目标", Kind: DefinitionAction, GoName: "MoveTo", Params: []Parameter{{Name: "Target", Type: bt.EntityIDType, Comment: comment}}}}
			raw, err := Encode(p)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := decoded.Catalog[0].Params[0].Comment; got != comment {
				t.Fatalf("工程往返丢失注释：%q", got)
			}
			catalog, err := ExportCatalog(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(catalog, []byte(`"comment"`)) != (comment != "") {
				t.Fatalf("目录注释字段省略规则错误：%s", catalog)
			}
		})
	}
}
