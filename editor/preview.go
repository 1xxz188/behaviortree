package editor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// MaxGeneratedBytes 限制单个产物的读取大小，容纳展开后的代码并防止无界读取。
const MaxGeneratedBytes = 64 << 20

// generatedMapping 将源码摘要和节点位置一起持久化，拒绝交错发布或损坏的映射。
type generatedMapping struct {
	Version    string                   `json:"version"`    // 工程语义版本摘要。
	SourceHash string                   `json:"sourceHash"` // 完整源码 SHA-256 摘要。
	Locations  []codegen.SourceLocation `json:"locations"`  // 每个展开节点的位置。
}

// sourceHash 计算源码摘要，不依赖目录或文件时间。
func sourceHash(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

// preview 使用正式生成器返回源码和定位信息，不创建目录或写入产物。
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var project model.Project
	var result codegen.Result
	if err == nil {
		project, err = model.Decode(data)
	}
	if err == nil {
		result, err = codegen.Generate(project)
	}
	if err != nil {
		generationError(w, err)
		return
	}
	reply(w, 200, map[string]any{"source": string(result.Source), "sourceMap": result.SourceMap, "version": result.Version})
}

// scaffold 仅预览业务函数骨架，用户可以复制所需函数到自己的业务文件。
func (s *Server) scaffold(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var project model.Project
	var source []byte
	if err == nil {
		project, err = model.Decode(data)
	}
	if err == nil {
		source, err = codegen.Scaffold(project)
	}
	if err != nil {
		generationError(w, err)
		return
	}
	reply(w, 200, map[string]string{"source": string(source)})
}

// generationError 保留可定位的校验问题，供预览与骨架面板复用。
func generationError(w http.ResponseWriter, err error) {
	var validation *codegen.ValidationError
	if errors.As(err, &validation) {
		reply(w, 422, map[string]any{"error": err.Error(), "diagnostics": validation.Diagnostics})
	} else {
		reply(w, 400, map[string]string{"error": err.Error()})
	}
}

// readGenerated 读取上次实际发布的产物；同锁读取源码和映射，避免与发布交错。
func (s *Server) readGenerated(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var project model.Project
	if err == nil {
		project, err = model.Decode(data)
	}
	if err != nil {
		generationError(w, err)
		return
	}
	pkg := project.Generation.Package
	if !model.Identifier(pkg) {
		reply(w, 400, map[string]string{"error": "目标包名必须为 Go 标识符"})
		return
	}
	dir := filepath.Join("generated", pkg)
	s.mu.Lock()
	source, err := s.readArtifact(filepath.Join(dir, "tree_gen.go"))
	var raw []byte
	if err == nil {
		raw, err = s.readArtifact(filepath.Join(dir, "tree_gen.map.json"))
	}
	s.mu.Unlock()
	if err != nil {
		status := 409
		if errors.Is(err, fs.ErrNotExist) {
			status = 404
		}
		reply(w, status, map[string]string{"error": err.Error()})
		return
	}
	var mapping generatedMapping
	if err = json.Unmarshal(raw, &mapping); err == nil {
		err = validateGenerated(source, pkg, mapping)
	}
	if err != nil {
		reply(w, 409, map[string]string{"error": "生成产物不完整或已修改，请重新生成：" + err.Error()})
		return
	}
	currentVersion, err := codegen.ProjectVersion(project)
	if err != nil {
		generationError(w, err)
		return
	}
	reply(w, 200, map[string]any{"source": string(source), "sourceMap": mapping.Locations, "version": mapping.Version, "path": filepath.Join(s.path, dir, "tree_gen.go"), "matchesCurrent": currentVersion == mapping.Version})
}

