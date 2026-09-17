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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// MaxGeneratedBytes 限制整批源码和映射的大小，防止无界读取。
const MaxGeneratedBytes = 64 << 20

// generatedMapping 将源码摘要和节点位置一起持久化，拒绝交错发布或损坏的映射。
type generatedMapping struct {
	Generator string                   `json:"generator"` // 生成器所有权标记。
	Version   string                   `json:"version"`   // 工程语义版本摘要。
	Files     []generatedFileMapping   `json:"files"`     // 全部源码文件及摘要。
	Locations []codegen.SourceLocation `json:"locations"` // 每个展开节点的位置。
}

// generatedFileMapping 记录单个文件的定义树归属和内容摘要。
type generatedFileMapping struct {
	Name   string `json:"name"`   // 安全的相对文件名。
	TreeID string `json:"treeId"` // 定义树 ID，公共文件为空。
	SHA256 string `json:"sha256"` // 文件源码摘要。
}

// generatedResponseFile 使用字符串传输源码，避免字节数组被编码成 base64。
type generatedResponseFile struct {
	Name   string `json:"name"`   // 生成文件名。
	TreeID string `json:"treeId"` // 定义树 ID。
	Source string `json:"source"` // 完整 Go 源码。
}

// responseFiles 一次转换快照中的文件列表。
func responseFiles(files []codegen.GeneratedFile) []generatedResponseFile {
	result := make([]generatedResponseFile, len(files))
	for i, file := range files {
		result[i] = generatedResponseFile{Name: file.Name, TreeID: file.TreeID, Source: string(file.Source)}
	}
	return result
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
		project, _, err = PrepareProjectContext(s.root, project)
	}
	if err == nil {
		result, err = codegen.Generate(project)
	}
	if err != nil {
		generationError(w, err)
		return
	}
	reply(w, 200, map[string]any{"files": responseFiles(result.Files), "sourceMap": result.SourceMap, "version": result.Version})
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
		project, _, err = PrepareProjectContext(s.root, project)
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
	if err == nil {
		project, _, err = PrepareProjectContext(s.root, project)
	}
	if err != nil {
		generationError(w, err)
		return
	}
	resolved, err := model.ResolveGoPackage(s.path, project.Generation.PackagePath)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	dir := filepath.FromSlash(resolved.PackagePath)
	s.mu.Lock()
	files, mapping, err := readGeneratedFiles(s.root, dir, resolved.PackageName)
	s.mu.Unlock()
	if err != nil {
		status := 409
		if errors.Is(err, fs.ErrNotExist) {
			status = 404
		}
		reply(w, status, map[string]string{"error": "生成产物不完整或已修改，请重新生成：" + err.Error()})
		return
	}
	currentVersion, err := codegen.ProjectVersion(project)
	if err != nil {
		generationError(w, err)
		return
	}
	reply(w, 200, map[string]any{"files": responseFiles(files), "sourceMap": mapping.Locations, "version": mapping.Version, "directory": filepath.Join(s.path, dir), "matchesCurrent": currentVersion == mapping.Version})
}

// readGeneratedFiles 在同一批大小预算内读取清单和全部源码，并验证完整快照。
func readGeneratedFiles(root *os.Root, dir, pkg string) ([]codegen.GeneratedFile, generatedMapping, error) {
	var mapping generatedMapping
	budget := int64(MaxGeneratedBytes)
	raw, err := readArtifact(root, filepath.Join(dir, "tree_gen.map.json"), &budget)
	if err != nil {
		return nil, mapping, err
	}
	if err = json.Unmarshal(raw, &mapping); err != nil {
		return nil, mapping, err
	}
	if err = validateManifest(mapping); err != nil {
		return nil, mapping, err
	}
	files := make([]codegen.GeneratedFile, len(mapping.Files))
	for i, entry := range mapping.Files {
		source, err := readArtifact(root, filepath.Join(dir, entry.Name), &budget)
		if err != nil {
			return nil, mapping, errors.New("无法读取源码 " + entry.Name + ": " + err.Error())
		}
		files[i] = codegen.GeneratedFile{Name: entry.Name, TreeID: entry.TreeID, Source: source}
	}
	return files, mapping, validateGenerated(files, pkg, mapping)
}

