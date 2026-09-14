# Go 行为树

Vue3 + TypeScript + Vue Flow 本地编辑器，Go 原生控制流生成器，以及由宿主串行队列驱动的运行时。独立 Go 模块为 `github.com/1xxz188/behaviortree`，使用 Go 1.26.7，不依赖原服务器框架或构建标签。JSON 只用于编辑和生成，运行中的 AI 不解析 JSON，也不创建每实例 goroutine 或轮询定时器。

## 启动编辑器

在本仓库根目录执行（本地目录为 `E:\fl\behaviortree`）：

```powershell
go run ./cmd/bttool serve --workspace ./.workspace
```

浏览器打开 <http://127.0.0.1:8791>。首次启动带一个可直接校验和生成的巡逻示例。点击节点库添加节点，从节点端点拖拽连线，在右侧配置属性和调整子节点顺序。节点移动和自动布局只改变画布坐标。支持复制、删除、撤销重做、多树与子树引用、工程 JSON 导入导出、业务节点目录导入、保存和重开。

`--workspace` 是外部工程目录：工程保存为顶层 JSON，Web 生成结果写到 `generated/<包名>/tree_gen.go` 和 `tree_gen.map.json`。草稿允许存在未连完的节点；校验成功后才能生成。点击问题列表可以定位节点。工具只监听回环地址，文件读写限制在指定工作目录中。

### 业务定义与动作绑定

默认巡逻示例仅包含控制节点和等待节点，业务目录为空。添加动作或条件后，在右侧点击“新建业务定义”，填写稳定定义 ID、显示名称、Go 函数名、参数及宿主事件，可保存并绑定当前节点。也可以点击“导入目录”选择 `examples/catalog.json` 或 `model.ExportCatalog` 导出的 JSON 数组。左侧“管理目录”用于编辑已有定义。

目录按定义 ID 合并：新 ID 追加，同 ID 的变更逐项选择保留现有或使用导入定义；未导入的现有定义保持不变。更新已有定义时会说明对节点和手写函数的影响，原节点参数保留供后续校验。Go 函数名冲突及非法参数会阻止提交。业务定义 ID 用于绑定，画布节点 ID 用于节点身份，两者独立。

节点 ID 保持只读；新增和复制使用完整 UUID，并在已占用集合中查重重试。日常重命名修改“名称”即可。导入工程仍要求节点 ID 在所属树内唯一，源节点由 TreeID + NodeID 标识。

### 预览、生成与源码定位

- **预览 Go**：使用正式生成器生成当前工程的完整源码，不创建目录或写入文件。校验失败自动显示可定位问题，旧源码仍保留。
- **生成到目录**：重新校验当前工程并写入源码及映射，显示实际路径和版本；与预览使用相同生成逻辑。
- **查看上次生成**：读取当前目标包的实际生成产物。重新打开已保存工程时也会自动读取，并核对是否对应当前工程；多个工程共用包名时，展示该目录最后一次生成的版本。
- **生成代码面板**：选中节点定位并高亮对应函数；点击有链接的代码行号返回画布。同一子树节点的多个展开位置分页展示。长文件仅渲染可见行。
- **业务骨架**：从目录和生成配置创建动作 Start/Resume/Abort、条件函数及 TODO。支持复制或下载 `actions.go`；参数结构体来自 `tree_gen.go`。骨架在未完成时返回 Failure/false，需要手写实现后共同编译。

修改行为、参数、名称或生成配置后，已有源码会标记过期并停用旧映射定位；仅移动节点、自动布局及其撤销不会使源码过期。撤销到原有内容时恢复有效标记。编辑期间的旧请求结果不能覆盖新工程，没有定时轮询或逐次输入自动生成。

源码映射格式统一包含 `sourceHash`（源码 SHA-256）、版本及节点位置，读取时核对源码与映射；不支持缺少摘要的旧产物。工程保存与代码生成是独立操作，生成源码不等于业务 Go 函数已实现或编译通过。

Go 代码不能在页面内任意编写或执行。业务动作留在 GoLand 中的手写文件，和生成文件使用同一个包；固定业务上下文可从另一个共享包导入。重复生成只覆盖标记为自动生成的文件，遇到手写 `tree_gen.go` 会拒绝覆盖。

## 独立发行与前端开发

已包含 `web/dist`，直接构建 Go 工具即可得到带完整离线页面的发行包：

