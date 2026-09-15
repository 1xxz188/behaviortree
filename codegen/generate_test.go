package codegen

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/model"
)

// TestStableNativeSource 验证画布与集合排列不改变生成控制流，且映射定位实际 Go 函数。
func TestStableNativeSource(t *testing.T) {
	p := model.Example()
	a, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Trees[0].Layout["root"] = model.Position{X: 999, Y: 999}
	p.Trees[0].Nodes[0], p.Trees[0].Nodes[1] = p.Trees[0].Nodes[1], p.Trees[0].Nodes[0]
	b, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Files, b.Files) || a.Version != b.Version {
		t.Fatal("布局或节点集合排序改变了生成结果")
	}
	for _, loc := range a.SourceMap {
		lines := strings.Split(string(generatedSource(t, a, loc.File)), "\n")
		if loc.FunctionName == "" || !strings.Contains(lines[loc.Line-1], "func "+loc.FunctionName+"(") {
			t.Fatalf("错误源码映射: %+v", loc)
		}
	}
	if !strings.Contains(string(allGeneratedSource(a)), "switch s.Cursor") || strings.Contains(string(allGeneratedSource(a)), "json.Unmarshal") {
		t.Fatal("未生成实际原生控制流")
	}
}

// TestExpansionLimit 验证指数子树引用在实际展开前被拒绝。
func TestExpansionLimit(t *testing.T) {
	p := model.Example()
	p.Trees = []model.Tree{{ID: "t0", Name: "leaf", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeWait}}}}
	for i := 1; i < 22; i++ {
		prior := fmt.Sprintf("t%d", i-1)
		p.Trees = append(p.Trees, model.Tree{ID: fmt.Sprintf("t%d", i), Name: "double", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodeParallel, Children: []string{"a", "b"}}, {ID: "a", Type: model.NodeSubtree, Tree: prior}, {ID: "b", Type: model.NodeSubtree, Tree: prior}}})
	}
	if _, err := Generate(p); err == nil || !strings.Contains(err.Error(), "展开超过") {
		t.Fatalf("应在展开前拒绝指数增长: %v", err)
	}
}

// TestWideParallelSource 验证宽 Parallel 生成量线性增长并使用受影响子队列。
func TestWideParallelSource(t *testing.T) {
	build := func(count int) Result {
		p := model.Example()
		root := model.Node{ID: "root", Type: model.NodeParallel}
		nodes := []model.Node{root}
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("child%d", i)
			nodes[0].Children = append(nodes[0].Children, id)
			nodes = append(nodes, model.Node{ID: id, Type: model.NodeWait, DurationMS: 1})
		}
		p.Trees[0].Nodes = nodes
		result, err := Generate(p)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	small, large := build(100), build(200)
	if len(allGeneratedSource(large)) > len(allGeneratedSource(small))*23/10 {
		t.Fatal("宽节点代码生成量不是线性的")
	}
	if !strings.Contains(string(allGeneratedSource(large)), "PopDirtyChild") {
		t.Fatal("Parallel 未使用受影响分支队列")
	}
}

