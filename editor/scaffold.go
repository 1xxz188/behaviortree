package editor

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/model"
)

// saveScaffold 将业务骨架写入生成包目录，已有文件必须针对当前内容再次确认覆盖。
func (s *Server) saveScaffold(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(w, r)
	var req struct {
		Project      json.RawMessage `json:"project"`      // 点击下载时的完整工程快照。
		Overwrite    bool            `json:"overwrite"`    // 用户明确确认覆盖已有业务文件。
		ExpectedHash string          `json:"expectedHash"` // 用户确认时已有文件的内容摘要。
	}
	if err == nil {
		err = json.Unmarshal(data, &req)
	}
	var project model.Project
	var source []byte
	var context *ContextScaffold
	if err == nil {
		project, err = model.Decode(req.Project)
	}
	if err == nil {
		project, context, err = PrepareProjectContext(s.root, project)
	}
	if err == nil {
		source, err = codegen.Scaffold(project)
	}
	if err != nil {
		generationError(w, err)
		return
	}
	resolved, err := model.ResolveGoPackage(s.path, project.Generation.PackagePath)
	if err != nil {
		generationError(w, err)
		return
	}
	name := filepath.Join(filepath.FromSlash(resolved.PackagePath), "actions.go")
	path := filepath.Join(s.path, name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = context.Ensure(s.root); err != nil {
		reply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	if err = s.root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		reply(w, 409, map[string]string{"error": err.Error(), "path": path})
		return
	}
	if !req.Overwrite {
		// 排他创建将同名检查与占用合为一步，多个页面同时下载也不会静默覆盖。
		err = createScaffold(s.root, name, source)
		if err == nil {
			reply(w, 200, map[string]string{"path": path})
			return
		}
		if !errors.Is(err, fs.ErrExist) {
			reply(w, 409, map[string]string{"error": err.Error(), "path": path})
			return
		}
	}
	hash, err := scaffoldFileHash(s.root, name)
	if err != nil {
		reply(w, 409, map[string]string{"error": "无法确认已有业务文件，请重新下载：" + err.Error(), "path": path})
		return
	}
	if !req.Overwrite || req.ExpectedHash != hash {
		message := "actions.go 已存在，请确认是否覆盖；覆盖会替换已有业务实现"
		if req.Overwrite {
			message = "actions.go 在确认期间已发生变化，请重新确认覆盖"
		}
		reply(w, 409, map[string]string{"error": message, "path": path, "existingHash": hash})
		return
	}
	if err = atomicWrite(s.root, name, source); err != nil {
		reply(w, 409, map[string]string{"error": err.Error(), "path": path})
		return
	}
	reply(w, 200, map[string]string{"path": path})
}

// createScaffold 只创建不存在的文件，写入失败时清除本次创建的残缺内容。
func createScaffold(root *os.Root, name string, source []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write(source); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = root.Remove(name)
	}
	return err
}

// scaffoldFileHash 拒绝目录、符号链接和特殊文件，并限制读取已有业务源码的大小。
func scaffoldFileHash(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("actions.go 不是普通文件")
	}
	budget := int64(MaxGeneratedBytes)
	source, err := readArtifact(root, name, &budget)
	if err != nil {
		return "", err
	}
	return sourceHash(source), nil
}
