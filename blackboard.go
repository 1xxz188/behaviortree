package behaviortree

import (
	"fmt"
	"math"
	"time"
)

// ValueType 是元数据中的强类型字段类型。
type ValueType string

const (
	BoolType     ValueType = "bool"     // BoolType 为布尔值。
	IntType      ValueType = "int64"    // IntType 为有符号整数。
	UIntType     ValueType = "uint64"   // UIntType 为无符号整数。
	FloatType    ValueType = "float64"  // FloatType 为浮点数。
	StringType   ValueType = "string"   // StringType 为字符串。
	EnumType     ValueType = "enum"     // EnumType 为具名枚举字符串。
	EntityIDType ValueType = "entityID" // EntityIDType 为业务实体标识。
	DurationType ValueType = "duration" // DurationType 为纳秒时长。
)

// Value 是黑板槽位的无反射值；仅与 Field.Type 对应的成员有意义。
type Value struct {
	Bool     bool          // Bool 保存布尔值。
	Int      int64         // Int 保存有符号整数。
	Uint     uint64        // Uint 保存无符号整数。
	Float    float64       // Float 保存浮点数。
	String   string        // String 保存字符串或枚举。
	EntityID uint64        // EntityID 保存实体 ID。
	Duration time.Duration // Duration 保存时长。
}

// Field 通过稳定 ID 跨版本保留值；名字可变，类型和已有枚举值不可删除。
type Field struct {
	ID      string    // ID 是稳定字段标识。
	Name    string    // Name 是供编辑器显示的名称。
	Type    ValueType // Type 决定槽位访问方法。
	Default Value     // Default 是新建和新增字段的初始值。
	Enum    []string  // Enum 是枚举允许值，仅 EnumType 使用。
}

// Blackboard 按生成代码中的整数槽位访问，不进行字符串查找或反射。
// 所有 getter/setter 由生成器保证类型和范围；公开给宿主使用时亦须遵守字段布局。
type Blackboard struct {
	values   []Value   // values 按编译槽位保存强类型值。
	fields   []Field   // fields 引用当前不可变字段布局。
	onChange func(int) // onChange 只标记实例的受影响路径。
}

// Len 返回该版本的字段槽位数量。
func (b *Blackboard) Len() int { return len(b.values) }

// Bool 读取布尔槽位。
func (b *Blackboard) Bool(slot int) bool { return b.values[slot].Bool }

// Int 读取有符号整数槽位。
func (b *Blackboard) Int(slot int) int64 { return b.values[slot].Int }

// Uint 读取无符号整数槽位。
func (b *Blackboard) Uint(slot int) uint64 { return b.values[slot].Uint }

// Float 读取浮点槽位。
func (b *Blackboard) Float(slot int) float64 { return b.values[slot].Float }

// String 读取字符串槽位。
func (b *Blackboard) String(slot int) string { return b.values[slot].String }

// Enum 读取枚举字符串槽位。
func (b *Blackboard) Enum(slot int) string { return b.values[slot].String }

// EntityID 读取实体 ID 槽位。
func (b *Blackboard) EntityID(slot int) uint64 { return b.values[slot].EntityID }

// Duration 读取时长槽位。
func (b *Blackboard) Duration(slot int) time.Duration { return b.values[slot].Duration }

// SetBool 仅在值变化时通知依赖该槽位的条件。
func (b *Blackboard) SetBool(slot int, value bool) {
	if b.values[slot].Bool != value {
		b.values[slot].Bool = value
		b.changed(slot)
	}
}

// SetInt 更新整数槽位并通知依赖者。
func (b *Blackboard) SetInt(slot int, value int64) {
	if b.values[slot].Int != value {
		b.values[slot].Int = value
		b.changed(slot)
	}
}

// SetUint 更新无符号整数槽位并通知依赖者。
func (b *Blackboard) SetUint(slot int, value uint64) {
	if b.values[slot].Uint != value {
		b.values[slot].Uint = value
		b.changed(slot)
	}
}

// SetFloat 更新浮点槽位；相同 NaN 位模式不会反复产生变化通知。
func (b *Blackboard) SetFloat(slot int, value float64) {
	if math.Float64bits(b.values[slot].Float) != math.Float64bits(value) {
		b.values[slot].Float = value
		b.changed(slot)
	}
}

// SetString 更新字符串槽位并通知依赖者。
func (b *Blackboard) SetString(slot int, value string) {
	if b.values[slot].String != value {
		b.values[slot].String = value
		b.changed(slot)
	}
}

// SetEnum 更新已由业务或生成器校验的枚举值并通知依赖者。
func (b *Blackboard) SetEnum(slot int, value string) { b.SetString(slot, value) }

// SetEntityID 更新实体 ID 槽位并通知依赖者。
func (b *Blackboard) SetEntityID(slot int, value uint64) {
	if b.values[slot].EntityID != value {
		b.values[slot].EntityID = value
		b.changed(slot)
	}
}

// SetDuration 更新时长槽位并通知依赖者。
func (b *Blackboard) SetDuration(slot int, value time.Duration) {
	if b.values[slot].Duration != value {
		b.values[slot].Duration = value
		b.changed(slot)
	}
}

// changed 将通知交给实例；回调只标记脏路径，不重入生成代码。
func (b *Blackboard) changed(slot int) {
	if b.onChange != nil {
		b.onChange(slot)
	}
}

// compatibleFields 校验布局只能追加字段或保留 ID 改名，允许重新排列槽位。
func compatibleFields(old, next []Field) error {
	byID := make(map[string]Field, len(next))
	for _, f := range next {
		byID[f.ID] = f
	}
	for _, f := range old {
		n, ok := byID[f.ID]
		if !ok || n.Type != f.Type {
			return fmt.Errorf("blackboard field %q removed or type changed; restart required", f.ID)
		}
		if f.Type == EnumType {
			allowed := make(map[string]bool, len(n.Enum))
			for _, value := range n.Enum {
				allowed[value] = true
			}
			for _, value := range f.Enum {
				if !allowed[value] {
					return fmt.Errorf("enum field %q removed value %q; restart required", f.ID, value)
				}
			}
		}
	}
	return nil
}

// migrateBlackboard 仅在版本切换时按稳定 ID 转换值，不在节点执行路径分配。
func migrateBlackboard(old *Blackboard, fields []Field, change func(int)) *Blackboard {
	b := &Blackboard{values: make([]Value, len(fields)), fields: fields, onChange: change}
	for n, f := range fields {
		b.values[n] = f.Default
	}
	if old == nil {
		return b
	}
	byID := make(map[string]int, len(old.fields))
	for n, f := range old.fields {
		byID[f.ID] = n
	}
	for n, f := range fields {
		if slot, ok := byID[f.ID]; ok {
			b.values[n] = old.values[slot]
		}
	}
	return b
}

// sameLayout 识别相同槽位布局，字段重命名不触发值迁移。
func sameLayout(old, next []Field) bool {
	if len(old) != len(next) {
		return false
	}
	for n := range old {
		if old[n].ID != next[n].ID || old[n].Type != next[n].Type {
			return false
		}
	}
	return true
}