// TestGeneratedRuntime 编译真实生成代码，并运行组合语义、异步取消和黑板事件用例。
func TestGeneratedRuntime(t *testing.T) {
	p := fixtureProject()
	// 明确覆盖全集，防止新增节点或值类型时真实编译样例漏掉分支。
	nodes := map[model.NodeType]bool{}
	for _, tree := range p.Trees {
		for _, node := range tree.Nodes {
			nodes[node.Type] = true
		}
	}
	for typ := model.NodeSequence; typ <= model.NodeSubtree; typ++ {
		if !nodes[typ] {
			t.Fatalf("真实生成编译缺少节点类型 %v", typ)
		}
	}
	values := map[bt.ValueType]bool{}
	for _, field := range p.Blackboard {
		values[field.Type] = true
	}
	for _, typ := range []bt.ValueType{bt.BoolType, bt.IntType, bt.UIntType, bt.FloatType, bt.StringType, bt.EnumType, bt.EntityIDType, bt.DurationType} {
		if !values[typ] {
			t.Fatalf("真实生成编译缺少值类型 %v", typ)
		}
	}
	result, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	// 结构检查补足 Steps 计数：缓存探测不增加 Steps，快速路径也不能逐个调用 btNode。
	for _, location := range result.SourceMap {
		if location.TreeID == "priority1000" && location.NodeID == "root" {
			source := string(generatedSource(t, result, location.File))
			start := strings.Index(source, "func "+location.FunctionName+"(")
			if start < 0 {
				t.Fatal("找不到优先级节点函数")
			}
			reselection := strings.Index(source[start:], "previousGuard0 :=")
			if start < 0 || reselection < 0 {
				t.Fatal("找不到优先级控制流")
			}
			fastPath := source[start : start+reselection]
			if !strings.Contains(fastPath, "f.OnlyDirtyChild(") || !strings.Contains(fastPath, "btStep(f, s.Cursor)") || strings.Count(fastPath, "btNode") != 1 || strings.Contains(fastPath, "for ") {
				t.Fatal("优先级快速路径仍逐个探测静态候选")
			}
		}
	}
	dir := t.TempDir()
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gomod := "module bt.generated.test\n\ngo 1.26.7\n\nrequire github.com/1xxz188/behaviortree v0.0.0\nreplace github.com/1xxz188/behaviortree => " + strconv.Quote(filepath.ToSlash(moduleRoot)) + "\n"
	writeGeneratedFiles(t, dir, result)
	for name, content := range map[string][]byte{"go.mod": []byte(gomod), "nodes_test.go": []byte(runtimeFixture)} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-count=1", "-bench", "BenchmarkGeneratedPriorityFastPath", "-benchtime", "50ms", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("生成代码集成测试失败: %v\n%s", err, output)
	}
	t.Log(string(output))
}

