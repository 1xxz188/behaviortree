package behavior

import (
	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/examples/behavior/helper"
	"github.com/1xxz188/behaviortree/examples/shared"
)

// ActionMarker 在集成构建中更新，验证手写动作本身和辅助函数均使用新版本。
const ActionMarker = "baseline_action"

// Gate 模拟非阻塞异步动作；宿主在自己的串行队列调用 Complete。
func Gate(f *bt.Frame[*shared.Context], node int, phase bt.Phase, _ GateParams) bt.Status {
	switch phase {
	case bt.Start:
		f.Context.Token = f.Token(node)
		f.Context.Starts++
		f.Context.Events = append(f.Context.Events, helper.Label()+":"+ActionMarker+":start")
		return bt.Running
	case bt.Resume:
		result := f.Consume(node)
		if result == bt.Invalid {
			return bt.Running
		}
		f.Context.Events = append(f.Context.Events, helper.Label()+":"+ActionMarker+":resume")
		return result
	case bt.Abort:
		f.Context.Aborts++
		f.Context.Events = append(f.Context.Events, helper.Label()+":"+ActionMarker+":abort")
	}
	return bt.Failure
}

// Record 演示生成的参数结构体直接绑定黑板字段或编译时常量。
func Record(f *bt.Frame[*shared.Context], _ int, phase bt.Phase, params RecordParams) bt.Status {
	if phase == bt.Start {
		f.Context.Events = append(f.Context.Events, helper.Label()+":"+params.Message)
	}
	return bt.Success
}