```powershell
go build -o ./.build/bttool.exe ./cmd/bttool
.\.build\bttool.exe serve --workspace D:\BTProjects
```

Linux 构建时自行选择无 `.exe` 的输出名。发行工具不需要 Node.js；页面不依赖 CDN、远程字体或外部接口。修改前端时，在 `web` 下使用：

```sh
npm ci
npm run dev
npm test
npm run build
```

开发服务器通过 Vite 将 `/api` 代理到 `127.0.0.1:8791`，因此还需运行 Go 服务。发布顺序是先 `npm run build`，再 `go build`，否则 Go 会内嵌上一次的前端资源。开发验证使用 Node 22.17、npm 10.9.2、Go 1.26.7；前端依赖版本固定在锁文件中。

## CLI 和程序员工作流

以下命令均在本仓库根目录执行；发行后可以用 `bttool` 替换 `go run ./cmd/bttool`。

```powershell
# 首次使用 CLI 时创建工程目录。
New-Item -ItemType Directory -Force ./.workspace | Out-Null

# 初始化文件；同名文件存在时拒绝覆盖。
go run ./cmd/bttool init --file ./.workspace/new.json

# 校验和生成使用与 Web 相同的实现，失败返回非零退出码。
go run ./cmd/bttool validate --file ./.workspace/new.json
go run ./cmd/bttool generate --file ./.workspace/new.json --out ./.workspace/generated/example

# 查看项目内的 Go 节点目录；另可在 Go 中调用 model.ExportCatalog。
go run ./cmd/bttool catalog --file ./examples/project.json

# 运行不依赖数据库和外部服务的真实生成示例。
go run ./examples/host
```

可从 [Go 元数据声明](examples/definition/project.go)、[手写动作](examples/behavior/actions.go)、[生成代码](examples/behavior/tree_gen.go) 和 [宿主示例](examples/host/main.go) 开始接入。`scripts/generate` 重建默认示例，不能用于保存你对示例 JSON 的修改；编辑后的工程应交给 `bttool generate`。

动作函数的形状为 `func(*bt.Frame[Context], node int, phase bt.Phase, params ActionParams) bt.Status`；条件为 `func(*bt.Frame[Context], node int, params ConditionParams) bool`。参数结构体由 Go 元数据生成。`Start` 启动操作并快速返回 `Running`；`Resume` 用 `Frame.Consume(node)` 读取完成结果；`Abort` 撤销仍在进行的操作。动作需要主动捕获 `Frame.Token(node)`，外部完成事件携带该令牌回到宿主队列。

GoLand 直接打开本仓库根目录，使用 Go 1.26.7 工具链即可，无需额外构建标签。`tree_gen.map.json` 保留源 TreeID、NodeID、展开后的整数索引和 Go 行号；同一子树的多个调用位置有各自索引。当前没有 Web 断点、单步或 Delve 集成。

## 作为 Go 依赖使用

在业务项目的 Go 模块目录中添加依赖：

```sh
go get github.com/1xxz188/behaviortree
```

运行时包的导入方式为：

```go
import bt "github.com/1xxz188/behaviortree"
```

元数据、生成器和插件加载器分别位于 `github.com/1xxz188/behaviortree/model`、`github.com/1xxz188/behaviortree/codegen`、`github.com/1xxz188/behaviortree/hotload`。生成的代码直接引用该运行时模块；工程的 `generation.contextImport` 应填写业务项目自身的固定上下文包路径。生成文件与手写动作放在业务项目的同一个包内。宿主和插件必须使用相同版本的运行库及公共业务依赖。

从原服务器内的行为树包迁移时，需要更新业务 Go 导入路径和工程中的 `generation.contextImport`，再重新生成代码。本次模块身份改变，宿主与插件都必须重新构建，并重启宿主；旧模块生成的 `.so` 不能直接用于新的独立模块宿主。

## 宿主接入约定