// fixtureProject 覆盖首版全部节点种类以及常量、字段两种强类型绑定。
func fixtureProject() model.Project {
	value := func(v string) model.Value { raw, _ := json.Marshal(v); return model.Value{Value: raw} }
	action := func(id, binding, label string) model.Node {
		return model.Node{ID: id, Type: model.NodeAction, Binding: binding, Params: map[string]model.Value{"Label": value(label)}}
	}
	wrap := func(id string, typ model.NodeType, children ...string) model.Node {
		return model.Node{ID: id, Type: typ, Children: children}
	}
	tree := func(id string, nodes ...model.Node) model.Tree {
		return model.Tree{ID: id, Name: id, Root: nodes[0].ID, Nodes: nodes}
	}
	p := model.Project{SchemaVersion: 1, Name: "generated runtime tests", Generation: model.Generation{Package: "generated", ContextType: "*testContext"}, Blackboard: []model.Field{{ID: "enabled", Name: "Enabled", Type: bt.BoolType}}, Catalog: []model.Definition{{ID: "ok", Name: "成功", Kind: model.DefinitionAction, GoName: "Pass", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}}, {ID: "fail", Name: "失败", Kind: model.DefinitionAction, GoName: "Fail", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}}, {ID: "hold", Name: "异步", Kind: model.DefinitionAction, GoName: "Hold", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}, Events: []string{"resume"}}, {ID: "flaky", Name: "重试", Kind: model.DefinitionAction, GoName: "Flaky", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}}, {ID: "gate", Name: "条件", Kind: model.DefinitionCondition, GoName: "Gate", Params: []model.Parameter{{Name: "Flag", Type: bt.BoolType}}}}}
	p.Catalog = append(p.Catalog, model.Definition{ID: "enumWrite", Name: "枚举写入", Kind: model.DefinitionAction, GoName: "WriteEnum", Params: []model.Parameter{{Name: "Label", Type: bt.StringType}}})
	p.Trees = []model.Tree{
		tree("enumValid", action("root", "enumWrite", "move")),
		tree("enumInvalid", action("root", "enumWrite", "missing")),
		tree("sequence", wrap("root", model.NodeSequence, "a", "b"), action("a", "ok", "a"), action("b", "hold", "b")),
		tree("selector", wrap("root", model.NodeSelector, "a", "b"), action("a", "fail", "a"), action("b", "ok", "b")),
		tree("parallel", wrap("root", model.NodeParallel, "a", "b"), action("a", "hold", "a"), action("b", "fail", "b")),
		tree("priority", wrap("root", model.NodePriority, "high", "low"), wrap("high", model.NodeSequence, "guard", "work"), model.Node{ID: "guard", Type: model.NodeCondition, Binding: "gate", Params: map[string]model.Value{"Flag": {Field: "enabled"}}}, action("work", "hold", "high"), action("low", "hold", "low")),
		tree("priorityFailure", wrap("root", model.NodePriority, "high", "low"), wrap("high", model.NodeSequence, "guard", "work"), model.Node{ID: "guard", Type: model.NodeCondition, Binding: "gate", Params: map[string]model.Value{"Flag": {Field: "enabled"}}}, action("work", "fail", "high"), action("low", "hold", "low")),
		tree("repeat", model.Node{ID: "root", Type: model.NodeRepeat, Count: 3, Children: []string{"a"}}, action("a", "ok", "a")),
		tree("retry", model.Node{ID: "root", Type: model.NodeRetry, Count: 3, Children: []string{"a"}}, action("a", "flaky", "a")),
		tree("repeatFail", model.Node{ID: "root", Type: model.NodeRepeat, Count: 3, Children: []string{"a"}}, action("a", "fail", "a")),
		tree("retryPass", model.Node{ID: "root", Type: model.NodeRetry, Count: 3, Children: []string{"a"}}, action("a", "ok", "a")),
		tree("retryFail", model.Node{ID: "root", Type: model.NodeRetry, Count: 2, Children: []string{"a"}}, action("a", "fail", "a")),
		tree("repeatHold", model.Node{ID: "root", Type: model.NodeRepeat, Count: 2, Children: []string{"a"}}, action("a", "hold", "a")),
		tree("wait", model.Node{ID: "root", Type: model.NodeWait, DurationMS: 10}),
		tree("timeout", model.Node{ID: "root", Type: model.NodeTimeout, DurationMS: 10, Children: []string{"a"}}, action("a", "hold", "a")),
		tree("inverter", wrap("root", model.NodeInverter, "a"), action("a", "ok", "a")),
		tree("succeed", wrap("root", model.NodeSucceed, "a"), action("a", "fail", "a")),
		tree("fail", wrap("root", model.NodeFail, "a"), action("a", "ok", "a")),
		tree("shared", action("root", "hold", "shared")),
		tree("subtree", wrap("root", model.NodeParallel, "a", "b"), model.Node{ID: "a", Type: model.NodeSubtree, Tree: "shared"}, model.Node{ID: "b", Type: model.NodeSubtree, Tree: "shared"}),
	}
	deep := model.Tree{ID: "deep", Name: "deep"}
	for n := 0; n < 64; n++ {
		deep.Nodes = append(deep.Nodes, wrap(fmt.Sprintf("n%d", n), model.NodeSequence, fmt.Sprintf("n%d", n+1)))
	}
	deep.Root = "n0"
	deep.Nodes = append(deep.Nodes, action("n64", "ok", "a"))
	p.Trees = append(p.Trees, deep)
	for _, width := range []int{10, 1000} {
		wide := model.Tree{ID: fmt.Sprintf("priority%d", width), Name: "宽优先级树", Root: "root", Nodes: []model.Node{{ID: "root", Type: model.NodePriority}}}
		for n := 0; n < width; n++ {
			branch, guard, work := fmt.Sprintf("branch%d", n), fmt.Sprintf("guard%d", n), fmt.Sprintf("work%d", n)
			wide.Nodes[0].Children = append(wide.Nodes[0].Children, branch)
			wide.Nodes = append(wide.Nodes, wrap(branch, model.NodeSequence, guard, work), model.Node{ID: guard, Type: model.NodeCondition, Binding: "gate", Params: map[string]model.Value{"Flag": {Value: json.RawMessage("false")}}}, action(work, "ok", "unused"))
		}
		wide.Nodes[0].Children = append(wide.Nodes[0].Children, "low")
		wide.Nodes = append(wide.Nodes, action("low", "hold", "low"))
		p.Trees = append(p.Trees, wide)
	}
	for _, f := range []model.Field{{ID: "integer", Name: "Integer", Type: bt.IntType}, {ID: "unsigned", Name: "Unsigned", Type: bt.UIntType, Default: json.RawMessage("18446744073709551615")}, {ID: "floating", Name: "Floating", Type: bt.FloatType}, {ID: "text", Name: "Text", Type: bt.StringType}, {ID: "enum", Name: "Mode", Type: bt.EnumType, Enum: []string{"idle", "move"}}, {ID: "entity", Name: "Target", Type: bt.EntityIDType}, {ID: "duration", Name: "Delay", Type: bt.DurationType}} {
		p.Blackboard = append(p.Blackboard, f)
	}
	return p
}

