import { parseJSON, stringifyJSON } from "./json.ts";

export interface Value {
  field?: string;
  value?: unknown;
}
export interface Field {
  id: string;
  name: string;
  type: string;
  default?: unknown;
  enum?: string[];
}
export interface Parameter {
  name: string;
  type: string;
  default?: unknown;
  enum?: string[];
}
export interface Definition {
  id: string;
  name: string;
  kind: string;
  goName: string;
  params?: Parameter[];
  events?: string[];
}
export interface BTNode {
  id: string;
  type: string;
  name?: string;
  children?: string[];
  binding?: string;
  params?: Record<string, Value>;
  tree?: string;
  count?: number;
  durationMs?: number;
}
export interface Position {
  x: number;
  y: number;
}
export interface Tree {
  id: string;
  name: string;
  root: string;
  nodes: BTNode[];
  layout?: Record<string, Position>;
}
export interface Project {
  schemaVersion: number;
  name: string;
  blackboard: Field[];
  catalog: Definition[];
  trees: Tree[];
  generation: { package: string; contextImport: string; contextType: string };
}
export interface Diagnostic {
  treeId?: string;
  nodeId?: string;
  field?: string;
  message: string;
}

export const kinds: Record<
  string,
  { label: string; icon: string; color: string; help: string }
> = {
  sequence: {
    label: "顺序",
    icon: "→",
    color: "mint",
    help: "依次执行，任一失败即失败。Running 时保留当前子节点。",
  },
  selector: {
    label: "选择",
    icon: "?",
    color: "mint",
    help: "依次尝试，任一成功即成功。Running 时保留当前分支。",
  },
  priority: {
    label: "优先级",
    icon: "⇡",
    color: "mint",
    help: "事件触发条件重评，高优先级分支可取消并抢占当前动作。",
  },
  parallel: {
    label: "并行",
    icon: "⇉",
    color: "mint",
    help: "同线程推进；全部成功才成功，任一失败则取消剩余动作。",
  },
  condition: {
    label: "条件",
    icon: "◇",
    color: "amber",
    help: "调用 Go 条件函数；依赖变化时通知观察者。",
  },
  action: {
    label: "动作",
    icon: "▶",
    color: "blue",
    help: "调用 Go 动作函数，支持开始、恢复和取消。",
  },
  wait: {
    label: "等待",
    icon: "◷",
    color: "blue",
    help: "宿主一次性定时器到期后成功，无循环轮询。",
  },
  timeout: {
    label: "超时",
    icon: "⌛",
    color: "purple",
    help: "超时后取消子节点并返回失败。",
  },
  repeat: {
    label: "重复",
    icon: "↻",
    color: "purple",
    help: "成功后重复，达到指定次数才成功，失败立即结束。",
  },
  retry: {
    label: "重试",
    icon: "⟳",
    color: "purple",
    help: "失败后重试，成功立即结束；次数包含首次尝试。",
  },
  inverter: {
    label: "取反",
    icon: "¬",
    color: "purple",
    help: "互换成功与失败，Running 保持不变。",
  },
  succeed: {
    label: "强制成功",
    icon: "✓",
    color: "purple",
    help: "子节点结束时强制返回成功。",
  },
  fail: {
    label: "强制失败",
    icon: "×",
    color: "purple",
    help: "子节点结束时强制返回失败。",
  },
  subtree: {
    label: "子树",
    icon: "↳",
    color: "blue",
    help: "引用同项目另一棵树；调用实例状态独立。",
  },
};

// 深拷贝保持编辑历史与当前工程隔离。
export function clone<T>(value: T): T {
  return parseJSON<T>(stringifyJSON(value));
}
export function uid(prefix = "node"): string {
  return `${prefix}_${crypto.randomUUID().replaceAll("-", "").slice(0, 12)}`;
}

// 删除节点时同时移除父子连接，保留其余子节点作为可继续编辑的草稿。
export function removeNodes(tree: Tree, ids: string[]): void {
  const removed = new Set(ids);
  tree.nodes = tree.nodes.filter((n) => !removed.has(n.id));
  for (const node of tree.nodes)
    node.children = (node.children ?? []).filter((id) => !removed.has(id));
  for (const id of ids) if (tree.layout) delete tree.layout[id];
  if (removed.has(tree.root)) tree.root = "";
}

// 连接只改显式 children 顺序，画布坐标不参与业务优先级。
export function connect(
  tree: Tree,
  parent: string,
  child: string,
): string | null {
  const byID = new Map(tree.nodes.map((n) => [n.id, n]));
  const p = byID.get(parent);
  if (!p || !byID.has(child) || parent === child)
    return "不能连接到自身或不存在的节点";
  if (["wait", "action", "condition", "subtree"].includes(p.type))
    return "叶节点不能连接子节点";
  if ((p.children ?? []).includes(child)) return "连接已存在";
  if (tree.nodes.some((n) => n.children?.includes(child)))
    return "节点只能有一个父节点";
  if (
    !["sequence", "selector", "priority", "parallel"].includes(p.type) &&
    p.children?.length
  )
    return "装饰节点只能有一个子节点";
  const stack = [child],
    seen = new Set<string>();
  while (stack.length) {
    const id = stack.pop()!;
    if (id === parent) return "连接会形成循环";
    if (seen.has(id)) continue;
    seen.add(id);
    stack.push(...(byID.get(id)?.children ?? []));
  }
  p.children = [...(p.children ?? []), child];
  if (tree.root === child) tree.root = parent;
  return null;
}

// 只修改布局；遇到草稿中的环也能有界结束。
export function autoLayout(tree: Tree): void {
  const byID = new Map(tree.nodes.map((n) => [n.id, n]));
  const seen = new Set<string>();
  const positions: Record<string, Position> = {};
  let row = 0;
  function place(id: string, depth: number): number {
    if (seen.has(id) || !byID.has(id)) return row;
    seen.add(id);
    const children = (byID.get(id)!.children ?? []).filter(
      (c) => !seen.has(c) && byID.has(c),
    );
    let y: number;
    if (!children.length) y = row++ * 115 + 70;
    else {
      const ys = children.map((c) => place(c, depth + 1));
      y = (ys[0]! + ys[ys.length - 1]!) / 2;
    }
    positions[id] = { x: depth * 260 + 70, y };
    return y;
  }
  place(tree.root, 0);
  for (const n of tree.nodes)
    if (!seen.has(n.id)) {
      row++;
      place(n.id, 0);
    }
  tree.layout = positions;
}

export function emptyProject(): Project {
  const tree: Tree = {
    id: "patrol",
    name: "巡逻示例",
    root: "root",
    nodes: [
      {
        id: "root",
        type: "sequence",
        name: "巡逻流程",
        children: ["pause", "again"],
      },
      { id: "pause", type: "wait", name: "观察周围", durationMs: 500 },
      {
        id: "again",
        type: "repeat",
        name: "巡视三次",
        count: 3,
        children: ["walk"],
      },
      { id: "walk", type: "wait", name: "等待移动完成", durationMs: 1000 },
    ],
  };
  autoLayout(tree);
  return {
    schemaVersion: 1,
    name: "巡逻行为",
    blackboard: [],
    catalog: [],
    trees: [tree],
    generation: { package: "patrol", contextImport: "", contextType: "any" },
  };
}
