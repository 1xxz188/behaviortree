import type { Definition } from "./project.ts";
import { mergeCatalog, parseCatalog } from "./catalog.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

// 独立定义在导入和导出时共用 Go 校验，保留原声明顺序及大整数默认值。
export async function validateCatalog(definitions: Definition[]): Promise<Definition[]> {
  const candidate = mergeCatalog([], parseCatalog(definitions));
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
  parseCatalog(result);
  // Go 返回值可能省略空字段；导出保留原声明的无损副本，避免无意义格式变化。
  return candidate;
}

// 单条和全部复制、下载共用此入口，始终返回可重新导入的格式化 JSON 数组。
export async function validatedCatalogJSON(definitions: Definition[]): Promise<string> {
  return stringifyJSON(await validateCatalog(definitions), 2);
}
