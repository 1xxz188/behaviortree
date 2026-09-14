package hotload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildModeRoundTrip 验证所有模式的 JSON 和文本保持可读并可往返。
func TestBuildModeRoundTrip(t *testing.T) {
	for _, mode := range []BuildMode{BuildRelease, BuildDebug} {
		t.Run(mode.String(), func(t *testing.T) {
			if !mode.Valid() {
				t.Fatal("valid mode rejected")
			}
			data, err := json.Marshal(mode)
			if err != nil || string(data) != `"`+mode.String()+`"` {
				t.Fatalf("JSON: %s %v", data, err)
			}
			var got BuildMode
			if err := json.Unmarshal(data, &got); err != nil || got != mode {
				t.Fatalf("JSON round trip: %v %v", got, err)
			}
			data, err = mode.MarshalText()
			if err != nil {
				t.Fatal(err)
			}
			got = InvalidBuildMode
			if err := got.UnmarshalText(data); err != nil || got != mode {
				t.Fatalf("text round trip: %v %v", got, err)
			}
		})
	}
}

// TestBuildModeRejectsInvalid 验证未知值、错误 JSON 类型、零值和越界值无法越过边界。
func TestBuildModeRejectsInvalid(t *testing.T) {
	for _, input := range []string{`""`, `"unknown"`, `"Release"`, `" release"`, `null`, `0`, `1`, `true`, `{}`, `[]`} {
		got := BuildDebug
		if err := json.Unmarshal([]byte(input), &got); err == nil || !strings.Contains(err.Error(), "mode") || got != BuildDebug {
			t.Fatalf("accepted or changed mode for %s: %v %v", input, got, err)
		}
	}
	for _, mode := range []BuildMode{InvalidBuildMode, BuildMode(3), BuildMode(255)} {
		if mode.Valid() {
			t.Fatalf("accepted %v", mode)
		}
		if _, err := json.Marshal(mode); err == nil {
			t.Fatalf("serialized %v", mode)
		}
		if _, err := mode.MarshalText(); err == nil {
			t.Fatalf("formatted %v", mode)
		}
		if err := (Contract{Mode: mode}).Validate(); err == nil {
			t.Fatalf("validated %v", mode)
		}
	}
}

// TestContractJSONBoundary 验证必填模式与布尔 CGO 开关，且内部解码仍拒绝未知字段。
func TestContractJSONBoundary(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		want := Contract{Mode: BuildRelease, CGOEnabled: enabled}
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got Contract
		if err = json.Unmarshal(data, &got); err != nil || got.CGOEnabled != enabled || got.Mode != want.Mode {
			t.Fatalf("contract round trip: %s %+v %v", data, got, err)
		}
	}
	for _, input := range []string{
		`{}`, `null`, `{"cgoEnabled":true}`, `{"mode":null,"cgoEnabled":true}`,
		`{"mode":1,"cgoEnabled":true}`, `{"mode":"","cgoEnabled":true}`,
		`{"mode":"unknown","cgoEnabled":true}`, `{"mode":"release"}`,
		`{"mode":"release","cgoEnabled":null}`, `{"mode":"release","cgoEnabled":"1"}`,
		`{"mode":"release","cgoEnabled":1}`, `{"mode":"release","cgoEnabled":true,"extra":true}`,
	} {
		var got Contract
		if err := json.Unmarshal([]byte(input), &got); err == nil {
			t.Fatalf("accepted invalid contract: %s", input)
		}
	}
}

// TestReadManifestRequiresMode 验证契约整体缺失与内部缺失模式均在文件入口被拒绝。
func TestReadManifestRequiresMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.so.json")
	for _, input := range []string{`{}`, `{"contract":{}}`, `{"contract":{"cgoEnabled":true}}`, `{"contract":null}`} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(path); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Fatalf("missing mode accepted: %s %v", input, err)
		}
	}
}
