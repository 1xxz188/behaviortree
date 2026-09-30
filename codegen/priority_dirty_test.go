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

// TestGeneratedPriorityRejectedDirty 验证条件拒绝候选后不妨碍异步续跑，且新通知仍能重新选路。
func TestGeneratedPriorityRejectedDirty(t *testing.T) {
	p := model.Project{
		SchemaVersion: model.SchemaVersion, Name: "拒绝候选调度回归", NextEventID: "3",
		Generation: model.Generation{PackagePath: "generated", ContextType: "*dirtyContext"},
		Events:     []model.EventDefinition{{ID: "1", Name: "指令变化", CodeName: "Command"}, {ID: "2", Name: "待机续跑", CodeName: "Continue"}},
		Catalog: []model.Definition{
			{ID: "guard", Name: "指令条件", Kind: model.DefinitionCondition, GoName: "CommandGuard", Params: []model.Parameter{{Name: "Command", Type: bt.StringType}}, EventIDs: []string{"1"}},
			{ID: "hold", Name: "指令动作", Kind: model.DefinitionAction, GoName: "CommandHold", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}, EventIDs: []string{"1"}},
			{ID: "idle", Name: "待机动作", Kind: model.DefinitionAction, GoName: "IdleHold", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}, EventIDs: []string{"2"}},
		},
	}
	for _, width := range []int{2, 10, 1000} {
		p.Trees = append(p.Trees, rejectedPriorityTree(width))
	}
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
	gomod := "module bt.priority.dirty.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(moduleRoot)) + "\n"
	for name, content := range map[string]string{"go.mod": gomod, "dirty_test.go": priorityDirtyFixture} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-count=1", "-v", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("拒绝候选生成代码回归失败: %v\n%s", err, output)
	}
	t.Log(string(output))
}

// rejectedPriorityTree 构造共享指令事件的候选，以及持续 Running 的并行后备分支。
func rejectedPriorityTree(width int) model.Tree {
	value := func(text string) model.Value { raw, _ := json.Marshal(text); return model.Value{Value: raw} }
	tree := model.Tree{ID: fmt.Sprintf("dirty%d", width), Name: "拒绝候选", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodePriority}}}
	for n := 0; n < width; n++ {
		label := fmt.Sprintf("unused%d", n)
		if n == 0 {
			label = "gather"
		} else if n == 1 {
			label = "pick"
		}
		branch, guard, work := label+"Branch", label+"Guard", label+"Work"
		tree.Nodes[0].Children = append(tree.Nodes[0].Children, branch)
		tree.Nodes = append(tree.Nodes,
			model.Node{ID: branch, Type: model.NodeSequence, Children: []string{guard, work}},
			model.Node{ID: guard, Type: model.NodeCondition, Binding: "guard", Params: map[string]model.Value{"Command": value(label)}},
			model.Node{ID: work, Type: model.NodeAction, Binding: "hold", Params: map[string]model.Value{"Label": value(label)}},
		)
	}
	tree.Nodes[0].Children = append(tree.Nodes[0].Children, "normal")
	tree.Nodes = append(tree.Nodes,
		model.Node{ID: "normal", Type: model.NodeParallel, Children: []string{"move", "standby"}},
		model.Node{ID: "move", Type: model.NodeAction, Binding: "hold", Params: map[string]model.Value{"Label": value("move")}},
		model.Node{ID: "standby", Type: model.NodeAction, Binding: "idle", Params: map[string]model.Value{"Label": value("standby")}},
	)
	return tree
}

