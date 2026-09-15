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

// Server 通过 os.Root 将工程读写限制在固定目录内，包括符号链接访问。
type Server struct {
	root *os.Root       // 受限文件系统根。
	path string         // 展示工作目录和生成产物的绝对路径。
	mu   sync.Mutex     // 串行化文件发布，防止同一生成文件交错写入。
	mux  *http.ServeMux // HTTP 路由与内嵌前端。
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
	s := &Server{root: root, path: abs, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/projects", s.listProjects)
	s.mux.HandleFunc("GET /api/project", s.readProject)
	s.mux.HandleFunc("POST /api/project", s.saveProject)
	s.mux.HandleFunc("POST /api/import", s.importProject)
	s.mux.HandleFunc("POST /api/catalog", s.importCatalog)
	s.mux.HandleFunc("POST /api/validate", s.validate)
	s.mux.HandleFunc("POST /api/generate", s.generate)
	s.mux.HandleFunc("POST /api/preview", s.preview)
	s.mux.HandleFunc("POST /api/generated", s.readGenerated)
	s.mux.HandleFunc("POST /api/scaffold", s.scaffold)
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
			return errors.New("行为树 ID 不能为空，且只能包含英文字母、数字和下划线")
		}
		if seen[t.ID] {
			return fmt.Errorf("行为树 ID 重复: %s", t.ID)
		}
		seen[t.ID] = true
		if t.Nodes == nil {
			t.Nodes = []model.Node{}
		}
		nodes := make(map[string]bool, len(t.Nodes))
		for _, n := range t.Nodes {
			if n.ID == "" || nodes[n.ID] {
				return fmt.Errorf("树 %s 的节点 ID 为空或重复", t.ID)
			}
			nodes[n.ID] = true
		}
	}
	return nil
}

// listProjects 返回工作目录绝对路径和已有工程文件名，不递归遍历整个工作目录。
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
	for _, entry := range entries {
		if !entry.IsDir() && projectName(entry.Name()) && entry.Type()&fs.ModeSymlink == 0 {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	reply(w, 200, map[string]any{"workspace": s.path, "files": files})
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
		Name    string          `json:"name"`
		Project json.RawMessage `json:"project"`
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
	if err == nil {
		s.mu.Lock()
		err = atomicWrite(s.root, req.Name, data)
		s.mu.Unlock()
	}
	if err != nil {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, map[string]string{"name": req.Name})
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

// generate 只写受控 generated 目录下的生成产物，不编译或执行浏览器提供的代码。
func (s *Server) generate(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var p model.Project
	var result codegen.Result
	if err == nil {
		p, err = model.Decode(data)
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
	rel := filepath.Join("generated", p.Generation.Package)
	s.mu.Lock()
	err = writeGenerated(s.root, rel, result)
	s.mu.Unlock()
	if err != nil {
		reply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, map[string]any{"source": string(result.Source), "sourceMap": result.SourceMap, "version": result.Version, "path": filepath.Join(s.path, rel, "tree_gen.go")})
}

// WriteGenerated 将结果写入 CLI 明确指定的目录，拒绝覆盖手写 tree_gen.go。
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

// writeGenerated 发布可再生成文件；源映射以版本摘要关联代码。
func writeGenerated(root *os.Root, dir string, result codegen.Result) error {
	if err := root.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := filepath.Join(dir, "tree_gen.go")
	if f, err := root.Open(name); err == nil {
		head, readErr := io.ReadAll(io.LimitReader(f, 512))
		_ = f.Close()
		if readErr != nil {
			return readErr
		}
		if !bytes.Contains(head, []byte("Code generated")) || !bytes.Contains(head, []byte("DO NOT EDIT")) {
			return fmt.Errorf("拒绝覆盖手写文件 %s", name)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	mapping, err := json.MarshalIndent(generatedMapping{
		Version: result.Version, SourceHash: sourceHash(result.Source), Locations: result.SourceMap,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWrite(root, filepath.Join(dir, "tree_gen.map.json"), mapping); err != nil {
		return err
	}
	return atomicWrite(root, name, result.Source)
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
