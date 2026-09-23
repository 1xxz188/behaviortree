package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EventDefinition 保存一个枚举成员的稳定身份和独立的字段注释。
type EventDefinition struct {
	ID          string `json:"id"`                    // 非零 uint64 的规范十进制稳定 ID。
	Name        string `json:"name"`                  // 可包含中文的显示名称。
	CodeName    string `json:"codeName"`              // 不含 Event 前缀的 Go 代码名。
	Description string `json:"description,omitempty"` // 此成员的用途及通知时机。
}

// CatalogExchange 是带事件定义的自包含目录交换对象。
type CatalogExchange struct {
	Kind                 string            `json:"kind"`                           // 固定为 behaviortree.catalog。
	SchemaVersion        int               `json:"schemaVersion"`                  // 与工程格式版本一致。
	EventEnumDescription string            `json:"eventEnumDescription,omitempty"` // 来源工程的整体枚举说明。
	Events               []EventDefinition `json:"events"`                         // 被目录定义引用的事件。
	Catalog              []Definition      `json:"catalog"`                        // 交换的业务节点定义。
}

// MaxNextEventID 表示 uint64 事件身份已经分配至上限，不能继续创建成员。
const MaxNextEventID = "18446744073709551616"

// ParseEventID 严格解析 JSON 使用的非零 uint64 十进制身份。
func ParseEventID(id string) (uint64, error) {
	if id == "" || id[0] == '0' {
		return 0, fmt.Errorf("事件 ID 必须是 1 到 uint64 上限的规范十进制字符串: %q", id)
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; c < '0' || c > '9' {
			return 0, fmt.Errorf("事件 ID 必须是 1 到 uint64 上限的规范十进制字符串: %q", id)
		}
	}
	value, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("事件 ID 必须是 1 到 uint64 上限的规范十进制字符串: %q", id)
	}
	return value, nil
}

// NormalizeEventDescription 统一换行并校验注释，不裁剪有意义的缩进。
func NormalizeEventDescription(description string) (string, error) {
	description = strings.ReplaceAll(strings.ReplaceAll(description, "\r\n", "\n"), "\r", "\n")
	if strings.ContainsRune(description, 0) || !utf8.ValidString(description) {
		return "", fmt.Errorf("注释不能包含 NUL 或无效 UTF-8")
	}
	if strings.Trim(description, " \t\n") == "" {
		description = ""
	}
	if len(description) > 16<<10 {
		return "", fmt.Errorf("注释不能超过 16 KiB UTF-8 字节")
	}
	return description, nil
}

