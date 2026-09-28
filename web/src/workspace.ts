// 工作目录返回绝对路径及可读取的工程，另保留完整候选供覆盖确认。
export interface WorkspaceFiles {
  workspace: string; // 服务端实际使用的绝对目录。
  files: string[]; // 通过工程读取校验的 JSON 文件。
  allFiles?: string[]; // 包括无效工程在内的同名覆盖候选。
}

// 调用本地服务打开系统目录窗口；选择阶段只返回目录信息，不切换或写文件。
export async function selectNativeDirectory(workspace: string, initial = workspace, fetcher: typeof fetch = fetch): Promise<WorkspaceFiles | undefined> {
  const response = await fetcher("/api/directory-picker", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-BT-Workspace": encodeURIComponent(workspace) },
    body: JSON.stringify({ directory: initial }),
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || "无法打开系统目录选择窗口");
  return result.cancelled ? undefined : result;
}

// 对话框返回明确的保存位置，或未保存修改的处理选择。
export type ProjectDialogResult = { name: string; directory: string; overwrite: boolean } | "save" | "discard";

// 页签记录只包含目录及已保存文件身份，不保存未落盘的工程内容。
export interface TabSession {
  workspace: string; // 服务端已确认的绝对目录。
  fileName: string; // 空字符串表示此页签没有已打开的磁盘工程。
}

// 使用 sessionStorage 所需的最小接口，存储不可用时编辑器仍可工作。
export interface SessionStorage {
  getItem(key: string): string | null; // 读取当前页签身份。
  setItem(key: string, value: string): void; // 写入当前页签身份。
}

export const tabSessionKey = "bttool:tab-session";

// 保留旧版按目录记录最近工程的键；仅无页签身份时用于新页签启动。
export function recentProjectKey(workspace: string): string {
  return `bttool:recent-project:${workspace}`;
}

// 无效或旧版记录不参与恢复，由服务端继续提供默认目录。
export function readTabSession(storage?: SessionStorage): TabSession | undefined {
  try {
    const value = storage?.getItem(tabSessionKey);
    if (!value) return undefined;
    const session: unknown = JSON.parse(value);
    if (typeof session !== "object" || session === null) return undefined;
    const { workspace, fileName } = session as Partial<TabSession>;
    return typeof workspace === "string" && workspace.length > 0 && typeof fileName === "string"
      ? { workspace, fileName } : undefined;
  } catch {
    return undefined;
  }
}

// 仅在目录、打开或保存成功后记录身份；存储错误不影响操作结果。
export function rememberTabSession(workspace: string, fileName: string, storage?: SessionStorage): void {
  try {
    storage?.setItem(tabSessionKey, JSON.stringify({ workspace, fileName } satisfies TabSession));
  } catch {
    // 隐私模式或存储空间不足时仍允许手动选择目录和工程。
  }
}

// 最近工程只在成功打开或保存时更新，不跨目录查询或保存工程内容。
export function rememberProject(workspace: string, name: string, storage?: SessionStorage): void {
  try {
    storage?.setItem(recentProjectKey(workspace), name);
  } catch {
    // 浏览器禁止本地存储时仍允许手动选择。
  }
}

// 页签身份优先；无有效页签身份时恢复该目录的最近工程，再考虑单候选。
export function startupProject(list: WorkspaceFiles, session?: TabSession, recentStorage?: SessionStorage): string | undefined {
  if (session?.workspace === list.workspace)
    return session.fileName && list.files.includes(session.fileName) ? session.fileName : undefined;
  try {
    const recent = recentStorage?.getItem(recentProjectKey(list.workspace));
    if (recent) return list.files.includes(recent) ? recent : undefined;
  } catch {
    // 读取历史失败时仍可打开默认目录中的唯一工程。
  }
  return list.files.length === 1 ? list.files[0] : undefined;
}

// 文件名限制与服务端顶层 JSON 文件规则一致，禁止将路径当作文件名。
export function validProjectFileName(name: string): boolean {
  return name.length > 0 && !name.startsWith(".") && !/[\\/:]/.test(name) && /\.json$/i.test(name);
}

// Windows 工作目录按不区分大小写识别同一文件，避免另存为漏掉覆盖确认。
export function projectFileKey(workspace: string, name: string): string {
  return /^(?:[a-z]:[\\/]|\\\\)/i.test(workspace) ? name.toLowerCase() : name;
}