// readArtifact 通过受限根读取普通文件，不跟随越界符号链接或读取特殊设备。
func (s *Server) readArtifact(name string) ([]byte, error) {
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxGeneratedBytes {
		return nil, errors.New("产物不是普通文件或超过大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxGeneratedBytes+1))
	if len(data) > MaxGeneratedBytes {
		return nil, errors.New("产物超过大小限制")
	}
	return data, err
}

// validateGenerated 单次解析源码核对版本、节点身份和函数行号，防止损坏映射误联节点。
func validateGenerated(source []byte, pkg string, mapping generatedMapping) error {
	if mapping.SourceHash == "" || mapping.SourceHash != sourceHash(source) {
		return errors.New("源码摘要不匹配")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "tree_gen.go", source, 0)
	if err != nil {
		return err
	}
	if file.Name.Name != pkg {
		return errors.New("源码包名不匹配")
	}
	lines := make(map[string]int)
	slots := generatedSlots(file)
	var step *ast.FuncDecl
	var identities []codegen.SourceLocation
	version := ""
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		lines[function.Name.Name] = fset.Position(function.Pos()).Line
		if function.Name.Name == "btStep" {
			step = function
		}
		if function.Name.Name != "NewProgram" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if assignment, ok := node.(*ast.AssignStmt); ok && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
				if name, ok := assignment.Lhs[0].(*ast.Ident); ok && name.Name == "version" {
					version = stringLiteral(assignment.Rhs[0])
				}
			}
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			array, ok := literal.Type.(*ast.ArrayType)
			if !ok {
				return true
			}
			selector, ok := array.Elt.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Node" {
				return true
			}
			alias, ok := selector.X.(*ast.Ident)
			if !ok || alias.Name != "bt" {
				return true
			}
			for _, entry := range literal.Elts {
				identity := codegen.SourceLocation{Index: len(identities)}
				if item, ok := entry.(*ast.CompositeLit); ok {
					for _, element := range item.Elts {
						pair, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := pair.Key.(*ast.Ident)
						if !ok {
							continue
						}
						if key.Name == "ID" {
							identity.NodeID = stringLiteral(pair.Value)
						}
						if key.Name == "TreeID" {
							identity.TreeID = stringLiteral(pair.Value)
						}
					}
				}
				identities = append(identities, identity)
			}
			return false
		})
	}
	if version == "" || mapping.Version != version || len(identities) == 0 || len(identities) != len(mapping.Locations) {
		return errors.New("版本或节点数量不匹配")
	}
	dispatch, err := generatedDispatch(step, slots)
	if err != nil || len(dispatch) != len(identities) {
		return errors.New("节点分派与源码不匹配")
	}
	for i, identity := range identities {
		location := mapping.Locations[i]
		name := location.FunctionName
		if name == "" || dispatch[i] != name {
			return errors.New("节点函数与分派槽位不匹配")
		}
		identity.FunctionName = location.FunctionName
		identity.Line = lines[name]
		if identity.Line == 0 || identity.NodeID == "" || identity.TreeID == "" || identity != mapping.Locations[i] {
			return errors.New("节点位置与源码不匹配")
		}
	}
	return nil
}

// generatedSlots 读取生成器的顶层 iota 槽位常量，不执行源码。
func generatedSlots(file *ast.File) map[string]int {
	slots := make(map[string]int)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		var inherited []ast.Expr
		for ordinal, declaration := range group.Specs {
			spec, ok := declaration.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if len(spec.Values) != 0 {
				inherited = spec.Values
			}
			if len(spec.Names) != 1 || len(inherited) != 1 {
				continue
			}
			if name, ok := inherited[0].(*ast.Ident); ok && name.Name == "iota" {
				slots[spec.Names[0].Name] = ordinal
			}
		}
	}
	return slots
}

// generatedDispatch 一次读取 btStep 的直接分派，确保映射不能将一个槽位关联到其他节点函数。
func generatedDispatch(step *ast.FuncDecl, slots map[string]int) (map[int]string, error) {
	invalid := errors.New("无效的节点分派")
	if step == nil || step.Body == nil || len(step.Body.List) != 2 {
		return nil, invalid
	}
	statement, ok := step.Body.List[0].(*ast.SwitchStmt)
	if !ok {
		return nil, invalid
	}
	tag, ok := statement.Tag.(*ast.Ident)
	if !ok || tag.Name != "node" || statement.Init != nil {
		return nil, invalid
	}
	dispatch := make(map[int]string)
	for _, entry := range statement.Body.List {
		branch, ok := entry.(*ast.CaseClause)
		if !ok {
			return nil, invalid
		}
		if len(branch.List) == 0 {
			continue
		}
		if len(branch.List) != 1 || len(branch.Body) != 1 {
			return nil, invalid
		}
		constant, ok := branch.List[0].(*ast.Ident)
		if !ok {
			return nil, invalid
		}
		slot, ok := slots[constant.Name]
		if !ok || dispatch[slot] != "" {
			return nil, invalid
		}
		returned, ok := branch.Body[0].(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			return nil, invalid
		}
		call, ok := returned.Results[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return nil, invalid
		}
		function, ok := call.Fun.(*ast.Ident)
		if !ok {
			return nil, invalid
		}
		frame, ok := call.Args[0].(*ast.Ident)
		if !ok || frame.Name != "f" {
			return nil, invalid
		}
		dispatch[slot] = function.Name
	}
	return dispatch, nil
}

// stringLiteral 读取 AST 字符串常量，不执行源码表达式。
func stringLiteral(expr ast.Expr) string {
	if value, ok := expr.(*ast.BasicLit); ok && value.Kind == token.STRING {
		text, _ := strconv.Unquote(value.Value)
		return text
	}
	return ""
}
