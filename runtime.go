package behaviortree

import (
	"fmt"
	"sync/atomic"
	"time"
)

// instanceSequence 为迟到回调提供跨实例唯一标识，不参与节点执行热点。
var instanceSequence atomic.Uint64

// Frame 是生成代码与手写动作共用的当前执行上下文。
type Frame[C any] struct {
	Context C            // Context 是宿主持有、接口固定的业务上下文。
	Board   *Blackboard  // Board 是当前版本布局的强类型黑板。
	owner   *Instance[C] // owner 持有当前串行执行状态。
}

// Instance 保存单个 AI 的连续节点状态，所有可变方法只能由宿主串行队列调用。
type Instance[C any] struct {
	id           string              // id 是宿主业务实例标识。
	treeID       string              // treeID 是固定的入口树标识。
	uid          uint64              // uid 隔离其他实例的异步回调。
	program      *preparedProgram[C] // program 固定当前轮的完整版本。
	registry     *Registry[C]        // registry 为空时采用独立实例模式。
	options      Options             // options 保存宿主调度和日志接口。
	frame        Frame[C]            // frame 复用当前执行上下文。
	states       []NodeState         // states 只分配当前入口树范围的连续状态。
	root         int                 // root 是当前版本的绝对入口索引。
	status       Status              // status 是当前轮的根状态。
	nonce        uint64              // nonce 生成单调异步代号。
	round        uint64              // round 隔离延迟取消与后续新轮。
	logSeq       uint64              // logSeq 是结构化事件序号。
	steps        uint64              // steps 累计真正进入执行的节点次数。
	budget       int                 // budget 是当前宿主调度的剩余步数。
	busy         bool                // busy 阻止通知和取消重入动作。
	yielded      bool                // yielded 表示当前调度已因预算让出。
	queued       bool                // queued 合并待处理续跑通知。
	closed       bool                // closed 拒绝已关闭实例的所有回调。
	err          error               // err 保存最近一轮框架或业务 panic。
	resume       func()              // resume 预绑定宿主续跑函数以避免稳态分配。
	dependencies *rootDependencies   // dependencies 引用当前入口的共享观察者视图。
	pending      []int               // pending 合并当前同步步骤内的依赖组通知。
	pendingMask  []bool              // pendingMask 在 O(1) 时间去重待处理依赖组。
}

// NewInstance 创建不参与热更注册表的独立实例。
func NewInstance[C any](program *Program[C], id, treeID string, context C, options Options) (*Instance[C], error) {
	p, err := prepareProgram(program)
	if err != nil {
		return nil, err
	}
	return newInstance(p, nil, id, treeID, context, options)
}

// newInstance 分配连续状态和预绑定的续跑回调，避免每次唤醒分配闭包。
func newInstance[C any](p *preparedProgram[C], registry *Registry[C], id, treeID string, context C, options Options) (*Instance[C], error) {
	root, exists := p.program.Roots[treeID]
	if !exists {
		return nil, fmt.Errorf("unknown tree %q", treeID)
	}
	if options.Post == nil {
		return nil, fmt.Errorf("Options.Post must enqueue onto the instance owner queue")
	}
	if options.Budget < 0 {
		return nil, fmt.Errorf("negative step budget")
	}
	if options.Budget == 0 {
		options.Budget = 1024
	}
	if options.Logger == nil {
		options.Logger = defaultLog
	}
	i := &Instance[C]{id: id, treeID: treeID, uid: instanceSequence.Add(1), program: p, registry: registry, options: options, root: root}
	i.frame = Frame[C]{Context: context, owner: i}
	i.states = make([]NodeState, p.program.Nodes[root].End-root)
	i.frame.Board = migrateBlackboard(nil, p.program.Fields, i.fieldChanged)
	i.setDependencies(p.dependencies[root])
	i.resume = func() { i.queued = false; i.Tick() }
	return i, nil
}

// Start 启动新一轮；当前轮仍在运行时不会重启或混入新版本。
func (i *Instance[C]) Start() Status {
	if i.closed {
		return Failure
	}
	if i.busy {
		return i.status
	}
	if i.status == Running {
		return i.Tick()
	}
	if i.registry != nil {
		if err := i.bind(i.registry.latest()); err != nil {
			i.err = err
			i.log(LogError, -1, Invalid, Failure, err.Error())
			return Failure
		}
	}
	i.frame.Reset(i.root)
	i.err = nil
	i.round++
	i.status = Running
	if i.registry != nil {
		i.registry.setActive(i, true)
	}
	return i.run()
}

