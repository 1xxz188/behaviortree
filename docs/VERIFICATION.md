# 验收记录

本文件分别记录独立仓库迁移后的验证与迁移前历史证据。当前模块为 `github.com/1xxz188/behaviortree`，仓库根目录为 `E:\fl\behaviortree`；构建、运行和测试无需原服务器构建标签。

## 独立仓库迁移后验证

2026-09-12，在独立模块根目录重新执行下列检查；这些结果不沿用迁移前记录。

| 范围 | 当前命令或检查 | 结果 |
| --- | --- | --- |
| Go 全目录回归 | `go test -count=1 ./...` | Windows 62 项顶层测试通过，14 个包；Linux 亦通过 |
| 静态检查 | `go vet ./...` | Windows、Linux 均通过 |
| 独立工具构建 | `go build -o ./.build/bttool.exe ./cmd/bttool` | Windows、Linux 均通过；Windows 启动后首页 HTTP 200，能读取已迁移工程列表 |
| 默认示例生成及运行 | `go run ./scripts/generate`；`go run ./examples/host` | 通过；示例及源码映射已重建，动作完成后根返回 Success |
| CLI 与生成代码 | init → validate → generate；独立模块导入与生成结果编译 | 通过，生成包实际编译成功 |
| 前端 | 在 `web` 执行 `npm test`、`npm run build` | 4 项测试通过；类型检查与生产构建通过 |
| 插件源码准备 | `go run ./scripts/builddemo -prepare` | 通过，生成四个版本与独立暂存工作区 |
| Linux release/debug 原生插件 | `go run ./scripts/builddemo`；`go run ./scripts/builddemo -mode debug` | 两套均通过实际三版本连续加载；release 宿主按预期拒绝 debug 插件 |
| Linux race | `go test -race -count=1 ./...` | 通过 |
| 干净目录验证 | 仅导出 Git 待提交文件，未带入 `.workspace`、`node_modules` 或旧二进制 | 工具构建、README CLI 流程、生成包编译和宿主运行全部通过 |
| 完整迁移 | 源目录移除、独立模块导入、运行工作目录、生成器与文档路径 | 全部内容迁入新仓库，旧路径已不存在；原服务器仓库工作区干净 |

上述命令均在本仓库根目录运行，前端命令除外。Linux 构建可执行文件时使用不带 `.exe` 的输出名。真实插件验证要求 Linux/amd64、Go 1.26.7、CGO=1 和可用 C 编译器；release 无构建标签，debug 由脚本统一设置 `debug` 标签和禁优化参数。

实际移动前后均为 1936 个文件、84766826 字节，包含被 Git 忽略的原本地工程、构建产物和前端依赖；这些本地文件保留在新目录但不提交。Go 模块仅依赖标准库和自身包，`go mod tidy` 未产生外部依赖或 go.sum。原有目标仓库的 `.git` 与远端配置保持不变。

本轮尝试了 gopls 和 codegraph：前者仍绑定原服务器工作区，无法为新路径建立有效包诊断；后者明确返回独立仓库未索引。因此独立仓库以实际 Go 编译、测试与 vet 结果验收。子代理 `migration_audit` 完成耦合点审查、文档更新和最终只读复核。

本次 Linux 使用 `/data/xxz/bt/standalone-9fuNeSzr`，在用户指定测试目录内与旧验收分开。源码归档 SHA256 为 `2af334281e4b19dda8edbbb3e83747900d218e571ee4799d43b69380af03eb22`，上传后核对一致；归档生成后仅继续完善本地文档和 Git 属性，Go 源码未再改变。系统 Go 未替换，复测可先执行 `source /data/xxz/bt/standalone-9fuNeSzr/env.sh`，再使用上方独立仓库命令。

