package editor

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/1xxz188/behaviortree/model"
)

// ContextScaffold 描述首次生成的业务上下文，不属于可覆盖或清理的生成产物。
type ContextScaffold struct {
	directory   string // 工程内的上下文目录。
	packageName string // 上下文声明所属的 Go 包。
	typeName    string // 去除指针后的业务类型名。
}

// PrepareProjectContext 解析本模块上下文路径；仅按需读取 go.mod，不创建文件或修改工程配置。
func PrepareProjectContext(root *os.Root, project model.Project) (model.Project, *ContextScaffold, error) {
	typeName := strings.TrimPrefix(project.Generation.ContextType, "*")
	if !model.Identifier(typeName) {
		return project, nil, fmt.Errorf("上下文类型必须为 any、类型名或 *类型名")
	}
	local := project.Generation.PackagePath
	importPath := project.Generation.ContextImport
	if importPath == "" && types.Universe.Lookup(typeName) != nil {
		return project, nil, nil
	}
	if importPath != "" {
		// 标准库沿用完整导入语义；定点检查目录，避免 go list 子进程和外部依赖解析。
		if filepath.IsLocal(importPath) && !strings.Contains(importPath, ".") {
			if info, err := os.Stat(filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(importPath))); err == nil && info.IsDir() {
				return project, nil, nil
			}
		}
		budget := int64(1 << 20)
		data, err := readArtifact(root, "go.mod", &budget)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return project, nil, err
		}
		module := ""
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "module" {
				module = fields[1]
				if strings.HasPrefix(module, "\"") {
					module, err = strconv.Unquote(module)
					if err != nil {
						return project, nil, err
					}
				}
				break
			}
		}
		switch {
		case module != "" && strings.HasPrefix(importPath, module+"/"):
			local = strings.TrimPrefix(importPath, module+"/")
		case importPath == module || strings.Contains(strings.Split(importPath, "/")[0], "."):
			// 模块根包和外部完整导入路径由业务项目管理，不推测磁盘目录。
			return project, nil, nil
		default:
			if module == "" {
				return project, nil, fmt.Errorf("本地上下文路径 %q 需要工作目录中的 go.mod；也可填写完整外部导入路径", importPath)
			}
			local = importPath
			project.Generation.ContextImport = module + "/" + local
		}
	}
	resolved, err := model.ResolveGoPackage("", local)
	if err != nil {
		return project, nil, fmt.Errorf("上下文目录无效: %w", err)
	}
	if importPath != "" && resolved.PackagePath == model.NormalizePackagePath(project.Generation.PackagePath) {
		project.Generation.ContextImport = ""
	}
	return project, &ContextScaffold{directory: resolved.PackagePath, packageName: resolved.PackageName, typeName: typeName}, nil
}

// Ensure 首次创建缺失类型；已有类型可位于任意业务文件，重复生成不会改写手写内容。
func (c *ContextScaffold) Ensure(root *os.Root) error {
	if c == nil {
		return nil
	}
	// 常见路径只读取 context.go；命中已有类型后不再枚举目录或读取生成树。
	budget := int64(MaxGeneratedBytes)
	name := filepath.Join(c.directory, "context.go")
	packageName, found, err := c.inspect(root, name, &budget)
	if err == nil && found {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	dir, err := root.Open(c.directory)
	var entries []fs.DirEntry
	if err == nil {
		entries, err = dir.ReadDir(-1)
		_ = dir.Close()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "context.go" || strings.HasPrefix(entry.Name(), "_") || strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		actualPackage, found, err := c.inspect(root, filepath.Join(c.directory, entry.Name()), &budget)
		if err != nil {
			return err
		}
		if actualPackage == "" {
			continue
		}
		if packageName != "" && actualPackage != packageName {
			return fmt.Errorf("上下文目录 %s 包含不同的包名 %s 和 %s", c.directory, packageName, actualPackage)
		}
		packageName = actualPackage
		if found {
			return nil
		}
	}
	if packageName == "" {
		packageName = c.packageName
	}
	source, err := format.Source([]byte(fmt.Sprintf("package %s\n\n// %s 保存行为树使用的业务上下文，由宿主持有并填充。\n// 此文件仅首次创建，后续生成不会覆盖；请按业务需要添加字段。\ntype %s struct {}\n", packageName, c.typeName, c.typeName)))
	if err != nil {
		return err
	}
	if err = root.MkdirAll(c.directory, 0755); err != nil {
		return err
	}
	if err = createScaffold(root, name, source); errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s 已存在但未声明 %s，请在业务文件中补充该类型", name, c.typeName)
	}
	return err
}

// inspect 在统一读取预算内检查类型声明，跳过显式排除构建的工具文件。
func (c *ContextScaffold) inspect(root *os.Root, name string, budget *int64) (string, bool, error) {
	data, err := readArtifact(root, name, budget)
	if err != nil {
		return "", false, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), name, data, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return "", false, fmt.Errorf("检查已有上下文: %w", err)
	}
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, comment := range group.List {
			if strings.TrimSpace(comment.Text) == "//go:build ignore" || strings.TrimSpace(comment.Text) == "// +build ignore" {
				return "", false, nil
			}
		}
	}
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if spec.(*ast.TypeSpec).Name.Name == c.typeName {
					return file.Name.Name, true, nil
				}
			}
		}
	}
	return file.Name.Name, false, nil
}
