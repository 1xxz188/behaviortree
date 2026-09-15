package behaviortree

import (
	"reflect"
	"strings"
	"testing"
)

// TestPublishRejectsRenamedTreeEntry 验证两种发布策略均拒绝移除旧入口，且发布失败不改变版本或运行实例。
func TestPublishRejectsRenamedTreeEntry(t *testing.T) {
	for name, policy := range map[string]SwitchPolicy{"轮次结束切换": EndOfRound, "取消并重启": CancelAndRestart} {
		t.Run(name, func(t *testing.T) {
			registry, err := NewRegistry(gateProgram("original"))
			if err != nil {
				t.Fatal(err)
			}
			registry.SetLogger(nil)
			queue, context := &testQueue{}, &probe{}
			instance, err := registry.NewInstance("running", "main", context, queue.opts())
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			if instance.Start() != Running {
				t.Fatal("旧入口实例未进入等待状态")
			}
			current, stats, token := registry.latest(), registry.Stats(), context.tokens[0]
			renamed := gateProgram("renamed")
			renamed.Roots = map[string]int{"9_Main": 0}
			for i := range renamed.Nodes {
				renamed.Nodes[i].TreeID = "9_Main"
			}
			if err := ValidateProgram(renamed); err != nil {
				t.Fatalf("改号后的独立程序应合法: %v", err)
			}
			if err := registry.Publish(renamed, policy); err == nil || !strings.Contains(err.Error(), `tree "main" removed; restart required`) {
				t.Fatalf("热更移除入口未明确要求重启: %v", err)
			}
			queue.drain(t)
			if registry.latest() != current || registry.CurrentVersion() != "original" || !reflect.DeepEqual(registry.Stats(), stats) {
				t.Fatal("发布失败改变了当前指针、版本记录或活跃实例统计")
			}
			if instance.Version() != "original" || instance.Status() != Running || context.starts != [2]int{1, 0} || context.aborts != [2]int{} || context.tokens[0] != token {
				t.Fatal("发布失败取消、重启或替换了正在运行的旧实例")
			}
			created, err := registry.NewInstance("new", "main", &probe{}, queue.opts())
			if err != nil {
				t.Fatalf("旧入口不再接受新实例: %v", err)
			}
			defer created.Close()
			if created.Version() != "original" {
				t.Fatal("新实例采用了未发布版本")
			}
			if _, err := registry.NewInstance("unknown", "9_Main", &probe{}, queue.opts()); err == nil {
				t.Fatal("未发布的新入口被意外接受")
			}
			if !instance.Complete(token, Success) || !instance.Complete(context.tokens[1], Success) || instance.Status() != Success {
				t.Fatal("旧回调不能继续完成原有轮次")
			}
			if instance.Start() != Running || instance.Version() != "original" {
				t.Fatal("失败发布影响了下一轮的入口或版本")
			}
		})
	}
}
