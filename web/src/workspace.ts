// 工作目录返回绝对路径及顶层候选文件；读取时才验证工程内容。
export interface WorkspaceFiles {
  workspace: string; // 服务端实际使用的绝对目录。
  files: string[]; // 可按名称打开的 JSON 候选文件。
}

// 浏览器存储只记录成功打开的文件名，不保存工程内容。
export interface RecentStorage {
  getItem(key: string): string | null; // 读取本目录最近工程。
  setItem(key: string, value: string): void; // 写入本目录最近工程。
}

// 通过服务端目录隔离历史，同一端口切换工作目录不会串用记录。
export function recentProjectKey(workspace: string): string {
  return `bttool:recent-project:${workspace}`;
}

// 单候选直接打开；多个候选只恢复仍存在的历史项，不擅自选第一个。
export function startupProject(list: WorkspaceFiles, storage?: RecentStorage): string | undefined {
  if (list.files.length === 1) return list.files[0];
  try {
    const recent = storage?.getItem(recentProjectKey(list.workspace));
    return recent && list.files.includes(recent) ? recent : undefined;
  } catch {
    return undefined; // 禁用本地存储时仍可手动选择工程。
  }
}

// 记忆失败不影响工程的正常打开或保存。
export function rememberProject(workspace: string, name: string, storage?: RecentStorage): void {
  try {
    storage?.setItem(recentProjectKey(workspace), name);
  } catch {
    // 浏览器隐私模式或存储空间不足时忽略历史记录。
  }
}

// 文件名限制与服务端顶层 JSON 文件规则一致，禁止将路径当作文件名。
export function validProjectFileName(name: string): boolean {
  return name.length > 0 && !name.startsWith(".") && !/[\\/:]/.test(name) && /\.json$/i.test(name);
}

// Windows 工作目录按不区分大小写识别同一文件，避免另存为漏掉覆盖确认。
export function projectFileKey(workspace: string, name: string): string {
  return /^(?:[a-z]:[\\/]|\\\\)/i.test(workspace) ? name.toLowerCase() : name;
}