// readArtifact 通过受限根读取普通文件，并消耗整批共享的读取预算。
func readArtifact(root *os.Root, name string, budget *int64) ([]byte, error) {
	// 在打开前拒绝非普通文件，防止命名管道阻塞；打开后仍复核实际文件。
	info, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > *budget {
		return nil, errors.New("产物不是普通文件或整批超过大小限制")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > *budget {
		return nil, errors.New("产物不是普通文件或整批超过大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(f, *budget+1))
	*budget -= int64(len(data))
	if *budget < 0 {
		return nil, errors.New("整批产物超过大小限制")
	}
	return data, err
}

// validateManifest 在文件访问前校验清单所有权、路径、大小写碰撞及树归属。
func validateManifest(mapping generatedMapping) error {
	if mapping.Generator != "behaviortree/codegen" || mapping.Version == "" || len(mapping.Files) < 2 {
		return errors.New("无效生成清单")
	}
	names := make(map[string]bool, len(mapping.Files))
	trees := make(map[string]bool, len(mapping.Files))
	for i, file := range mapping.Files {
		name := file.Name
		if name == "" || strings.ContainsAny(name, "/\\:") || filepath.Base(name) != name || names[strings.ToLower(name)] {
			return errors.New("文件名无效或冲突")
		}
		if i == 0 {
			if name != "glue.gen.go" || file.TreeID != "" {
				return errors.New("缺少公共 glue 文件")
			}
		} else {
			validName := model.ValidTreeID(file.TreeID) && name == codegen.TreeFileName(file.TreeID)
			if file.TreeID == "" || trees[file.TreeID] || !validName {
				return errors.New("树文件归属无效")
			}
			trees[file.TreeID] = true
		}
		if hash, err := hex.DecodeString(file.SHA256); err != nil || len(hash) != sha256.Size {
			return errors.New("无效源码摘要")
		}
		names[strings.ToLower(name)] = true
	}
	return nil
}

// validateGenerated 单次解析源码核对版本、节点身份和函数行号，防止损坏映射误联节点。
func validateGenerated(files []codegen.GeneratedFile, pkg string, mapping generatedMapping) error {
	if err := validateManifest(mapping); err != nil {
		return err
	}
	if len(files) != len(mapping.Files) {
		return errors.New("文件数量不匹配")
	}
	fset := token.NewFileSet()
	lines := make(map[string]int)
	owners := make(map[string]generatedFileMapping)
	var file *ast.File
	for i, source := range files {
		entry := mapping.Files[i]
		if source.Name != entry.Name || source.TreeID != entry.TreeID || sourceHash(source.Source) != entry.SHA256 || !generatedSource(source.Source) {
			return errors.New("源码摘要或归属不匹配")
		}
		parsed, err := parser.ParseFile(fset, source.Name, source.Source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if parsed.Name.Name != pkg {
			return errors.New("源码包名不匹配")
		}
		if i == 0 {
			file = parsed
		}
		for _, decl := range parsed.Decls {
			if function, ok := decl.(*ast.FuncDecl); ok && function.Recv == nil {
				name := function.Name.Name
				if lines[name] != 0 {
					return errors.New("重复生成函数")
				}
				lines[name] = fset.Position(function.Pos()).Line
				owners[name] = entry
			}
		}
	}
	slots := generatedSlots(file)
	var step *ast.FuncDecl
	var identities []codegen.SourceLocation
	version := ""
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
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
		owner := owners[name]
		identity.File = owner.Name
		if owner.TreeID != identity.TreeID {
			return errors.New("节点不属于定义树文件")
		}
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
