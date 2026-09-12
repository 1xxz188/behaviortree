// Package hotload 加载与宿主构建契约一致的 Go 原生插件。
package hotload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
)

// Contract 描述共享 ABI；构建工具必须为宿主和插件提供同一份契约。
type Contract struct {
	GoVersion      string            `json:"goVersion"`      // 完整工具链版本。
	GOOS           string            `json:"goos"`           // 目标操作系统。
	GOARCH         string            `json:"goarch"`         // 目标架构。
	CGOEnabled     string            `json:"cgoEnabled"`     // CGO 开关。
	Tags           []string          `json:"tags"`           // 排序后的构建标签。
	Mode           string            `json:"mode"`           // release 或 debug。
	Flags          []string          `json:"flags"`          // 影响共享代码的编译选项。
	Environment    map[string]string `json:"environment"`    // CGO、GOAMD64 等影响代码生成的环境。
	Shared         map[string]string `json:"shared"`         // 共享包导入路径到源码与模块信息 SHA256。
	APIFingerprint string            `json:"apiFingerprint"` // 框架和固定业务上下文源码指纹。
}

// Manifest 将唯一版本与二进制文件绑定，放在 plugin.so.json 旁车文件。
type Manifest struct {
	SchemaVersion int      `json:"schemaVersion"` // 当前格式为 1。
	Version       string   `json:"version"`       // 发布的不可复用版本号。
	PrivateModule string   `json:"privateModule"` // 本版本所有可变业务代码的唯一模块路径。
	SHA256        string   `json:"sha256"`        // 插件文件校验和。
	Contract      Contract `json:"contract"`      // 与宿主一致的构建契约。
}

// ReadManifest 读取严格 JSON 旁车，拒绝未知字段及尾随内容。
func ReadManifest(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	var m Manifest
	if err = d.Decode(&m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("manifest contains trailing data")
	}
	return m, nil
}

// Check 拒绝工具链、参数、共享依赖或 ABI 不一致的版本。
func (m Manifest) Check(expected Contract) error {
	if m.SchemaVersion != 1 || m.Version == "" || m.PrivateModule == "" {
		return fmt.Errorf("invalid manifest identity")
	}
	if len(m.SHA256) != 64 {
		return fmt.Errorf("invalid plugin SHA256")
	}
	if expected.GoVersion == "" || expected.APIFingerprint == "" || len(expected.Shared) == 0 {
		return fmt.Errorf("incomplete host build contract")
	}
	if !reflect.DeepEqual(m.Contract, expected) {
		return fmt.Errorf("plugin build contract differs from host (toolchain, flags, ABI or shared dependencies)")
	}
	return nil
}

// FileSHA256 在加载前校验插件文件；调用方应使用只读、可信发布目录防止校验后替换。
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
