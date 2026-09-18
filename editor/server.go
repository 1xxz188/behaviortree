// Package editor 提供局限在用户指定工作目录中的本地编辑与生成服务。
package editor

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
	"github.com/1xxz188/behaviortree/web"
)

// MaxProjectBytes 限制单个工程的输入体积，避免误导入大型非工程文件。
const MaxProjectBytes = 8 << 20

// Server 通过 os.Root 将工程读写限制在当前选定目录内，包括符号链接访问。
type Server struct {
	workspaceMu     sync.RWMutex                 // 请求持有目录读锁；切换和另存为持有写锁，避免关闭在用句柄。
	pickerMu        sync.Mutex                   // 同一服务最多打开一个系统目录选择窗口。
	directoryPicker func(string) (string, error) // 原生选择器边界，测试可注入确定性的用户选择。
	root            *os.Root                     // 受限文件系统根。
	path            string                       // 展示工作目录和生成产物的绝对路径。
	mu              sync.Mutex                   // 串行化文件发布，防止同一生成文件交错写入。
	mux             *http.ServeMux               // HTTP 路由与内嵌前端。
}

// New 创建本地工程服务；不会覆盖已有工程或启动后台协程。
func New(workspace string) (*Server, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	s := &Server{root: root, path: abs, mux: http.NewServeMux(), directoryPicker: pickNativeDirectory}
	s.mux.HandleFunc("GET /api/projects", s.listProjects)
	s.mux.HandleFunc("GET /api/directories", s.listDirectories)
	s.mux.HandleFunc("POST /api/directory-picker", s.selectDirectory)
	s.mux.HandleFunc("POST /api/workspace", s.switchWorkspace)
	s.mux.HandleFunc("GET /api/project", s.readProject)
	s.mux.HandleFunc("POST /api/project", s.saveProject)
	s.mux.HandleFunc("POST /api/import", s.importProject)
	s.mux.HandleFunc("POST /api/catalog", s.importCatalog)
	s.mux.HandleFunc("POST /api/validate", s.validate)
	s.mux.HandleFunc("POST /api/generate", s.generate)
	s.mux.HandleFunc("POST /api/preview", s.preview)
	s.mux.HandleFunc("POST /api/generated", s.readGenerated)
	s.mux.HandleFunc("POST /api/scaffold", s.scaffold)
	s.mux.HandleFunc("POST /api/scaffold/save", s.saveScaffold)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 404, map[string]string{"error": "未知接口"})
	})
	s.mux.Handle("/", http.FileServerFS(web.Assets()))
	return s, nil
}

// Close 关闭工作目录句柄，须在 HTTP 服务停止后调用。
func (s *Server) Close() error { return s.root.Close() }

// ServeHTTP 拒绝非本地 Host 和跨站写请求，不将本地文件能力暴露给外部网页。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !loopbackHost(r.Host) {
		reply(w, 403, map[string]string{"error": "编辑器仅允许本地访问"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !loopbackHost(u.Host) || (u.Scheme != "http" && u.Scheme != "https") {
				reply(w, 403, map[string]string{"error": "拒绝跨站请求"})
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			reply(w, 403, map[string]string{"error": "拒绝跨站请求"})
			return
		}
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			reply(w, 415, map[string]string{"error": "请求必须使用 application/json"})
			return
		}
		// 系统窗口等待用户操作时不持有工作区锁，选择器自行检查前后的目录身份。
		if r.URL.Path == "/api/directory-picker" {
			s.mux.ServeHTTP(w, r)
			return
		}
		// 文件操作与目录切换在同一锁边界内，旧页面不能把工程误存到新目录。
		if r.Method == http.MethodPost && (r.URL.Path == "/api/workspace" || r.URL.Path == "/api/project") {
			s.workspaceMu.Lock()
			defer s.workspaceMu.Unlock()
		} else {
			s.workspaceMu.RLock()
			defer s.workspaceMu.RUnlock()
		}
		if expected := r.Header.Get("X-BT-Workspace"); expected != "" && r.URL.Path != "/api/projects" {
			path, err := url.PathUnescape(expected)
			if err != nil || path != s.path {
				reply(w, 409, map[string]string{"error": "工作目录已被其他页面切换，请先导出当前草稿，再刷新页面"})
				return
			}
		}
	}
	s.mux.ServeHTTP(w, r)
}

