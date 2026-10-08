package codegen

import "github.com/1xxz188/behaviortree/model"

// priorityGuard 返回候选 Sequence 的首条件；无首条件的候选直接参与选择。
func (g *generator) priorityGuard(child int) int {
	candidate := g.nodes[child]
	if candidate.node.Type == model.NodeSequence && len(candidate.children) > 0 && g.nodes[candidate.children[0]].node.Type == model.NodeCondition {
		return candidate.children[0]
	}
	return -1
}

// emitPriorityReselectSupport 将完成锁存与预算续跑封装在生成包中，复用既有运行时 Data。
func (g *generator) emitPriorityReselectSupport() {
	if g.reselectStateSymbol == "" {
		return
	}
	g.line("// %s 保存单次激活的终态锁存和最后观测到的守卫结果。", g.reselectCandidateSymbol)
	g.line("type %s struct {", g.reselectCandidateSymbol)
	g.line("finished bool // 已结束的候选不因普通通知重试。")
	g.line("guard bt.Status // 观测 false 到 true 后允许重新激活。")
	g.line("}")
	g.line("// %s 保存重选阶段，预算让出后从未完成的检查继续。", g.reselectStateSymbol)
	g.line("type %s struct {", g.reselectStateSymbol)
	g.line("scan int // -1 表示已有稳定运行分支，其余为待检查候选序号。")
	g.line("refresh bool // 完成或显式重置后读取最新条件。")
	g.line("prepared bool // 当前守卫仅清理一次，避免小预算反复清理。")
	g.line("checked bool // 已完成的条件检查可跨预算保留。")
	g.line("guard bt.Status // 当前候选的已检查条件。")
	g.line("last bt.Status // 没有后续候选时返回最近的真实终态。")
	g.line("candidates []%s // 各候选的激活状态，仅首次进入分配。", g.reselectCandidateSymbol)
	g.line("}")
	g.line("// restart 从最高优先级开始重选，不清除已完成候选。")
	g.line("func (d *%s) restart(refresh bool) {d.scan=0;d.refresh=refresh;d.prepared=false;d.checked=false;d.guard=bt.Invalid}", g.reselectStateSymbol)
	g.line("// btRearmPriorityCandidate 取消指定候选的旧调用并重置选择进度，保留其他运行分支。")
	g.line("func btRearmPriorityCandidate(f *bt.Frame[%s], branch int, reason string) {", g.context)
	g.line("var owner,index int;switch branch {")
	for i, n := range g.nodes {
		if !n.node.ReselectOnCompletion {
			continue
		}
		for j, child := range n.children {
			g.line("case %s:owner=%s;index=%d", g.slot(child), g.slot(i), j)
		}
	}
	g.line("default:panic(\"not a completion-reselect priority candidate\")}")
	g.line("f.Abort(branch,reason);s:=f.State(owner);d,ok:=s.Data.(*%s);if !ok{return}", g.reselectStateSymbol)
	g.line("d.candidates[index]=%s{};if s.Count!=0&&s.Cursor==branch{s.Count=0};d.last=bt.Invalid;d.restart(true)", g.reselectCandidateSymbol)
	g.line("}")
}

// emitReselectPriority 在真实终态后重新选择，将条件刷新和候选锁存集中在生成控制流。
func (g *generator) emitReselectPriority(i int) {
	n := g.nodes[i]
	g.line("d,ok:=s.Data.(*%s);if !ok{d=&%s{candidates:make([]%s,%d)};s.Data=d}", g.reselectStateSymbol, g.reselectStateSymbol, g.reselectCandidateSymbol, len(n.children))
	// 稳定期只续跑实际被唤醒的分支，宽树的空闲事件不扫描所有条件。
	g.line("if d.scan<0&&s.Count!=0&&f.State(s.Cursor).Status==bt.Running&&f.OnlyDirtyChild(node,s.Cursor){guardDirty:=false;switch s.Cursor{")
	for _, child := range n.children {
		if guard := g.priorityGuard(child); guard >= 0 {
			g.line("case %s:guardDirty=f.IsDirty(%s)", g.slot(child), g.slot(guard))
		}
	}
	g.line("};if !guardDirty{status:=btStep(f,s.Cursor);if status==bt.Running{return f.Exit(node,status)};switch s.Cursor{")
	for j, child := range n.children {
		g.line("case %s:d.candidates[%d].finished=true", g.slot(child), j)
	}
	g.line("};d.last=status;s.Count=0;d.restart(true)}}")
	// 重选期间的新条件通知使已走过的候选重新检查；自身预算通知不重启进度。
	g.line("if d.scan<0{d.restart(false)}else{for child:=f.PopDirtyChild(node);child>=0;child=f.PopDirtyChild(node){switch child{")
	for _, child := range n.children {
		if guard := g.priorityGuard(child); guard >= 0 {
			g.line("case %s:if f.State(%s).Status!=bt.Invalid&&f.IsDirty(%s){d.restart(d.refresh)}", g.slot(child), g.slot(guard), g.slot(guard))
		}
	}
	g.line("}}}")
	g.line("for d.scan<%d{switch d.scan{", len(n.children))
	for j, child := range n.children {
		g.line("case %d:candidate:=&d.candidates[%d]", j, j)
		if guard := g.priorityGuard(child); guard >= 0 {
			g.line("if !d.prepared{if d.refresh{f.Reset(%s)};d.prepared=true}", g.slot(guard))
			g.line("if !d.checked{value:=%s(f);if value==bt.Running{return f.Exit(node,bt.Running)};d.guard=value;d.checked=true", g.nodes[guard].function)
			// 整个候选重置才能使动作再次 Start；仅更新守卫会留下 Sequence 的终态缓存。
			g.line("if value==bt.Success&&candidate.guard==bt.Failure&&candidate.finished{f.Abort(%s,\"priority reactivated\");candidate.finished=false;candidate.guard=value;d.prepared=false;d.checked=false;continue};candidate.guard=value}", g.slot(child))
			g.line("if d.guard==bt.Failure{f.SkipDirtyChild(%s);d.scan++;d.prepared=false;d.checked=false;continue}", g.slot(child))
		}
		// 先排除已结束候选，避免普通通知抢占仍在运行的低优先分支。
		g.line("if candidate.finished{f.SkipDirtyChild(%s);d.scan++;d.prepared=false;d.checked=false;continue}", g.slot(child))
		g.line("if s.Count!=0&&s.Cursor!=%s{f.Abort(s.Cursor,\"priority preempted\")};s.Count=1;s.Cursor=%s", g.slot(child), g.slot(child))
		g.line("status:=%s(f);if status==bt.Running{if f.State(%s).Status!=bt.Invalid{d.scan=-1;d.refresh=false;d.prepared=false;d.checked=false};return f.Exit(node,status)}", g.nodes[child].function, g.slot(child))
		g.line("candidate.finished=true;d.last=status;s.Count=0;d.restart(true)")
	}
	g.line("}}")
	g.line("if s.Count!=0{f.Abort(s.Cursor,\"priority guard failed\")};s.Count=0;d.scan=-1;d.refresh=false;d.prepared=false;d.checked=false")
	g.line("if d.last==bt.Invalid{return f.Exit(node,bt.Failure)};return f.Exit(node,d.last)")
}
