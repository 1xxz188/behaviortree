package editor

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// directoryEntry 表示可进入的直接子目录，不递归扫描文件树。
type directoryEntry struct {
	Name string `json:"name"` // 显示用目录名称。
	Path string `json:"path"` // 下一次浏览使用的绝对路径。
}

// directoryListing 同时提供目录导航与工程候选，避免同一目录重复读取。
type directoryListing struct {
	Workspace   string           `json:"workspace"`   // 已验证可读取的绝对目录。
	Parent      string           `json:"parent"`      // 上级目录；文件系统根目录为空。
	Directories []directoryEntry `json:"directories"` // 仅包含直接子目录。
	Files       []string         `json:"files"`       // 当前目录内的 JSON 候选。
}

// openDirectory 打开已有目录并一次枚举直接子项；失败时不创建目录或改变工作区。
func openDirectory(path string) (*os.Root, directoryListing, error) {
	var listing directoryListing
	if !filepath.IsAbs(path) {
		return nil, listing, fmt.Errorf("请选择绝对目录路径")
	}
	path = filepath.Clean(path)
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, listing, err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		_ = root.Close()
		return nil, listing, err
	}
	listing = directoryListing{Workspace: path, Directories: []directoryEntry{}, Files: []string{}}
	if parent := filepath.Dir(path); parent != path {
		listing.Parent = parent
	}
	for _, entry := range entries {
		if entry.IsDir() {
			listing.Directories = append(listing.Directories, directoryEntry{Name: entry.Name(), Path: filepath.Join(path, entry.Name())})
		} else if entry.Type()&fs.ModeSymlink == 0 && projectName(entry.Name()) {
			listing.Files = append(listing.Files, entry.Name())
		}
	}
	return root, listing, nil
}

// listDirectories 按用户导航按需读取一级目录，浏览本身不切换工作目录。
func (s *Server) listDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.path
	}
	root, listing, err := openDirectory(path)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	defer root.Close()
	reply(w, 200, listing)
}

// switchWorkspace 在目标目录可读后替换根句柄；调用者持有工作区写锁。
func (s *Server) switchWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"` // 用户在目录选择器中确认的绝对路径。
	}
	data, err := readBody(w, r)
	if err == nil {
		err = json.Unmarshal(data, &req)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": "目录请求无效"})
		return
	}
	root, listing, err := openDirectory(req.Directory)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	old := s.root
	s.root, s.path = root, listing.Workspace
	_ = old.Close()
	reply(w, 200, listing)
}
