package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/1xxz188/behaviortree/hotload"
)

// listedPackage 是 go list 提供的实际参与构建的包，不扫描无关业务目录。
type listedPackage struct {
	ImportPath                                                                                 string        // 包的稳定导入路径。
	Dir                                                                                        string        // 包源码目录。
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SysoFiles, EmbedFiles []string      // 当前构建选择的源文件。
	Module                                                                                     *listedModule // 模块版本同时进入指纹。
}

// listedModule 在本地 replace 时采用替换目标的版本，避免同一行为树模块源码出现虚假的版本差异。
type listedModule struct {
	Path, Version, Sum string        // 原模块身份。
	Replace            *listedModule // 生效的替换目标。
}

// sourceHash 对包路径、模块身份和按名称排序的实际源码生成确定性指纹。
func sourceHash(p listedPackage) (string, error) {
	h := sha256.New()
	fmt.Fprintln(h, p.ImportPath)
	if p.Module != nil {
		effective := p.Module
		if p.Module.Replace != nil {
			effective = p.Module.Replace
		}
		fmt.Fprintf(h, "%s\n%s\n%s\n", p.Module.Path, effective.Version, effective.Sum)
	}
	var files []string
	for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SysoFiles, p.EmbedFiles} {
		files = append(files, group...)
	}
	sort.Strings(files)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(p.Dir, name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(name), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// collectShared 收集宿主与所有版本的共享包并集，拒绝同路径源码不同的构建。
func (b *builder) collectShared(targets []buildTarget) (hotload.Contract, error) {
	envData, err := b.goOutput(b.root, "env", "-json", "GOAMD64", "GOEXPERIMENT", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS")
	if err != nil {
		return hotload.Contract{}, err
	}
	var env map[string]string
	if err = json.Unmarshal(envData, &env); err != nil {
		return hotload.Contract{}, err
	}
	c := hotload.Contract{GoVersion: "go1.26.7", GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1", Mode: b.mode, Flags: b.commonFlags(), Environment: env, Shared: make(map[string]string)}
	if b.mode == "debug" {
		c.Tags = []string{"debug"}
	}
	api := make(map[string]string)
	for _, target := range targets {
		args := append([]string{"list", "-deps", "-json"}, b.commonFlags()...)
		args = append(args, target.pattern)
		data, err := b.goOutput(target.dir, args...)
		if err != nil {
			return c, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p listedPackage
			if err = dec.Decode(&p); err == io.EOF {
				break
			} else if err != nil {
				return c, err
			}
			if p.ImportPath == "C" || p.ImportPath == target.importPath || strings.HasPrefix(p.ImportPath, "bt.local/") {
				continue
			}
			if strings.HasPrefix(p.ImportPath, behaviorImport) {
				if target.private {
					return c, fmt.Errorf("plugin imports unversioned mutable package %s", p.ImportPath)
				}
				continue
			}
			sum, err := sourceHash(p)
			if err != nil {
				return c, err
			}
			if old, exists := c.Shared[p.ImportPath]; exists && old != sum {
				return c, fmt.Errorf("shared source changed during build: %s", p.ImportPath)
			}
			c.Shared[p.ImportPath] = sum
			if p.ImportPath == runtimeImport || strings.HasPrefix(p.ImportPath, sharedImport) {
				api[p.ImportPath] = sum
			}
		}
	}
	data, _ := json.Marshal(api)
	sum := sha256.Sum256(data)
	c.APIFingerprint = hex.EncodeToString(sum[:])
	return c, nil
}
