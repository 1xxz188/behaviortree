package codegen

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestGeneratedPriorityReselect 编译并执行真实生成代码，覆盖完成重选、候选锁存及低预算指令替换。
func TestGeneratedPriorityReselect(t *testing.T) {
	p := reselectPriorityProject()
	result, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedFiles(t, dir, result)
	gomod := "module bt.priority.reselect.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(moduleRoot)) + "\n"
	for name, content := range map[string]string{"go.mod": gomod, "reselect_test.go": priorityReselectFixture} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-count=1", "-v", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("完成重选生成代码回归失败: %v\n%s", err, output)
	}
	t.Log(string(output))
}

// TestPriorityReselectPrivateNameCollisions 验证重选私有类型避开合法同包上下文类型和业务函数名称。
func TestPriorityReselectPrivateNameCollisions(t *testing.T) {
	for _, names := range [][2]string{
		{"btPriorityReselectState", "btPriorityReselectCandidate"},
		{"btPriorityReselectCandidate", "btPriorityReselectState"},
	} {
		t.Run(names[0], func(t *testing.T) {
			p := model.Project{
				SchemaVersion: model.SchemaVersion, Name: "重选私有名称消歧", NextEventID: "1",
				Generation: model.Generation{PackagePath: "generated", ContextType: "*" + names[0]},
				Catalog:    []model.Definition{{ID: "action", Name: "冲突名称动作", Kind: model.DefinitionAction, GoName: names[1]}},
				Trees: []model.Tree{{ID: "main", Name: "重选入口", Root: "root", Nodes: []model.Node{
					{ID: "root", Type: model.NodePriority, ReselectOnCompletion: true, Children: []string{"action"}},
					{ID: "action", Type: model.NodeAction, Binding: "action"},
				}}},
			}
			result, err := Generate(p)
			if err != nil {
				t.Fatal(err)
			}
			// 真实编译同时验证私有状态、候选类型和生成函数中的引用已全部正确消歧。
			compileNamedProject(t, p, result, "type "+names[0]+" struct{}\n")
		})
	}
}

