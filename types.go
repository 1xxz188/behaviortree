// Package behaviortree 提供原生 Go 行为树代码的事件驱动执行状态和版本管理。
// 实例及黑板只允许宿主串行队列访问；框架不创建 goroutine 或轮询定时器。
package behaviortree

import "time"

// Status 表示节点执行状态；Invalid 同时表示没有携带执行结果的事件通知。
type Status uint8

const (
	Invalid Status = iota // Invalid 表示尚未执行。
	Running               // Running 表示等待事件或预算续跑。
	Success               // Success 表示执行成功。
	Failure               // Failure 表示执行失败。
)

// String 返回用于日志的稳定状态名。
func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Success:
		return "success"
	case Failure:
		return "failure"
	default:
		return "invalid"
	}
}

// Phase 表示手写动作的生命周期阶段。
type Phase uint8

const (
	Start  Phase = iota // Start 表示首次执行动作。
	Resume              // Resume 表示收到通知后继续动作。
	Abort               // Abort 表示取消仍在运行的动作。
)

// Node 保存生成器产生的先序节点索引；End 是子树范围的排他上界。
type Node struct {
	ID     string // ID 是编辑器稳定节点标识。
	TreeID string // TreeID 是节点所属树标识。
	Parent int    // Parent 是父节点索引，根节点为 -1。
	End    int    // End 是该节点子树的排他上界。
}

// EventID 标识工程显式声明的宿主事件，不承载展示名称。
type EventID uint64

// InvalidEventID 不代表任何业务事件。
const InvalidEventID EventID = 0

// EventDefinition 保存发布版本中的事件描述，通知调度仅使用 ID。
type EventDefinition struct {
	ID          EventID // ID 是稳定事件身份。
	Name        string  // Name 是诊断与界面使用的展示名称。
	CodeName    string  // CodeName 是生成的枚举成员代码名。
	Description string  // Description 是事件项的字段注释。
}

// Program 是完整不可变版本，树、动作及子树必须一起发布。
// NewRegistry/NewInstance 会复制描述数据；函数闭包引用的数据仍须由调用方保持不可变。
type Program[C any] struct {
	Version              string                      // Version 是唯一发布版本。
	Roots                map[string]int              // Roots 将树 ID 映射到生成代码入口。
	Nodes                []Node                      // Nodes 使用先序连续索引。
	Fields               []Field                     // Fields 描述强类型黑板布局。
	EventEnumDescription string                      // EventEnumDescription 是整个事件枚举的功能注释。
	Events               []EventDefinition           // Events 是当前版本允许通知的完整事件集合。
	FieldDependencies    map[string][]int            // FieldDependencies 以黑板字段稳定 ID 为键。
	EventDependencies    map[EventID][]int           // EventDependencies 以已声明事件 ID 为键。
	Step                 func(*Frame[C], int) Status // Step 分发到生成的原生 Go 节点函数。
	Abort                func(*Frame[C], int)        // Abort 只取消给定动作，不递归取消子节点。
}

// NodeState 保存一个调用位置的独立执行状态。
type NodeState struct {
	Cursor    int        // Cursor 是组合节点的子节点游标。
	Count     int        // Count 是已完成循环次数。
	Token     uint64     // Token 是当前异步执行代号，业务代码应通过 Frame.Token 获取完整令牌。
	Data      any        // Data 供手写动作存放该调用位置的临时状态。
	cancel    CancelFunc // cancel 取消节点拥有的一次性定时器。
	child     int        // child 指向第一个已访问子节点。
	next      int        // next 指向下一个已访问兄弟。
	prev      int        // prev 指向上一个已访问兄弟。
	dirtyHead int        // dirtyHead 指向待处理直接子分支队首。
	dirtyTail int        // dirtyTail 指向待处理直接子分支队尾。
	queueNext int        // queueNext 指向父节点待处理队列中的后继。
	queuePrev int        // queuePrev 指向父节点待处理队列中的前驱。
	Status    Status     // Status 是最近一次节点结果。
	Result    Status     // Result 是异步结果，Invalid 表示普通事件。
	Ready     bool       // Ready 表示收到一次有效唤醒。
	Started   bool       // Started 由生成代码标记动作或定时器已经启动。
	dirty     bool       // dirty 表示该节点需要重评。
	linked    bool       // linked 表示已纳入访问状态子树。
	queued    bool       // queued 表示已加入父节点待处理队列。
}

// CallbackToken 标识某实例中某节点的一次异步操作；取消后和重复回调均无效。
type CallbackToken struct {
	Instance uint64 // Instance 是运行实例的进程内唯一代号。
	Node     int    // Node 是动作索引。
	Sequence uint64 // Sequence 是单调执行代号。
}

// CancelFunc 取消宿主提供的一次性定时器。
type CancelFunc func()

// Options 将调度和日志交给宿主；Post 必须支持并发调用，并异步投递到实例所属的串行队列。
type Options struct {
	Budget int                                    // Budget 是每次驱动的节点步数，零值使用 1024。
	Post   func(func())                           // Post 投递回调；不得内联执行或在实例关闭前丢弃回调。
	After  func(time.Duration, func()) CancelFunc // After 安排一次性通知；通知将由框架经 Post 回到宿主。
	Logger func(LogRecord)                        // Logger 默认使用 slog 接收切换、错误及可选节点追踪；传空函数显式关闭。
	Trace  bool                                   // Trace 为该实例开启详细节点追踪。
}

// LogRecord 是不需要预先格式化字符串的结构化运行日志。
type LogRecord struct {
	Kind       LogKind // Kind 为发布、切换、错误、节点追踪或取消事件。
	Version    string  // Version 是执行所固定的程序版本。
	InstanceID string  // InstanceID 是宿主提供的实例标识。
	TreeID     string  // TreeID 是实例入口树。
	NodeID     string  // NodeID 是编辑器节点标识。
	NodeTreeID string  // NodeTreeID 是节点定义树，与子树调用时的实例入口树区分。
	NodeIndex  int     // NodeIndex 区分子树复用时同一源节点的多个调用位置。
	Sequence   uint64  // Sequence 是该实例日志事件序号。
	From       Status  // From 是节点变化前状态。
	To         Status  // To 是节点变化后状态。
	Reason     string  // Reason 是取消、错误或切换原因。
}

// SwitchPolicy 决定已运行实例何时切换完整程序版本。
type SwitchPolicy uint8

const (
	EndOfRound       SwitchPolicy = iota // EndOfRound 在下一轮启动时切换，当前轮继续固定旧版本。
	CancelAndRestart                     // CancelAndRestart 经宿主队列取消当前轮，再从新版本根节点开始。
)

// VersionStats 描述已发布版本及仍固定该版本的运行实例数量。
type VersionStats struct {
	Version string // Version 是发布版本。
	Active  int    // Active 是仍在 Running 的实例数。
	Current bool   // Current 表示新实例默认使用该版本。
}
