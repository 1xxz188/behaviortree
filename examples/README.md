# 行为树与插件示例

在本仓库根目录执行以下命令。模块路径为 `github.com/1xxz188/behaviortree`，使用 Go 1.26.7，无需额外构建标签。普通示例支持 Windows/Linux，不需要 Redis、MongoDB、NATS，也不创建后台 goroutine。

```sh
go run ./scripts/generate
go run ./examples/host
go test ./examples/... ./hotload ./scripts/...
```

`definition/project.go` 用 Go 声明节点目录及示例树。生成命令写出 `project.json`、`catalog.json`、`behavior/glue.gen.go`、每棵定义树对应的 `behavior/tree_<snake_case_tree_id>.gen.go` 和 `behavior/tree_gen.map.json`。例如默认示例输出 `tree_main.gen.go`，节点代码名使用 `Root`、`Gate`、`Record`；工程 JSON 同时保留节点稳定 ID 和可读 `codeName`。生成器只更新变化的文件，并清理旧清单中的废弃生成文件。`behavior/actions.go` 是 GoLand 中编写的手工动作，参数结构体由公共 glue 提供。编辑 `project.json` 后使用根工具的 `generate` 命令，不要再次运行上述重建默认示例的命令。

示例工程使用 `generation.packagePath: "behavior"`，以 `examples` 为工程目录直接生成到 `examples/behavior/`，目录内 Go 文件均为 `package behavior`。重建脚本直接读取这项配置；没有固定 `generated` 层。若从仓库根目录生成已编辑的示例，请执行 `go run ./cmd/bttool generate --file examples/project.json --out examples/behavior`；CLI 的相对路径以命令工作目录为基准。

`shared.Context` 是宿主固定业务 ABI，角色/房间对象和异步回调令牌归宿主持有。`behavior` 整个子树包括 `helper` 都是可以改变的版本私有代码。树返回 Running 后，宿主在实例所属串行队列中调用 `Complete(token, result)`。日志使用稳定树 ID、节点 ID、版本和事件序号。

## 真实 Linux 插件验收

需要 **Linux/amd64、Go 1.26.7、可用 C 编译器、CGO_ENABLED=1**。在本仓库根目录执行：

```sh
go run ./scripts/builddemo
go run ./scripts/builddemo -mode debug
```

脚本固定 Linux/amd64、Go 1.26.7、CGO=1、GOAMD64=v1，清除外部 GOFLAGS；release 不使用构建标签，debug 使用 `debug` 标签及 `-gcflags=all=-N -l`。宿主和插件同时应用 `-trimpath`。它不修改根 go.mod/go.work，而是在 `.build/release_<唯一时间>/source` 下建立独立模块，并在该发布目录生成单独的 go.work，统一纳入本仓库模块和全部私有模块。所有构建使用这个暂存工作区及 `-mod=readonly`，保证公共运行库在宿主和插件里具有同一主模块身份；单纯以不同模块的本地 replace 配合 trimpath 会产生不同包 BuildID，不能这样发布。`-root` 可指定本仓库绝对路径，默认 `.`；`-out` 指定输出父目录，默认 `.build`。

每次构建创建唯一版本、`.so` 文件名和 `bt.local/<版本>` 模块路径。整个 `behavior` 目录被复制，Go AST 将目录内所有 helper 引用改为版本路径。固定 `shared` 和行为树运行时始终从本仓库模块引用。将需要热更的 helper 放进这个私有目录；目录之外的依赖属于共享 ABI，修改后必须重建并重启宿主。

构建工具使用 `go list -deps` 选出的源文件及模块身份，计算宿主和插件共享依赖的 SHA256，并计算运行时和固定上下文指纹，记录工具链、标签、优化参数和 CGO 环境。构建前后重新比对指纹；每个 `.so.json` 还绑定对应文件校验和。宿主契约以链接参数嵌入可执行文件，加载前依次检查契约、唯一身份及文件校验和，加载后检查 `func BuildExporter() *bt.Program[*shared.Context]` 签名和程序有效性，再显式发布。

构建完成立即运行一个宿主进程，真实加载三个成功版本和一个错误签名插件，检查：

- 每个版本都修改树结构、动作代码和 helper 代码；结果必须体现三个变化。
- EndOfRound 保持旧 Running 版本，旧轮完成后下一轮采用新版本，新实例立即采用最新版本。
- CancelAndRestart 在旧实例队列调用旧动作 Abort，切换后重开新树；已取消令牌失效。
- 错误签名、构建契约不匹配均不发布；已有运行实例与默认版本保持原状。
- 加载数量包括错误签名但已经 `plugin.Open` 的插件，共 4 个；这些代码不能卸载。

Windows 可执行 `go run ./scripts/builddemo -prepare`，只生成四个版本的真实源码供审查；这不代表 `.so` 构建或加载验收通过。独立模块迁移后已在 Linux 环境重新通过 release/debug 两套实际三版本加载、release 宿主拒绝 debug 插件，以及 Go race 检查；环境和原始日志见 [验收记录](../docs/VERIFICATION.md)。

## 接入实际服务器

管理入口使用 `hotload.New[*bt.Program[业务上下文类型]](宿主构建契约)` 建立加载器；成功 `Load` 后检查 `program.Version == manifest.Version`，再调用 `Registry.Publish(program, bt.EndOfRound)` 或 `bt.CancelAndRestart`。`Registry.Stats()` 查看各版本活跃数，`Loader.LoadedCount()` 查看不可卸载的已打开插件数量。初始版本可以直接调用生成的 `NewProgram` 静态链接。

Go 官方插件会在 `Open` 时运行此前未加载包的 `init`；后续签名或程序校验失败不会撤销 init 副作用。插件 init 应无业务副作用，插件文件应来自可信构建并放在不可被并发修改的发布目录。校验和不是代码签名，不能隔离不可信代码。Go 插件无法卸载，必须通过服务重启释放历次加载的代码内存。详见 [Go 官方 plugin 约束](https://pkg.go.dev/plugin)。
