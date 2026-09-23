package behaviortree

import (
	"fmt"
	"testing"
	"unsafe"
)

// BenchmarkNotifyEventID 比较已监听、未监听和未知数值事件的通知成本。
func BenchmarkNotifyEventID(b *testing.B) {
	for _, name := range []string{"observed", "unobserved", "unknown"} {
		b.Run(name, func(b *testing.B) {
			event := EventID(1)
			p := benchmarkProgram(10, false, true)
			p.Events = append(p.Events, EventDefinition{ID: 2, Name: "未监听", CodeName: "Unobserved"})
			i, err := NewInstance(p, "event-name", "main", 0, Options{Post: func(func()) { b.Fatal("unexpected yield") }, Logger: func(LogRecord) {}})
			if err != nil {
				b.Fatal(err)
			}
			i.Start()
			if name == "unobserved" {
				event = 2
			}
			if name == "unknown" {
				event = 3
			}
			b.ReportAllocs()
			for b.Loop() {
				i.Notify(event)
			}
			i.Close()
		})
	}
}

// benchmarkProgram 模拟生成器的整数索引控制流，业务动作只读状态，不混入业务成本。
func benchmarkProgram(nodes int, parallel, wait bool) *Program[int] {
	p := &Program[int]{Version: "benchmark", Roots: map[string]int{"main": 0}, Nodes: make([]Node, nodes), Events: []EventDefinition{{ID: 1, Name: "就绪", CodeName: "Ready"}}, EventDependencies: map[EventID][]int{1: {1}}}
	p.Nodes[0] = Node{"root", "main", -1, nodes}
	for n := 1; n < nodes; n++ {
		p.Nodes[n] = Node{fmt.Sprint(n), "main", 0, n + 1}
	}
	var step func(*Frame[int], int) Status
	step = func(f *Frame[int], node int) Status {
		if cached, run := f.Enter(node); !run {
			return cached
		}
		s := f.State(node)
		if node != 0 {
			if wait {
				s.Started = true
				if s.Ready {
					f.Consume(node)
				}
				return f.Exit(node, Running)
			}
			return f.Exit(node, Success)
		}
		if !parallel {
			for s.Cursor < nodes-1 {
				result := step(f, s.Cursor+1)
				if result != Success {
					return f.Exit(node, result)
				}
				s.Cursor++
			}
			return f.Exit(node, Success)
		}
		for s.Cursor < nodes-1 {
			child := s.Cursor + 1
			result := step(f, child)
			if f.State(child).Status == Invalid {
				return f.Exit(node, Running)
			}
			if result == Success {
				s.Count++
			}
			s.Cursor++
		}
		for child := f.PopDirtyChild(node); child >= 0; child = f.PopDirtyChild(node) {
			step(f, child)
		}
		if s.Count == nodes-1 {
			return f.Exit(node, Success)
		}
		return f.Exit(node, Running)
	}
	p.Step = step
	return p
}

// BenchmarkMatrix 覆盖 1 千/1 万 AI 与 10/100/1000 节点，按单实例一次操作报告稳态成本。
// instance-bytes 为框架主要分配大小，不包含 Go 分配器、宿主和业务上下文。
func BenchmarkMatrix(b *testing.B) {
	for _, population := range []int{1000, 10000} {
		for _, nodes := range []int{10, 100, 1000} {
			for _, mode := range []string{"idle", "event", "parallel", "round", "trace"} {
				b.Run(fmt.Sprintf("ai=%d/nodes=%d/%s", population, nodes, mode), func(b *testing.B) {
					p := benchmarkProgram(nodes, mode == "parallel", mode != "round" && mode != "trace")
					r, err := NewRegistry(p)
					if err != nil {
						b.Fatal(err)
					}
					opts := Options{Budget: 4096, Post: func(func()) { b.Fatal("benchmark unexpectedly yielded") }, Trace: mode == "trace", Logger: func(LogRecord) {}}
					instances := make([]*Instance[int], population)
					for n := range instances {
						instances[n], err = r.NewInstance("benchmark", "main", 0, opts)
						if err != nil {
							b.Fatal(err)
						}
						instances[n].Start()
					}
					before := uint64(0)
					for _, i := range instances {
						before += i.Steps()
					}
					b.ReportAllocs()
					n := 0
					for b.Loop() {
						i := instances[n]
						n++
						if n == population {
							n = 0
						}
						switch mode {
						case "idle":
							i.Tick()
						case "round", "trace":
							i.Start()
						default:
							i.Notify(1)
						}
					}
					after := uint64(0)
					for _, i := range instances {
						after += i.Steps()
					}
					b.ReportMetric(float64(after-before)/float64(b.N), "nodes/op")
					pendingBytes := cap(instances[0].pending)*int(unsafe.Sizeof(int(0))) + len(instances[0].pendingMask)
					b.ReportMetric(float64(unsafe.Sizeof(Instance[int]{}))+float64(unsafe.Sizeof(NodeState{}))*float64(nodes)+float64(unsafe.Sizeof(Blackboard{}))+float64(pendingBytes), "instance-bytes")
				})
			}
		}
	}
}

// BenchmarkCreate 测量共享已发布程序下实例启动的分配和每实例成本。
func BenchmarkCreate(b *testing.B) {
	for _, nodes := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("nodes=%d", nodes), func(b *testing.B) {
			r, _ := NewRegistry(benchmarkProgram(nodes, false, false))
			opts := Options{Budget: 4096, Post: func(func()) {}}
			b.ReportAllocs()
			for b.Loop() {
				i, err := r.NewInstance("benchmark", "main", 0, opts)
				if err != nil {
					b.Fatal(err)
				}
				i.Start()
				i.Close()
			}
		})
	}
}

// BenchmarkVersionSwitch 分别测量普通发布、下一轮切换及取消重启的批量成本。
func BenchmarkVersionSwitch(b *testing.B) {
	for _, population := range []int{1000, 10000} {
		for _, policy := range []SwitchPolicy{EndOfRound, CancelAndRestart} {
			b.Run(fmt.Sprintf("ai=%d/policy=%d", population, policy), func(b *testing.B) {
				p := benchmarkProgram(10, false, true)
				r, _ := NewRegistry(p)
				r.SetLogger(nil)
				queue := make([]func(), 0, population+256)
				post := func(fn func()) { queue = append(queue, fn) }
				instances := make([]*Instance[int], population)
				for n := range instances {
					instances[n], _ = r.NewInstance("benchmark", "main", 0, Options{Post: post, Logger: func(LogRecord) {}})
					instances[n].Start()
				}
				version := 0
				b.ReportAllocs()
				for b.Loop() {
					version++
					p.Version = fmt.Sprint(version)
					if err := r.Publish(p, policy); err != nil {
						b.Fatal(err)
					}
					if policy == EndOfRound {
						for _, i := range instances {
							i.Abort("benchmark round boundary")
							i.Start()
						}
					}
					for n := 0; n < len(queue); n++ {
						queue[n]()
					}
					clear(queue)
					queue = queue[:0]
				}
				b.ReportMetric(float64(population), "instances/op")
			})
		}
	}
}
