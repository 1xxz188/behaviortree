package behaviortree

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

// TestDefaultLifecycleLogging 验证默认 slog 记录发布、切换和错误，同时默认关闭节点追踪。
func TestDefaultLifecycleLogging(t *testing.T) {
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	r, err := NewRegistry(gateProgram("v1"))
	if err != nil {
		t.Fatal(err)
	}
	q, context := &testQueue{}, &probe{}
	i, err := r.NewInstance("default-log", "main", context, q.opts())
	if err != nil {
		t.Fatal(err)
	}
	i.Start()
	i.Complete(context.tokens[0], Success)
	if buffer.Len() != 0 {
		t.Fatal("ordinary execution produced node logs with Trace disabled")
	}
	if err := r.Publish(gateProgram("v1"), EndOfRound); err == nil {
		t.Fatal("duplicate publication should fail")
	}
	if err := r.Publish(gateProgram("v2"), EndOfRound); err != nil {
		t.Fatal(err)
	}
	i.Complete(context.tokens[1], Success)
	i.Start()
	decoder := json.NewDecoder(&buffer)
	seen := map[string]int{}
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		kind, _ := record["kind"].(string)
		seen[kind]++
		if record["component"] != "behaviortree" {
			t.Fatal("missing structured component", record)
		}
		if kind == "error" && record["level"] != "ERROR" {
			t.Fatal("errors not logged at error level")
		}
		if kind == "switch" && (record["version"] != "v2" || record["instance"] != "default-log") {
			t.Fatal("missing switch context", record)
		}
	}
	if seen["publish"] != 1 || seen["error"] != 1 || seen["switch"] != 1 || seen["node"] != 0 {
		t.Fatal("unexpected default logging", seen)
	}
	i.Close()
}

// TestExplicitLogDisable 验证 SetLogger(nil) 和实例空接收函数显式关闭各自日志。
func TestExplicitLogDisable(t *testing.T) {
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	r, _ := NewRegistry(gateProgram("v1"))
	r.SetLogger(nil)
	q, context := &testQueue{}, &probe{}
	opts := q.opts()
	opts.Logger = func(LogRecord) {}
	opts.Trace = true
	i, _ := r.NewInstance("muted", "main", context, opts)
	i.Start()
	if err := r.Publish(gateProgram("v2"), CancelAndRestart); err != nil {
		t.Fatal(err)
	}
	q.drain(t)
	if buffer.Len() != 0 {
		t.Fatal("explicitly disabled logging still reached slog")
	}
	i.Close()
}

// TestDefaultTraceKindNames 验证整数日志种类在默认接收者中仍输出 node/abort 文本和原有状态字段。
func TestDefaultTraceKindNames(t *testing.T) {
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	q, context := &testQueue{}, &probe{}
	opts := q.opts()
	opts.Trace = true
	i, err := NewInstance(gateProgram("v1"), "trace-log", "main", context, opts)
	if err != nil {
		t.Fatal(err)
	}
	i.Start()
	i.Close()
	decoder := json.NewDecoder(&buffer)
	seen := map[string]int{}
	for {
		var record map[string]any
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		kind, ok := record["kind"].(string)
		if !ok {
			t.Fatal("log kind changed from a string", record)
		}
		seen[kind]++
		if kind == "node" && (record["from"] != "invalid" || record["to"] != "running") {
			t.Fatal("node log lost state names", record)
		}
		if kind == "abort" && record["reason"] != "instance closed" {
			t.Fatal("abort log lost its reason", record)
		}
	}
	if seen["node"] == 0 || seen["abort"] == 0 || len(seen) != 2 {
		t.Fatal("unexpected trace log kinds", seen)
	}
}