// Tick 只推进已被事件、回调或预算续跑标记的路径；空闲调用不会执行节点。
func (i *Instance[C]) Tick() Status {
	if i.closed {
		return Failure
	}
	if i.busy || i.status != Running || !i.states[0].dirty {
		return i.status
	}
	return i.run()
}

// run 在一次串行队列调度中执行至等待点或预算边界。
func (i *Instance[C]) run() (status Status) {
	i.busy = true
	i.yielded = false
	i.budget = i.options.Budget
	// 宿主 safe.Go 只能承接最外层 panic，这里仍需恢复实例生命周期，避免永久 busy。
	defer func() {
		if failure := recover(); failure != nil {
			i.err = fmt.Errorf("behavior execution panic: %v", failure)
			i.log(LogError, i.root, i.status, Failure, i.err.Error())
		}
		if i.err != nil {
			i.frame.Abort(i.root, "runtime error")
			status = Failure
		}
		i.status = status
		i.busy = false
		i.flushNotifications(status == Running)
		if i.registry != nil && status != Running {
			i.registry.setActive(i, false)
		}
		if status == Running && i.states[0].dirty {
			i.schedule()
		}
	}()
	status = i.program.program.Step(&i.frame, i.root)
	if status != Running && status != Success && status != Failure {
		i.err = fmt.Errorf("generated root returned invalid status %d", status)
		i.log(LogError, i.root, i.status, Failure, i.err.Error())
	}
	return status
}

// Notify 通知显式声明的宿主事件；只检查受影响的节点。
func (i *Instance[C]) Notify(event string) Status {
	if i.closed || i.status != Running {
		return i.status
	}
	if group, exists := i.dependencies.events[event]; exists {
		i.observe(group)
	}
	if !i.busy {
		return i.Tick()
	}
	return i.status
}

// Complete 接收已投递到宿主队列的异步结果；迟到、跨实例、重复回调返回 false。
func (i *Instance[C]) Complete(token CallbackToken, result Status) bool {
	if i.closed || i.status != Running || token.Instance != i.uid || token.Sequence == 0 || token.Node < i.root || token.Node >= i.program.program.Nodes[i.root].End {
		return false
	}
	if result != Success && result != Failure {
		return false
	}
	s := i.state(token.Node)
	if s.Token != token.Sequence || !s.linked {
		return false
	}
	s.Token = 0
	s.Ready = true
	s.Result = result
	if s.cancel != nil {
		i.cancelTimer(token.Node)
	}
	i.markDirty(token.Node)
	if !i.busy {
		i.Tick()
	}
	return true
}

// Abort 取消当前轮的活跃动作和定时器；不会自动启动下一轮。
func (i *Instance[C]) Abort(reason string) {
	if i.closed {
		return
	}
	if i.busy {
		round := i.round
		i.options.Post(func() {
			if i.round == round && i.status == Running {
				i.Abort(reason)
			}
		})
		return
	}
	i.busy = true
	i.frame.Abort(i.root, reason)
	i.status = Invalid
	i.busy = false
	i.flushNotifications(false)
	if i.registry != nil {
		i.registry.setActive(i, false)
	}
}

// Close 在宿主队列取消并释放实例注册；之后所有回调均被忽略。
func (i *Instance[C]) Close() {
	if i.closed {
		return
	}
	if i.busy {
		i.options.Post(i.Close)
		return
	}
	i.Abort("instance closed")
	i.closed = true
	i.frame.Board.onChange = nil
}

// Status 返回最近一次根节点结果，仅在实例所属队列读取。
func (i *Instance[C]) Status() Status { return i.status }

// Version 返回当前轮固定的完整程序版本。
func (i *Instance[C]) Version() string { return i.program.program.Version }

// Blackboard 返回当前版本的黑板；热更后调用方必须重新取得该指针。
func (i *Instance[C]) Blackboard() *Blackboard { return i.frame.Board }

// SetTrace 在实例所属队列切换详细节点日志。
func (i *Instance[C]) SetTrace(enabled bool) { i.options.Trace = enabled }

// Error 返回最近一轮宿主接入或生成代码错误。
func (i *Instance[C]) Error() error { return i.err }

// Steps 返回真正执行的节点步数，用于衡量事件调度和性能。
func (i *Instance[C]) Steps() uint64 { return i.steps }