// reselectPriorityProject 使用真实动作和守卫组合构造调度、有限终态与并行运动三类入口。
func reselectPriorityProject() model.Project {
	value := func(label string) model.Value { raw, _ := json.Marshal(label); return model.Value{Value: raw} }
	action := func(id, label string, idle bool) model.Node {
		binding := "action"
		if idle {
			binding = "idle"
		}
		return model.Node{ID: id, Type: model.NodeAction, Binding: binding, Params: map[string]model.Value{"Label": value(label)}}
	}
	guarded := func(label string) []model.Node {
		return []model.Node{
			{ID: label + "Branch", Type: model.NodeSequence, Children: []string{label + "Guard", label + "Work"}},
			{ID: label + "Guard", Type: model.NodeCondition, Binding: "guard", Params: map[string]model.Value{"Label": value(label)}},
			action(label+"Work", label, false),
		}
	}
	work := model.Tree{ID: "work", Name: "完成重选", Root: "root", Nodes: []model.Node{
		{ID: "root", Type: model.NodePriority, ReselectOnCompletion: true, Children: []string{"highBranch", "taskBranch", "lowBranch", "idle"}},
	}}
	for _, label := range []string{"high", "task", "low"} {
		work.Nodes = append(work.Nodes, guarded(label)...)
	}
	work.Nodes = append(work.Nodes, action("idle", "idle", true))
	terminal := model.Tree{ID: "terminal", Name: "没有后续候选", Root: "root", Nodes: []model.Node{
		{ID: "root", Type: model.NodePriority, ReselectOnCompletion: true, Children: []string{"taskBranch"}},
	}}
	terminal.Nodes = append(terminal.Nodes, guarded("task")...)
	ordinary := model.Tree{ID: "ordinary", Name: "普通优先级", Root: "root", Nodes: []model.Node{
		{ID: "root", Type: model.NodePriority, Children: []string{"taskBranch", "idle"}},
	}}
	ordinary.Nodes = append(ordinary.Nodes, guarded("task")...)
	ordinary.Nodes = append(ordinary.Nodes, action("idle", "idle", true))
	chain := model.Tree{ID: "chain", Name: "最近完成结果", Root: "root", Nodes: []model.Node{
		{ID: "root", Type: model.NodePriority, ReselectOnCompletion: true, Children: []string{"firstBranch", "second"}},
	}}
	chain.Nodes = append(chain.Nodes, guarded("first")...)
	chain.Nodes = append(chain.Nodes, action("second", "second", false))
	parallel := model.Tree{ID: "parallel", Name: "并行运动调度", Root: "root", Nodes: []model.Node{
		{ID: "root", Type: model.NodeParallel, Children: []string{"motion", "combat"}},
		{ID: "motion", Type: model.NodePriority, ReselectOnCompletion: true, Children: []string{"moveBranch", "stay"}},
	}}
	parallel.Nodes = append(parallel.Nodes, guarded("move")...)
	parallel.Nodes = append(parallel.Nodes, action("stay", "stay", true), action("combat", "combat", true))
	wide := model.Tree{ID: "wide", Name: "宽优先级快速续跑", Root: "root", Nodes: []model.Node{
		{ID: "root", Type: model.NodePriority, ReselectOnCompletion: true},
	}}
	for n := 0; n < 100; n++ {
		label := fmt.Sprintf("unused%d", n)
		wide.Nodes[0].Children = append(wide.Nodes[0].Children, label+"Branch")
		wide.Nodes = append(wide.Nodes, guarded(label)...)
	}
	wide.Nodes[0].Children = append(wide.Nodes[0].Children, "idle")
	wide.Nodes = append(wide.Nodes, action("idle", "idle", true))
	return model.Project{
		SchemaVersion: model.SchemaVersion, Name: "完成重选回归", NextEventID: "4",
		Generation: model.Generation{PackagePath: "generated", ContextType: "*reselectContext"},
		Events: []model.EventDefinition{
			{ID: "1", Name: "指令变化", CodeName: "Command"},
			{ID: "2", Name: "普通唤醒", CodeName: "Pulse"},
			{ID: "3", Name: "停留续跑", CodeName: "IdleResume"},
		},
		Catalog: []model.Definition{
			{ID: "guard", Name: "候选守卫", Kind: model.DefinitionCondition, GoName: "ReselectGuard", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}, EventIDs: []string{"1"}},
			{ID: "action", Name: "有限动作", Kind: model.DefinitionAction, GoName: "ReselectAction", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}, EventIDs: []string{"1", "2"}},
			{ID: "idle", Name: "持续动作", Kind: model.DefinitionAction, GoName: "ReselectIdle", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}, EventIDs: []string{"2", "3"}},
		},
		Trees: []model.Tree{work, terminal, ordinary, chain, parallel, wide},
	}
}

