package behaviortree

import "testing"

// TestMultipleRootStateIsolation 验证每个实例只分配入口树范围，绝对节点索引和事件不会串到其他入口。
func TestMultipleRootStateIsolation(t *testing.T) {
	p := &Program[int]{Version: "v1", Roots: map[string]int{"a": 0, "b": 2}, Nodes: []Node{{"a", "a", -1, 2}, {"action", "a", 0, 2}, {"b", "b", -1, 4}, {"action", "b", 2, 4}}, Dependencies: map[string][]int{"event:a": {1}, "event:b": {3}}}
	var step func(*Frame[int], int) Status
	step = func(f *Frame[int], n int) Status {
		if cached, run := f.Enter(n); !run {
			return cached
		}
		if n%2 == 0 {
			return f.Exit(n, step(f, n+1))
		}
		s := f.State(n)
		s.Started = true
		if s.Ready {
			f.Consume(n)
		}
		return f.Exit(n, Running)
	}
	p.Step = step
	p.Dependencies["event:shared"] = []int{3, 1}
	q := &testQueue{}
	i, err := NewInstance(p, "second tree", "b", 0, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(i.states) != 2 {
		t.Fatal("allocated state for unrelated roots")
	}
	group := i.dependencies.events["event:shared"]
	if len(i.dependencies.groups[group]) != 1 || i.dependencies.groups[group][0] != 3 {
		t.Fatal("current root retained unrelated tree observers")
	}
	i.Start()
	before := i.Steps()
	i.Notify("a")
	if i.Steps() != before {
		t.Fatal("other root event executed current tree")
	}
	i.Notify("b")
	if i.Steps() != before+2 {
		t.Fatal("absolute offset dispatch failed")
	}
	i.Close()
}

// TestSameLayoutKeepsBlackboard 验证仅动作变更或字段改名不会重新分配、迁移黑板。
func TestSameLayoutKeepsBlackboard(t *testing.T) {
	p := gateProgram("v1")
	p.Fields = []Field{{ID: "score", Name: "Score", Type: IntType}}
	r, _ := NewRegistry(p)
	q, ctx := &testQueue{}, &probe{}
	i, _ := r.NewInstance("one", "main", ctx, q.opts())
	i.Start()
	board := i.Blackboard()
	board.SetInt(0, 77)
	next := gateProgram("v2")
	next.Fields = []Field{{ID: "score", Name: "Renamed", Type: IntType, Default: Value{Int: 9}}}
	if err := r.Publish(next, EndOfRound); err != nil {
		t.Fatal(err)
	}
	i.Complete(ctx.tokens[0], Success)
	i.Complete(ctx.tokens[1], Success)
	i.Start()
	if i.Blackboard() != board || board.Int(0) != 77 {
		t.Fatal("same layout unnecessarily migrated blackboard")
	}
}

// TestStaleForceDoesNotRestartNewerRound 验证旧强制发布回调不会打断已经自然采用更新版本的一轮。
func TestStaleForceDoesNotRestartNewerRound(t *testing.T) {
	r, _ := NewRegistry(gateProgram("v1"))
	q, ctx := &testQueue{}, &probe{}
	i, _ := r.NewInstance("one", "main", ctx, q.opts())
	i.Start()
	r.Publish(gateProgram("v2"), CancelAndRestart)
	r.Publish(gateProgram("v3"), EndOfRound)
	i.Complete(ctx.tokens[0], Success)
	i.Complete(ctx.tokens[1], Success)
	i.Start()
	r.Publish(gateProgram("v4"), EndOfRound)
	q.drain(t)
	if i.Version() != "v3" || ctx.aborts != [2]int{} {
		t.Fatal("stale forced task interrupted newer round")
	}
}

// TestSelfNotificationIsDeferred 验证动作首次执行修改依赖字段时通知保留，且下一次宿主调度才 Resume。
func TestSelfNotificationIsDeferred(t *testing.T) {
	p := &Program[int]{Version: "v1", Roots: map[string]int{"main": 0}, Nodes: []Node{{"action", "main", -1, 1}}, Fields: []Field{{ID: "flag", Name: "Flag", Type: BoolType}}, Dependencies: map[string][]int{"field:flag": {0}}}
	starts, resumes := 0, 0
	p.Step = func(f *Frame[int], n int) Status {
		if cached, run := f.Enter(n); !run {
			return cached
		}
		s := f.State(n)
		if !s.Started {
			s.Started = true
			starts++
			f.Board.SetBool(0, true)
			if s.Ready {
				t.Fatal("field notification applied before synchronous step returned")
			}
			if resumes != 0 {
				t.Fatal("setter reentered action")
			}
			return f.Exit(n, Running)
		}
		resumes++
		if !s.Ready {
			t.Fatal("notification during Start was lost")
		}
		f.Consume(n)
		return f.Exit(n, Success)
	}
	q := &testQueue{}
	i, _ := NewInstance(p, "self", "main", 0, q.opts())
	if i.Start() != Running || starts != 1 || resumes != 0 {
		t.Fatal("incorrect first phase")
	}
	q.drain(t)
	if i.Status() != Success || resumes != 1 {
		t.Fatal("deferred notification failed")
	}
}

// TestDirtyParallelConstantWork 验证千分支 Parallel 单事件只执行根和受影响叶子，且稳态零分配。
func TestDirtyParallelConstantWork(t *testing.T) {
	q := &testQueue{}
	i, err := NewInstance(benchmarkProgram(1000, true, true), "wide", "main", 0, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	i.Start()
	q.drain(t)
	before := i.Steps()
	i.Notify("ready")
	if i.Steps() != before+2 {
		t.Fatal("unrelated parallel children executed")
	}
	allocations := testing.AllocsPerRun(1000, func() { i.Notify("ready") })
	if allocations != 0 {
		t.Fatalf("steady notification allocated %.1f objects", allocations)
	}
	if i.state(0).dirtyHead != -1 || len(q.items) != 0 {
		t.Fatal("stale dirty queue caused unsolicited work")
	}
}

// TestActionPanicRecovery 验证业务 panic 能恢复 busy、取消已启动动作并清除活跃索引。
func TestActionPanicRecovery(t *testing.T) {
	p := &Program[int]{Version: "v1", Roots: map[string]int{"main": 0}, Nodes: []Node{{"action", "main", -1, 1}}}
	aborts := 0
	p.Step = func(f *Frame[int], n int) Status { f.Enter(n); f.State(n).Started = true; panic("test action failure") }
	p.Abort = func(*Frame[int], int) { aborts++ }
	r, _ := NewRegistry(p)
	q := &testQueue{}
	i, _ := r.NewInstance("panic", "main", 0, q.opts())
	if i.Start() != Failure || i.Error() == nil || i.busy || aborts != 1 || r.Stats()[0].Active != 0 {
		t.Fatal("panic left stuck lifecycle")
	}
	next := *p
	next.Version = "v2"
	next.Step = func(f *Frame[int], n int) Status { f.Enter(n); return f.Exit(n, Success) }
	if err := r.Publish(&next, EndOfRound); err != nil {
		t.Fatal(err)
	}
	if i.Start() != Success || i.Version() != "v2" {
		t.Fatal("panic prevented future version recovery")
	}
}

// TestAbortAndLoggerPanicIsolation 验证单个 Abort 或日志接收者 panic 不阻止其他分支和热更完成。
func TestAbortAndLoggerPanicIsolation(t *testing.T) {
	p := benchmarkProgram(3, true, true)
	aborts := 0
	p.Abort = func(*Frame[int], int) { aborts++; panic("test cleanup failure") }
	r, _ := NewRegistry(p)
	r.SetLogger(func(LogRecord) { panic("test publish logger") })
	q := &testQueue{}
	opts := q.opts()
	opts.Trace = true
	opts.Logger = func(LogRecord) { panic("test instance logger") }
	i, _ := r.NewInstance("panic", "main", 0, opts)
	if i.Start() != Running {
		t.Fatal("logger panic changed start result")
	}
	next := benchmarkProgram(3, true, true)
	next.Version = "v2"
	if err := r.Publish(next, CancelAndRestart); err != nil {
		t.Fatal(err)
	}
	q.drain(t)
	if aborts != 2 || i.busy || i.Version() != "v2" || i.Status() != Running {
		t.Fatalf("panic blocked cleanup or switch: aborts=%d version=%s status=%s", aborts, i.Version(), i.Status())
	}
}

// TestDeferredAbortIsBoundToRound 验证动作内部延迟取消不会作用到宿主随后启动的新一轮。
func TestDeferredAbortIsBoundToRound(t *testing.T) {
	q := &testQueue{}
	p := &Program[int]{Version: "v1", Roots: map[string]int{"main": 0}, Nodes: []Node{{"action", "main", -1, 1}}}
	var i *Instance[int]
	rounds := 0
	p.Step = func(f *Frame[int], n int) Status {
		if cached, run := f.Enter(n); !run {
			return cached
		}
		f.State(n).Started = true
		rounds++
		if rounds == 1 {
			i.Abort("first round only")
			return f.Exit(n, Success)
		}
		return f.Exit(n, Running)
	}
	i, _ = NewInstance(p, "round", "main", 0, q.opts())
	if i.Start() != Success || i.Start() != Running {
		t.Fatal("unexpected setup")
	}
	q.drain(t)
	if i.Status() != Running || rounds != 2 {
		t.Fatal("deferred old cancellation hit new round")
	}
}

// TestSeveralPendingPublications 验证多个尚未执行的强更合并到最新版本，之后不会重复取消。
func TestSeveralPendingPublications(t *testing.T) {
	r, _ := NewRegistry(gateProgram("v1"))
	q, ctx := &testQueue{}, &probe{}
	i, _ := r.NewInstance("one", "main", ctx, q.opts())
	i.Start()
	for _, version := range []string{"v2", "v3"} {
		if err := r.Publish(gateProgram(version), CancelAndRestart); err != nil {
			t.Fatal(err)
		}
	}
	r.Publish(gateProgram("v4"), EndOfRound)
	q.drain(t)
	if i.Version() != "v4" || ctx.aborts[0] != 1 || ctx.starts[0] != 2 {
		t.Fatal("pending publication caused repeated cancellation")
	}
}

// TestDirtyChildReadOnlyQueries 验证优先级快捷路径查询不会消费通知，多个分支变化时不会误报唯一分支。
func TestDirtyChildReadOnlyQueries(t *testing.T) {
	p := benchmarkProgram(4, true, true)
	q := &testQueue{}
	i, err := NewInstance(p, "dirty-query", "main", 0, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	i.Start()
	if i.frame.OnlyDirtyChild(0, 1) || i.frame.IsDirty(1) {
		t.Fatal("clean tree reported dirty child")
	}
	i.markDirty(1)
	if !i.frame.OnlyDirtyChild(0, 1) || !i.frame.IsDirty(1) || !i.frame.IsDirty(0) {
		t.Fatal("single dirty child not recognized")
	}
	if !i.frame.OnlyDirtyChild(0, 1) || i.frame.PopDirtyChild(0) != 1 {
		t.Fatal("read-only query consumed notification")
	}
	i.markDirty(1)
	i.markDirty(2)
	if i.frame.OnlyDirtyChild(0, 1) || i.frame.OnlyDirtyChild(0, 2) {
		t.Fatal("multiple pending branches reported as unique")
	}
	if i.frame.PopDirtyChild(0) != 1 || !i.frame.OnlyDirtyChild(0, 2) || i.frame.PopDirtyChild(0) != 2 {
		t.Fatal("query changed dirty queue order")
	}
	i.Close()
}
