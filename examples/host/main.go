// host 演示固定 Go 业务上下文与生成控制流，并可验证真实原生插件连续发布。
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bt "github.com/1xxz188/behaviortree"
	"github.com/1xxz188/behaviortree/examples/behavior"
	"github.com/1xxz188/behaviortree/examples/shared"
	"github.com/1xxz188/behaviortree/hotload"
)

// buildContract 由构建脚本通过 -ldflags 嵌入，运行时不信任外部宿主契约文件。
var buildContract string

// ownerQueue 模拟房间串行队列，Post 只入队并在当前同步执行结束后消费。
type ownerQueue struct{ pending []func() } // pending 是尚未运行的事件。

// post 将后续驱动保存到当前宿主队列。
func (q *ownerQueue) post(fn func()) { q.pending = append(q.pending, fn) }

// drain 消费宿主已就绪事件；没有事件时不轮询任何行为树。
func (q *ownerQueue) drain() {
	for len(q.pending) > 0 {
		fn := q.pending[0]
		q.pending[0] = nil
		q.pending = q.pending[1:]
		fn()
	}
}

// main 默认运行普通 Go 示例；-integration 验证构建目录中的原生插件。
func main() {
	dir := flag.String("integration", "", "验证 builddemo 生成的 Linux 插件目录")
	flag.Parse()
	if *dir == "" {
		staticDemo()
		return
	}
	integration(*dir)
}

// staticDemo 在 Windows/Linux 上均可运行，不依赖数据库或外部服务。
func staticDemo() {
	registry, err := bt.NewRegistry(behavior.NewProgram("baseline"))
	must(err)
	registry.SetLogger(printRecord)
	queue := ownerQueue{}
	context := &shared.Context{}
	instance, err := registry.NewInstance("demo", "main", context, bt.Options{Post: queue.post, Trace: true, Logger: printRecord})
	must(err)
	defer instance.Close()
	ensure(instance.Start() == bt.Running, "action should wait for host completion")
	// 无监听者的已声明事件是合法空操作，调用方直接使用生成常量。
	ensure(instance.Notify(behavior.EventHostStateChanged) == bt.Running, "unused declared event changed running action")
	ensure(instance.Complete(context.Token, bt.Success), "completion should be accepted")
	queue.drain()
	ensure(instance.Status() == bt.Success, "round should finish")
	fmt.Println("business events:", strings.Join(context.Events, ", "))
}

// printRecord 将结构化记录以 JSON 输出，稳定 NodeID 方便定位 GoLand 中的生成代码。
func printRecord(record bt.LogRecord) {
	data, err := json.Marshal(record)
	must(err)
	fmt.Println(string(data))
}

