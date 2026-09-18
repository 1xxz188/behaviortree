import type { Definition, Project } from "./project.ts";

// 目录以稳定 ID 关联父级；空父 ID 表示固定根目录。
export interface CatalogFolder {
  id: string; // 工程内稳定目录 ID。
  name: string; // 同级唯一的显示名称。
  parentId: string; // 父目录 ID，根为 ""。
  order: number; // 与同级定义混合排序的位置。
}
// 标签是工程共享的交叉分类，名称全局唯一。
export interface CatalogTag {
  id: string; // 工程内稳定标签 ID。
  name: string; // 标签显示名称。
}
// 每个定义只拥有一个目录归属，可关联多个标签。
export interface CatalogAssignment {
  folderId: string; // 所属目录，根为 ""。
  tagIds: string[]; // 无重复的标签 ID 集合。
  order: number; // 同级混合排序位置。
}
// 组织信息仅随工程保存，不进入独立定义库或生成语义。
export interface CatalogOrganization {
  folders: CatalogFolder[]; // 自定义目录表。
  tags: CatalogTag[]; // 工程共享标签表。
  assignments: Record<string, CatalogAssignment>; // 按定义 ID 索引的分类表。
}
// 同级列表统一表示目录与定义，方便只重排受影响的列表。
export interface CatalogEntry {
  kind: "folder" | "definition"; // 条目类型，防止同名 ID 混淆。
  id: string; // 对应稳定 ID。
  order: number; // 混合排序位置。
}

// JSON 对象检查不能依赖原型，允许 __proto__ 等合法业务 ID。
function object(value: unknown, path: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error(`${path}: 应为对象`);
  return value as Record<string, unknown>;
}
// 可选集合与 Go 的 omitempty/null 空集合表示保持一致。
function array(value: unknown, path: string): unknown[] {
  if (value == null) return [];
  if (!Array.isArray(value)) throw new Error(`${path}: 应为数组`);
  return value;
}
// 标识使用原值，名称判重时统一裁剪以避免隐形重名。
function string(value: unknown, path: string, empty = false): string {
  if (typeof value !== "string" || (!empty && !value.trim())) throw new Error(`${path}: 应为${empty ? "" : "非空"}字符串`);
  return value;
}
// 输入排序允许有限数值；本地重排写入紧凑整数。
function order(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) throw new Error(`${path}: 应为有限数值`);
  return value;
}
// 名称统一去除两端空白，比较时忽略英文大小写。
function nameKey(name: string): string { return name.trim().toLowerCase(); }

// 输入边界一次构建映射，以 O(目录数 + 关联数) 验证引用和环，深层目录不递归。
export function validateCatalogOrganization(catalog: readonly Pick<Definition, "id">[], value: unknown): void {
  if (value == null) return;
  const organization = object(value, "catalogOrganization");
  const folders = new Map<string, string>();
  const names = new Set<string>();
  for (const [i, item] of array(organization.folders, "catalogOrganization.folders").entries()) {
    const path = `catalogOrganization.folders[${i}]`, folder = object(item, path);
    const id = string(folder.id, `${path}.id`), parent = string(folder.parentId, `${path}.parentId`, true);
    const name = string(folder.name, `${path}.name`);
    if (folders.has(id)) throw new Error(`${path}.id: 目录 ID 重复`);
    const key = JSON.stringify([parent, nameKey(name)]);
    if (names.has(key)) throw new Error(`${path}.name: 同级目录名称重复`);
    names.add(key); folders.set(id, parent); order(folder.order, `${path}.order`);
  }
  for (const [id, parent] of folders) if (parent && !folders.has(parent)) throw new Error(`目录 ${id}: 父目录不存在`);
  const complete = new Set<string>();
  for (const start of folders.keys()) {
    const chain = new Set<string>();
    let id = start;
    while (id && !complete.has(id)) {
      if (chain.has(id)) throw new Error(`目录 ${id}: 存在目录环`);
      chain.add(id); id = folders.get(id)!;
    }
    for (const item of chain) complete.add(item);
  }
  const tags = new Set<string>(), tagNames = new Set<string>();
  for (const [i, item] of array(organization.tags, "catalogOrganization.tags").entries()) {
    const path = `catalogOrganization.tags[${i}]`, tag = object(item, path);
    const id = string(tag.id, `${path}.id`), name = string(tag.name, `${path}.name`);
    if (tags.has(id)) throw new Error(`${path}.id: 标签 ID 重复`);
    if (tagNames.has(nameKey(name))) throw new Error(`${path}.name: 标签名称重复`);
    tags.add(id); tagNames.add(nameKey(name));
  }
  const definitions = new Set(catalog.map((definition) => definition.id));
  for (const [id, item] of Object.entries(organization.assignments == null ? {} : object(organization.assignments, "catalogOrganization.assignments"))) {
    const path = `catalogOrganization.assignments[${JSON.stringify(id)}]`, assignment = object(item, path);
    if (!definitions.has(id)) throw new Error(`${path}: 定义不存在`);
    const folder = string(assignment.folderId, `${path}.folderId`, true);
    if (folder && !folders.has(folder)) throw new Error(`${path}: 目录不存在`);
    const seen = new Set<string>();
    for (const item of array(assignment.tagIds, `${path}.tagIds`)) {
      const tag = string(item, `${path}.tagIds`);
      if (!tags.has(tag)) throw new Error(`${path}: 标签不存在`);
      if (seen.has(tag)) throw new Error(`${path}: 标签关联重复`);
      seen.add(tag);
    }
    order(assignment.order, `${path}.order`);
  }
}

