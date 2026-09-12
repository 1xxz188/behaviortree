package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	runtimeImport  = "github.com/1xxz188/behaviortree"    // 固定运行时 ABI 路径。
	behaviorImport = runtimeImport + "/examples/behavior" // 所有可热更源码必须在该私有子树内。
	sharedImport   = runtimeImport + "/examples/shared"   // 宿主固定业务上下文。
)

// rewriteImports 通过 Go AST 重写可变包引用，避免只修改 .so 文件名却复用旧 helper 包。
func rewriteImports(name string, data []byte, module string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		if path == behaviorImport || strings.HasPrefix(path, behaviorImport+"/") {
			spec.Path.Value = strconv.Quote(module + "/behavior" + strings.TrimPrefix(path, behaviorImport))
		}
	}
	var result strings.Builder
	if err = format.Node(&result, fset, file); err != nil {
		return nil, err
	}
	return []byte(result.String()), nil
}

// stageBehavior 复制完整私有源码树，重写全部 Go 文件的内部引用；不复制测试及旧生成文件。
func stageBehavior(src, dst, module string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("mutable source symlink is not supported: %s", path)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if strings.HasSuffix(rel, "_test.go") || rel == "tree_gen.go" || rel == "tree_gen.map.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(rel, ".go") {
			data, err = rewriteImports(path, data, module)
			if err != nil {
				return err
			}
		}
		return os.WriteFile(target, data, 0644)
	})
}

// replaceStringConstant 修改示例私有 helper 的常量以实际验证同名 Go 包可以连续更新。
func replaceStringConstant(path, name, value string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return err
	}
	found := false
	ast.Inspect(f, func(node ast.Node) bool {
		decl, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, id := range decl.Names {
			if id.Name == name && i < len(decl.Values) {
				decl.Values[i] = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}
				found = true
			}
		}
		return true
	})
	if !found {
		return fmt.Errorf("constant %s missing from %s", name, path)
	}
	var output strings.Builder
	if err = format.Node(&output, fset, f); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(output.String()), 0644)
}