// State 返回生成代码使用的固定槽位状态。
func (f *Frame[C]) State(node int) *NodeState { return f.owner.state(node) }

// state 将程序绝对索引转换为当前入口树范围，避免每个 AI 为其他入口树分配状态。
func (i *Instance[C]) state(node int) *NodeState { return &i.states[node-i.root] }

// Enter 是生成节点入口；缓存干净状态并在预算不足时仅安排一次宿主续跑。
func (f *Frame[C]) Enter(node int) (Status, bool) {
	i := f.owner
	s := i.state(node)
	if s.Status != Invalid && !s.dirty {
		return s.Status, false
	}
	// 续跑仅穿过等待中的组合祖先时不重复扣预算，否则小预算永远无法到达深层叶子。
	if s.Status != Running || s.Ready {
		if i.budget == 0 {
			i.yielded = true
			i.markDirty(node)
			return Running, false
		}
		i.budget--
	}
	i.steps++
	i.touch(node)
	i.unqueue(node)
	s.dirty = false
	if s.Token == 0 {
		i.nonce++
		s.Token = i.nonce
	}
	return s.Status, true
}

// Exit 记录节点结果；追踪关闭时不构造节点日志。
func (f *Frame[C]) Exit(node int, status Status) Status {
	i := f.owner
	s := i.state(node)
	if status != Running && status != Success && status != Failure {
		i.err = fmt.Errorf("node %q returned invalid status %d", i.program.program.Nodes[node].ID, status)
		i.log(LogError, node, s.Status, Failure, i.err.Error())
		status = Failure
	}
	previous := s.Status
	s.Status = status
	if status == Success || status == Failure {
		s.Token = 0
		if s.cancel != nil {
			i.cancelTimer(node)
		}
	}
	if i.options.Trace && previous != status {
		i.log(LogNode, node, previous, status, "")
	}
	return status
}

// Token 返回当前异步操作的令牌；旧操作完成后再次调用会生成新执行代号。
func (f *Frame[C]) Token(node int) CallbackToken {
	i := f.owner
	s := i.state(node)
	if s.Token == 0 {
		i.nonce++
		s.Token = i.nonce
	}
	return CallbackToken{Instance: i.uid, Node: node, Sequence: s.Token}
}

// Consume 消费一次通知，允许动作随后安排下一次异步操作。
func (f *Frame[C]) Consume(node int) Status {
	s := f.State(node)
	result := s.Result
	s.Ready, s.Result = false, Invalid
	return result
}

// After 为节点安排一次性宿主定时器，回调经 Post 后校验令牌。
func (f *Frame[C]) After(node int, duration time.Duration) {
	i := f.owner
	s := i.state(node)
	if s.cancel != nil {
		return
	}
	if i.options.After == nil {
		i.err = fmt.Errorf("node %q requires Options.After", i.program.program.Nodes[node].ID)
		i.log(LogError, node, s.Status, Failure, i.err.Error())
		return
	}
	token := f.Token(node)
	s.cancel = i.options.After(duration, func() { i.options.Post(func() { i.Complete(token, Success) }) })
}

// Reset 只清理已实际访问的子树状态，未访问的静态节点不参与遍历。
func (f *Frame[C]) Reset(node int) {
	i := f.owner
	s := i.state(node)
	if !s.linked {
		s.dirty = false
		return
	}
	i.unqueue(node)
	for child := s.child; child >= 0; {
		next := i.state(child).next
		f.Reset(child)
		child = next
	}
	if s.cancel != nil {
		i.cancelTimer(node)
	}
	if parent := i.program.program.Nodes[node].Parent; parent >= 0 {
		if s.prev >= 0 {
			i.state(s.prev).next = s.next
		} else {
			i.state(parent).child = s.next
		}
		if s.next >= 0 {
			i.state(s.next).prev = s.prev
		}
	}
	*s = NodeState{}
}

// cancelTimer 隔离宿主清理函数的 panic，避免异常定时器阻止整棵树退出。
func (i *Instance[C]) cancelTimer(node int) {
	s := i.state(node)
	cancel := s.cancel
	s.cancel = nil
	defer func() {
		if failure := recover(); failure != nil {
			if i.err == nil {
				i.err = fmt.Errorf("node %q timer cancel panic: %v", i.program.program.Nodes[node].ID, failure)
			}
			i.log(LogError, node, s.Status, Failure, i.err.Error())
		}
	}()
	cancel()
}

