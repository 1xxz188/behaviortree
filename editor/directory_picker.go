package editor

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
)

// selectDirectory 只选择并检查目录，不切换或保存；取消时原工程保持不变。
func (s *Server) selectDirectory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"` // 系统窗口初始定位的绝对目录。
	}
	data, err := readBody(w, r)
	if err == nil {
		err = json.Unmarshal(data, &req)
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": "目录选择请求无效"})
		return
	}
	s.workspaceMu.RLock()
	workspace := s.path
	s.workspaceMu.RUnlock()
	if expected := r.Header.Get("X-BT-Workspace"); expected != "" {
		path, decodeErr := url.PathUnescape(expected)
		if decodeErr != nil || path != workspace {
			reply(w, 409, map[string]string{"error": "工作目录已被其他页面切换，请先导出当前草稿，再刷新页面"})
			return
		}
	}
	if req.Directory == "" {
		req.Directory = workspace
	}
	if !filepath.IsAbs(req.Directory) {
		reply(w, 400, map[string]string{"error": "请选择绝对目录路径"})
		return
	}
	if !s.pickerMu.TryLock() {
		reply(w, 409, map[string]string{"error": "已有目录选择窗口打开，请先完成或取消该窗口"})
		return
	}
	defer s.pickerMu.Unlock()
	// 系统对话框可能打开超过普通请求的 30 秒，只为此次用户交互解除写超时。
	if err = http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	selected, err := s.directoryPicker(req.Directory)
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		reply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if selected == "" {
		reply(w, 200, map[string]bool{"cancelled": true})
		return
	}
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	if s.path != workspace {
		reply(w, 409, map[string]string{"error": "选择期间工作目录已被其他页面切换，请先导出当前草稿，再刷新页面"})
		return
	}
	root, listing, err := openDirectory(selected)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	defer root.Close()
	reply(w, 200, listing)
}