| API | 用途与调用约束 |
| --- | --- |
| `NewRegistry(program)` | 校验并复制共享不可变描述，一组 AI 共用一个注册表 |
| `Registry.NewInstance(id, treeID, context, options)` | 只为该入口树分配连续状态；业务 Context 由宿主持有 |
| `Instance.Start()` | 显式启动新一轮；上一轮结束后才自动采用当前最新版本 |
| `Instance.Notify(event)` | 在所属队列通知显式依赖，按受影响条件或动作驱动 |
| `Instance.Complete(token, result)` | 在所属队列接收完成结果，拒绝重复、迟到、取消后和跨实例令牌 |
| `Instance.Tick()` | 仅推进脏路径，通常由宿主就绪队列调用；无需周期遍历 AI |
| `Instance.Abort(reason)` / `Close()` | 在所属队列取消当前轮，或关闭实例 |
| `Registry.Publish(program, policy)` | 管理入口发布完整版本；兼容校验失败保留当前有效版本 |
| `Registry.Stats()` / `Loader.LoadedCount()` | 分别查看各版本 Running 实例数和不可卸载插件数量 |

`Options.Post` 必须能够从不同线程安全投递到该实例所属串行队列，不能内联执行，不能在实例关闭前丢弃已接收任务。实例及黑板的所有读写都由这个队列串行执行。外部工作线程先投递，再调用 `Notify` 或 `Complete`，不要跨线程直接修改实例。

`Options.After` 接入宿主一次性定时服务，返回取消函数；框架把到期通知经 `Post` 投递回队列。需要 Wait/Timeout 的宿主必须提供此接口。实际服务器优先使用已有房间定时服务。业务 goroutine 的生命周期及 panic 处理遵守宿主工程的约定。

步数预算默认 1024；耗尽后合并一个就绪任务继续。预算控制同步节点推进，不会抢占一个阻塞的业务函数；业务动作必须保持非阻塞。执行中产生的字段或宿主事件通知先合并，在当前同步步骤返回后处理，避免重入。业务执行 panic 会清理活跃动作并将本轮标为失败，通过 `Instance.Error()` 和错误日志观察；日志接收者 panic 不会破坏发布和切换。

批量 AI 应使用 `Registry.NewInstance`。独立便利函数 `bt.NewInstance(program, ...)` 每次会单独校验并复制描述，适用于单个独立实例。

## 黑板和运行语义

黑板使用编译后的槽位和强类型 getter/setter，支持 bool、int64、uint64、float64、string、enum、entity、duration。枚举在 JSON 中使用字符串，并由生成的 setter 静态检查允许值；实体 ID 使用 uint64，duration 默认值与参数常量使用纳秒整数，Wait/Timeout 的 `durationMs` 使用毫秒。Web 对超出 JavaScript 安全整数范围的值使用无损 JSON 往返。

字段绑定由参数的 `field` 指定稳定字段 ID，常量由 `value` 指定，二者不能同时出现。绑定字段变化会通知依赖该字段的节点；条件依赖外部业务状态时，应在 Go 节点目录中声明 `Events`，由宿主显式 `Notify`，避免隐藏的轮询检查。

元数据的节点种类、业务目录种类、值类型，以及日志种类和构建模式使用 `uint8` 枚举。Go 声明使用 `model.NodeSequence`、`model.DefinitionAction`、`bt.IntType`、`bt.LogNode`、`hotload.BuildRelease` 等具名常量，不能直接赋字符串。值类型由运行时统一定义，模型字段及参数直接复用 `bt.ValueType`；业务目录种类与节点种类分别校验，不允许互换。

JSON 与日志仍使用 `"sequence"`、`"action"`、`"int64"` 等可读名称，实体类型仅接受 `"entity"`。新增枚举的零值无效；导入、保存、校验与生成入口拒绝缺失、空、null、数字和未知类型，错误提供字段位置。连线未完成等结构草稿仍可保存。业务枚举的允许值属于工程数据，继续使用字符串，并非这些固定种类枚举的整数编号。

构建契约的 `mode` 使用 `"release"` / `"debug"`，`cgoEnabled` 使用 JSON 布尔值；构建脚本仅在环境边界转换成 `CGO_ENABLED=0/1`。本次改造不保留旧 Go 接口或旧 CGO 字符串格式，升级时应重新生成代码，并用同一份运行库整体重编译宿主与插件后重启宿主。

字段新增或槽位改变时，仅在切换版本时按 ID 和类型迁移数据；改名保留 ID 不丢数据，同布局更新动作时保留黑板对象。删除字段、改变类型、移除已声明枚举值或更改公共业务接口需重启。黑板指针可能在迁移后更换，宿主应重新取得，不能长期缓存旧指针或把某版生成 getter 用于另一版实例。默认值变化仅作用于新建或新增字段，不覆盖已有值。

