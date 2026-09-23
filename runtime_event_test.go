package behaviortree

import (
	"strings"
	"testing"
)

// TestEventProgramValidation 验证直接构造的 Program 也拒绝非法元数据和依赖，包括空观察者的未知事件。
func TestEventProgramValidation(t *testing.T) {
	cases := []struct {
		name   string                 // name 标识被拒绝的输入类别。
		change func(*Program[*probe]) // change 只修改本用例的程序副本。
		want   string                 // want 是期望的错误字段或原因。
	}{
		{"zero ID", func(p *Program[*probe]) { p.Events[0].ID = 0 }, "events[0]"},
		{"duplicate ID", func(p *Program[*probe]) { p.Events = append(p.Events, p.Events[0]) }, "duplicate event ID"},
		{"empty name", func(p *Program[*probe]) { p.Events[0].Name = " \t" }, "events[0].name"},
		{"bad code name", func(p *Program[*probe]) { p.Events[0].CodeName = "wake" }, "events[0].codeName"},
		{"folded duplicate code name", func(p *Program[*probe]) {
			p.Events = append(p.Events, EventDefinition{ID: 2, Name: "另一个", CodeName: "WAKE"})
		}, "duplicate event code name"},
		{"enum NUL", func(p *Program[*probe]) { p.EventEnumDescription = "bad\x00" }, "eventEnumDescription"},
		{"member over limit", func(p *Program[*probe]) { p.Events[0].Description = strings.Repeat("中", 6000) }, "events[0].description"},
		{"invalid UTF-8", func(p *Program[*probe]) { p.Events[0].Description = string([]byte{0xff}) }, "events[0].description"},
		{"unknown event with empty observers", func(p *Program[*probe]) { p.EventDependencies[2] = []int{} }, "unknown event"},
		{"unknown field with empty observers", func(p *Program[*probe]) { p.FieldDependencies = map[string][]int{"missing": {}} }, "unknown field"},
		{"duplicate event observer", func(p *Program[*probe]) { p.EventDependencies[1] = []int{1, 1} }, "duplicate dependency node"},
		{"invalid field observer", func(p *Program[*probe]) {
			p.Fields = []Field{{ID: "flag", Name: "Flag", Type: BoolType}}
			p.FieldDependencies = map[string][]int{"flag": {99}}
		}, "invalid dependency node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := gateProgram("validation")
			tc.change(p)
			if err := ValidateProgram(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got error %v; want %q", err, tc.want)
			}
		})
	}
}

// TestEventProgramSnapshot 验证发布时复制事件描述及两类依赖，原程序变动不能影响运行中的版本。
func TestEventProgramSnapshot(t *testing.T) {
	p := gateProgram("snapshot")
	p.EventEnumDescription = "整体\r\n说明"
	p.Events[0].Description = "成员\r说明"
	p.Fields = []Field{{ID: "flag", Name: "Flag", Type: BoolType}}
	p.FieldDependencies = map[string][]int{"flag": {1}}
	prepared, err := prepareProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	p.EventEnumDescription = "changed"
	p.Events[0].ID = 99
	p.Events[0].Name = "changed"
	p.Events[0].Description = "changed"
	p.EventDependencies[1][0] = 2
	p.FieldDependencies["flag"][0] = 2
	delete(p.EventDependencies, 1)
	delete(p.FieldDependencies, "flag")
	if prepared.program.EventEnumDescription != "整体\n说明" || prepared.program.Events[0].Description != "成员\n说明" || prepared.program.Events[0].ID != 1 || prepared.program.Events[0].Name != "唤醒" {
		t.Fatalf("published event metadata changed: %+v", prepared.program.Events)
	}
	if prepared.program.EventDependencies[1][0] != 1 || prepared.program.FieldDependencies["flag"][0] != 1 {
		t.Fatal("published dependency slices changed")
	}
	if _, exists := prepared.eventByID[1]; !exists {
		t.Fatal("published event index changed")
	}
	if _, exists := prepared.eventByID[99]; exists {
		t.Fatal("caller mutation entered published event index")
	}
}

// TestNotifyUnknownEventLifecycle 验证未知通知只产生日志，不推进树或吞掉异步完成。
func TestNotifyUnknownEventLifecycle(t *testing.T) {
	q, ctx := &testQueue{}, &probe{}
	var records []LogRecord
	opts := q.opts()
	opts.Logger = func(record LogRecord) { records = append(records, record) }
	i, err := NewInstance(gateProgram("event-version"), "event-instance", "main", ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if i.Notify(77) != Invalid || len(records) != 0 {
		t.Fatal("inactive instance logged or started on unknown event")
	}
	if i.Start() != Running {
		t.Fatal("instance did not start")
	}
	before, token := i.Steps(), ctx.tokens[0]
	if i.Notify(77) != Running || i.Notify(0) != Running || i.Steps() != before || i.Error() != nil || len(q.items) != 0 {
		t.Fatal("unknown event changed running instance")
	}
	if len(records) != 2 || records[0].Version != "event-version" || records[0].InstanceID != "event-instance" || !strings.Contains(records[0].Reason, "unknown event ID 77") {
		t.Fatalf("unknown event diagnosis missing context: %+v", records)
	}
	if !i.Complete(token, Success) || ctx.starts[1] != 1 {
		t.Fatal("unknown event disrupted async completion")
	}
	i.Close()
	logged := len(records)
	if i.Notify(77) != Invalid || len(records) != logged {
		t.Fatal("closed instance diagnosed unknown event")
	}
}

// TestNotifyUnobservedEventDoesNotTick 验证合法但当前入口未监听的事件不会执行已有待处理路径。
func TestNotifyUnobservedEventDoesNotTick(t *testing.T) {
	p := gateProgram("unobserved")
	p.Events = append(p.Events, EventDefinition{ID: 2, Name: "未监听", CodeName: "Unobserved"})
	q, ctx := &testQueue{}, &probe{}
	i, err := NewInstance(p, "one", "main", ctx, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	if i.Start() != Running {
		t.Fatal("instance did not start")
	}
	i.markDirty(1)
	before := i.Steps()
	if i.Notify(2) != Running || i.Steps() != before {
		t.Fatal("unobserved event executed pending work")
	}
	if i.Tick() != Running || i.Steps() <= before {
		t.Fatal("pending work disappeared")
	}
}
