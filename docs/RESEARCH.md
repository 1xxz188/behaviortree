# 选型调研与落实

调研对象为用户指定的本地 behaviac 3.6.38、原服务器仓库的 roommgr 热更入口，以及开源项目的一手文档。实现现已迁移为独立模块 `github.com/1xxz188/behaviortree`，不依赖原服务器框架、工作区或日志构建标签。以下是本项目的设计取舍，不表示这些框架之间存在统一性能排名；未对不同语言框架做同机压测。

| 参考 | 可借鉴做法 | 本实现采用的范围 |
| --- | --- | --- |
| 腾讯 behaviac | 行为定义与 BehaviorTask 实例状态分离、稳定节点 ID、动作进入/执行/退出生命周期 | 共享 Program + 连续实例状态；原生 Go 函数保留节点执行边界与映射 |
| Unreal Engine | 观察者驱动的条件变化和分支中断，减少每帧全树重评 | 显式字段/事件依赖、按入口树编译观察者表、仅唤醒受影响实例和分支 |
| BehaviorTree.CPP | 非阻塞 Running、异步动作可 halt、清晰的同步与异步边界 | Start/Resume/Abort、过期令牌过滤、取消后再抢占；不引入 C++ 运行时 |
| Behavior3 Editor | Web 图编辑和项目数据导入导出 | 自有版本化 JSON + Vue3 工程，不兼容旧编辑器格式 |
| Go 标准 plugin | 原生代码可直接使用共享数据结构 | 全版本构建/加载约束、私有包隔离、两种队列安全切换策略 |

## 本地源码依据

以下路径记录初次调研时的本地源码位置，供原环境溯源，不是本仓库的依赖或运行前提。

- [behaviac 定义](E:/fl/develop/behaviac-3.6.38/inc/behaviac/behaviortree/behaviortree.h) 与 [实例任务](E:/fl/develop/behaviac-3.6.38/inc/behaviac/behaviortree/behaviortree_task.h)：共享结构与每实例状态分开；本项目继续减少逐节点临时对象。
- [behaviac workspace](E:/fl/develop/behaviac-3.6.38/src/common/workspace.cpp)：工作区加载、变更和实例处理。其现有重载机制不是本项目的 Go 插件发布单元。
- [roommgr/hotfix.go](E:/fl/develop/server/server_go/appfw/mapcore/roommgr/hotfix.go)：借鉴宿主持有状态、插件提供逻辑的边界。行为树另设完整 Program 发布，不依赖该入口。

调研发现 behaviac C++ 导出依然创建和连接运行时节点图，因此“导出源码”不等同于生成原生控制流。本项目生成基于 TreeID + NodeID 命名的 `btNode…` 函数及直接分支调用，槽位用语义化整数常量表示；节点描述只用于状态范围、依赖索引和日志定位。

## 一手资料

- [Unreal 行为树概览](https://dev.epicgames.com/documentation/en-us/unreal-engine/behavior-tree-in-unreal-engine---overview)：事件驱动与 Blackboard observer 思路。本项目要求宿主显式通知，不推测 Go 业务状态变化。
- [BehaviorTree.CPP 异步动作指南](https://www.behaviortree.dev/docs/guides/asynchronous_nodes/)：Running 必须快速返回，取消需要释放当前工作。本项目不为动作自动创建线程。
- [Behavior3 Editor 源码](https://github.com/behavior3/behavior3editor)：图编辑与 JSON 工程管理的参考。
- [Vue Flow](https://vueflow.dev/guide/)：Vue3 节点、边、自定义节点及交互组件。应用自己维护有序 children，不让画布坐标决定执行优先级。
- [Go embed](https://pkg.go.dev/embed) 与 [Vite 构建](https://vite.dev/guide/build)：开发时独立前端热更新，发行时内嵌静态文件，工程数据保留在外部工作目录。
- [Go plugin](https://pkg.go.dev/plugin)：平台、工具链与公共依赖必须匹配，首次加载会运行 init，插件无法卸载。这些约束由构建工具和加载契约落实；不能完全回滚 init 副作用。

## 对比结论

Vue3 + Go 内嵌满足当前本地工程管理、离线运行和单工具交付。独立前后端部署适合将来的集中协作服务，但首版没有账号、多人同步和集中发布需求。桌面封装可增加原生窗口集成，当前浏览器已经承载所需编辑功能。

相比解释 JSON，原生 Go 控制流具有静态参数检查、直接业务调用及可读源码定位的优势；代价是每次逻辑变化都需生成、编译和发布，版本私有包管理更严格。执行热点不依赖反射或字符串节点查找，但整棵树运行成本仍取决于实际执行节点数，不能承诺整树 O(1)。

默认整轮切换保证正在执行的动作和子树保持同一版本，代价是长期 Running 可能延后更新。取消并重启更及时，代价是中断原轮，业务必须实现可取消动作。每 Tick 直接换函数表会混合状态和代码版本，因此本项目不提供这种策略。