本次原始日志：[环境与归档校验](linux-validation/standalone-env.log)、[Go 测试](linux-validation/standalone-tests.log)、[vet](linux-validation/standalone-vet.log)、[release 插件](linux-validation/standalone-plugin-release.log)、[debug 插件](linux-validation/standalone-plugin-debug.log)、[跨构建模式拒绝](linux-validation/standalone-cross-mode.log)、[race](linux-validation/standalone-race.log)。两套原生插件流程均实际修改树、动作和 helper，验证两种切换策略、长期 Running、过期回调、错误签名和不匹配契约。原生插件集成与 race 是分别执行的检查，生成器测试内部临时 Go 子进程未追加 `-race`。

## 迁移前：Windows 与功能验收

历史开发主机：2026-09-12，Windows/amd64，Go 1.26.7，CGO_ENABLED=0，Node 22.17，npm 10.9.2。以下测试在原服务器 `appfw` 模块内完成，并使用当时的服务器构建配置。迁移改变了模块身份、导入路径和构建参数，旧结果仅作为历史证据。

| 范围 | 证据 |
| --- | --- |
| Go 全目录回归 | 在原 appfw 模块验证全部行为树包，Windows 62 项顶层测试通过，涉及 14 个包；Linux 对应测试亦通过 |
| 静态检查 | 原 appfw 模块内的行为树包 `go vet` 通过 |
| 生成代码 | 生成器测试把工程输出到临时 Go 包，实际编译和运行；不是只比较源码字符串 |
| CLI | 实际 init → validate → generate，产物通过 Go 编译；编辑器测试另验证手写文件不能被覆盖 |
| 示例宿主 | 原示例宿主完成 Start → Running → Complete → Resume → Success，输出结构化节点日志 |
| 前端 | `npm test` 四项通过；`npm run build` 完成 TypeScript 检查和 Vite 生产打包 |
| 浏览器编辑 | 实际修改节点名称、调整 children 顺序、撤销重做、重复保存并重开，确认名称和顺序保留 |
| 浏览器导入 | 用文件选择器导入真实 examples/project.json，业务目录和黑板字段进入界面，Go 校验通过 |
| 独立内嵌工具 | Go 可执行文件提供首页、CSS、JS 和全部 API；浏览器从该服务重开工程并生成 Go，未使用 Vite 资源 |
| 窄窗口 | 800 像素宽度下属性面板可打开、编辑和关闭；验证后恢复浏览器默认尺寸 |
| 日志与页面工具 | 浏览器没有发现 error/warn；WebMCP 工程读取、共同 Go 校验工具均成功调用 |
| 性能 | 子代理执行完整 30 组矩阵，全部稳态零分配，详见 PERFORMANCE.md；主代理独立复核万 AI / 千节点的 idle/event/Parallel 三组 |
| 插件构建准备 | 私有源码暂存、AST 引用重写及构建指纹测试通过；已核对统一暂存 go.work 下宿主和插件的共享包 BuildID 相同 |

运行语义覆盖 Running 续跑与终态重置、优先级抢占及 false→true 重新进入、Parallel 取消和单分支恢复、有限 Repeat/Retry、Wait/Timeout、低预算深树、子树调用状态隔离、重复/迟到/取消后/跨实例完成通知。黑板覆盖类型与枚举、变化通知、追加字段、保留 ID 的改名、不兼容变更拒绝和跨版本状态保留。

版本测试覆盖普通边界切换、分批取消重启、连续发布和陈旧任务、长期 Running、活跃实例统计、业务及日志 panic、跨轮延迟 Abort。执行中的字段或事件通知先合并，避免业务重入。

历史复核选择的基准为 `BenchmarkMatrix/ai=10000/nodes=1000/(idle|event|parallel)`，固定 10000 次，启用内存统计。独立仓库对应的复测命令为：

```powershell
go test -run '^$' -bench 'BenchmarkMatrix/ai=10000$/nodes=1000$/(idle|event|parallel)$' -benchtime=10000x -benchmem .
```

