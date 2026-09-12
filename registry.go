package behaviortree

import (
	"fmt"
	"sort"
	"sync"
)

// Registry 管理完整不可变版本和按版本索引的活跃实例；发布可来自管理线程。
type Registry[C any] struct {
	mu       sync.RWMutex                         // mu 保护发布指针和跨宿主活跃索引。
	current  *preparedProgram[C]                  // current 是新实例和下一轮的默认版本。
	versions map[string]*preparedProgram[C]       // versions 防止重复版本并保留发布统计。
	active   map[string]map[*Instance[C]]struct{} // active 只包含当前仍 Running 的实例。
	logger   func(LogRecord)                      // logger 接收发布及失败事件。
	forced   uint64                               // forced 补偿开始执行与强制发布的竞争窗口。
}

// NewRegistry 创建完整版本注册表，初始程序必须通过相同的发布校验。
func NewRegistry[C any](initial *Program[C]) (*Registry[C], error) {
	p, err := prepareProgram(initial)
	if err != nil {
		return nil, err
	}
	p.generation = 1
	return &Registry[C]{current: p, versions: map[string]*preparedProgram[C]{p.program.Version: p}, active: make(map[string]map[*Instance[C]]struct{}), logger: defaultLog}, nil
}

// SetLogger 设置发布和发布失败日志接收者；nil 显式关闭注册表日志，不改变实例日志配置。
func (r *Registry[C]) SetLogger(logger func(LogRecord)) {
	r.mu.Lock()
	r.logger = logger
	r.mu.Unlock()
}

// NewInstance 创建默认采用最新版本的宿主所有实例。
func (r *Registry[C]) NewInstance(id, treeID string, context C, options Options) (*Instance[C], error) {
	return newInstance(r.latest(), r, id, treeID, context, options)
}

// Publish 校验并原子发布完整版本；普通发布不访问或扫描任何实例。
// 强制策略只从旧版本的活跃索引取出目标，再按最多 128 个一批投递到实例所属队列。
func (r *Registry[C]) Publish(program *Program[C], policy SwitchPolicy) (err error) {
	defer func() {
		if err != nil {
			r.mu.RLock()
			logger := r.logger
			r.mu.RUnlock()
			if logger != nil {
				emitLog(logger, LogRecord{Kind: "error", Reason: err.Error()})
			}
		}
	}()
	p, err := prepareProgram(program)
	if err != nil {
		return err
	}
	if policy != EndOfRound && policy != CancelAndRestart {
		return fmt.Errorf("unknown switch policy %d", policy)
	}
	r.mu.Lock()
	if _, exists := r.versions[p.program.Version]; exists {
		r.mu.Unlock()
		return fmt.Errorf("version %q already published", p.program.Version)
	}
	if err = compatibleFields(r.current.program.Fields, p.program.Fields); err != nil {
		r.mu.Unlock()
		return err
	}
	for tree := range r.current.program.Roots {
		if _, exists := p.program.Roots[tree]; !exists {
			r.mu.Unlock()
			return fmt.Errorf("tree %q removed; restart required", tree)
		}
	}
	p.generation = r.current.generation + 1
	r.current = p
	r.versions[p.program.Version] = p
	logger := r.logger
	var pending []*Instance[C]
	if policy == CancelAndRestart {
		r.forced = p.generation
		for version, instances := range r.active {
			if version == p.program.Version {
				continue
			}
			for instance := range instances {
				pending = append(pending, instance)
			}
		}
	}
	r.mu.Unlock()
	if logger != nil {
		emitLog(logger, LogRecord{Kind: "publish", Version: p.program.Version})
	}
	if len(pending) > 0 {
		batch := &restartBatch[C]{registry: r, pending: pending, generation: p.generation}
		batch.dispatch()
	}
	return nil
}

// Stats 返回各版本活跃计数；已加载的 Go 插件代码由进程持有，不能卸载。
func (r *Registry[C]) Stats() []VersionStats {
	r.mu.RLock()
	stats := make([]VersionStats, 0, len(r.versions))
	for version := range r.versions {
		stats = append(stats, VersionStats{Version: version, Active: len(r.active[version]), Current: r.current.program.Version == version})
	}
	r.mu.RUnlock()
	sort.Slice(stats, func(a, b int) bool { return stats[a].Version < stats[b].Version })
	return stats
}

// CurrentVersion 返回新实例和下一轮将采用的版本。
func (r *Registry[C]) CurrentVersion() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current.program.Version
}

// latest 只获取不可变版本指针，持锁时间不包括业务执行。
func (r *Registry[C]) latest() *preparedProgram[C] {
	r.mu.RLock()
	p := r.current
	r.mu.RUnlock()
	return p
}

// setActive 在运行状态变化时维护索引；无需轮询检查实例是否完成。
func (r *Registry[C]) setActive(i *Instance[C], active bool) {
	r.mu.Lock()
	version := i.program.program.Version
	instances := r.active[version]
	if active {
		if instances == nil {
			instances = make(map[*Instance[C]]struct{})
			r.active[version] = instances
		}
		instances[i] = struct{}{}
	} else if instances != nil {
		delete(instances, i)
	}
	// 补偿实例在取版本后、登记运行前恰好发生强制发布的窗口。
	forced := r.forced
	restart := active && i.program.generation < forced
	r.mu.Unlock()
	if restart {
		i.options.Post(func() {
			if !i.closed && i.status == Running && i.program.generation < forced {
				i.Abort("concurrent forced publication")
				i.Start()
			}
		})
	}
}

// restartBatch 持有一次强制发布的目标快照；每批回到宿主队列后继续投递下一批。
type restartBatch[C any] struct {
	registry   *Registry[C]   // registry 提供重启时的最新完整版本。
	pending    []*Instance[C] // pending 是本次强制发布的活跃目标快照。
	offset     int            // offset 是下一批开始的位置。
	generation uint64         // generation 防止陈旧强更任务取消更新的一轮。
}

// dispatch 在批次间让出宿主队列，既不创建 goroutine，也不要求定时轮询。
func (b *restartBatch[C]) dispatch() {
	end := min(b.offset+128, len(b.pending))
	for n := b.offset; n < end; n++ {
		i := b.pending[n]
		i.options.Post(func() {
			if i.closed || i.status != Running || i.program.generation >= b.generation {
				return
			}
			i.Abort("cancel and restart for published version")
			i.Start()
		})
	}
	b.offset = end
	if end < len(b.pending) {
		b.pending[end].options.Post(b.dispatch)
	}
}
