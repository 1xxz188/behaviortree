package hotload

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// ErrUnsupported 表示当前操作系统或 CGO 配置不支持原生插件。
var ErrUnsupported = errors.New("Go native plugin loading requires Linux, macOS or FreeBSD with CGO; Windows supports editing and static execution only")

// Loader 串行校验并加载完整版本；T 应为宿主与插件共享的版本类型。
// 加载不自动发布：调用方必须先检查树、黑板，再交给运行时注册表发布。
type Loader[T any] struct {
	mu       sync.Mutex        // 保护插件身份及加载统计。
	expected Contract          // 宿主构建时固定的兼容契约。
	versions map[string]string // 已尝试 Open 的版本与路径，禁止复用。
	modules  map[string]bool   // 已尝试 Open 的私有模块路径，禁止复用。
	loaded   int               // Open 成功的数量，包含导出检查失败但无法卸载的插件。
}

// New 创建加载器并复制契约，避免调用者后续修改共享 map。
func New[T any](contract Contract) *Loader[T] {
	copyContract := contract
	copyContract.Tags = append([]string(nil), contract.Tags...)
	copyContract.Flags = append([]string(nil), contract.Flags...)
	copyContract.Shared = cloneMap(contract.Shared)
	copyContract.Environment = cloneMap(contract.Environment)
	return &Loader[T]{expected: copyContract, versions: make(map[string]string), modules: make(map[string]bool)}
}

// cloneMap 复制构建阶段元数据，加载热路径不涉及实例执行。
func cloneMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// LoadedCount 返回已打开插件数；Go 插件无法卸载，服务重启才会释放代码内存。
func (l *Loader[T]) LoadedCount() int { l.mu.Lock(); defer l.mu.Unlock(); return l.loaded }

// Load 要求 BuildExporter 的精确签名为 func() T，并在校验后返回完整版本。
// plugin.Open 会执行 init；init 副作用和崩溃无法回滚，插件应保持 init 无副作用。
func (l *Loader[T]) Load(path string, validate func(T) error) (T, Manifest, error) {
	var zero T
	if !supported() {
		return zero, Manifest{}, ErrUnsupported
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	abs, err := filepath.Abs(path)
	if err != nil {
		return zero, Manifest{}, err
	}
	m, err := ReadManifest(abs + ".json")
	if err != nil {
		return zero, m, err
	}
	if err = m.Check(l.expected); err != nil {
		return zero, m, err
	}
	if _, exists := l.versions[m.Version]; exists || l.modules[m.PrivateModule] {
		return zero, m, fmt.Errorf("plugin version and private module must be unique: %s", m.Version)
	}
	sum, err := FileSHA256(abs)
	if err != nil {
		return zero, m, err
	}
	if sum != m.SHA256 {
		return zero, m, fmt.Errorf("plugin checksum mismatch")
	}
	// 即使 Open 失败也不复用身份，避免部分初始化后的 Go 包缓存污染下一次加载。
	l.versions[m.Version] = abs
	l.modules[m.PrivateModule] = true
	lookup, err := openPlugin(abs)
	if err != nil {
		return zero, m, fmt.Errorf("plugin.Open: %w", err)
	}
	l.loaded++
	symbol, err := lookup("BuildExporter")
	if err != nil {
		return zero, m, fmt.Errorf("BuildExporter: %w", err)
	}
	build, ok := symbol.(func() T)
	if !ok {
		return zero, m, fmt.Errorf("BuildExporter signature must be func() %T", zero)
	}
	value, err := callExporter(build, validate)
	if err != nil {
		return zero, m, err
	}
	return value, m, nil
}

// callExporter 将导出构建及校验回调的普通 panic 转成加载错误；不尝试撤销 plugin init。
func callExporter[T any](build func() T, validate func(T) error) (value T, err error) {
	defer func() {
		if cause := recover(); cause != nil {
			var zero T
			value = zero
			err = fmt.Errorf("BuildExporter or validator panic: %v", cause)
		}
	}()
	value = build()
	if validate != nil {
		if err = validate(value); err != nil {
			var zero T
			return zero, fmt.Errorf("invalid exported program: %w", err)
		}
	}
	return value, nil
}
