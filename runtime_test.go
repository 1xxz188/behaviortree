package behaviortree

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// testQueue 模拟宿主串行队列及一次性时钟，不启动后台协程。
type testQueue struct {
	items  []func()
	timers []*testTimer
}

// testTimer 记录一次性通知是否已取消。
type testTimer struct {
	callback func()
	canceled bool
}

// post 保持回调异步入队，供测试显式驱动。
func (q *testQueue) post(callback func()) { q.items = append(q.items, callback) }

// after 保存人工触发的时钟通知。
func (q *testQueue) after(_ time.Duration, callback func()) CancelFunc {
	timer := &testTimer{callback: callback}
	q.timers = append(q.timers, timer)
	return func() { timer.canceled = true }
}

// drain 驱动已就绪回调，并检测预算实现导致的无限自唤醒。
func (q *testQueue) drain(t *testing.T) {
	t.Helper()
	for count := 0; len(q.items) > 0; count++ {
		if count > 10000 {
			t.Fatal("queue did not settle")
		}
		fn := q.items[0]
		q.items[0] = nil
		q.items = q.items[1:]
		fn()
	}
}

// opts 生成统一宿主选项。
func (q *testQueue) opts() Options { return Options{Post: q.post, After: q.after} }

// probe 保存动作生命周期和版本结果。
type probe struct {
	starts   [2]int
	resumes  [2]int
	aborts   [2]int
	tokens   [2]CallbackToken
	versions []string
}

// gateProgram 创建两步串行动作，验证生成代码与运行时的最小契约。
func gateProgram(version string) *Program[*probe] {
	p := &Program[*probe]{Version: version, Roots: map[string]int{"main": 0}, Nodes: []Node{{"root", "main", -1, 3}, {"first", "main", 0, 2}, {"second", "main", 0, 3}}, Events: []EventDefinition{{ID: 1, Name: "唤醒", CodeName: "Wake"}}, EventDependencies: map[EventID][]int{1: {1}}}
	var step func(*Frame[*probe], int) Status
	step = func(f *Frame[*probe], n int) Status {
		if cached, execute := f.Enter(n); !execute {
			return cached
		}
		s := f.State(n)
		if n == 0 {
			for s.Cursor < 2 {
				result := step(f, s.Cursor+1)
				if result != Success {
					return f.Exit(n, result)
				}
				s.Cursor++
			}
			return f.Exit(n, Success)
		}
		k := n - 1
		if !s.Started {
			s.Started = true
			f.Context.starts[k]++
			f.Context.tokens[k] = f.Token(n)
			f.Context.versions = append(f.Context.versions, version)
			return f.Exit(n, Running)
		}
		f.Context.resumes[k]++
		result := f.Consume(n)
		if result == Invalid {
			result = Success
		}
		return f.Exit(n, result)
	}
	p.Step = step
	p.Abort = func(f *Frame[*probe], n int) {
		if n > 0 {
			f.Context.aborts[n-1]++
		}
	}
	return p
}