// Abort 先调用活跃动作的单节点 Abort，再清理对应子树并使回调令牌失效。
func (f *Frame[C]) Abort(node int, reason string) {
	f.abortActions(node, reason)
	f.Reset(node)
}

// AbortChildren 仅取消已访问的子分支，保留父组合节点的执行状态。
func (f *Frame[C]) AbortChildren(node int, reason string) {
	s := f.State(node)
	if !s.linked {
		return
	}
	for child := s.child; child >= 0; {
		next := f.State(child).next
		f.Abort(child, reason)
		child = next
	}
}

// PopDirtyChild 弹出一个受影响的直接子分支，供 Parallel 只执行被唤醒分支。
// 本轮预算已让出时停止出队，避免同一 Running 节点在单次调用中反复重排。
func (f *Frame[C]) PopDirtyChild(node int) int {
	i := f.owner
	s := i.state(node)
	if !s.linked || i.yielded || s.dirtyHead < 0 {
		return -1
	}
	child := s.dirtyHead
	i.unqueue(child)
	return child
}

// OnlyDirtyChild 在 O(1) 时间判断是否只有指定直接子分支待处理，不消费队列。
// 生成的优先级选择器可据此直接续跑当前动作，避免无关事件重新检查全部高优先守卫。
func (f *Frame[C]) OnlyDirtyChild(node, child int) bool {
	s := f.State(node)
	return child >= 0 && s.linked && s.dirtyHead == child && s.dirtyTail == child
}

// IsDirty 只读查询节点是否需要重评，供生成器确认当前优先级守卫仍可使用缓存。
func (f *Frame[C]) IsDirty(node int) bool { return f.State(node).dirty }

// abortActions 沿已访问子树取消动作；叶动作拥有自己的清理逻辑。
func (f *Frame[C]) abortActions(node int, reason string) {
	i := f.owner
	s := i.state(node)
	if !s.linked {
		return
	}
	for child := s.child; child >= 0; {
		next := i.state(child).next
		f.abortActions(child, reason)
		child = next
	}
	if (s.Status == Running || s.Status == Invalid) && s.Started {
		if i.program.program.Abort != nil {
			f.abortAction(node)
		}
		if i.options.Trace {
			i.log(LogAbort, node, s.Status, Invalid, reason)
		}
	}
}

// abortAction 隔离单个业务清理的 panic，确保其他分支仍可取消并释放状态。
func (f *Frame[C]) abortAction(node int) {
	i := f.owner
	defer func() {
		if failure := recover(); failure != nil {
			if i.err == nil {
				i.err = fmt.Errorf("node %q Abort panic: %v", i.program.program.Nodes[node].ID, failure)
			}
			i.log(LogError, node, i.state(node).Status, Failure, i.err.Error())
		}
	}()
	i.program.program.Abort(f, node)
}

// markDirty 只触及受影响节点的祖先路径；祖先可能已被本轮消费，因此不能按后代脏标记提前终止。
func (i *Instance[C]) markDirty(node int) {
	i.touch(node)
	for node >= i.root {
		s := i.state(node)
		s.dirty = true
		parent := i.program.program.Nodes[node].Parent
		if parent >= i.root && !s.queued {
			p := i.state(parent)
			s.queued, s.queuePrev, s.queueNext = true, p.dirtyTail, -1
			if p.dirtyTail >= 0 {
				i.state(p.dirtyTail).queueNext = node
			} else {
				p.dirtyHead = node
			}
			p.dirtyTail = node
		}
		node = parent
	}
}

// unqueue 在 O(1) 时间摘除待处理分支，Reset 和普通组合入口也复用该逻辑。
func (i *Instance[C]) unqueue(node int) {
	s := i.state(node)
	if !s.queued {
		return
	}
	p := i.state(i.program.program.Nodes[node].Parent)
	if s.queuePrev >= 0 {
		i.state(s.queuePrev).queueNext = s.queueNext
	} else {
		p.dirtyHead = s.queueNext
	}
	if s.queueNext >= 0 {
		i.state(s.queueNext).queuePrev = s.queuePrev
	} else {
		p.dirtyTail = s.queuePrev
	}
	s.queued, s.queuePrev, s.queueNext = false, -1, -1
}