// runtimeFixture 是临时生成包中的手写业务与验收代码，避免提交任何生成产物。
const runtimeFixture = `package generated
import("testing";"time";"strings";"fmt";bt "github.com/1xxz188/behaviortree")
// testContext 保存测试宿主拥有的状态。
type testContext struct{counts map[string]int;tokens map[string][]bt.CallbackToken;log []string;queue []func();timers []*timer;conditions int}
// timer 模拟可取消的一次性宿主计时器。
type timer struct{fire func();cancelled bool}
// Pass 同步成功并记录启动次数。
func Pass(f *bt.Frame[*testContext],node int,phase bt.Phase,p PassParams)bt.Status{if phase==bt.Start{f.Context.counts[p.Label]++};return bt.Success}
// Fail 同步失败。
func Fail(f *bt.Frame[*testContext],node int,phase bt.Phase,p FailParams)bt.Status{if phase==bt.Start{f.Context.counts[p.Label]++};return bt.Failure}
// Flaky 前两次失败，第三次成功。
func Flaky(f *bt.Frame[*testContext],node int,phase bt.Phase,p FlakyParams)bt.Status{f.Context.counts[p.Label]++;if f.Context.counts[p.Label]<3{return bt.Failure};return bt.Success}
// Hold 启动异步动作，通过有效完成结果结束，取消时记录顺序。
func Hold(f *bt.Frame[*testContext],node int,phase bt.Phase,p HoldParams)bt.Status{switch phase{case bt.Start:f.Context.counts[p.Label]++;f.Context.tokens[p.Label]=append(f.Context.tokens[p.Label],f.Token(node));f.Context.log=append(f.Context.log,"start:"+p.Label);case bt.Resume:if result:=f.Consume(node);result==bt.Success||result==bt.Failure{return result};case bt.Abort:f.Context.log=append(f.Context.log,"abort:"+p.Label)};return bt.Running}
// Gate 验证依赖字段的条件缓存。
func Gate(f *bt.Frame[*testContext],node int,p GateParams)bool{f.Context.conditions++;return p.Flag}
// WriteEnum 验证生成的枚举写入器在业务执行期间拒绝声明之外的字符串。
func WriteEnum(f *bt.Frame[*testContext],node int,phase bt.Phase,p WriteEnumParams)bt.Status{if phase==bt.Abort{f.Context.counts["abort"]++;return bt.Success};SetMode(f,p.Label);return bt.Success}
// newTest 创建串行宿主；低预算通过同一个队列续跑。
func newTest(t *testing.T,tree string,budget int)(*bt.Instance[*testContext],*testContext){t.Helper();c:=&testContext{counts:map[string]int{},tokens:map[string][]bt.CallbackToken{}};i,err:=bt.NewInstance(NewProgram("test"),tree,tree,c,bt.Options{Budget:budget,Post:func(fn func()){c.queue=append(c.queue,fn)},After:func(d time.Duration,fn func())bt.CancelFunc{tm:=&timer{fire:fn};c.timers=append(c.timers,tm);return func(){tm.cancelled=true}}});if err!=nil{t.Fatal(err)};t.Cleanup(i.Close);return i,c}
// drain 执行预算或事件投递，防止死循环悄悄通过测试。
func drain(t *testing.T,c *testContext){t.Helper();for count:=0;len(c.queue)>0;count++{if count>1000{t.Fatal("队列没有收敛")};q:=c.queue;c.queue=nil;for _,f:=range q{f()}}}
// TestGeneratedEnumSetter 验证有效值可写入，非法值保持原值并让当前轮失败。
func TestGeneratedEnumSetter(t *testing.T){slot:=-1;for n,f:=range NewProgram("").Fields{if f.ID=="enum"{slot=n}};valid,_:=newTest(t,"enumValid",1024);if valid.Start()!=bt.Success||valid.Blackboard().Enum(slot)!="move"{t.Fatal("有效枚举值写入失败")};invalid,c:=newTest(t,"enumInvalid",1024);if invalid.Start()!=bt.Failure||invalid.Blackboard().Enum(slot)!="idle"{t.Fatal("非法枚举值污染了黑板",invalid.Status(),invalid.Blackboard().Enum(slot))};if invalid.Error()==nil||!strings.Contains(invalid.Error().Error(),"invalid enum value for field enum")||c.counts["abort"]!=1{t.Fatal("枚举错误未正确结束本轮",invalid.Error(),c.counts)}}
// TestGeneratedCompositeSemantics 验证组合节点的终态、续跑和有限循环。
func TestGeneratedCompositeSemantics(t *testing.T){for _,tc:=range []struct{tree string;want bt.Status;count int}{{"selector",bt.Success,1},{"parallel",bt.Failure,1},{"repeat",bt.Success,3},{"retry",bt.Success,3},{"repeatFail",bt.Failure,1},{"retryPass",bt.Success,1},{"retryFail",bt.Failure,2},{"deep",bt.Success,1},{"inverter",bt.Failure,1},{"succeed",bt.Success,1},{"fail",bt.Failure,1}}{t.Run(tc.tree,func(t *testing.T){i,c:=newTest(t,tc.tree,1);i.Start();drain(t,c);if i.Status()!=tc.want||c.counts["a"]!=tc.count{t.Fatalf("status=%v count=%d err=%v",i.Status(),c.counts["a"],i.Error())};if tc.tree=="parallel"&&(len(c.log)!=2||c.log[1]!="abort:a"){t.Fatal(c.log)}})}}
// TestGeneratedRunning 验证 Running 续跑不会重复前置副作用，旧和重复完成被拒绝。
func TestGeneratedRunning(t *testing.T){i,c:=newTest(t,"sequence",1);i.Start();drain(t,c);if i.Status()!=bt.Running||c.counts["a"]!=1||c.counts["b"]!=1{t.Fatal(i.Status(),c.counts)};steps:=i.Steps();i.Tick();if steps!=i.Steps(){t.Fatal("无通知不应执行节点")};token:=c.tokens["b"][0];if !i.Complete(token,bt.Success){t.Fatal("有效完成被拒绝")};drain(t,c);if i.Status()!=bt.Success||i.Complete(token,bt.Success){t.Fatal("重复完成未过滤")};i.Start();drain(t,c);if c.counts["a"]!=2||i.Complete(token,bt.Success){t.Fatal("新轮重复执行或旧令牌有效")}}
// TestGeneratedPriority 验证高优先条件变化时先 Abort 再 Start，并缓存未变化条件。
func TestGeneratedPriority(t *testing.T){i,c:=newTest(t,"priority",1024);i.Start();if len(c.log)!=1||c.log[0]!="start:low"{t.Fatal(c.log)};checked:=c.conditions;i.Notify("resume");if c.conditions!=checked{t.Fatal("无关事件重复求值条件")};slot:=-1;for n,f:=range NewProgram("").Fields{if f.ID=="enabled"{slot=n}};i.Blackboard().SetBool(slot,true);drain(t,c);if len(c.log)!=3||c.log[1]!="abort:low"||c.log[2]!="start:high"{t.Fatal(c.log)};if i.Complete(c.tokens["low"][0],bt.Success){t.Fatal("取消分支仍可完成")};i.Blackboard().SetBool(slot,false);drain(t,c);if c.log[len(c.log)-2]!="abort:high"||c.log[len(c.log)-1]!="start:low"{t.Fatal(c.log)}}
// TestGeneratedFailedPriorityRearm 验证失败候选在守卫假后恢复为真时重启，保持真时不因无关通知重试。
func TestGeneratedFailedPriorityRearm(t *testing.T){i,c:=newTest(t,"priorityFailure",1024);slot:=-1;for n,f:=range NewProgram("").Fields{if f.ID=="enabled"{slot=n}};i.Blackboard().SetBool(slot,true);i.Start();if c.counts["high"]!=1||i.Status()!=bt.Running{t.Fatal(c.counts,i.Status())};for n:=0;n<3;n++{i.Notify("resume")};if c.counts["high"]!=1{t.Fatal("无关事件重试了失败动作",c.counts)};i.Blackboard().SetBool(slot,false);drain(t,c);i.Notify("resume");if c.counts["high"]!=1{t.Fatal("守卫为假时重试动作")};i.Blackboard().SetBool(slot,true);drain(t,c);if c.counts["high"]!=2||i.Status()!=bt.Running{t.Fatal("守卫恢复后未重新启动失败分支",c.counts,i.Status())};i.Notify("resume");if c.counts["high"]!=2{t.Fatal("失败重试没有重新停止")}}
// TestGeneratedPriorityFastPath 验证一千个高优先守卫不影响低分支事件的执行数量。
func TestGeneratedPriorityFastPath(t *testing.T){i,c:=newTest(t,"priority1000",4096);if i.Start()!=bt.Running||c.conditions!=1000{t.Fatal(i.Status(),c.conditions)};before:=i.Steps();for n:=0;n<100;n++{i.Notify("resume")};if c.conditions!=1000||i.Steps()-before!=200{t.Fatal("低分支事件执行了额外节点",c.conditions,i.Steps()-before)}}
// BenchmarkGeneratedPriorityFastPath 比较十个与一千个守卫的同一低分支续跑成本。
func BenchmarkGeneratedPriorityFastPath(b *testing.B){for _,width:=range []int{10,1000}{b.Run(fmt.Sprint(width),func(b *testing.B){c:=&testContext{counts:map[string]int{},tokens:map[string][]bt.CallbackToken{}};i,err:=bt.NewInstance(NewProgram("benchmark"),"one",fmt.Sprintf("priority%d",width),c,bt.Options{Budget:4096,Post:func(fn func()){c.queue=append(c.queue,fn)}});if err!=nil{b.Fatal(err)};i.Start();b.Cleanup(i.Close);b.ReportAllocs();b.ResetTimer();for n:=0;n<b.N;n++{i.Notify("resume")}})}}
// TestGeneratedTimers 验证 Wait 和 Timeout 仅使用一次性计时及取消语义。
func TestGeneratedTimers(t *testing.T){for _,tree:=range []string{"wait","timeout"}{t.Run(tree,func(t *testing.T){i,c:=newTest(t,tree,1024);if i.Start()!=bt.Running||len(c.timers)!=1{t.Fatal(i.Status(),len(c.timers))};c.timers[0].fire();drain(t,c);want:=bt.Success;if tree=="timeout"{want=bt.Failure;if c.log[len(c.log)-1]!="abort:a"{t.Fatal(c.log)}};if i.Status()!=want{t.Fatal(i.Status())};c.timers[0].fire();drain(t,c);if i.Status()!=want{t.Fatal("重复计时回调改变终态")}})}}
// TestGeneratedTimeoutOrder 验证计时与动作完成在同一宿主队列按先后顺序决定结果。
func TestGeneratedTimeoutOrder(t *testing.T){for _,first:=range []string{"completion","timeout"}{t.Run(first,func(t *testing.T){i,c:=newTest(t,"timeout",1024);i.Start();token:=c.tokens["a"][0];accepted:=false;complete:=func(){accepted=i.Complete(token,bt.Success)};if first=="completion"{c.queue=append(c.queue,complete);c.timers[0].fire()}else{c.timers[0].fire();c.queue=append(c.queue,complete)};drain(t,c);want:=bt.Failure;if first=="completion"{want=bt.Success;if !accepted||!c.timers[0].cancelled||len(c.log)!=1{t.Fatal("正常完成未取消超时",accepted,c.log)}}else if accepted||len(c.log)!=2||c.log[1]!="abort:a"{t.Fatal("超时没有取消动作",accepted,c.log)};if i.Status()!=want{t.Fatal(i.Status())}})}}
// TestGeneratedRepeatRunning 验证 Running 不消耗循环次数，每次终态后重新获得异步令牌。
func TestGeneratedRepeatRunning(t *testing.T){i,c:=newTest(t,"repeatHold",1);i.Start();drain(t,c);i.Notify("resume");drain(t,c);if c.counts["a"]!=1{t.Fatal("Running 增加循环次数")};first:=c.tokens["a"][0];i.Complete(first,bt.Success);drain(t,c);if c.counts["a"]!=2||i.Status()!=bt.Running||i.Complete(first,bt.Success){t.Fatal("第二轮状态或旧令牌错误",c.counts,i.Status())};i.Complete(c.tokens["a"][1],bt.Success);drain(t,c);if i.Status()!=bt.Success||c.counts["a"]!=2{t.Fatal(i.Status(),c.counts)}}
// TestGeneratedSubtreeIsolation 验证同一子树两个调用位置具有独立动作状态和令牌。
func TestGeneratedSubtreeIsolation(t *testing.T){i,c:=newTest(t,"subtree",1024);if i.Start()!=bt.Running||c.counts["shared"]!=2{t.Fatal(i.Status(),c.counts)};a,b:=c.tokens["shared"][0],c.tokens["shared"][1];if a.Node==b.Node{t.Fatal("子树状态未隔离")};i.Complete(a,bt.Success);if i.Status()!=bt.Running{t.Fatal(i.Status())};i.Complete(b,bt.Success);if i.Status()!=bt.Success{t.Fatal(i.Status())}}
`
