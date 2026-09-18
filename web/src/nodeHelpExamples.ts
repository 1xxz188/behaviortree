import type { NodeType } from "./enums.ts";

// 说明弹窗中的静态示例节点，仅用于展示，不进入工程数据。
export interface HelpExampleNode {
  type: NodeType; // 用于复用画布节点的类型名称、图标和颜色。
  label: string; // 示例中该节点的业务名称。
  detail?: string; // 节点绑定或关键配置的简短说明。
  children?: HelpExampleNode[]; // 按执行顺序排列的本地子节点。
}

// 与文字场景配套的示例树及阅读提示。
export interface HelpExample {
  root: HelpExampleNode; // 示例画布的根节点。
  caption: string; // 解释结构、执行顺序或引用关系。
}

// 按类型直接读取，穷举约束确保每类内置节点都有对应画布示例。
export const nodeHelpExamples: Record<NodeType, HelpExample> = {
  sequence: {
    root: {
      type: "sequence", label: "拾取流程", children: [
        { type: "condition", label: "检查目标有效" },
        { type: "action", label: "移动到目标" },
        { type: "action", label: "拾取物品" },
      ],
    },
    caption: "三个步骤是顺序节点的同级子节点，按编号依次执行；移动成功后才拾取。",
  },
  selector: {
    root: {
      type: "selector", label: "选择攻击方式", children: [
        { type: "action", label: "近战攻击" },
        { type: "action", label: "远程攻击" },
        { type: "action", label: "待机" },
      ],
    },
    caption: "按编号尝试同级方案；近战失败才尝试远程，任一成功就结束选择。",
  },
  priority: {
    root: {
      type: "priority", label: "状态优先级", children: [
        { type: "sequence", label: "逃跑分支", children: [
          { type: "condition", label: "低血量？", detail: "血量 < 30" },
          { type: "action", label: "逃跑" },
        ] },
        { type: "sequence", label: "战斗分支", children: [
          { type: "condition", label: "发现敌人？" },
          { type: "action", label: "攻击" },
        ] },
        { type: "action", label: "巡逻", detail: "无条件兜底" },
      ],
    },
    caption: "分支编号越小优先级越高；前两个顺序分支以条件为首节点，血量变化可使逃跑抢占巡逻。",
  },
  parallel: {
    root: {
      type: "parallel", label: "集合与通知", children: [
        { type: "action", label: "移动到集合点" },
        { type: "action", label: "播放语音" },
      ],
    },
    caption: "两个动作在同一线程中推进，等待全部成功；移动失败会取消尚未结束的语音。",
  },
  condition: {
    root: {
      type: "sequence", label: "低血量逃跑", children: [
        { type: "condition", label: "低血量？", detail: "IsHealthLow · 阈值 30" },
        { type: "action", label: "逃跑" },
      ],
    },
    caption: "条件与逃跑是同级步骤；血量为 20 时条件成功并逃跑，为 80 时流程失败。",
  },
  action: {
    root: { type: "action", label: "移动到目标", detail: "MoveTo · 目标来自黑板" },
    caption: "动作是叶节点：开始移动后等待到达通知，再恢复并成功；被抢占时取消旧移动。",
  },
  wait: {
    root: {
      type: "sequence", label: "对话选项流程", children: [
        { type: "action", label: "播放对话" },
        { type: "wait", label: "停顿半秒", detail: "500 ms" },
        { type: "action", label: "显示选项" },
      ],
    },
    caption: "三个步骤依次执行；对话成功后等待 500 ms，再显示选项。",
  },
  timeout: {
    root: {
      type: "timeout", label: "路径规划时限", detail: "2000 ms", children: [
        { type: "action", label: "请求路径规划" },
      ],
    },
    caption: "超时节点只包装一个动作；超过 2 秒仍未完成时取消请求并失败。",
  },
  repeat: {
    root: {
      type: "repeat", label: "重复巡查", detail: "总共执行 3 次", children: [
        { type: "sequence", label: "一轮巡查", children: [
          { type: "action", label: "巡查一圈" },
          { type: "wait", label: "巡查间隔", detail: "1000 ms" },
        ] },
      ],
    },
    caption: "重复节点的唯一子节点是一整轮顺序流程；三轮全部成功才结束，任意一轮失败则停止。",
  },
  retry: {
    root: {
      type: "selector", label: "开门与备用方案", children: [
        { type: "retry", label: "重试开门", detail: "最多尝试 3 次", children: [
          { type: "action", label: "尝试开门" },
        ] },
        { type: "action", label: "执行备用方案" },
      ],
    },
    caption: "尝试次数包含首次；任一次开门成功就结束，连续失败三次才进入外层选择节点的备用方案。",
  },
  inverter: {
    root: {
      type: "sequence", label: "搜索流程", children: [
        { type: "inverter", label: "判断目标不可见", children: [
          { type: "condition", label: "目标可见？" },
        ] },
        { type: "action", label: "搜索目标" },
      ],
    },
    caption: "取反与搜索是顺序节点的同级步骤；目标不可见时取反成功，才继续搜索。",
  },
  succeed: {
    root: {
      type: "sequence", label: "任务提交流程", children: [
        { type: "succeed", label: "允许提示失败", children: [
          { type: "action", label: "播放提示音" },
        ] },
        { type: "action", label: "提交任务" },
      ],
    },
    caption: "提示音结束后，无论成功还是失败都会继续提交任务；提示音仍在运行时继续等待。",
  },
  fail: {
    root: {
      type: "selector", label: "记录后执行备用", children: [
        { type: "fail", label: "记录后返回失败", children: [
          { type: "action", label: "记录首次尝试" },
        ] },
        { type: "action", label: "执行备用方案" },
      ],
    },
    caption: "记录结束后第一分支总是失败，让选择节点继续备用方案；记录仍在运行时继续等待。",
  },
  subtree: {
    root: { type: "subtree", label: "返回出生点", detail: "引用目标树：返回出生点" },
    caption: "此节点引用另一棵树，不连接本地子节点。巡逻树和战斗树可分别放置此引用，各自保留调用进度。",
  },
};