// TestCompletionIsolation 验证重复、跨实例、取消及上一轮的迟到回调均被过滤。
func TestCompletionIsolation(t *testing.T) {
	q, context := &testQueue{}, &probe{}
	i, err := NewInstance(gateProgram("v1"), "one", "main", context, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	if i.Start() != Running {
		t.Fatal("expected waiting first action")
	}
	first := context.tokens[0]
	if !i.Complete(first, Success) || i.Complete(first, Success) {
		t.Fatal("completion must be accepted exactly once")
	}
	if context.starts != [2]int{1, 1} {
		t.Fatalf("sequence restarted completed action: %v", context.starts)
	}
	if i.Complete(CallbackToken{first.Instance + 1, 2, context.tokens[1].Sequence}, Success) {
		t.Fatal("cross instance token accepted")
	}
	second := context.tokens[1]
	i.Abort("test cancel")
	if context.aborts != [2]int{0, 1} || i.Complete(second, Success) {
		t.Fatal("canceled action callback accepted")
	}
	i.Start()
	if i.Complete(first, Success) || context.tokens[0] == first {
		t.Fatal("old round token accepted")
	}
	i.Complete(context.tokens[0], Failure)
	if i.Status() != Failure {
		t.Fatal("failure not propagated")
	}
	i.Start()
	if context.starts != [2]int{3, 1} {
		t.Fatal("failed sequence cursor not reset")
	}
	i.Close()
	if i.Complete(context.tokens[0], Success) || i.Start() != Failure {
		t.Fatal("closed instance accepted work")
	}
}

// TestEventAndIdleWork 验证空闲和无关事件不执行节点，关联事件只续跑当前路径。
func TestEventAndIdleWork(t *testing.T) {
	q, context := &testQueue{}, &probe{}
	i, _ := NewInstance(gateProgram("v1"), "one", "main", context, q.opts())
	i.Start()
	steps := i.Steps()
	for range 1000 {
		i.Tick()
		i.Notify(2)
	}
	if i.Steps() != steps {
		t.Fatal("idle instance executed nodes")
	}
	i.Notify(1)
	if context.resumes[0] != 1 || context.starts[1] != 1 {
		t.Fatal("event did not resume dependent action")
	}
	if len(q.items) != 0 {
		t.Fatal("unsolicited polling task")
	}
}

// TestBudgetProgressAtDepth 验证每次仅一步预算也能穿过运行中的祖先并完成深树。
func TestBudgetProgressAtDepth(t *testing.T) {
	const depth = 40
	p := &Program[int]{Version: "v1", Roots: map[string]int{"main": 0}, Nodes: make([]Node, depth)}
	for n := range p.Nodes {
		p.Nodes[n] = Node{ID: fmt.Sprint(n), TreeID: "main", Parent: n - 1, End: depth}
	}
	var step func(*Frame[int], int) Status
	step = func(f *Frame[int], node int) Status {
		if cached, execute := f.Enter(node); !execute {
			return cached
		}
		if node+1 == depth {
			return f.Exit(node, Success)
		}
		return f.Exit(node, step(f, node+1))
	}
	p.Step = step
	q := &testQueue{}
	i, err := NewInstance(p, "deep", "main", 0, Options{Budget: 1, Post: q.post})
	if err != nil {
		t.Fatal(err)
	}
	if i.Start() != Running {
		t.Fatal("budget was not applied")
	}
	q.drain(t)
	if i.Status() != Success {
		t.Fatal("small budget stalled deep tree")
	}
}

// TestTimerCancellationAndLateDelivery 验证宿主一次性计时、取消和已入队通知的令牌过滤。
func TestTimerCancellationAndLateDelivery(t *testing.T) {
	q := &testQueue{}
	p := &Program[int]{Version: "v1", Roots: map[string]int{"main": 0}, Nodes: []Node{{"wait", "main", -1, 1}}}
	p.Step = func(f *Frame[int], n int) Status {
		if cached, execute := f.Enter(n); !execute {
			return cached
		}
		s := f.State(n)
		if !s.Started {
			s.Started = true
			f.After(n, time.Second)
			return f.Exit(n, Running)
		}
		if s.Ready {
			return f.Exit(n, f.Consume(n))
		}
		return f.Exit(n, Running)
	}
	i, _ := NewInstance(p, "wait", "main", 0, q.opts())
	i.Start()
	q.timers[0].callback()
	i.Abort("timer canceled after delivery")
	q.drain(t)
	if i.Status() != Invalid || !q.timers[0].canceled {
		t.Fatal("late timer resurrected canceled instance")
	}
	i.Start()
	q.timers[1].callback()
	q.drain(t)
	if i.Status() != Success || !q.timers[1].canceled {
		t.Fatal("timer did not finish and release")
	}
	broken, _ := NewInstance(p, "missing timer", "main", 0, Options{Post: q.post})
	if broken.Start() != Failure || broken.Error() == nil {
		t.Fatal("missing host timer must fail explicitly")
	}
}

// TestGuardBeforeParentTouch 验证优先级节点先求值守卫不会破坏已访问子树的状态链接。
func TestGuardBeforeParentTouch(t *testing.T) {
	q, context := &testQueue{}, &probe{}
	p := gateProgram("v1")
	p.Nodes = []Node{{"root", "main", -1, 4}, {"sequence", "main", 0, 4}, {"guard", "main", 1, 3}, {"action", "main", 1, 4}}
	p.EventDependencies = nil
	p.Step = func(f *Frame[*probe], n int) Status {
		f.Enter(0)
		f.Enter(2)
		f.Exit(2, Success)
		f.Enter(1)
		f.Enter(3)
		f.State(3).Started = true
		f.Exit(3, Running)
		f.Exit(1, Running)
		return f.Exit(0, Running)
	}
	p.Abort = func(f *Frame[*probe], n int) {
		if n == 3 {
			f.Context.aborts[0]++
		}
	}
	i, err := NewInstance(p, "priority", "main", context, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	i.Start()
	i.Abort("check linked state")
	if context.aborts[0] != 1 {
		t.Fatal("action missing from linked subtree")
	}
	for n := range i.states {
		if i.states[n].linked {
			t.Fatalf("node %d retained stale linked state", n)
		}
	}
}

// TestBlackboardNotificationsAndMigration 验证变化通知、稳定 ID 改名、重排、追加和不兼容拒绝。
func TestBlackboardNotificationsAndMigration(t *testing.T) {
	fields := []Field{{ID: "enabled", Name: "Enabled", Type: BoolType}, {ID: "score", Name: "Score", Type: IntType, Default: Value{Int: 7}}}
	p := gateProgram("v1")
	p.Fields = fields
	p.FieldDependencies = map[string][]int{"enabled": {1}}
	r, _ := NewRegistry(p)
	q, context := &testQueue{}, &probe{}
	i, _ := r.NewInstance("one", "main", context, q.opts())
	i.Start()
	i.Blackboard().SetBool(0, false)
	if len(q.items) != 0 {
		t.Fatal("unchanged value notified")
	}
	i.Blackboard().SetBool(0, true)
	i.Blackboard().SetBool(0, true)
	if len(q.items) != 1 {
		t.Fatal("field notifications not coalesced")
	}
	q.drain(t)
	i.Blackboard().SetInt(1, 88)
	p2 := gateProgram("v2")
	p2.Fields = []Field{{ID: "score", Name: "RenamedScore", Type: IntType}, {ID: "enabled", Name: "Enabled", Type: BoolType}, {ID: "label", Name: "Label", Type: StringType, Default: Value{String: "new"}}}
	if err := r.Publish(p2, EndOfRound); err != nil {
		t.Fatal(err)
	}
	oldBoard := i.Blackboard()
	if i.Version() != "v1" || oldBoard.Len() != 2 {
		t.Fatal("active layout switched mid-round")
	}
	i.Complete(context.tokens[1], Success)
	i.Start()
	if i.Version() != "v2" || i.Blackboard().Int(0) != 88 || !i.Blackboard().Bool(1) || i.Blackboard().String(2) != "new" {
		t.Fatal("stable field migration lost values")
	}
	oldBoard.SetBool(0, false)
	if !i.Blackboard().Bool(1) {
		t.Fatal("stale board changed migrated state")
	}
	bad := gateProgram("v3")
	bad.Fields = fields
	if err := r.Publish(bad, EndOfRound); err == nil {
		t.Fatal("removed field accepted")
	}
	bad.Fields = append([]Field(nil), p2.Fields...)
	bad.Fields[0].Type = StringType
	if err := r.Publish(bad, EndOfRound); err == nil {
		t.Fatal("field type change accepted")
	}
	if r.CurrentVersion() != "v2" {
		t.Fatal("invalid publication replaced current version")
	}
}

// TestRegistryPoliciesAndCounts 验证整轮固定版本、强制取消、新实例版本和活跃计数。
func TestRegistryPoliciesAndCounts(t *testing.T) {
	r, _ := NewRegistry(gateProgram("v1"))
	q, context := &testQueue{}, &probe{}
	i, _ := r.NewInstance("one", "main", context, q.opts())
	i.Start()
	if err := r.Publish(gateProgram("v2"), EndOfRound); err != nil {
		t.Fatal(err)
	}
	if len(q.items) != 0 || i.Version() != "v1" {
		t.Fatal("normal publish touched running instance")
	}
	i.Complete(context.tokens[0], Success)
	if !reflect.DeepEqual(context.versions, []string{"v1", "v1"}) {
		t.Fatal("single round mixed versions")
	}
	newInstance, _ := r.NewInstance("two", "main", &probe{}, q.opts())
	if newInstance.Version() != "v2" {
		t.Fatal("new instance did not use latest version")
	}
	oldToken := context.tokens[1]
	if err := r.Publish(gateProgram("v3"), CancelAndRestart); err != nil {
		t.Fatal(err)
	}
	if i.Version() != "v1" || len(q.items) != 1 {
		t.Fatal("forced restart bypassed owner queue")
	}
	q.drain(t)
	if i.Version() != "v3" || context.aborts[1] != 1 || i.Complete(oldToken, Success) {
		t.Fatal("forced restart did not cancel old action")
	}
	stats := r.Stats()
	if !reflect.DeepEqual(stats, []VersionStats{{"v1", 0, false}, {"v2", 0, false}, {"v3", 1, true}}) {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	i.Close()
	if r.Stats()[2].Active != 0 {
		t.Fatal("closed instance retained in active index")
	}
	if err := r.Publish(gateProgram("v3"), EndOfRound); err == nil {
		t.Fatal("duplicate version accepted")
	}
}

// TestForceBatching 验证大批热更通过宿主队列继续分批投递，不创建定时轮询。
func TestForceBatching(t *testing.T) {
	r, _ := NewRegistry(gateProgram("v1"))
	q := &testQueue{}
	instances := make([]*Instance[*probe], 300)
	for n := range instances {
		instances[n], _ = r.NewInstance(fmt.Sprint(n), "main", &probe{}, q.opts())
		instances[n].Start()
	}
	if err := r.Publish(gateProgram("v2"), CancelAndRestart); err != nil {
		t.Fatal(err)
	}
	if len(q.items) != 129 {
		t.Fatalf("first batch posted %d instead of 128 + continuation", len(q.items))
	}
	q.drain(t)
	for _, i := range instances {
		if i.Version() != "v2" {
			t.Fatal("batch did not reach instance")
		}
		i.Close()
	}
}

// TestProgramValidationAndSnapshot 验证发布描述被复制且畸形树范围被拒绝。
func TestProgramValidationAndSnapshot(t *testing.T) {
	p := gateProgram("v1")
	r, err := NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Roots["main"] = 99
	p.Nodes[0].End = 99
	q := &testQueue{}
	i, err := r.NewInstance("one", "main", &probe{}, q.opts())
	if err != nil || i.Start() != Running {
		t.Fatal("caller mutation corrupted published descriptor")
	}
	if err := ValidateProgram(p); err == nil {
		t.Fatal("invalid root accepted")
	}
	p = gateProgram("bad")
	p.Nodes[1].End = 3
	if err := ValidateProgram(p); err == nil {
		t.Fatal("crossing subtree interval accepted")
	}
}

// TestTraceSwitch 验证默认无节点追踪，显式开启后事件包含节点映射和执行版本。
func TestTraceSwitch(t *testing.T) {
	q, context := &testQueue{}, &probe{}
	var records []LogRecord
	opts := q.opts()
	opts.Logger = func(record LogRecord) { records = append(records, record) }
	i, _ := NewInstance(gateProgram("v1"), "logged", "main", context, opts)
	i.Start()
	if len(records) != 0 {
		t.Fatal("trace disabled but node logs produced")
	}
	i.SetTrace(true)
	i.Complete(context.tokens[0], Success)
	if len(records) == 0 || records[0].NodeID != "first" || records[0].NodeIndex != 1 || records[0].Version != "v1" {
		t.Fatalf("missing mapped trace: %+v", records)
	}
}
