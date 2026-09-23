# 枚举改造与验证记录

> 本文记录 2026-09-14 的历史改造和测试数据。当前工程事件已改用 `bt.EventID` 数值枚举、Schema 4 事件注册表和生成常量；下文关于字符串事件名、`event:` 前缀及旧基准的描述不代表现行接口。

2026-09-14，Windows/amd64，Go 1.26.7，AMD Ryzen 5 9600X。

## 类型与格式

| 类型 | Go 表示 | 文本格式 |
| --- | --- | --- |
| `model.NodeType` | `uint8`，14 种节点 | `sequence`、`wait` 等 |
| `model.DefinitionKind` | 独立 `uint8` | `action` / `condition` |
| `bt.ValueType` | `uint8`，模型和运行时共用 | `bool`、`int64`、`uint64`、`float64`、`string`、`enum`、`entity`、`duration` |
| `bt.LogKind` | `uint8` | `publish`、`switch`、`error`、`node`、`abort` |
| `hotload.BuildMode` | `uint8` | `release` / `debug` |
| `Contract.CGOEnabled` | `bool` | JSON 布尔值，环境边界转换为 `0` / `1` |

这些新增整数枚举的零值均无效。Go 调用使用具名常量；JSON 不暴露整数编号，不接受未知值、空字符串、null、数字或缺失的必填类型。实体标签仅接受 `entity`。编辑器允许类型有效而结构未完成的草稿，完整拓扑、参数值和绑定仍由 Go 验证器检查。

前端通过字面量联合类型约束声明、节点表和类型选项，在导入与加载边界实际检查枚举值，并显示属性路径。业务自定义枚举、事件名、ID、版本及错误原因仍保留字符串语义。

## 性能结果

基准每组运行 100ms，共 3 次，表内为中位数；短时测量存在调度与 CPU 频率噪声，不作为整体运行性能提升的统计证明。

| 场景 | 改前 ns/op | 改后 ns/op | 分配变化 |
| --- | ---: | ---: | --- |
| 事件专项：短名称命中 | 47.86 | 39.63 | 均为 0 B / 0 alloc |
| 事件专项：长名称命中 | 82.47 | 45.92 | 192 B / 1 alloc → 0 B / 0 alloc |
| 事件专项：未知长名称 | 124.2 | 13.58 | 192 B / 1 alloc → 0 B / 0 alloc |
| Matrix：1000 AI、10 节点、idle | 2.929 | 3.044 | 均为 0 B / 0 alloc |
| Matrix：1000 AI、10 节点、event | 51.41 | 47.43 | 均为 0 B / 0 alloc |
| Matrix：1000 AI、10 节点、trace | 384.9 | 373.1 | 均为 0 B / 0 alloc |
| 创建实例：10 节点 | 564.6 | 591.6 | 均为 1568 B / 7 alloc |
| 切换版本：1000 AI、EndOfRound | 392969 | 403644 | 均为 3048 alloc |
| 切换版本：1000 AI、CancelAndRestart | 505683 | 515943 | 均为 4067 alloc |

事件专项的改进来自发布阶段移除 `event:` 前缀、通知阶段直接查找原始事件名，避免每次拼接。零分配回归覆盖长短名称、未知名称、与字段同名及包含协议前缀的业务名称。

`NodeType` 仅参与编辑、校验和生成；生成的执行代码已使用整数索引。`ValueType` 用于共享字段描述，黑板 getter/setter 不按字符串种类分派。因此不把事件优化收益归因于元数据枚举，也不声称每个实例内存随之减少。

复测命令：

```sh
go test . -run '^$' -bench '^BenchmarkNotifyEventName$' -benchmem -benchtime=100ms -count=3
go test . -run '^$' -bench '^BenchmarkMatrix/ai=1000$/nodes=10$/(idle|event|trace)$' -benchmem -benchtime=100ms -count=3
go test . -run '^$' -bench '^BenchmarkCreate/nodes=10$' -benchmem -benchtime=100ms -count=3
go test . -run '^$' -bench '^BenchmarkVersionSwitch/ai=1000$' -benchmem -benchtime=100ms -count=3
```

## 回归与交付

- 模型与运行时测试覆盖合法枚举往返、非法零值及越界值、JSON 错误输入、缺失类型、目录种类错配、日志文本及事件隔离。
- 编辑器测试覆盖导入、保存、加载、校验、生成和目录导入；未知类型不能保存或生成文件，未完成结构草稿仍能保存。
- 生成器测试实际编译并执行生成的 Go 代码，覆盖全部 14 种节点及 8 种值类型；描述表缺项不再默认为 duration。
- 前端 8 项测试与类型检查通过；浏览器验证 entity 字段的添加、保存、生成及重开，`18446744073709551615` 保持完整，生成 `uint64` 访问器。
- 示例生成产物已重新生成；Windows 工具和宿主重新构建，静态宿主演示通过。需要用同一份运行库整体重建插件并重启宿主，不提供旧 Go 接口或旧 CGO 字符串格式兼容层。
- 当前 Windows 未启用 WSL，也未安装 Docker，未执行本次原生 Linux `.so` 构建/加载。历史 `docs/linux-validation` 记录不能代替本次改造的原生插件验收。

本次结构及引用检查使用 gopls、codegraph MCP，并由模型/生成器、运行时、构建契约三个子代理分别实施和复核。