| 节点 | 固定语义 |
| --- | --- |
| Sequence / Selector | Running 时保留当前子节点；整体结束后下一次进入重新开始 |
| priority | 前面的候选为 Sequence，首个子节点必须是 Condition；最后一个候选可为无条件 fallback。依赖变化时重评，高优先级分支进入前先 Abort 旧分支 |
| Action / Condition | 动作支持 Start/Resume/Abort 和 Running；条件同步返回 bool |
| Parallel | 同线程顺序启动，全部成功才成功，任一失败即失败并取消剩余 Running 分支；恢复只访问脏分支 |
| Repeat / Retry | 有限次数，包含首次尝试；Running 不增加次数；Repeat 遇失败停止，Retry 遇成功停止 |
| Wait / Timeout | 宿主一次性通知；Timeout 到期取消子节点并失败 |
| Inverter / succeed / fail | 转换终态，Running 不变 |
| SubTree | 版本内引用，调用位置状态独立；禁止递归引用 |

校验覆盖重复 ID、环、多父节点、悬挂引用、不可达节点、子节点数量、未知类型、Go 名称冲突、参数类型及枚举范围。生成前预计算子树展开量，超过 100000 个调用位置拒绝生成。节点 ID 在所属树内唯一，TreeID + NodeID 标识源节点。

## 原生插件发布

完整说明和可执行三版本验收位于 [examples/README.md](examples/README.md)。固定 Linux/amd64、Go 1.26.7、CGO=1，宿主与插件必须对应 debug/release 参数。release 不使用构建标签；debug 由脚本统一设置 `debug` 标签和禁优化参数。

| 策略 | 何时采用新版本 | 代价与适用场景 |
| --- | --- | --- |
| `EndOfRound`，默认 | 当前根 Success/Failure 后，下一次 Start；新实例立即采用最新版本 | 不中断正在做的行为，普通发布不扫描 AI；长期 Running 可长期保留旧版本 |
| `CancelAndRestart` | 通过活跃实例索引分批投递，在各自队列 Abort 旧轮、迁移兼容黑板、启动新根 | 更新更及时，但业务必须正确取消外部操作，原轮进度被放弃 |

“整轮结束”指行为树根进入终态，**不是一次 Tick 函数返回**。Tick 返回 Running 时仍属于同一轮；不会把旧动作、新子树和新字段布局混入正在执行的一轮。两种策略都不能卸载 Go 插件，代码内存需要服务重启释放。[Go 官方 plugin 约束](https://pkg.go.dev/plugin)

每个插件具有唯一文件名及私有模块路径，可热更动作和 helper 进入该版本目录；运行库、业务上下文及共享依赖保持固定。构建脚本将宿主和所有版本放进同一个暂存 go.work，记录源码/接口指纹、编译参数与文件 SHA256，避免仅用本地 replace 导致公共包 BuildID 不同。加载检查 manifest、身份、校验和及 `BuildExporter` 精确签名后，由调用方校验并发布整个 Program。现有入口树 ID 不能在热更版本中移除，以保证旧实例下一轮仍有入口；移除入口需重启并调整宿主的实例创建逻辑。

## 日志、验证与边界

默认使用标准库 `slog` 输出发布、切换和错误等生命周期日志。注册表可通过 `SetLogger`，实例可通过 `Options.Logger` 接入宿主日志；`SetLogger(nil)` 显式关闭注册表日志，实例使用空接收函数可关闭日志。节点追踪默认关闭，可用 `Options.Trace` 或 `SetTrace` 按实例开启。关闭追踪时不构造节点日志字符串或黑板快照。记录包含版本、入口树、实例、节点 ID、展开索引、状态、原因和事件序号；子树源位置按 NodeIndex 查询源码映射。

```sh
go test -count=1 ./...
go vet ./...
```

查看 [性能基线](docs/PERFORMANCE.md)、[调研取舍](docs/RESEARCH.md) 和 [验收记录](docs/VERIFICATION.md)。验收记录区分独立仓库验证与迁移前的历史结果。Windows 支持编辑、生成和普通 Go 执行；原生插件需在 Linux 等受支持的平台验证，Windows 加载接口返回明确的不支持错误。

本仓库只包含独立框架、工具及完整示例，不包含原服务器的机器人业务。首版不兼容 behaviac XML 或 Behavior3 JSON，也不依赖原服务器的 go.work、日志包或 roommgr 热更入口。