// priorityDirtyFixture 在临时包运行真实生成代码，回放指令抢占、完成回调及低预算调度。
const priorityDirtyFixture = `package generated

import (
	"fmt"
	"reflect"
	"testing"
	bt "github.com/1xxz188/behaviortree"
)

// dirtyContext 保存宿主指令、动作令牌、节点追踪与串行调度队列。
type dirtyContext struct {
	command string // command 是宿主先更新再通知的权威指令。
	checks int // checks 统计实际业务条件求值次数。
	starts map[string]int // starts 统计每个动作的启动次数。
	tokens map[string]bt.CallbackToken // tokens 保存最新异步操作令牌。
	log []string // log 验证旧动作取消先于新动作启动。
	trace []bt.LogRecord // trace 检测无关完成是否产生额外条件日志。
	queue []func() // queue 保存预算让出后的宿主续跑。
	frame *bt.Frame[*dirtyContext] // frame 仅用于检查生成代码消费后的队列和失效状态。
	root int // root 是当前入口的根节点槽位。
}

// CommandGuard 判断指令，并统计条件的真实执行次数。
func CommandGuard(f *bt.Frame[*dirtyContext], _ int, p CommandGuardParams) bool {
	f.Context.frame = f
	f.Context.checks++
	return f.Context.command == p.Command
}

// runDirtyHold 模拟非阻塞异步动作，完成结果只在 Resume 时消费。
func runDirtyHold(f *bt.Frame[*dirtyContext], node int, phase bt.Phase, label string) bt.Status {
	c := f.Context
	switch phase {
	case bt.Start:
		c.starts[label]++
		c.tokens[label] = f.Token(node)
		c.log = append(c.log, "start:"+label)
	case bt.Resume:
		if result := f.Consume(node); result != bt.Invalid { return result }
	case bt.Abort:
		c.log = append(c.log, "abort:"+label)
	}
	return bt.Running
}

// CommandHold 订阅与守卫相同的指令事件，复现未进入分支的动作失效标记。
func CommandHold(f *bt.Frame[*dirtyContext], node int, phase bt.Phase, p CommandHoldParams) bt.Status {
	return runDirtyHold(f, node, phase, p.Label)
}

// IdleHold 仅订阅待机续跑，验证无关事件不扫描已拒绝的高优先候选。
func IdleHold(f *bt.Frame[*dirtyContext], node int, phase bt.Phase, p IdleHoldParams) bt.Status {
	return runDirtyHold(f, node, phase, p.Label)
}

// newDirtyTest 创建串行宿主并开启节点追踪；不用真实时钟或后台协程。
func newDirtyTest(t *testing.T, width, budget int) (*bt.Instance[*dirtyContext], *dirtyContext) {
	t.Helper()
	c := &dirtyContext{command: "move", starts: map[string]int{}, tokens: map[string]bt.CallbackToken{}}
	program := NewProgram("dirty")
	tree := fmt.Sprintf("dirty%d", width)
	c.root = program.Roots[tree]
	i, err := bt.NewInstance(program, "one", tree, c, bt.Options{
		Budget: budget, Post: func(fn func()) { c.queue = append(c.queue, fn) },
		Trace: true, Logger: func(log bt.LogRecord) { c.trace = append(c.trace, log) },
	})
	if err != nil { t.Fatal(err) }
	t.Cleanup(i.Close)
	return i, c
}

// drainDirty 完成所有预算续跑，防止丢失通知或自旋被掩盖。
func drainDirty(t *testing.T, c *dirtyContext) {
	t.Helper()
	for n := 0; len(c.queue) > 0; n++ {
		if n > 10000 { t.Fatal("预算续跑没有收敛") }
		queue := c.queue
		c.queue = nil
		for _, fn := range queue { fn() }
	}
}

// TestRejectedCompletion 回放 gather→move 后的完成通知，验证缓存、日志、取消和再次抢占。
func TestRejectedCompletion(t *testing.T) {
	for _, budget := range []int{1, 2, 1024} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			i, c := newDirtyTest(t, 2, budget)
			i.Start(); drainDirty(t, c)
			oldMove := c.tokens["move"]
			c.command = "gather"; i.Notify(EventCommand); drainDirty(t, c)
			gather := c.tokens["gather"]
			c.command = "move"; i.Notify(EventCommand); drainDirty(t, c)
			if i.Status() != bt.Running || i.Complete(gather, bt.Success) || i.Complete(oldMove, bt.Success) {
				t.Fatal("抢占后的旧令牌仍有效", i.Status())
			}
			checks, steps, trace := c.checks, i.Steps(), len(c.trace)
			if !i.Complete(c.tokens["move"], bt.Success) { t.Fatal("有效完成被拒绝") }
			drainDirty(t, c)
			if c.checks != checks { t.Fatalf("无关完成重新执行条件: before=%d after=%d", checks, c.checks) }
			for _, log := range c.trace[trace:] {
				if log.Kind == bt.LogNode && log.NodeID == "gatherGuard" { t.Fatal("无关完成产生额外采集条件日志", log) }
			}
			if budget == 1024 && i.Steps()-steps != 3 { t.Fatal("完成未只推进根、并行分支和动作", i.Steps()-steps) }
			if i.Status() != bt.Running || c.starts["move"] != 2 || c.starts["standby"] != 2 || c.starts["pick"] != 0 {
				t.Fatal("完成改变未唤醒动作的生命周期", i.Status(), c.starts)
			}
			c.command = "pick"; i.Notify(EventCommand); drainDirty(t, c)
			if c.starts["pick"] != 1 || c.checks <= checks { t.Fatal("后续事件未重新唤醒拒绝分支", c.starts, c.checks) }
			want := []string{"start:move", "start:standby", "abort:standby", "abort:move", "start:gather", "abort:gather", "start:move", "start:standby", "abort:standby", "start:pick"}
			if !reflect.DeepEqual(c.log, want) { t.Fatal("动作取消与启动顺序错误", c.log) }
		})
	}
}

// TestRejectedWideFastPath 验证十个和千个候选被同一事件拒绝后，单分支续跑仍为常量工作且零分配。
func TestRejectedWideFastPath(t *testing.T) {
	for _, width := range []int{10, 1000} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			i, c := newDirtyTest(t, width, 4096)
			i.Start(); i.Notify(EventCommand); drainDirty(t, c)
			if child := c.frame.PopDirtyChild(c.root); child >= 0 { t.Fatal("已拒绝候选仍留在根队列", child) }
			i.SetTrace(false)
			checks, steps := c.checks, i.Steps()
			for n := 0; n < 100; n++ { i.Notify(EventContinue) }
			if c.checks != checks || i.Steps()-steps != 300 { t.Fatal("拒绝候选阻碍常量时间续跑", c.checks-checks, i.Steps()-steps) }
			if allocs := testing.AllocsPerRun(100, func() { i.Notify(EventContinue) }); allocs != 0 { t.Fatal("稳态续跑出现分配", allocs) }
		})
	}
}
`