// loopbackHost 支持 localhost、IPv4 与 IPv6 回环地址。
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidListenAddr 检查编辑器监听地址，避免误开放工作目录写权限。
func ValidListenAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	return err == nil && port != "" && loopbackHost(host)
}

// reply 写入统一 JSON 响应。
func reply(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

// readBody 有界读取请求，不运行上传内容。
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, MaxProjectBytes))
}

// projectName 仅允许工作目录顶层 JSON 文件，路径检查另由 os.Root 保证。
func projectName(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\:") && strings.HasSuffix(strings.ToLower(name), ".json") && !strings.HasPrefix(name, ".")
}

// normalizeDraft 保留未连完的树作为草稿，但拒绝无树、格式版本错误等不可编辑输入。
func normalizeDraft(p *model.Project) error {
	if p.SchemaVersion != model.SchemaVersion {
		return fmt.Errorf("不支持 schemaVersion %d", p.SchemaVersion)
	}
	if err := model.ValidateCatalogOrganization(*p); err != nil {
		return err
	}
	p.CatalogOrganization = model.NormalizedCatalogOrganization(p.CatalogOrganization)
	if len(p.Trees) == 0 {
		return errors.New("工程至少需要一棵树")
	}
	if p.Blackboard == nil {
		p.Blackboard = []model.Field{}
	}
	if p.Catalog == nil {
		p.Catalog = []model.Definition{}
	}
	seen := make(map[string]bool, len(p.Trees))
	for i := range p.Trees {
		t := &p.Trees[i]
		if !model.ValidTreeID(t.ID) {
			return errors.New("行为树 ID 必须为 1–80 个英文字母、数字或下划线")
		}
		if seen[strings.ToLower(t.ID)] {
			return fmt.Errorf("行为树 ID 重复（不区分大小写）: %s", t.ID)
		}
		seen[strings.ToLower(t.ID)] = true
		if t.Nodes == nil {
			t.Nodes = []model.Node{}
		}
		nodes := make(map[string]bool, len(t.Nodes))
		codeNames := make(map[string]bool, len(t.Nodes))
		for _, n := range t.Nodes {
			if n.ID == "" || nodes[n.ID] {
				return fmt.Errorf("树 %s 的节点 ID 为空或重复", t.ID)
			}
			nodes[n.ID] = true
			if n.CodeName != "" {
				if !model.ValidCodeName(n.CodeName) || codeNames[n.CodeName] {
					return fmt.Errorf("树 %s 的节点 %s 代码名非法或重复: %s", t.ID, n.ID, n.CodeName)
				}
				codeNames[n.CodeName] = true
			}
		}
	}
	return nil
}

// listProjects 按需枚举顶层文件，仅将可读取的工程列入选择列表。
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	f, err := s.root.Open(".")
	if err != nil {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	files := []string{}
	allFiles := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && projectName(entry.Name()) {
			allFiles = append(allFiles, entry.Name())
			if readableProject(s.root, entry.Name()) {
				files = append(files, entry.Name())
			}
		}
	}
	sort.Strings(files)
	sort.Strings(allFiles)
	reply(w, 200, map[string]any{"workspace": s.path, "files": files, "allFiles": allFiles})
}

// readProject 从受限目录读取和解析工程。
func (s *Server) readProject(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !projectName(name) {
		reply(w, 400, map[string]string{"error": "无效的 JSON 文件名"})
		return
	}
	f, err := s.root.Open(name)
	if err != nil {
		reply(w, 404, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxProjectBytes+1))
	if err != nil || len(data) > MaxProjectBytes {
		reply(w, 400, map[string]string{"error": "读取失败或工程过大"})
		return
	}
	p, err := model.Decode(data)
	if err == nil {
		err = normalizeDraft(&p)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, p)
}