// integration 在一个真实宿主进程内加载三个成功版本和一个错误导出版本。
func integration(dir string) {
	if buildContract == "" {
		must(fmt.Errorf("host must be built by scripts/builddemo to embed its contract"))
	}
	data, err := base64.RawStdEncoding.DecodeString(buildContract)
	must(err)
	var contract hotload.Contract
	must(json.Unmarshal(data, &contract))
	loader := hotload.New[*bt.Program[*shared.Context]](contract)
	registry, err := bt.NewRegistry(behavior.NewProgram("baseline"))
	must(err)
	registry.SetLogger(printRecord)
	queue := ownerQueue{}
	context := &shared.Context{}
	instance, err := registry.NewInstance("existing", "main", context, bt.Options{Post: queue.post, Trace: true, Logger: printRecord})
	must(err)
	defer instance.Close()
	ensure(instance.Start() == bt.Running, "baseline must be running")
	baselineToken := context.Token
	p1 := load(loader, artifact(dir, "v1"))
	must(registry.Publish(p1, bt.EndOfRound))
	queue.drain()
	ensure(instance.Version() == "baseline", "old running round must pin baseline")
	ensure(instance.Complete(baselineToken, bt.Success), "baseline completion rejected")
	queue.drain()
	ensure(instance.Status() == bt.Success, "baseline completion failed")
	ensure(instance.Start() == bt.Running && instance.Version() == p1.Version, "next round must use v1")
	v1Token := context.Token
	p2 := load(loader, artifact(dir, "v2"))
	must(registry.Publish(p2, bt.EndOfRound))
	queue.drain()
	ensure(instance.Version() == p1.Version && instance.Status() == bt.Running, "long running v1 should remain pinned")
	newContext := &shared.Context{}
	newInstance, err := registry.NewInstance("new", "main", newContext, bt.Options{Post: queue.post})
	must(err)
	ensure(newInstance.Version() == p2.Version, "new instance must use v2")
	ensure(newInstance.Start() == bt.Running, "v2 start failed")
	ensure(newInstance.Complete(newContext.Token, bt.Success), "v2 completion rejected")
	queue.drain()
	ensure(newInstance.Status() == bt.Success, "v2 round failed")
	ensure(strings.Contains(strings.Join(newContext.Events, ","), "v2:v2_action:resume") && strings.Contains(strings.Join(newContext.Events, ","), "extra_b"), "v2 action/helper/tree did not update")
	newInstance.Close()
	// 错误签名已经进入 plugin.Open，仍不允许影响当前已发布版本。
	_, _, err = loader.Load(artifact(dir, "bad_signature"), nil)
	ensure(err != nil && strings.Contains(err.Error(), "signature"), "wrong signature must be rejected")
	// 错误构建契约与损坏文件在 Open 之前失败，不增加已加载数量。
	count := loader.LoadedCount()
	badPath := filepath.Join(dir, "mismatch.so")
	copyArtifact(artifact(dir, "v3"), badPath)
	m, err := hotload.ReadManifest(badPath + ".json")
	must(err)
	// 使用另一种合法模式，验证契约不匹配在 plugin.Open 前被拒绝。
	m.Contract.Mode = hotload.BuildDebug
	if contract.Mode == hotload.BuildDebug {
		m.Contract.Mode = hotload.BuildRelease
	}
	writeJSON(badPath+".json", m)
	_, _, err = loader.Load(badPath, nil)
	ensure(err != nil, "build mismatch accepted")
	ensure(loader.LoadedCount() == count, "mismatch should be rejected before Open")
	ensure(instance.Version() == p1.Version, "failed loads changed running version")
	probe, err := registry.NewInstance("probe", "main", &shared.Context{}, bt.Options{Post: queue.post})
	must(err)
	ensure(probe.Version() == p2.Version, "failed loads changed current version")
	probe.Close()
	p3 := load(loader, artifact(dir, "v3"))
	must(registry.Publish(p3, bt.CancelAndRestart))
	queue.drain()
	ensure(instance.Version() == p3.Version && instance.Status() == bt.Running, "force restart did not start v3")
	ensure(context.Aborts == 1, "old v1 action must Abort exactly once")
	ensure(!instance.Complete(v1Token, bt.Success), "cancelled v1 callback should be ignored")
	ensure(instance.Complete(context.Token, bt.Success), "v3 completion rejected")
	queue.drain()
	ensure(instance.Status() == bt.Success, "v3 round failed")
	events := strings.Join(context.Events, ",")
	ensure(strings.Contains(events, "v1:v1_action:abort") && strings.Contains(events, "v3:v3_action:resume") && strings.Contains(events, "v3:extra_c"), "versioned tree/action/helper behavior mismatch")
	ensure(loader.LoadedCount() == 4, "expected 3 valid plugins plus non-unloadable bad-signature plugin")
	fmt.Println("PASS: three native versions, tree/action/helper changes, both switch policies, pinned Running, stale callback, bad signature and build mismatch; opened plugins:", loader.LoadedCount())
}

// load 先验证导出程序和版本一致性，发布由调用者显式执行。
func load(loader *hotload.Loader[*bt.Program[*shared.Context]], path string) *bt.Program[*shared.Context] {
	p, m, err := loader.Load(path, func(p *bt.Program[*shared.Context]) error { _, err := bt.NewRegistry(p); return err })
	must(err)
	ensure(p.Version == m.Version, "exported program version does not match manifest")
	return p
}

// artifact 通过唯一完整文件名找到本次构建的指定演示版本。
func artifact(dir, label string) string {
	paths, err := filepath.Glob(filepath.Join(dir, label+"_*.so"))
	must(err)
	ensure(len(paths) == 1, "expected one artifact for "+label)
	return paths[0]
}

// copyArtifact 创建隔离的负例，不改动成功发布产物。
func copyArtifact(src, dst string) {
	for _, suffix := range []string{"", ".json"} {
		data, err := os.ReadFile(src + suffix)
		must(err)
		must(os.WriteFile(dst+suffix, data, 0600))
	}
}

// writeJSON 写入集成测试负例旁车。
func writeJSON(path string, value any) {
	data, err := json.Marshal(value)
	must(err)
	must(os.WriteFile(path, data, 0600))
}

// ensure 为演示集成测试提供立即失败的断言。
func ensure(ok bool, message string) {
	if !ok {
		must(fmt.Errorf("integration assertion: %s", message))
	}
}

// must 输出可诊断错误并以非零状态退出。
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