// 缓存目录关系、混合子条目和标签成员；实例只绑定当前工程，恢复快照后重新构建。
export class CatalogOrganizationIndex {
  readonly project: Project; // 当前工程原对象，操作保持 Vue 响应式引用。
  readonly folders = new Map<string, CatalogFolder>(); // 目录常数时间定位。
  readonly tags = new Map<string, CatalogTag>(); // 标签常数时间定位。
  readonly definitions = new Map<string, Definition>(); // 业务定义常数时间定位。
  readonly assignments = new Map<string, CatalogAssignment>(); // 含未持久化的默认根归属。
  readonly children = new Map<string, CatalogEntry[]>(); // 每个目录的混合顺序列表。
  readonly tagMembers = new Map<string, Set<string>>(); // 标签倒排索引。
  private folderNames = new Map<string, Map<string, string>>(); // 同级名称索引。
  private tagNames = new Map<string, string>(); // 全局标签名称索引。
  private pendingDefaults = new Set<string>(); // 首次编辑时一次性固化根缺省顺序，避免保存后次序漂移。

  // 构造只读取工程，不因打开面板产生未保存修改。
  constructor(project: Project) {
    this.project = project;
    validateCatalogOrganization(project.catalog, project.catalogOrganization);
    this.children.set("", []);
    for (const folder of project.catalogOrganization?.folders ?? []) {
      this.folders.set(folder.id, folder); this.children.set(folder.id, []);
      this.names(folder.parentId).set(nameKey(folder.name), folder.id);
    }
    for (const folder of this.folders.values()) this.entries(folder.parentId).push({ kind: "folder", id: folder.id, order: folder.order });
    for (const tag of project.catalogOrganization?.tags ?? []) {
      this.tags.set(tag.id, tag); this.tagNames.set(nameKey(tag.name), tag.id); this.tagMembers.set(tag.id, new Set());
    }
    const defaults: Definition[] = [];
    for (const definition of project.catalog) {
      this.definitions.set(definition.id, definition);
      const stored = project.catalogOrganization?.assignments;
      const assignment = stored && Object.hasOwn(stored, definition.id) ? stored[definition.id] : undefined;
      if (!assignment) { defaults.push(definition); continue; }
      this.assignments.set(definition.id, assignment);
      this.entries(assignment.folderId).push({ kind: "definition", id: definition.id, order: assignment.order });
      for (const tag of assignment.tagIds ?? []) this.tagMembers.get(tag)!.add(definition.id);
    }
    for (const entries of this.children.values()) entries.sort((a, b) => a.order - b.order);
    let nextOrder = this.nextOrder("");
    for (const definition of defaults) {
      this.assignments.set(definition.id, { folderId: "", tagIds: [], order: nextOrder });
      this.entries().push({ kind: "definition", id: definition.id, order: nextOrder++ });
      this.pendingDefaults.add(definition.id);
    }
  }
  // 固定根和自定义目录使用相同列表接口；不存在的目录及时失败。
  entries(folderId = ""): CatalogEntry[] {
    const entries = this.children.get(folderId);
    if (!entries) throw new Error("目标目录不存在");
    return entries;
  }
  // 空目录判断直接读取子条目数量，不遍历全部定义。
  isFolderEmpty(id: string): boolean { return this.entries(id).length === 0; }
  // 沿目标父链检查，复杂度仅与目录深度相关。
  canMoveFolder(id: string, target: string): boolean {
    if (!this.folders.has(id) || (target !== "" && !this.folders.has(target))) return false;
    for (let parent = target; parent; parent = this.folders.get(parent)!.parentId) if (parent === id) return false;
    return true;
  }
  // 路径按需迭代构造，避免深树递归栈溢出。
  folderPath(folderId: string): string {
    const parts: string[] = [];
    for (let id = folderId; id;) {
      const folder = this.folders.get(id);
      if (!folder) throw new Error("目录不存在");
      parts.push(folder.name); id = folder.parentId;
    }
    return ["根目录", ...parts.reverse()].join(" / ");
  }
  // 定义结果展示其当前目录路径。
  definitionPath(id: string): string { return this.folderPath(this.assignment(id).folderId); }
  // 标签 AND 优先遍历最小倒排集合；无标签时线性搜索一次定义库。
  search(query = "", tagIds: readonly string[] = []): Definition[] {
    const text = query.trim().toLowerCase();
    const groups = [...new Set(tagIds)].map((id) => this.tagMembers.get(id) ?? new Set<string>()).sort((a, b) => a.size - b.size);
    const candidates = groups.length ? [...groups[0]].map((id) => this.definitions.get(id)!) : this.definitions.values();
    return [...candidates].filter((definition) => groups.every((group) => group.has(definition.id)) && (!text || [definition.name, definition.id, definition.goName].some((value) => value.toLowerCase().includes(text))));
  }
  // 创建目录时追加同级末尾，用户名称在此统一裁剪。
  addFolder(name: string, parentId = "", id: string = crypto.randomUUID()): CatalogFolder {
    this.entries(parentId); name = this.checkedName(name, this.names(parentId));
    if (!id.trim() || this.folders.has(id)) throw new Error("目录 ID 无效或重复");
    const folder = { id, name, parentId, order: this.nextOrder(parentId) };
    this.organization().folders.push(folder); this.folders.set(id, folder); this.children.set(id, []);
    this.names(parentId).set(nameKey(name), id); this.entries(parentId).push({ kind: "folder", id, order: folder.order });
    return folder;
  }
  // 重命名仅更新目标目录及名称索引。
  renameFolder(id: string, name: string): void {
    const folder = this.folder(id), names = this.names(folder.parentId);
    name = this.checkedName(name, names, id); names.delete(nameKey(folder.name)); names.set(nameKey(name), id); folder.name = name;
  }
  // 只允许删除空自定义目录，根目录始终不能删除。
  deleteFolder(id: string): void {
    const folder = this.folder(id);
    if (!this.isFolderEmpty(id)) throw new Error("只能删除空目录，请先移出定义和子目录");
    const organization = this.organization(); organization.folders.splice(organization.folders.indexOf(folder), 1);
    const entries = this.entries(folder.parentId); entries.splice(entries.findIndex((entry) => entry.kind === "folder" && entry.id === id), 1);
    this.names(folder.parentId).delete(nameKey(folder.name)); this.folderNames.delete(id); this.children.delete(id); this.folders.delete(id);
  }
  // 混合移动在验证全部约束后提交；只重写来源与目标同级顺序。
  move(entry: Pick<CatalogEntry, "kind" | "id">, targetFolderId: string, before?: Pick<CatalogEntry, "kind" | "id">): void {
    const target = this.entries(targetFolderId);
    const folder = entry.kind === "folder" ? this.folder(entry.id) : undefined;
    const assignment = folder ? undefined : this.assignment(entry.id);
    const sourceId = folder?.parentId ?? assignment!.folderId;
    if (folder) {
      if (!this.canMoveFolder(folder.id, targetFolderId)) throw new Error("不能将目录移入自身或后代");
      this.checkedName(folder.name, this.names(targetFolderId), folder.id);
    }
    if (before && !target.some((item) => item.kind === before.kind && item.id === before.id)) throw new Error("排序目标不存在");
    if (before?.kind === entry.kind && before.id === entry.id) return;
    const source = this.entries(sourceId), offset = source.findIndex((item) => item.kind === entry.kind && item.id === entry.id);
    if (offset < 0) throw new Error("移动条目不存在");
    const [item] = source.splice(offset, 1);
    const position = before ? target.findIndex((candidate) => candidate.kind === before.kind && candidate.id === before.id) : target.length;
    target.splice(position, 0, item);
    if (folder) { this.names(sourceId).delete(nameKey(folder.name)); this.names(targetFolderId).set(nameKey(folder.name), folder.id); folder.parentId = targetFolderId; }
    else assignment!.folderId = targetFolderId;
    this.persistOrder(source); if (source !== target) this.persistOrder(target);
  }
  // 新建标签与任何业务定义无关，不改变生成语义。
  addTag(name: string, id: string = crypto.randomUUID()): CatalogTag {
    name = this.checkedName(name, this.tagNames);
    if (!id.trim() || this.tags.has(id)) throw new Error("标签 ID 无效或重复");
    const tag = { id, name }; this.organization().tags.push(tag); this.tags.set(id, tag); this.tagNames.set(nameKey(name), id); this.tagMembers.set(id, new Set()); return tag;
  }
  // 标签改名不需要逐个修改定义关联。
  renameTag(id: string, name: string): void {
    const tag = this.tag(id); name = this.checkedName(name, this.tagNames, id);
    this.tagNames.delete(nameKey(tag.name)); tag.name = name; this.tagNames.set(nameKey(name), id);
  }
  // 删除标签只遍历使用该标签的定义，保留定义和其他标签。
  deleteTag(id: string): void {
    const tag = this.tag(id), members = this.tagMembers.get(id)!;
    for (const definitionId of members) {
      const assignment = this.assignment(definitionId); assignment.tagIds = assignment.tagIds.filter((tagId) => tagId !== id); this.persist(definitionId);
    }
    const organization = this.organization(); organization.tags.splice(organization.tags.indexOf(tag), 1);
    this.tags.delete(id); this.tagNames.delete(nameKey(tag.name)); this.tagMembers.delete(id);
  }
  // 设置单个定义标签时同步倒排索引，防止筛选结果使用过期关联。
  setTags(id: string, tagIds: readonly string[]): void {
    const assignment = this.assignment(id), ids = [...new Set(tagIds)];
    for (const tag of ids) this.tag(tag);
    for (const tag of assignment.tagIds ?? []) this.tagMembers.get(tag)!.delete(id);
    assignment.tagIds = ids; for (const tag of ids) this.tagMembers.get(tag)!.add(id); this.persist(id);
  }
  // 新定义加入 catalog 后调用；已有定义分类保持不变，新定义依输入顺序追加。
  assignNewDefinitions(ids: readonly string[], folderId = ""): void {
    const target = this.entries(folderId), requested = new Set(ids);
    const moving: string[] = [], sources = new Set<string>();
    // 先确定整批新增定义，不能让首条持久化其他缺省项后改变后续条目的判断。
    for (const definition of this.project.catalog) if (requested.has(definition.id)) {
      this.definitions.set(definition.id, definition);
      if (!Object.hasOwn(this.project.catalogOrganization?.assignments ?? {}, definition.id)) {
        moving.push(definition.id);
        const assignment = this.assignments.get(definition.id);
        if (assignment) sources.add(assignment.folderId);
      }
    }
    if (!moving.length) return;
    const movingIDs = new Set(moving);
    // 整批压缩一次来源列表；新定义只会来自隐式根，避免每条分别移动的平方成本。
    for (const source of sources) {
      const entries = this.entries(source);
      let output = 0;
      for (const entry of entries) if (entry.kind !== "definition" || !movingIDs.has(entry.id)) entries[output++] = entry;
      entries.length = output;
    }
    for (const id of moving) {
      const assignment = this.assignments.get(id) ?? { folderId, tagIds: [], order: 0 };
      assignment.folderId = folderId; assignment.order = this.nextOrder(folderId); this.assignments.set(id, assignment);
      target.push({ kind: "definition", id, order: assignment.order });
    }
    for (const source of sources) if (source !== folderId) this.persistOrder(this.entries(source));
    this.persistOrder(target);
  }
  // 删除定义前清理分类；调用方负责业务引用诊断和移除 catalog 声明。
  removeDefinition(id: string): void {
    const assignment = this.assignments.get(id); if (!assignment) return;
    for (const tag of assignment.tagIds ?? []) this.tagMembers.get(tag)!.delete(id);
    const entries = this.entries(assignment.folderId); entries.splice(entries.findIndex((entry) => entry.kind === "definition" && entry.id === id), 1);
    if (this.project.catalogOrganization?.assignments) delete this.project.catalogOrganization.assignments[id];
    this.assignments.delete(id); this.definitions.delete(id);
    this.pendingDefaults.delete(id);
  }
  // 仅在真正编辑时创建持久化组织对象，避免读取产生脏状态。
  private organization(): CatalogOrganization {
    const organization = this.project.catalogOrganization ??= { folders: [], tags: [], assignments: Object.create(null) };
    organization.folders ??= []; organization.tags ??= []; organization.assignments ??= Object.create(null);
    // 首次写入任意分类时固化隐式根条目，否则部分显式条目会在重开时越过缺省条目。
    if (this.pendingDefaults.size) {
      for (const id of this.pendingDefaults) Object.defineProperty(organization.assignments, id, { value: this.assignment(id), enumerable: true, writable: true, configurable: true });
      this.pendingDefaults.clear();
    }
    return organization;
  }
  // 持久化定义归属时安全定义自身属性，防止特殊 ID 改写对象原型。
  private persist(id: string): void {
    Object.defineProperty(this.organization().assignments, id, { value: this.assignment(id), enumerable: true, writable: true, configurable: true });
  }
  // 受影响同级使用紧凑整数次序，其他目录不被重写。
  private persistOrder(entries: CatalogEntry[]): void {
    entries.forEach((entry, order) => { entry.order = order; if (entry.kind === "folder") this.folder(entry.id).order = order; else { this.assignment(entry.id).order = order; this.persist(entry.id); } });
  }
  // 列表始终有序，末位读取无需重新扫描全部定义。
  private nextOrder(folderId: string): number { const entries = this.entries(folderId); return entries.length ? entries[entries.length - 1].order + 1 : 0; }
  // 名称索引按父目录懒初始化，不写工程数据。
  private names(parent: string): Map<string, string> { let names = this.folderNames.get(parent); if (!names) { names = new Map(); this.folderNames.set(parent, names); } return names; }
  // 改名允许名称仍属于自身，其他重复或空名称拒绝。
  private checkedName(name: string, names: Map<string, string>, ownId?: string): string {
    name = name.trim(); if (!name) throw new Error("名称不能为空");
    const existing = names.get(nameKey(name)); if (existing !== undefined && existing !== ownId) throw new Error("名称重复"); return name;
  }
  // 根目录没有可编辑记录，因此在目录操作边界自然禁止改名和删除。
  private folder(id: string): CatalogFolder { const folder = this.folders.get(id); if (!folder) throw new Error("目录不存在或为固定根目录"); return folder; }
  // 缺失定义不允许通过分类操作隐式创建。
  private assignment(id: string): CatalogAssignment { const assignment = this.assignments.get(id); if (!assignment) throw new Error("定义不存在"); return assignment; }
  // 标签操作统一检查目标仍属于当前工程。
  private tag(id: string): CatalogTag { const tag = this.tags.get(id); if (!tag) throw new Error("标签不存在"); return tag; }
}