// saveProject 原子保存草稿；校验和生成是单独操作。
func (s *Server) saveProject(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var req struct {
		Name      string          `json:"name"`      // 目标目录内的 JSON 文件名。
		Project   json.RawMessage `json:"project"`   // 待保存的工程快照。
		Directory string          `json:"directory"` // 另存为目录，省略时沿用当前目录。
		Overwrite bool            `json:"overwrite"` // 另存为遇到同名文件时须显式确认覆盖。
	}
	if err == nil {
		err = json.Unmarshal(data, &req)
	}
	if err != nil || !projectName(req.Name) {
		reply(w, 400, map[string]string{"error": "工程请求或文件名无效"})
		return
	}
	p, err := model.Decode(req.Project)
	if err == nil {
		err = normalizeDraft(&p)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	data, err = model.Encode(p)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	root, path := s.root, s.path
	var listing *directoryListing
	if req.Directory != "" {
		var target directoryListing
		root, target, err = openDirectory(req.Directory)
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		defer func() {
			if root != s.root {
				_ = root.Close()
			}
		}()
		path, listing = target.Workspace, &target
		if _, statErr := root.Lstat(req.Name); !req.Overwrite && !errors.Is(statErr, fs.ErrNotExist) {
			reply(w, 409, map[string]string{"error": "目标文件已存在或无法检查，请重新选择并确认覆盖"})
			return
		}
	}
	s.mu.Lock()
	err = atomicWrite(root, req.Name, data)
	s.mu.Unlock()
	if err != nil {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	// 完整写入成功后才发布新目录，失败不会改变原工程的保存位置。
	if root != s.root {
		old := s.root
		s.root, s.path = root, path
		_ = old.Close()
	}
	response := map[string]any{"name": req.Name, "workspace": s.path}
	if listing != nil {
		response["files"] = listing.Files
		response["allFiles"] = listing.AllFiles
	}
	reply(w, 200, response)
}

// importProject 只规范化导入数据，不写入磁盘。
func (s *Server) importProject(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var p model.Project
	if err == nil {
		p, err = model.Decode(data)
	}
	if err == nil {
		err = normalizeDraft(&p)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, p)
}

// importCatalog 校验 Go 导出的节点声明，返回统一数组形式。
func (s *Server) importCatalog(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var catalog []model.Definition
	if err == nil && !bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		err = errors.New("业务定义必须为 JSON 数组，单个定义也须放入数组")
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&catalog)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errors.New("目录后含多余 JSON 数据")
			}
		}
	}
	if err == nil {
		_, err = model.ExportCatalog(catalog)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if catalog == nil {
		catalog = []model.Definition{}
	}
	reply(w, 200, catalog)
}

// validate 对 Web 和 CLI 使用同一个元数据验证器。
func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var p model.Project
	if err == nil {
		p, err = model.Decode(data)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	d := model.Validate(p)
	if d == nil {
		d = []model.Diagnostic{}
	}
	reply(w, 200, map[string]any{"diagnostics": d})
}

// generate 只向工程内的生成包路径发布产物，不编译或执行浏览器提供的代码。
func (s *Server) generate(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var p model.Project
	var result codegen.Result
	var context *ContextScaffold
	if err == nil {
		p, err = model.Decode(data)
	}
	if err == nil {
		p, context, err = PrepareProjectContext(s.root, p)
	}
	if err == nil {
		result, err = codegen.Generate(p)
	}
	if err != nil {
		var validation *codegen.ValidationError
		if errors.As(err, &validation) {
			reply(w, 422, map[string]any{"error": err.Error(), "diagnostics": validation.Diagnostics})
		} else {
			reply(w, 400, map[string]string{"error": err.Error()})
		}
		return
	}
	resolved, err := model.ResolveGoPackage(s.path, p.Generation.PackagePath)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	rel := filepath.FromSlash(resolved.PackagePath)
	s.mu.Lock()
	err = context.Ensure(s.root)
	if err == nil {
		err = writeGenerated(s.root, rel, result)
	}
	s.mu.Unlock()
	if err != nil {
		reply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, map[string]any{"files": responseFiles(result.Files), "sourceMap": result.SourceMap, "version": result.Version, "directory": filepath.Join(s.path, rel)})
}

// WriteProjectGenerated 通过工程根目录发布产物，阻止路径中的符号链接逃出工程。
func WriteProjectGenerated(projectDir, packagePath string, result codegen.Result) error {
	resolved, err := model.ResolveGoPackage(projectDir, packagePath)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeGenerated(root, filepath.FromSlash(resolved.PackagePath), result)
}

// WriteGenerated 将完整工程写入 CLI 指定目录，保护手写文件并跳过未变化的产物。
func WriteGenerated(dir string, result codegen.Result) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeGenerated(root, ".", result)
}

// atomicWrite 使用同目录临时文件和重命名发布，失败时保留原文件。
func atomicWrite(root *os.Root, name string, data []byte) error {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), ".bt-"+hex.EncodeToString(nonce[:])+".tmp")
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