// touch 支持优先级守卫先于父组合节点求值，只建立状态链接而不执行祖先。
func (i *Instance[C]) touch(node int) {
	s := i.state(node)
	if s.linked {
		return
	}
	s.linked, s.child, s.next, s.prev = true, -1, -1, -1
	s.dirtyHead, s.dirtyTail, s.queueNext, s.queuePrev = -1, -1, -1, -1
	if parent := i.program.program.Nodes[node].Parent; parent >= 0 {
		i.touch(parent)
		p := i.state(parent)
		s.next = p.child
		if p.child >= 0 {
			i.state(p.child).prev = node
		}
		p.child = node
	}
}

// markObservers 将相关节点标记为可重评，不扫描全树或其他实例。
func (i *Instance[C]) markObservers(nodes []int) {
	for _, node := range nodes {
		if node < i.root || node >= i.program.program.Nodes[i.root].End {
			continue
		}
		s := i.state(node)
		if s.Started && (s.Status == Running || i.busy && s.Status == Invalid) && !s.Ready {
			s.Ready, s.Result = true, Invalid
		}
		i.markDirty(node)
	}
}

// fieldChanged 延迟驱动依赖条件，确保 setter 不重入正在执行的动作。
func (i *Instance[C]) fieldChanged(slot int) {
	if i.closed || i.status != Running {
		return
	}
	i.observe(slot)
	if !i.busy && i.states[0].dirty {
		i.schedule()
	}
}

// observe 合并同步执行期间的通知；只在根步骤返回后才改变待重评路径。
func (i *Instance[C]) observe(group int) {
	if len(i.dependencies.groups[group]) == 0 {
		return
	}
	if !i.busy {
		i.markObservers(i.dependencies.groups[group])
		return
	}
	if !i.pendingMask[group] {
		i.pendingMask[group] = true
		i.pending = append(i.pending, group)
	}
}

// flushNotifications 以稳定通知顺序处理受影响组，终态和取消会丢弃本轮未处理通知。
func (i *Instance[C]) flushNotifications(apply bool) {
	for _, group := range i.pending {
		i.pendingMask[group] = false
		if apply {
			i.markObservers(i.dependencies.groups[group])
		}
	}
	i.pending = i.pending[:0]
}

// setDependencies 在创建或切换版本时预分配去重队列，避免 setter 和事件热点分配。
func (i *Instance[C]) setDependencies(dependencies *rootDependencies) {
	i.dependencies = dependencies
	i.pending = make([]int, 0, len(dependencies.groups))
	i.pendingMask = make([]bool, len(dependencies.groups))
}

// schedule 复用实例创建时绑定的回调，合并同一队列周期内重复唤醒。
func (i *Instance[C]) schedule() {
	if i.queued || i.closed {
		return
	}
	i.queued = true
	i.options.Post(i.resume)
}

// log 在追踪开关判断之后组装结构化事件，不复制黑板或格式化业务字符串。
func (i *Instance[C]) log(kind LogKind, node int, from, to Status, reason string) {
	if i.options.Logger == nil {
		return
	}
	i.logSeq++
	record := LogRecord{Kind: kind, Version: i.Version(), InstanceID: i.id, TreeID: i.treeID, Sequence: i.logSeq, From: from, To: to, Reason: reason}
	if node >= 0 {
		record.NodeID = i.program.program.Nodes[node].ID
		record.NodeTreeID = i.program.program.Nodes[node].TreeID
		record.NodeIndex = node
	}
	emitLog(i.options.Logger, record)
}

// emitLog 保证观测接收者的 panic 不改变执行或发布结果。
func emitLog(logger func(LogRecord), record LogRecord) {
	defer func() { _ = recover() }()
	logger(record)
}

// bind 在轮次边界转换兼容黑板并重新创建该版本的执行状态。
func (i *Instance[C]) bind(next *preparedProgram[C]) error {
	if next == i.program {
		return nil
	}
	root, exists := next.program.Roots[i.treeID]
	if !exists {
		return fmt.Errorf("new version removed tree %q", i.treeID)
	}
	i.frame.Reset(i.root)
	if sameLayout(i.program.program.Fields, next.program.Fields) {
		i.frame.Board.fields = next.program.Fields
	} else {
		i.frame.Board.onChange = nil
		i.frame.Board = migrateBlackboard(i.frame.Board, next.program.Fields, i.fieldChanged)
	}
	i.program, i.root = next, root
	i.setDependencies(next.dependencies[root])
	i.states = make([]NodeState, next.program.Nodes[root].End-root)
	i.log(LogSwitch, -1, Invalid, Invalid, "program version changed")
	return nil
}