// ValidEventCodeName 判断成员代码名是否符合固定 ASCII 格式。
func ValidEventCodeName(name string) bool {
	if len(name) == 0 || len(name) > 80 || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// ValidateEvents 校验注册表、成员注释及定义引用，不检查未完成的树草稿。
func ValidateEvents(p Project) []Diagnostic {
	var diagnostics []Diagnostic
	add := func(field, message string) {
		diagnostics = append(diagnostics, Diagnostic{Field: field, Message: message})
	}
	if _, err := NormalizeEventDescription(p.EventEnumDescription); err != nil {
		add("eventEnumDescription", err.Error())
	}
	ids := make(map[string]bool, len(p.Events))
	var maxID uint64
	names := make(map[string]bool, len(p.Events))
	generated := make(map[string]bool, len(p.Catalog)*2+len(p.Blackboard)*2)
	for _, field := range p.Blackboard {
		generated["Get"+field.Name], generated["Set"+field.Name] = true, true
	}
	for _, definition := range p.Catalog {
		generated[definition.GoName], generated[definition.GoName+"Params"] = true, true
	}
	for i, event := range p.Events {
		prefix := fmt.Sprintf("events[%d]", i)
		if value, err := ParseEventID(event.ID); err != nil {
			add(prefix+".id", err.Error())
		} else {
			if ids[event.ID] {
				add(prefix+".id", "事件 ID 重复: "+event.ID)
			}
			if value > maxID {
				maxID = value
			}
		}
		ids[event.ID] = true
		if strings.TrimSpace(event.Name) == "" {
			add(prefix+".name", "事件显示名称不能为空")
		}
		folded := strings.ToLower(event.CodeName)
		if !ValidEventCodeName(event.CodeName) || names[folded] {
			add(prefix+".codeName", "事件代码名格式非法或重复: "+event.CodeName)
		}
		if generated["Event"+event.CodeName] {
			add(prefix+".codeName", "事件常量与业务符号冲突: Event"+event.CodeName)
		}
		if p.Generation.ContextImport == "" && strings.TrimPrefix(p.Generation.ContextType, "*") == "Event"+event.CodeName {
			add(prefix+".codeName", "事件常量与上下文类型冲突: Event"+event.CodeName)
		}
		names[folded] = true
		if _, err := NormalizeEventDescription(event.Description); err != nil {
			add(prefix+".description", err.Error())
		}
	}
	// 游标严格大于所有现存成员；删除成员后仍保留原游标，避免重用旧 ID。
	if p.NextEventID != MaxNextEventID {
		if next, err := ParseEventID(p.NextEventID); err != nil {
			add("nextEventId", "下一个事件 ID 必须是规范十进制字符串或耗尽值")
		} else if next <= maxID {
			add("nextEventId", "下一个事件 ID 必须大于所有现存事件 ID")
		}
	}
	for i, definition := range p.Catalog {
		seen := make(map[string]bool, len(definition.EventIDs))
		for j, id := range definition.EventIDs {
			field := fmt.Sprintf("catalog[%d].eventIds[%d]", i, j)
			if _, err := ParseEventID(id); err != nil {
				add(field, err.Error())
			} else if !ids[id] {
				add(field, "业务定义引用未声明的事件 ID: "+id)
			} else if seen[id] {
				add(field, "业务定义重复引用事件 ID: "+id)
			}
			seen[id] = true
		}
	}
	return diagnostics
}

// normalizeEvents 复制注册表并规范化两层注释，避免改动调用者对象。
func normalizeEvents(p Project) (Project, error) {
	var err error
	p.EventEnumDescription, err = NormalizeEventDescription(p.EventEnumDescription)
	if err != nil {
		return p, fmt.Errorf("eventEnumDescription: %w", err)
	}
	p.Events = append([]EventDefinition{}, p.Events...)
	for i := range p.Events {
		p.Events[i].Name = strings.TrimSpace(p.Events[i].Name)
		p.Events[i].Description, err = NormalizeEventDescription(p.Events[i].Description)
		if err != nil {
			return p, fmt.Errorf("events[%d].description: %w", i, err)
		}
	}
	return p, nil
}

// checkEventJSONShape 检查普通 string 解码会吞掉的 null 和数组缺失。
func checkEventJSONShape(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		return fmt.Errorf("旧目录数组格式已停用，请使用 Schema %d 事件枚举格式重新导出", SchemaVersion)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("工程或目录必须是 JSON 对象")
	}
	checkString := func(raw json.RawMessage, field string) error {
		if len(raw) == 0 {
			return nil
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || bytes.TrimSpace(raw)[0] != '"' {
			return fmt.Errorf("%s 必须是字符串", field)
		}
		return nil
	}
	if err := checkString(root["eventEnumDescription"], "eventEnumDescription"); err != nil {
		return err
	}
	eventsRaw := bytes.TrimSpace(root["events"])
	if len(eventsRaw) == 0 || eventsRaw[0] != '[' {
		return fmt.Errorf("events 必须显式为数组")
	}
	var events []map[string]json.RawMessage
	if err := json.Unmarshal(eventsRaw, &events); err != nil {
		return fmt.Errorf("events: %w", err)
	}
	for i, event := range events {
		if err := checkString(event["description"], fmt.Sprintf("events[%d].description", i)); err != nil {
			return err
		}
	}
	if raw := root["catalog"]; len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var definitions []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &definitions); err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		for i, definition := range definitions {
			if _, old := definition["events"]; old {
				return fmt.Errorf("catalog[%d].events 是旧字符串事件字段，请使用 Schema %d 事件枚举格式重新导出", i, SchemaVersion)
			}
			if raw, exists := definition["eventIds"]; exists {
				if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '[' {
					return fmt.Errorf("catalog[%d].eventIds 必须是数组", i)
				}
			}
		}
	}
	return nil
}

// DecodeCatalogExchange 严格读取自包含目录，不接受旧数组格式。
func DecodeCatalogExchange(data []byte) (CatalogExchange, error) {
	var exchange CatalogExchange
	if err := checkEventJSONShape(data); err != nil {
		return exchange, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&exchange); err != nil {
		return exchange, fmt.Errorf("读取事件枚举目录: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return exchange, fmt.Errorf("目录后含多余 JSON 数据")
	}
	if exchange.Kind != "behaviortree.catalog" || exchange.SchemaVersion != SchemaVersion {
		return exchange, fmt.Errorf("目录必须使用 behaviortree.catalog Schema %d", SchemaVersion)
	}
	if exchange.Catalog == nil {
		return exchange, fmt.Errorf("catalog 必须显式为数组")
	}
	p := Example()
	p.Events, p.EventEnumDescription, p.Catalog = exchange.Events, exchange.EventEnumDescription, exchange.Catalog
	for _, event := range p.Events {
		value, err := ParseEventID(event.ID)
		if err != nil {
			continue // 原始格式错误交给共享校验器定位。
		}
		if value == ^uint64(0) {
			p.NextEventID = MaxNextEventID
			break
		}
		if next, _ := ParseEventID(p.NextEventID); value >= next {
			p.NextEventID = strconv.FormatUint(value+1, 10)
		}
	}
	if err := validateCatalogExchangeProject(p); err != nil {
		return exchange, err
	}
	p, err := normalizeEvents(p)
	if err != nil {
		return exchange, err
	}
	exchange.Events, exchange.EventEnumDescription = p.Events, p.EventEnumDescription
	return exchange, nil
}
