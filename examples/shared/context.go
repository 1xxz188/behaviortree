// Package shared 定义宿主与所有插件共享、热更期间不改变的业务 ABI。
package shared

import bt "github.com/1xxz188/behaviortree"

// Context 由宿主持有；真实项目可在这里放房间、角色及服务接口。
type Context struct {
	Token  bt.CallbackToken // Token 保存示例异步请求令牌，回调必须回到宿主队列。
	Events []string         // Events 收集示例执行记录，生产环境应使用业务日志。
	Starts int              // Starts 统计异步动作启动次数。
	Aborts int              // Aborts 统计取消次数。
}