历史结果分别为 38.93 / 128.5 / 115.7 ns/op，均 0 B/op、0 allocs/op；实际节点访问量分别 0 / 2 / 2，每实例主要内存 112353 字节。数字与完整矩阵的差异来自重复采样，不替换性能报告的原始一轮记录，也不是上方独立模块命令的新测量。

## 迁移前：Linux 原生插件与 race

测试服务器为用户提供的 `10.0.3.10`，独立测试目录 `/data/xxz/bt/run-WO1ylfth/appfw`。Linux/amd64，内核 4.18.0，GCC 13.2.0，CGO=1。系统原有 Go 1.26.3 没有替换；Go 1.26.7 工具链及构建缓存放在 `/data/xxz/bt/cache`，只在测试进程设置 PATH/GOROOT。归档包含新框架、原始 appfw/go.mod、go.sum 和本地 SDK replace 的 go.mod，没有同步整个服务器业务源码。

当时上传源码归档 SHA256 为 `43a2672859c33d8aef1f07b7502713e71d534cdad0fd0b0a8a7eee1848e16520`，解压前在 Linux 核对一致。该归档对应迁移前源码，不是当前独立模块的源码归档。下面的原始日志保持原文，其包名、路径和构建参数均属于原环境。

| 检查 | 结果 | 原始日志 |
| --- | --- | --- |
| 全目录 Go 测试 | 退出码 0 | [tests.log](linux-validation/tests.log) |
| release 原生插件 | 退出码 0，同进程三个成功版本及一个错误签名插件 | [plugin-release.log](linux-validation/plugin-release.log) |
| debug 原生插件 | 退出码 0，使用对应 debug 标签和禁优化参数完成同样流程 | [plugin-debug.log](linux-validation/plugin-debug.log) |
| release 宿主加载 debug 产物 | 按预期退出码 1，在加载前拒绝不匹配的构建契约 | [cross-mode.log](linux-validation/cross-mode.log) |
| 原模块全目录 race 测试 | 退出码 0 | [race.log](linux-validation/race.log) |
| 原模块全目录静态检查 | 退出码 0，无诊断 | [环境与产物目录](linux-validation/environment.log) |

两套原生插件测试均实际改变树结构、动作和私有 helper；验证默认整轮切换、取消并重启、新实例采用新版本、旧 Running 固定旧版本、取消后令牌失效，以及错误签名和构建不匹配不改变已发布版本。已打开数量为 4，包括无法卸载的错误签名插件。

当时的环境脚本位于 `/data/xxz/bt/run-WO1ylfth/env.sh`，旧产物保留在其 appfw 目录的 `third/behaviortree/.build`，具体 release/debug 目录见环境日志。该脚本切换到旧模块，只用于历史追溯；当前独立模块应在自己的检出根目录使用上方新命令。原生插件集成使用普通 release/debug 构建；race 是另外执行的 Go 测试，生成器测试内部的临时 Go 子进程未追加 `-race`。Go 官方指出插件 race 支持存在局限，因此本结果不代表所有插件竞争都能被检测。[Go plugin 文档](https://pkg.go.dev/plugin)

## 尚未确认的浏览器回执

已点击 JSON 导出，当前内嵌浏览器测试接口未返回下载事件，因此没有把下载落盘标为通过。导出使用标准 Blob 下载；无损序列化、实际保存文件和重新导入均已验证。可在实际使用的浏览器执行「导出 JSON → 选择下载文件导入」确认往返。当前没有实现多人协同、认证或在线发布。

迁移前 gopls 对新增目录未报告错误，但返回了原服务器其他服务已有的诊断，以及部分工作区构建配置诊断。未据此声明原服务器整个仓库可构建，也未修改这些无关文件。独立仓库不包含这些服务或原多模块依赖同步脚本。

迁移前实现协作：runtime_impl、compiler_impl、plugin_impl；主代理负责编辑器、CLI、集成复核与文档。使用 gopls、codegraph、一手资料 Web 浏览和 CUA/WebMCP 页面验收；前置调研还包含仓库热更、behaviac 和开源框架三个独立探索子任务。
