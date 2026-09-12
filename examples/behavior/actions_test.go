package behavior

import (
	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/examples/shared"
	"testing"
)

// TestGeneratedAsyncLifecycle 验证真实生成树连接业务动作、失败结果、取消及迟到回调。
func TestGeneratedAsyncLifecycle(t *testing.T) {
	for _, result := range []bt.Status{bt.Success, bt.Failure} {
		t.Run(result.String(), func(t *testing.T) {
			context := &shared.Context{}
			var queue []func()
			i, err := bt.NewInstance(NewProgram("test"), "ai", "main", context, bt.Options{Post: func(fn func()) { queue = append(queue, fn) }})
			if err != nil {
				t.Fatal(err)
			}
			defer i.Close()
			if i.Start() != bt.Running {
				t.Fatal("expected asynchronous wait")
			}
			token := context.Token
			if !i.Complete(token, result) {
				t.Fatal("completion rejected")
			}
			for len(queue) > 0 {
				fn := queue[0]
				queue = queue[1:]
				fn()
			}
			if i.Status() != result {
				t.Fatalf("result=%s want=%s", i.Status(), result)
			}
			if i.Complete(token, bt.Success) {
				t.Fatal("duplicate completion accepted")
			}
			i.Start()
			token = context.Token
			i.Abort("test")
			if context.Aborts != 1 {
				t.Fatal("Abort not delivered exactly once")
			}
			if i.Complete(token, bt.Success) {
				t.Fatal("cancelled completion accepted")
			}
		})
	}
}
