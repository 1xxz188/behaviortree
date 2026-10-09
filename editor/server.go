// Package editor 提供局限在用户指定工作目录中的本地编辑与生成服务。
package editor

import (
	"context"
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

// Server 保留启动目录作为默认值；每个请求独立打开受限根，防止页签间切换目录。
type Server struct {
	directoryPicker func(string) (string, error) // 原生选择器边界，测试可注入确定性的用户选择。
	path            string                       // 服务启动时的默认绝对目录，不随请求改变。
	lockMu          sync.Mutex                   // 保护按目录分配的发布锁及引用计数。
	locks           map[string]*directoryLock    // 仅保留正在使用的目录锁。
	mux             *http.ServeMux               // HTTP 路由与内嵌前端。
}

// directoryLock 让同目录的生成、读取与保存串行，不阻塞其他目录。
type directoryLock struct {
	mu   sync.Mutex // 同一目录的文件发布与读取边界。
	refs int        // 等待或持有此锁的请求数。
}

// workspaceRequest 保存当前 HTTP 请求唯一的工作目录及受限根。
type workspaceRequest struct {
	path string   // 当前页签请求的绝对目录。
	root *os.Root // 仅供本次请求使用，返回前关闭。
}

// workspaceContextKey 避免请求上下文键与其他包冲突。
type workspaceContextKey struct{}

// requestWorkspace 从已校验的请求上下文获取目录，不读取共享可变状态。
func requestWorkspace(r *http.Request) *workspaceRequest {
	return r.Context().Value(workspaceContextKey{}).(*workspaceRequest)
}

// lockDirectory 只串行化同一目录的发布操作，并在最后一个请求离开时释放锁条目。
func (s *Server) lockDirectory(path string) func() {
	s.lockMu.Lock()
	if s.locks == nil {
		s.locks = make(map[string]*directoryLock)
	}
	entry := s.locks[path]
	if entry == nil {
		entry = &directoryLock{}
		s.locks[path] = entry
	}
	entry.refs++
	s.lockMu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.lockMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.locks, path)
		}
		s.lockMu.Unlock()
	}
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
	_ = root.Close()
	s := &Server{path: abs, mux: http.NewServeMux(), directoryPicker: pickNativeDirectory}
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

// Close 保留服务生命周期接口；请求目录句柄已在各自请求结束时关闭。
func (s *Server) Close() error { return nil }

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
		// 头部是本次请求的目标目录；无头部时使用启动默认目录。
		path := s.path
		if encoded := r.Header.Get("X-BT-Workspace"); encoded != "" {
			decoded, err := url.PathUnescape(encoded)
			if err != nil || !filepath.IsAbs(decoded) {
				reply(w, 400, map[string]string{"error": "请选择绝对目录路径"})
				return
			}
			path = filepath.Clean(decoded)
		}
		workspace := &workspaceRequest{path: path}
		// 目标由请求体或查询参数指定的接口自行打开根；其余文件接口只打开一次。
		switch r.URL.Path {
		case "/api/projects", "/api/project", "/api/generate", "/api/preview", "/api/generated", "/api/scaffold", "/api/scaffold/save":
			if !(r.URL.Path == "/api/project" && r.Method == http.MethodPost) {
				root, err := os.OpenRoot(path)
				if err != nil {
					reply(w, 400, map[string]string{"error": err.Error()})
					return
				}
				workspace.root = root
				defer root.Close()
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), workspaceContextKey{}, workspace))
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
	if diagnostics := model.ValidateEvents(*p); len(diagnostics) != 0 {
		return fmt.Errorf("%s: %s", diagnostics[0].Field, diagnostics[0].Message)
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
	if p.Events == nil {
		p.Events = []model.EventDefinition{}
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
	workspace := requestWorkspace(r)
	f, err := workspace.root.Open(".")
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
			if readableProject(workspace.root, entry.Name()) {
				files = append(files, entry.Name())
			}
		}
	}
	sort.Strings(files)
	sort.Strings(allFiles)
	reply(w, 200, map[string]any{"workspace": workspace.path, "files": files, "allFiles": allFiles})
}

// readProject 从受限目录读取和解析工程。
func (s *Server) readProject(w http.ResponseWriter, r *http.Request) {
	workspace := requestWorkspace(r)
	name := r.URL.Query().Get("name")
	if !projectName(name) {
		reply(w, 400, map[string]string{"error": "无效的 JSON 文件名"})
		return
	}
	f, err := workspace.root.Open(name)
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
	workspace := requestWorkspace(r)
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
	path := workspace.path
	if req.Directory != "" {
		path = req.Directory
	}
	var root *os.Root
	var listing *directoryListing
	if req.Directory != "" {
		var target directoryListing
		root, target, err = openDirectory(req.Directory)
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		defer root.Close()
		path, listing = target.Workspace, &target
	} else {
		root, err = os.OpenRoot(path)
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		defer root.Close()
	}
	unlock := s.lockDirectory(path)
	defer unlock()
	if req.Directory != "" {
		// 同名检查与发布处于同一目录锁内，避免两个页签同时另存为时静默覆盖。
		if _, statErr := root.Lstat(req.Name); !req.Overwrite && !errors.Is(statErr, fs.ErrNotExist) {
			reply(w, 409, map[string]string{"error": "目标文件已存在或无法检查，请重新选择并确认覆盖"})
			return
		}
	}
	err = atomicWrite(root, req.Name, data)
	if err != nil {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	// 成功后将目标目录交给发起另存为的页签，不改变其他请求的目录。
	response := map[string]any{"name": req.Name, "workspace": path}
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

// importCatalog 校验自包含交换对象并完整返回事件及业务定义。
func (s *Server) importCatalog(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var exchange model.CatalogExchange
	if err == nil {
		exchange, err = model.DecodeCatalogExchange(data)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, exchange)
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
	workspace := requestWorkspace(r)
	data, err := readBody(w, r)
	var p model.Project
	var result codegen.Result
	var context *ContextScaffold
	if err == nil {
		p, err = model.Decode(data)
	}
	if err == nil {
		p, context, err = PrepareProjectContext(workspace.root, p)
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
	resolved, err := model.ResolveGoPackage(workspace.path, p.Generation.PackagePath)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	rel := filepath.FromSlash(resolved.PackagePath)
	unlock := s.lockDirectory(workspace.path)
	err = context.Ensure(workspace.root)
	if err == nil {
		err = writeGenerated(workspace.root, rel, result)
	}
	unlock()
	if err != nil {
		reply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, map[string]any{"files": responseFiles(result.Files), "sourceMap": result.SourceMap, "version": result.Version, "diagnostics": result.Diagnostics, "directory": filepath.Join(workspace.path, rel)})
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