// priorityReselectFixture 在临时模块内验证行为结果，避免只断言生成文本或复制生成器算法。
const priorityReselectFixture = `package generated

import (
	"fmt"
	"testing"
	bt "github.com/1xxz188/behaviortree"
)

// reselectContext 记录宿主权威条件、动作生命周期及串行预算续跑。
type reselectContext struct {
	enabled map[string]bool // enabled 是宿主在事件通知前更新的候选条件。
	starts map[string]int // starts 记录每个动作真正开始的次数。
	resumes map[string]int // resumes 检查已完成动作是否被普通通知续跑。
	aborts map[string]int // aborts 检查重复抢占与无关并行分支取消。
	tokens map[string]bt.CallbackToken // tokens 保存各动作最新异步操作令牌。
	startResult map[string]bt.Status // startResult 按需模拟同步终态动作。
	onFinish map[string]func() // onFinish 在动作返回终态前改变宿主条件。
	checks map[string]int // checks 统计真实业务条件执行。
	nodes map[string]int // nodes 从程序元数据定位当前入口的候选。
	queue []func() // queue 是宿主串行队列，不使用真实时钟或协程。
	log []string // log 验证旧动作取消先于新动作开始。
	frame *bt.Frame[*reselectContext] // frame 供宿主调用生成的候选重置接口。
	instance *bt.Instance[*reselectContext] // instance 在业务动作内部通知状态变化。
}

// ReselectGuard 只读取权威条件，用于检验动作完成后是否刷新旧缓存。
func ReselectGuard(f *bt.Frame[*reselectContext], _ int, p ReselectGuardParams) bool {
	c := f.Context
	c.frame = f
	c.checks[p.Label]++
	return c.enabled[p.Label]
}

// runReselectAction 以异步令牌模拟有限任务，以 Start/Resume/Abort 记录实际生命周期。
func runReselectAction(f *bt.Frame[*reselectContext], node int, phase bt.Phase, label string) bt.Status {
	c := f.Context
	c.frame = f
	result := bt.Running
	switch phase {
	case bt.Start:
		c.starts[label]++
		c.tokens[label] = f.Token(node)
		c.log = append(c.log, "start:"+label)
		if status := c.startResult[label]; status != bt.Invalid { result = status }
	case bt.Resume:
		c.resumes[label]++
		if status := f.Consume(node); status != bt.Invalid { result = status }
	case bt.Abort:
		c.aborts[label]++
		c.log = append(c.log, "abort:"+label)
	}
	if phase != bt.Abort && (result == bt.Success || result == bt.Failure) {
		if fn := c.onFinish[label]; fn != nil { fn() }
	}
	return result
}

// ReselectAction 是带指令及普通通知依赖的有限动作。
func ReselectAction(f *bt.Frame[*reselectContext], node int, phase bt.Phase, p ReselectActionParams) bt.Status {
	return runReselectAction(f, node, phase, p.Label)
}

// ReselectIdle 持续等待，只有显式普通通知才会唤醒。
func ReselectIdle(f *bt.Frame[*reselectContext], node int, phase bt.Phase, p ReselectIdleParams) bt.Status {
	return runReselectAction(f, node, phase, p.Label)
}

// newReselectTest 创建真实生成入口；节点槽位来自描述数据而非私有常量命名。
func newReselectTest(t *testing.T, tree string, budget int) (*bt.Instance[*reselectContext], *reselectContext) {
	t.Helper()
	c := &reselectContext{
		enabled: map[string]bool{}, starts: map[string]int{}, resumes: map[string]int{},
		aborts: map[string]int{}, tokens: map[string]bt.CallbackToken{},
		startResult: map[string]bt.Status{}, onFinish: map[string]func(){},
		checks: map[string]int{}, nodes: map[string]int{},
	}
	program := NewProgram("reselect")
	root := program.Roots[tree]
	for n := root; n < program.Nodes[root].End; n++ { c.nodes[program.Nodes[n].ID] = n }
	i, err := bt.NewInstance(program, "one", tree, c, bt.Options{
		Budget: budget, Post: func(fn func()) { c.queue = append(c.queue, fn) },
		Logger: func(bt.LogRecord) {},
	})
	if err != nil { t.Fatal(err) }
	c.instance = i
	t.Cleanup(i.Close)
	return i, c
}

// drainReselect 消费所有预算让出，有限上限使自激循环成为明确失败。
func drainReselect(t *testing.T, c *reselectContext) {
	t.Helper()
	for n := 0; len(c.queue) > 0; n++ {
		if n >= 1000 { t.Fatal("完成重选预算续跑未收敛", c.starts, c.checks) }
		queue := c.queue
		c.queue = nil
		for _, fn := range queue { fn() }
	}
}

// TestCompletionToIdle 验证采集成功后直接进入停留，终态与普通通知均不重启旧动作。
func TestCompletionToIdle(t *testing.T) {
	for _, budget := range []int{1, 2, 1024} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			i, c := newReselectTest(t, "work", budget)
			c.enabled["task"] = true
			c.onFinish["task"] = func() { c.enabled["task"] = false; i.Notify(EventCommand) }
			i.Start(); drainReselect(t, c)
			old := c.tokens["task"]
			if c.starts["task"] != 1 || c.starts["idle"] != 0 { t.Fatal("初始候选错误", c.starts) }
			if !i.Complete(old, bt.Success) { t.Fatal("有效完成被拒绝") }
			drainReselect(t, c)
			if i.Status() != bt.Running || c.starts["idle"] != 1 || c.starts["task"] != 1 {
				t.Fatal("成功未转入真实停留", i.Status(), c.starts)
			}
			if c.frame.State(c.nodes["taskWork"]).Status != bt.Success {
				t.Fatal("完成动作的真实成功结果被覆盖")
			}
			if i.Complete(old, bt.Success) { t.Fatal("重复完成仍被接受") }
			resumes := c.resumes["task"]
			for n := 0; n < 5; n++ { i.Notify(EventPulse); drainReselect(t, c) }
			if c.resumes["task"] != resumes || c.starts["task"] != 1 {
				t.Fatal("普通通知续跑已完成动作", c.resumes, c.starts)
			}
			steps := i.Steps()
			for n := 0; n < 100; n++ { i.Tick() }
			if i.Steps() != steps || len(c.queue) != 0 { t.Fatal("停留状态存在空闲轮询", i.Steps()-steps) }
		})
	}
}

// TestFinishRefreshesHigherGuard 验证动作内通知暂存时仍先刷新条件，不错误启动低优先副作用。
func TestFinishRefreshesHigherGuard(t *testing.T) {
	for _, budget := range []int{1, 1024} {
		for _, notify := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/notify=%v", budget, notify), func(t *testing.T) {
			i, c := newReselectTest(t, "work", budget)
			c.enabled["task"] = true
			c.enabled["low"] = true
			c.onFinish["task"] = func() {
				c.enabled["task"] = false
				c.enabled["high"] = true
				if notify { i.Notify(EventCommand) }
			}
			i.Start(); drainReselect(t, c)
			if !i.Complete(c.tokens["task"], bt.Success) { t.Fatal("有效完成被拒绝") }
			drainReselect(t, c)
			if c.starts["high"] != 1 || c.starts["low"] != 0 || c.starts["idle"] != 0 {
				t.Fatal("完成重选使用旧条件或启动低优先副作用", c.starts, c.checks)
			}
			})
		}
	}
}

// TestOrdinaryPriorityCompletion 保持未配置重选的 Priority 成功透传语义。
func TestOrdinaryPriorityCompletion(t *testing.T) {
	i, c := newReselectTest(t, "ordinary", 1024)
	c.enabled["task"] = true
	i.Start(); drainReselect(t, c)
	if !i.Complete(c.tokens["task"], bt.Success) { t.Fatal("有效完成被拒绝") }
	drainReselect(t, c)
	if i.Status() != bt.Success || c.starts["idle"] != 0 { t.Fatal("普通优先级成功语义被改变", i.Status(), c.starts) }
}

// TestReselectIdleFastPath 验证宽优先级中当前动作的独立事件续跑只执行两步且零分配。
func TestReselectIdleFastPath(t *testing.T) {
	i, c := newReselectTest(t, "wide", 4096)
	i.Start(); drainReselect(t, c)
	checks := 0
	for _, count := range c.checks { checks += count }
	steps := i.Steps()
	for n := 0; n < 100; n++ { i.Notify(EventIdleResume) }
	after := 0
	for _, count := range c.checks { after += count }
	if after != checks || i.Steps()-steps != 200 { t.Fatal("当前动作通知重新求值候选或增加节点工作", after-checks, i.Steps()-steps) }
	if allocs := testing.AllocsPerRun(100, func() { i.Notify(EventIdleResume) }); allocs != 0 { t.Fatal("当前动作稳态续跑出现分配", allocs) }
}

// TestFailureLatchAndGuardReactivation 验证持续真守卫不重试失败候选，假再真才允许重新 Start。
func TestFailureLatchAndGuardReactivation(t *testing.T) {
	for _, budget := range []int{1, 1024} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			i, c := newReselectTest(t, "work", budget)
			c.enabled["task"] = true
			c.startResult["task"] = bt.Failure
			i.Start(); drainReselect(t, c)
			if c.starts["task"] != 1 || c.starts["idle"] != 1 { t.Fatal("失败未进入停留", c.starts) }
			for n := 0; n < 10; n++ {
				i.Notify(EventCommand); drainReselect(t, c)
				i.Notify(EventPulse); drainReselect(t, c)
			}
			if c.starts["task"] != 1 || c.resumes["task"] != 0 || c.aborts["idle"] != 0 {
				t.Fatal("失败候选在守卫持续真时反复执行或抢占", c.starts, c.resumes, c.aborts)
			}
			c.enabled["task"] = false; i.Notify(EventCommand); drainReselect(t, c)
			c.enabled["task"] = true; i.Notify(EventCommand); drainReselect(t, c)
			if c.starts["task"] != 2 { t.Fatal("假转真未重新启动候选", c.starts) }
		})
	}
}

// TestNoFurtherCandidateReturnsTerminal 验证有限树保持真实终态，初始全拒绝为 Failure。
func TestNoFurtherCandidateReturnsTerminal(t *testing.T) {
	for _, budget := range []int{1, 1024} {
		for _, result := range []bt.Status{bt.Success, bt.Failure} {
			t.Run(fmt.Sprintf("%d/%s", budget, result), func(t *testing.T) {
				i, c := newReselectTest(t, "terminal", budget)
				c.enabled["task"] = true
				i.Start(); drainReselect(t, c)
				if !i.Complete(c.tokens["task"], result) { t.Fatal("有效完成被拒绝") }
				drainReselect(t, c)
				if i.Status() != result || c.starts["task"] != 1 { t.Fatal("无候选时未返回真实终态", i.Status(), c.starts) }
			})
		}
		t.Run(fmt.Sprintf("%d/initial", budget), func(t *testing.T) {
			i, c := newReselectTest(t, "terminal", budget)
			i.Start(); drainReselect(t, c)
			if i.Status() != bt.Failure || c.starts["task"] != 0 { t.Fatal("初始无候选应失败", i.Status(), c.starts) }
		})
		for _, latest := range []bt.Status{bt.Success, bt.Failure} {
			t.Run(fmt.Sprintf("%d/latest-%s", budget, latest), func(t *testing.T) {
				i, c := newReselectTest(t, "chain", budget)
				c.enabled["first"] = true
				first := bt.Failure
				if latest == bt.Failure { first = bt.Success }
				c.startResult["first"], c.startResult["second"] = first, latest
				i.Start(); drainReselect(t, c)
				if i.Status() != latest || c.starts["first"] != 1 || c.starts["second"] != 1 {
					t.Fatal("未返回最近完成候选结果", i.Status(), c.starts)
				}
			})
		}
	}
}

// TestParallelMotionRemainsSelectable 验证移动完成转停留后 Parallel 不锁存运动分支成功。
func TestParallelMotionRemainsSelectable(t *testing.T) {
	for _, budget := range []int{1, 1024} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			i, c := newReselectTest(t, "parallel", budget)
			c.enabled["move"] = true
			c.onFinish["move"] = func() { c.enabled["move"] = false; i.Notify(EventCommand) }
			i.Start(); drainReselect(t, c)
			combat := c.tokens["combat"]
			if !i.Complete(c.tokens["move"], bt.Success) { t.Fatal("有效完成被拒绝") }
			drainReselect(t, c)
			if i.Status() != bt.Running || c.frame.State(c.nodes["motion"]).Status != bt.Running || c.starts["stay"] != 1 {
				t.Fatal("运动完成被并行节点锁存", i.Status(), c.starts)
			}
			c.enabled["move"] = true; i.Notify(EventCommand); drainReselect(t, c)
			if c.starts["move"] != 2 || c.starts["combat"] != 1 || c.aborts["combat"] != 0 || c.tokens["combat"] != combat {
				t.Fatal("后续移动未进入或干扰并行分支", c.starts, c.aborts)
			}
		})
	}
}

// TestRearmDuringBudgetReselection 验证 Budget=1 重选途中替换指令及取消旧令牌不丢并行引用。
func TestRearmDuringBudgetReselection(t *testing.T) {
	i, c := newReselectTest(t, "parallel", 1)
	c.enabled["move"] = true
	c.onFinish["move"] = func() { c.enabled["move"] = false }
	i.Start(); drainReselect(t, c)
	old, combat := c.tokens["move"], c.tokens["combat"]
	if !i.Complete(old, bt.Success) { t.Fatal("有效完成被拒绝") }
	if len(c.queue) == 0 || c.frame.State(c.nodes["moveWork"]).Status != bt.Success {
		t.Fatal("未在完成后的条件刷新预算边界让出", len(c.queue))
	}
	c.enabled["move"] = true
	btRearmPriorityCandidate(c.frame, c.nodes["moveBranch"], "new command during reselection")
	i.Notify(EventCommand)
	drainReselect(t, c)
	if i.Status() != bt.Running || c.starts["move"] != 2 || c.starts["combat"] != 1 || c.aborts["combat"] != 0 || c.tokens["combat"] != combat {
		t.Fatal("预算重选中新指令丢失或并行运行引用被破坏", i.Status(), c.starts, c.aborts)
	}
	if i.Complete(old, bt.Success) { t.Fatal("旧指令迟到令牌仍有效") }
	old = c.tokens["move"]
	btRearmPriorityCandidate(c.frame, c.nodes["moveBranch"], "replace running command")
	i.Notify(EventCommand)
	drainReselect(t, c)
	if c.starts["move"] != 3 || c.aborts["move"] != 1 || c.aborts["combat"] != 0 || c.tokens["combat"] != combat || i.Complete(old, bt.Success) {
		t.Fatal("运行中指令重置未正确取消或破坏无关分支", c.starts, c.aborts)
	}
	if len(c.log) < 2 || c.log[len(c.log)-2] != "abort:move" || c.log[len(c.log)-1] != "start:move" {
		t.Fatal("新旧指令动作执行交叠", c.log)
	}
}

// TestGuardEventDuringBudgetReselection 验证已越过高优先条件后收到新事件会重新检查，不先启动停留。
func TestGuardEventDuringBudgetReselection(t *testing.T) {
	i, c := newReselectTest(t, "work", 1)
	c.enabled["task"] = true
	c.onFinish["task"] = func() { c.enabled["task"] = false }
	i.Start(); drainReselect(t, c)
	checks := c.checks["high"]
	if !i.Complete(c.tokens["task"], bt.Success) { t.Fatal("有效完成被拒绝") }
	// 只执行到 high 已重新拒绝、下一候选尚等待预算，确保事件发生在扫描中途。
	for n := 0; c.checks["high"] == checks; n++ {
		if n >= 20 || len(c.queue) == 0 { t.Fatal("未到达高优先条件已检查的预算边界", c.checks) }
		fn := c.queue[0]
		c.queue = c.queue[1:]
		fn()
	}
	if len(c.queue) == 0 || c.starts["idle"] != 0 { t.Fatal("扫描中途意外结束或已启动停留", c.starts) }
	c.enabled["high"] = true
	i.Notify(EventCommand)
	drainReselect(t, c)
	if i.Status() != bt.Running || c.starts["high"] != 1 || c.starts["task"] != 1 || c.starts["idle"] != 0 {
		t.Fatal("预算重选中高优先条件事件丢失或启动低优先副作用", i.Status(), c.starts, c.checks)
	}
}
`
