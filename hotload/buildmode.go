package hotload

import (
	"encoding/json"
	"fmt"
)

// BuildMode 固定宿主和插件的构建模式，零值无效。
type BuildMode uint8

const (
	InvalidBuildMode BuildMode = iota // 未指定或非法模式。
	BuildRelease                      // 启用默认编译优化。
	BuildDebug                        // 启用调试标签并关闭优化和内联。
)

// Valid 检查构建模式是否属于支持集合。
func (m BuildMode) Valid() bool { return m >= BuildRelease && m <= BuildDebug }

// String 返回稳定可读名称，非法值保留数值便于诊断。
func (m BuildMode) String() string {
	switch m {
	case BuildRelease:
		return "release"
	case BuildDebug:
		return "debug"
	default:
		return fmt.Sprintf("BuildMode(%d)", uint8(m))
	}
}

// ParseBuildMode 严格解析名称，拒绝缺失值和未知模式。
func ParseBuildMode(value string) (BuildMode, error) {
	switch value {
	case "release":
		return BuildRelease, nil
	case "debug":
		return BuildDebug, nil
	default:
		return InvalidBuildMode, fmt.Errorf("mode: invalid value %q", value)
	}
}

// MarshalText 输出可读名称，阻止非法枚举流出边界。
func (m BuildMode) MarshalText() ([]byte, error) {
	if !m.Valid() {
		return nil, fmt.Errorf("mode: invalid value %d", m)
	}
	return []byte(m.String()), nil
}

// UnmarshalText 仅在解析成功时替换当前模式。
func (m *BuildMode) UnmarshalText(data []byte) error {
	value, err := ParseBuildMode(string(data))
	if err == nil {
		*m = value
	}
	return err
}

// MarshalJSON 保持 manifest 使用可读字符串。
func (m BuildMode) MarshalJSON() ([]byte, error) {
	if !m.Valid() {
		return nil, fmt.Errorf("mode: invalid value %d", m)
	}
	return json.Marshal(m.String())
}

// UnmarshalJSON 拒绝 null、数字及未知字符串。
func (m *BuildMode) UnmarshalJSON(data []byte) error {
	var value string
	if len(data) == 0 || data[0] != '"' {
		return fmt.Errorf("mode: expected a string, got %s", data)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("mode: invalid value %s: %w", data, err)
	}
	return m.UnmarshalText([]byte(value))
}
