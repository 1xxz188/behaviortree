package hotload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManifestCompatibility 验证共享依赖、工具链和构建选项变化会阻止加载。
func TestManifestCompatibility(t *testing.T) {
	c := Contract{GoVersion: "go1.26.7", GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1", Mode: "release", Shared: map[string]string{"runtime": "hash"}, APIFingerprint: "api"}
	m := Manifest{SchemaVersion: 1, Version: "v1", PrivateModule: "bt.local/v1", SHA256: strings.Repeat("a", 64), Contract: c}
	if err := m.Check(c); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*Contract){func(v *Contract) { v.GoVersion = "go1.26.6" }, func(v *Contract) { v.Mode = "debug" }, func(v *Contract) { v.APIFingerprint = "different" }, func(v *Contract) { v.Tags = []string{"debug"} }, func(v *Contract) { v.Shared = map[string]string{"runtime": "changed"} }} {
		bad := c
		edit(&bad)
		if m.Check(bad) == nil {
			t.Fatal("accepted incompatible contract")
		}
	}
}

// TestManifestRoundTripAndChecksum 验证旁车往返及二进制损坏可检测。
func TestManifestRoundTripAndChecksum(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "tree.so")
	if err := os.WriteFile(file, []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	sum, err := FileSHA256(file)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{SchemaVersion: 1, Version: "v1", SHA256: sum}
	data, _ := json.Marshal(m)
	if err = os.WriteFile(file+".json", data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(file + ".json")
	if err != nil || got.SHA256 != sum {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if err = os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, _ := FileSHA256(file)
	if changed == sum {
		t.Fatal("undetected corruption")
	}
	if err = os.WriteFile(file+".json", append(data, []byte(" {}")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadManifest(file + ".json"); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

// TestUnsupportedLoader 验证 Windows/无 CGO 环境无需访问文件即返回平台错误。
func TestUnsupportedLoader(t *testing.T) {
	if supported() {
		t.Skip("native plugin supported")
	}
	_, _, err := New[int](Contract{}).Load("missing.so", nil)
	if err != ErrUnsupported {
		t.Fatalf("got %v", err)
	}
}

// TestExporterPanicRejected 验证导出及校验回调 panic 不会逃出可检查的加载边界。
func TestExporterPanicRejected(t *testing.T) {
	value, err := callExporter(func() int { panic("bad exporter") }, nil)
	if value != 0 || err == nil || !strings.Contains(err.Error(), "bad exporter") {
		t.Fatalf("panic not rejected: %d %v", value, err)
	}
	value, err = callExporter(func() int { return 42 }, func(int) error { panic("bad validator") })
	if value != 0 || err == nil || !strings.Contains(err.Error(), "bad validator") {
		t.Fatalf("validator panic not rejected: %d %v", value, err)
	}
}
