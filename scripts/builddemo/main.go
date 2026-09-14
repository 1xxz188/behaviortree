// builddemo 在独立暂存模块中构建宿主与连续热更插件，不改根 go.mod/go.work。
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/editor"
	"github.com/1xxz188/behaviortree/examples/definition"
	"github.com/1xxz188/behaviortree/hotload"
	"github.com/1xxz188/behaviortree/model"
)

// builder 固定构建参数，确保各产物使用完全一致的共享源码。
type builder struct {
	root string            // 行为树模块绝对路径。
	out  string            // 本次构建独有的输出目录。
	mode hotload.BuildMode // 宿主和插件共享的构建模式。
	env  []string          // 已规范化的子进程环境。
}

// buildTarget 描述宿主或一个私有版本的编译入口。
type buildTarget struct {
	dir        string // 命令工作目录。
	pattern    string // go build 包参数。
	importPath string // 排除主包指纹使用的实际路径。
	private    bool   // 是否为版本私有插件。
	version    string // 发布版本。
	module     string // 私有模块路径。
	output     string // 产物名称。
}

// main 要求真实 Linux 环境；prepare 只生成暂存源码用于其他平台静态审阅。
func main() {
	moduleRoot := flag.String("root", ".", "行为树模块路径")
	out := flag.String("out", ".build", "输出父目录，每次自动创建唯一子目录")
	mode := flag.String("mode", "release", "release 或 debug")
	prepare := flag.Bool("prepare", false, "仅生成版本化源码，不构建 .so")
	flag.Parse()
	buildMode, err := hotload.ParseBuildMode(*mode)
	fatal(err)
	root, err := filepath.Abs(*moduleRoot)
	fatal(err)
	base, err := filepath.Abs(*out)
	fatal(err)
	stamp := time.Now().UTC().Format("20060102_150405.000000000")
	dir := filepath.Join(base, "release_"+strings.ReplaceAll(stamp, ".", "_"))
	fatal(os.MkdirAll(dir, 0755))
	b := builder{root: root, out: dir, mode: buildMode}
	targets, err := b.prepare(stamp)
	fatal(err)
	b.env = buildEnvironment(filepath.Join(dir, "go.work"), true)
	if *prepare {
		fmt.Println("prepared (no .so validation):", dir)
		return
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		fatal(fmt.Errorf("real plugin build requires Linux/amd64; use -prepare only for source review"))
	}
	version, err := b.goOutput(root, "env", "GOVERSION")
	fatal(err)
	if strings.TrimSpace(string(version)) != "go1.26.7" {
		fatal(fmt.Errorf("toolchain must be go1.26.7, got %s", version))
	}
	contract, err := b.collectShared(targets)
	fatal(err)
	encoded, err := json.Marshal(contract)
	fatal(err)
	for _, target := range targets {
		args := append([]string{"build"}, b.commonFlags()...)
		if target.private {
			args = append(args, "-buildmode=plugin")
		} else {
			args = append(args, "-ldflags=-X main.buildContract="+base64.RawStdEncoding.EncodeToString(encoded))
		}
		args = append(args, "-o", filepath.Join(dir, target.output), target.pattern)
		_, err = b.goOutput(target.dir, args...)
		fatal(err)
		if target.private {
			sum, err := hotload.FileSHA256(filepath.Join(dir, target.output))
			fatal(err)
			m := hotload.Manifest{SchemaVersion: 1, Version: target.version, PrivateModule: target.module, SHA256: sum, Contract: contract}
			fatal(writeJSON(filepath.Join(dir, target.output+".json"), m))
		}
	}
	// 重新计算源码指纹，避免构建期间共享文件发生编辑而产物来自不同快照。
	after, err := b.collectShared(targets)
	fatal(err)
	afterData, _ := json.Marshal(after)
	if string(afterData) != string(encoded) {
		fatal(fmt.Errorf("shared source changed while building; discard this release directory"))
	}
	fmt.Println("built:", dir)
	cmd := exec.Command(filepath.Join(dir, "host"), "-integration", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = b.env
	fatal(cmd.Run())
}

// prepare 使用生成器修改树结构，并修改私有 action 和 helper 实现。
func (b *builder) prepare(stamp string) ([]buildTarget, error) {
	if !b.mode.Valid() {
		return nil, fmt.Errorf("mode: invalid value %d", b.mode)
	}
	targets := []buildTarget{{dir: b.root, pattern: runtimeImport + "/examples/host", importPath: runtimeImport + "/examples/host", output: "host"}}
	for index := 1; index <= 4; index++ {
		label := fmt.Sprintf("v%d", index)
		if index == 4 {
			label = "bad_signature"
		}
		version := label + "_" + strings.ReplaceAll(stamp, ".", "_")
		module := "bt.local/" + version
		dir := filepath.Join(b.out, "source", version)
		if err := stageBehavior(filepath.Join(b.root, "examples", "behavior"), filepath.Join(dir, "behavior"), module); err != nil {
			return nil, err
		}
		mod := "module " + module + "\n\ngo 1.26.7\n\nrequire " + runtimeImport + " v0.0.0\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644); err != nil {
			return nil, err
		}
		project := definition.Project(index)
		result, err := codegen.Generate(project)
		if err != nil {
			return nil, err
		}
		if err = editor.WriteGenerated(filepath.Join(dir, "behavior"), result); err != nil {
			return nil, err
		}
		data, err := model.Encode(project)
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(dir, "project.json"), data, 0644); err != nil {
			return nil, err
		}
		if err = replaceStringConstant(filepath.Join(dir, "behavior", "helper", "version.go"), "Marker", label); err != nil {
			return nil, err
		}
		if err = replaceStringConstant(filepath.Join(dir, "behavior", "actions.go"), "ActionMarker", label+"_action"); err != nil {
			return nil, err
		}
		source := fmt.Sprintf("package main\nimport bt %q\nimport shared %q\nimport behavior %q\n// BuildExporter 导出整个不可变程序版本。\nfunc BuildExporter() *bt.Program[*shared.Context] { return behavior.NewProgram(%q) }\n", runtimeImport, sharedImport, module+"/behavior", version)
		if index == 4 {
			source = "package main\n// BuildExporter 故意使用错误签名以验证拒绝逻辑。\nfunc BuildExporter() int {return 42}\n"
		}
		if err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0644); err != nil {
			return nil, err
		}
		targets = append(targets, buildTarget{dir: dir, pattern: module, importPath: module, private: true, version: version, module: module, output: version + ".so"})
	}
	// 行为树模块在宿主与所有插件中均作为相同工作区主模块，避免 trimpath 与包身份差异。
	work := "go 1.26.7\n\nuse (\n\t" + strconv.Quote(filepath.ToSlash(b.root)) + "\n"
	for _, target := range targets[1:] {
		work += "\t" + strconv.Quote(filepath.ToSlash(target.dir)) + "\n"
	}
	work += ")\n"
	if err := os.WriteFile(filepath.Join(b.out, "go.work"), []byte(work), 0644); err != nil {
		return nil, err
	}
	return targets, nil
}

// commonFlags 同时应用于 list、宿主和插件，以保证构建参数完全对应。
func (b *builder) commonFlags() []string {
	flags := []string{"-mod=readonly", "-trimpath", "-buildvcs=false"}
	if b.mode == hotload.BuildDebug {
		flags = append(flags, "-tags=debug", "-gcflags=all=-N -l")
	}
	return flags
}

// goOutput 直接传参数数组执行 Go，不通过 shell 拼接命令。
func (b *builder) goOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = b.env
	data, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go %v: %w\n%s", args, err, data)
	}
	return data, nil
}

// buildEnvironment 固定目标、CGO 和优化环境，清除外部 GOFLAGS 干扰。
func buildEnvironment(workspace string, cgoEnabled bool) []string {
	cgo := "0"
	if cgoEnabled {
		cgo = "1"
	}
	values := map[string]string{"GOWORK": workspace, "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": cgo, "GOAMD64": "v1", "GOFLAGS": "", "GOEXPERIMENT": "", "GOTOOLCHAIN": "local"}
	result := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, exists := values[name]; !exists {
			result = append(result, entry)
		}
	}
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

// writeJSON 保存构建产物旁车；目录在本次构建中唯一。
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// fatal 输出具体构建失败原因。
func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
