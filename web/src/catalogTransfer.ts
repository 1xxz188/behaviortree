import type { Definition, Project } from "./project.ts";
import { exportCatalogPackage, parseCatalogPackage } from "./catalog.ts";
import type { CatalogPackage } from "./catalog.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 交换包在导入和导出时共用 Go 校验，保留原声明顺序及大整数默认值。
export async function validateCatalog(value: CatalogPackage): Promise<CatalogPackage> {
  const candidate = parseCatalogPackage(value);
  const response = await fetch("/api/catalog", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: stringifyJSON(candidate),
  });
  const text = await response.text();
  let result: unknown;
  try { result = parseJSON(text); }
  catch { throw new Error(`业务定义校验服务返回无效 JSON（${response.status}）`); }
  if (!response.ok) {
    const detail = result as { error?: string } | null;
    throw new Error(detail?.error || `业务定义校验失败（${response.status}）`);
  }
  parseCatalogPackage(result);
  // Go 返回值可能省略空字段；导出保留原声明的无损副本，避免无意义格式变化。
  return candidate;
}

// 单条和全部复制、下载共用此入口，始终返回可重新导入的自包含对象。
export async function validatedCatalogJSON(project: Project, definitions: Definition[]): Promise<string> {
  return stringifyJSON(await validateCatalog(exportCatalogPackage(project, definitions)), 2);
}
