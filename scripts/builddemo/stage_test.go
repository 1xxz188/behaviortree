package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVersionedPrivateImports 验证嵌套 helper 引用进入版本目录，固定上下文引用保持一致。
func TestVersionedPrivateImports(t *testing.T) {
	source := []byte("package behavior\nimport helper \"" + behaviorImport + "/helper\"\nimport \"" + sharedImport + "\"\nvar _ = helper.Label\nvar _ shared.Context\n")
	data, err := rewriteImports("actions.go", source, "bt.local/v2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"bt.local/v2/behavior/helper"`) || !strings.Contains(string(data), strconvQuote(sharedImport)) {
		t.Fatalf("wrong imports: %s", data)
	}
	if strings.Contains(string(data), strconvQuote(behaviorImport+"/helper")) {
		t.Fatal("unversioned mutable import survived")
	}
}

// TestPreparedSharedContract 验证真实版本化生成源码可在独立工作区解析，共享包源码指纹一致。
func TestPreparedSharedContract(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	b := builder{root: root, out: t.TempDir(), mode: "release"}
	targets, err := b.prepare("test_contract")
	if err != nil {
		t.Fatal(err)
	}
	b.env = buildEnvironment(filepath.Join(b.out, "go.work"))
	// 此测试检查本机源码一致性；Linux 原生插件加载由 builddemo 独立验收。
	for index, entry := range b.env {
		if strings.HasPrefix(entry, "GOOS=") {
			b.env[index] = "GOOS=" + runtime.GOOS
		}
		if strings.HasPrefix(entry, "GOARCH=") {
			b.env[index] = "GOARCH=" + runtime.GOARCH
		}
		if strings.HasPrefix(entry, "CGO_ENABLED=") && runtime.GOOS == "windows" {
			b.env[index] = "CGO_ENABLED=0"
		}
	}
	contract, err := b.collectShared(targets)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Shared[runtimeImport] == "" || contract.Shared[sharedImport] == "" || len(contract.APIFingerprint) != 64 {
		t.Fatal("shared ABI not fingerprinted")
	}
	if _, exists := contract.Shared[behaviorImport]; exists {
		t.Fatal("private action package was treated as shared ABI")
	}
	for _, target := range targets[1:] {
		data, err := os.ReadFile(filepath.Join(target.dir, "behavior", "actions.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), target.module+"/behavior/helper") {
			t.Fatal("helper import escaped version namespace")
		}
	}
}

// strconvQuote 为测试匹配稳定的 Go 字符串字面量。
func strconvQuote(value string) string { return `"` + value + `"` }

// TestSourceFingerprintReplacement 验证同一行为树模块源码在主模块及本地 replace 中产生相同共享指纹。
func TestSourceFingerprintReplacement(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "source.go")
	if err := os.WriteFile(file, []byte("package shared\nconst Value = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := listedPackage{ImportPath: sharedImport, Dir: dir, GoFiles: []string{"source.go"}, Module: &listedModule{Path: runtimeImport}}
	host, err := sourceHash(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Module = &listedModule{Path: runtimeImport, Version: "v0.0.0", Replace: &listedModule{Path: dir}}
	plugin, err := sourceHash(p)
	if err != nil {
		t.Fatal(err)
	}
	if host != plugin {
		t.Fatal("local replace creates false incompatibility")
	}
	if err = os.WriteFile(file, []byte("package shared\nconst Value = 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := sourceHash(p)
	if err != nil {
		t.Fatal(err)
	}
	if changed == host {
		t.Fatal("changed shared implementation was not detected")
	}
}

// TestStageDoesNotCopyGeneratedAndKeepsHelpers 验证暂存完整业务子树并排除过期生成结果。
func TestStageDoesNotCopyGeneratedAndKeepsHelpers(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "behavior")
	if err := os.Mkdir(filepath.Join(src, "helper"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"actions.go": "package behavior\n", "tree_gen.go": "invalid old generation", "helper/version.go": "package helper\nconst Marker=\"base\"\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageBehavior(src, dst, "bt.local/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "tree_gen.go")); !os.IsNotExist(err) {
		t.Fatal("copied stale generation")
	}
	helper := filepath.Join(dst, "helper", "version.go")
	if err := replaceStringConstant(helper, "Marker", "v2"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(helper)
	if err != nil || !strings.Contains(string(data), `"v2"`) {
		t.Fatalf("helper missing or unchanged: %s %v", data, err)
	}
}
