package behaviortree

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// LogKind 表示固定的运行日志种类；业务原因仍由 LogRecord.Reason 保存。
type LogKind uint8

const (
	InvalidLogKind LogKind = iota // InvalidLogKind 表示未设置或无效的日志种类。
	LogPublish                    // LogPublish 表示发布完整程序版本。
	LogSwitch                     // LogSwitch 表示实例切换程序版本。
	LogError                      // LogError 表示执行或发布错误。
	LogNode                       // LogNode 表示节点状态追踪。
	LogAbort                      // LogAbort 表示取消正在执行的节点。
)

// Valid 判断日志种类是否属于已定义的有效值，不接受零值。
func (k LogKind) Valid() bool { return k >= LogPublish && k <= LogAbort }

// String 返回稳定的日志种类名称；无效值统一显示为 invalid。
func (k LogKind) String() string {
	switch k {
	case LogPublish:
		return "publish"
	case LogSwitch:
		return "switch"
	case LogError:
		return "error"
	case LogNode:
		return "node"
	case LogAbort:
		return "abort"
	default:
		return "invalid"
	}
}

// ParseLogKind 解析大小写严格匹配的日志名称，拒绝空值和未知名称。
func ParseLogKind(name string) (LogKind, error) {
	switch name {
	case "publish":
		return LogPublish, nil
	case "switch":
		return LogSwitch, nil
	case "error":
		return LogError, nil
	case "node":
		return LogNode, nil
	case "abort":
		return LogAbort, nil
	default:
		return InvalidLogKind, fmt.Errorf("unknown log kind %q", name)
	}
}

// MarshalJSON 保留日志字符串格式，拒绝将非法枚举编码成数字。
func (k LogKind) MarshalJSON() ([]byte, error) {
	if !k.Valid() {
		return nil, fmt.Errorf("invalid log kind %d", k)
	}
	return strconv.AppendQuote(nil, k.String()), nil
}

// UnmarshalJSON 只接受有效日志名称，解析失败时保留接收者原值。
func (k *LogKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("kind: invalid value %s: %w", data, err)
	}
	value, err := ParseLogKind(name)
	if err != nil {
		return fmt.Errorf("kind: invalid value %s: %w", data, err)
	}
	*k = value
	return nil
}
