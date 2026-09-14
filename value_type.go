package behaviortree

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ValueType 是黑板字段的内建类型；内部使用整数，JSON 使用稳定的类型名称。
type ValueType uint8

const (
	InvalidValueType ValueType = iota // InvalidValueType 表示未设置或无效的字段类型。
	BoolType                          // BoolType 为布尔值。
	IntType                           // IntType 为有符号整数。
	UIntType                          // UIntType 为无符号整数。
	FloatType                         // FloatType 为浮点数。
	StringType                        // StringType 为字符串。
	EnumType                          // EnumType 为业务定义的枚举字符串。
	EntityIDType                      // EntityIDType 为业务实体标识。
	DurationType                      // DurationType 为纳秒时长。
)

// Valid 判断字段类型是否属于已定义的有效值，不接受零值。
func (v ValueType) Valid() bool { return v >= BoolType && v <= DurationType }

// String 返回稳定的类型名称；无效值统一显示为 invalid。
func (v ValueType) String() string {
	switch v {
	case BoolType:
		return "bool"
	case IntType:
		return "int64"
	case UIntType:
		return "uint64"
	case FloatType:
		return "float64"
	case StringType:
		return "string"
	case EnumType:
		return "enum"
	case EntityIDType:
		return "entity"
	case DurationType:
		return "duration"
	default:
		return "invalid"
	}
}

// ParseValueType 解析大小写严格匹配的字段类型名称，拒绝空值和未知名称。
func ParseValueType(name string) (ValueType, error) {
	switch name {
	case "bool":
		return BoolType, nil
	case "int64":
		return IntType, nil
	case "uint64":
		return UIntType, nil
	case "float64":
		return FloatType, nil
	case "string":
		return StringType, nil
	case "enum":
		return EnumType, nil
	case "entity":
		return EntityIDType, nil
	case "duration":
		return DurationType, nil
	default:
		return InvalidValueType, fmt.Errorf("unknown value type %q", name)
	}
}

// MarshalJSON 保留可读的字符串协议，拒绝将非法枚举写入项目或元数据。
func (v ValueType) MarshalJSON() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("invalid value type %d", v)
	}
	return strconv.AppendQuote(nil, v.String()), nil
}

// UnmarshalJSON 只接受有效类型名称，解析失败时保留接收者原值。
func (v *ValueType) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("type: invalid value %s: %w", data, err)
	}
	value, err := ParseValueType(name)
	if err != nil {
		return fmt.Errorf("type: invalid value %s: %w", data, err)
	}
	*v = value
	return nil
}
