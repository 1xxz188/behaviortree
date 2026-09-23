<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import type { Definition, Parameter, EventDefinition, Project } from "./project";
import type { DefinitionKind, ValueType } from "./enums";
import { valueTypes } from "./enums";
import { PARAM_TYPE_META, parameterTypeTooltip } from "./parameterTypeMeta";
import { parseJSON, stringifyJSON } from "./json";
import { catalogConflicts, eventConflicts, exportCatalogPackage, mergeCatalog, mergeCatalogPackage, parseCatalog, parseCatalogPackage, prepareImportedEvents } from "./catalog";
import type { CatalogChoice, CatalogPackage } from "./catalog";
import { validateCatalog } from "./catalogTransfer";
import { normalizeEventDescription, validateEventRegistry } from "./eventRegistry";
import EventMultiSelect from "./EventMultiSelect.vue";
import CatalogBrowser from "./CatalogBrowser.vue";
import type { CatalogOrganizationIndex } from "./catalogOrganization";

const props = withDefaults(defineProps<{
  catalog: Definition[]; // 当前工程目录；提交成功前不修改。
  project: Project; // 事件交换和依赖选择使用工程统一注册表。
  projectRevision: number; // 任意工程事务均使旧草稿与导入预览失效。
  initialKind?: DefinitionKind; // 从节点属性打开时，预选种类并允许绑定。
  initialMode?: "create" | "edit" | "import" | "manage"; // 新建与编辑使用独立工作页。
  initialDefinition?: Definition; // 右键入口指定的已有定义，表单只编辑其副本。
  index?: CatalogOrganizationIndex; // 复用工程分类索引，避免重复构建。
  revision?: number; // 分类事务完成后更新筛选结果。
  initialFolder?: string; // 使用侧栏当前目录作为新增位置和初始筛选。
  disabled?: boolean; // 父级事务期间禁止重复操作。
  commit?: (change: () => void) => boolean; // 所有分类操作共用撤销事务。
  failureMessage?: string; // 父级事务错误在当前窗口展示。
  transferBusy?: boolean; // 导出进行中禁用重复提交。
}>(), { initialMode: "manage" });
const emit = defineEmits<{
  apply: [catalog: Definition[], bindID?: string, events?: EventDefinition[], enumDescription?: string, nextEventId?: string];
  manageEvents: [];
  close: [];
  select: [folderId: string];
  transfer: [operation: "copy" | "download"];
  menu: [event: MouseEvent | KeyboardEvent, definition: Definition];
}>();

// 参数编辑使用字符串保存尚未完成的输入，提交时才解析 JSON 默认值。
interface ParameterDraft {
  key: number; // 仅用于稳定定位表单行。
  name: string; // Go 参数成员名。
  type: ValueType; // 参数值类型。
  comment: string; // 参数字段注释，支持多行用途说明。
  defaultText: string; // 空白表示无默认值。
  enumText: string; // 每行一个枚举值。
}
const mode = ref(props.initialMode);
const activeTab = ref<"definitions" | "directories" | "tags">("definitions"); // 管理页导航与操作按钮分离。
// 从任意表单返回时统一显示定义列表，保留已选目录与搜索条件。
watch(mode, value => { if (value === "manage") activeTab.value = "definitions"; });
const query = ref(""); // 仅筛选定义名称、ID 和业务函数。
const kindFilter = ref<"" | DefinitionKind>(""); // 空值代表全部种类。
const selectedFolder = ref(props.initialFolder ?? ""); // 目录筛选只包含直属定义。
const organizationBrowser = ref<InstanceType<typeof CatalogBrowser>>(); // 隐藏时仍保留共用分类表单宿主。
const exportMenu = ref<HTMLDetailsElement>(); // 导出后收起菜单，避免遮挡定义列表。
const folderChoices = computed(() => {
  props.revision;
  return [{ id: "", name: "根目录" }, ...Array.from(props.index?.folders.values() ?? [], folder => ({ id: folder.id, name: props.index!.folderPath(folder.id) }))];
});
// 先通过目录索引取得直属定义；筛选结果按响应式输入缓存，不在模板逐行重复搜索。
const filteredDefinitions = computed(() => {
  props.revision;
  const needle = query.value.trim().toLocaleLowerCase();
  const candidates = props.index
    ? props.index.entries(selectedFolder.value && props.index.folders.has(selectedFolder.value) ? selectedFolder.value : "")
      .filter(entry => entry.kind === "definition").map(entry => props.index!.definitions.get(entry.id)!)
    : props.catalog;
  return candidates.filter(item => (!kindFilter.value || item.kind === kindFilter.value)
    && (!needle || `${item.name}\n${item.id}\n${item.goName}`.toLocaleLowerCase().includes(needle)));
});
// 撤销、删除或切换工程后修复目录选择，不保留失效 ID。
watch(() => [props.index, props.revision], () => {
  if (selectedFolder.value && !props.index?.folders.has(selectedFolder.value)) selectFolder("");
});
// 选择目录同时更新父级新增与导入归属。
function selectFolder(id: string) { selectedFolder.value = id; emit("select", id); }
// 分类修改仅交由父级事务执行，未连接工程时保持只读。
function commitOrganization(change: () => void) { return props.commit?.(change) ?? false; }
// 父级定义菜单复用目录页的分类操作表单。
function openMoveDefinition(id: string) { organizationBrowser.value?.openMoveDefinition(id); }
// 标签关联与侧栏调用同一表单和事务。
function openDefinitionTags(id: string) { organizationBrowser.value?.openDefinitionTags(id); }
// 导出始终作用于全部定义，与当前筛选无关。
function transfer(operation: "copy" | "download") {
  if (props.disabled || props.transferBusy || !props.catalog.length) return;
  if (exportMenu.value) exportMenu.value.open = false;
  emit("transfer", operation);
}
const editingID = ref("");
const draft = ref({ id: "", name: "", kind: props.initialKind ?? "action", goName: "", eventIds: [] as string[] });
const draftRevision = ref(props.projectRevision); // 提交时拒绝工程或注册表变化后的旧表单。
const parameters = ref<ParameterDraft[]>([]);
let parameterKey = 0;
const incoming = ref<Definition[]>([]);
const incomingPackage = ref<CatalogPackage | null>(null);
const eventMappings = ref<Record<string, string>>(Object.create(null));
const eventRenames = ref<Record<string, string>>(Object.create(null));
const importEnumDescription = ref(false);
const choices = ref<Record<string, CatalogChoice | "">>(Object.create(null));
const acknowledged = ref(false);
const bindNew = ref(Boolean(props.initialKind));
const error = ref("");
const busy = ref(false);
const fileLabel = ref("");
const importSource = ref<"paste" | "file">("paste"); // 文件和粘贴共用同一份待解析文本。
const importText = ref(""); // 尚未提交的原始 JSON，可反复编辑和解析。
const previewReady = ref(false); // 只有显式解析成功后才显示预览。
const previewValidated = ref(false); // 最终合并结果校验通过后允许应用。
const importRevision = ref(0); // 工程或输入更新后丢弃已过期的异步校验结果。
const dialog = ref<HTMLElement>();
// 页面切换销毁触发按钮后，将焦点带回标题区域，避免落入背景画布。
watch(mode, async () => { await nextTick(); dialog.value?.focus(); });
const preparedEvents = computed(() => {
  if (!incomingPackage.value) return null;
  try { return prepareImportedEvents(props.project, incomingPackage.value,
    new Map(Object.entries(eventMappings.value).filter(([, id]) => id)),
    new Map(Object.entries(eventRenames.value).filter(([, name]) => name))); }
  catch { return null; }
});
const mappedIncoming = computed(() => preparedEvents.value?.definitions ?? incoming.value);
const conflictRows = computed(() => catalogConflicts(props.catalog, mappedIncoming.value));
const eventConflictRows = computed(() => eventConflicts(props.project.events, incomingPackage.value?.events ?? []));
const enumDescriptionConflict = computed(() => normalizeEventDescription(incomingPackage.value?.eventEnumDescription, "eventEnumDescription") !== normalizeEventDescription(props.project.eventEnumDescription, "eventEnumDescription"));
const codeNameCollisionRows = computed(() => {
  const current = new Map(props.project.events.map(item => [item.codeName.toLowerCase(), item]));
  return (incomingPackage.value?.events ?? []).filter(item => {
    const sameID = props.project.events.find(other => other.id === item.id);
    return current.has(item.codeName.toLowerCase()) && (!sameID || eventConflicts([sameID], [item]).length > 0);
  });
});
const previewRows = computed(() => {
  const existing = new Set(props.catalog.map(item => item.id));
  const conflicts = new Set(conflictRows.value.map(item => item.id));
  return incoming.value.map(item => ({ item, status: !existing.has(item.id) ? "新增" : conflicts.has(item.id) ? "冲突" : "相同" }));
});
const previewCounts = computed(() => {
  const counts = { added: 0, same: 0, conflict: 0 };
  for (const row of previewRows.value) counts[row.status === "新增" ? "added" : row.status === "相同" ? "same" : "conflict"]++;
  return counts;
});
const pendingChoices = computed(() => conflictRows.value.some(row => !choices.value[row.id]) || codeNameCollisionRows.value.some(row => !eventMappings.value[row.id] && !eventRenames.value[row.id]));
const updatesExisting = computed(() => conflictRows.value.some((row) => choices.value[row.id] === "replace"));
const dialogTitle = computed(() => mode.value === "edit" ? `编辑业务定义：${draft.value.name || editingID.value}`
  : mode.value === "create" ? "新建业务定义" : mode.value === "import" ? "导入业务定义" : "业务节点管理"); // 标题明确区分修改已有定义和新增定义。

// 修改输入立即作废旧预览及确认；同步监听防止同一事件内误提交旧数据。
watch(importText, resetPreview, { flush: "sync" });
watch(() => props.catalog, resetPreview);
watch(() => props.projectRevision, resetPreview);

// 输入或工程发生变化后，必须重新解析而不能沿用旧冲突选择。
function resetPreview() {
  importRevision.value++;
  incoming.value = [];
  incomingPackage.value = null;
  choices.value = Object.create(null);
  eventMappings.value = Object.create(null);
  eventRenames.value = Object.create(null);
  importEnumDescription.value = false;
  acknowledged.value = false;
  previewReady.value = false;
  previewValidated.value = false;
  error.value = "";
}

// 切换页面时清空上次待提交内容，防止误应用其他工作页的导入。
function changeMode(next: "create" | "edit" | "import" | "manage") {
  if (busy.value || props.disabled) return;
  mode.value = next;
  incoming.value = [];
  choices.value = Object.create(null);
  acknowledged.value = false;
  error.value = "";
  editingID.value = "";
  draft.value = { id: "", name: "", kind: props.initialKind ?? "action", goName: "", eventIds: [] };
  draftRevision.value = props.projectRevision;
  parameters.value = [];
  fileLabel.value = "";
  importSource.value = "paste";
  importText.value = "";
  previewReady.value = false;
  previewValidated.value = false;
  bindNew.value = Boolean(props.initialKind);
}

// 稳定 ID 不允许在编辑表单中改名；参数草稿在提交时由工程入口统一同步。
function editDefinition(item: Definition) {
  if (busy.value || props.disabled) return;
  changeMode("edit");
  editingID.value = item.id;
  draft.value = { id: item.id, name: item.name, kind: item.kind, goName: item.goName, eventIds: [...(item.eventIds ?? [])] };
  draftRevision.value = props.projectRevision;
  parameters.value = (item.params ?? []).map((parameter) => ({
    key: parameterKey++, name: parameter.name, type: parameter.type, comment: parameter.comment ?? "",
    defaultText: parameter.default === undefined ? "" : stringifyJSON(parameter.default),
    enumText: (parameter.enum ?? []).join("\n"),
  }));
  bindNew.value = false;
}
defineExpose({ openMoveDefinition, openDefinitionTags, editDefinition });

// 直接编辑入口在首次渲染前填充草稿，避免短暂显示空白的新建表单。
if (props.initialMode === "edit") {
  if (props.initialDefinition) editDefinition(props.initialDefinition);
  else changeMode("manage");
}

// 添加独立参数行，不触碰工程中的已绑定参数。
function addParameter() {
  parameters.value.push({ key: parameterKey++, name: "", type: "string", comment: "", defaultText: "", enumText: "" });
}

// 逐行处理参数枚举，空行仅用于表单排版。
function lines(value: string): string[] {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

// 构造新声明时使用无损 JSON，默认值是否合法由 Go 统一校验。
function draftDefinition(): Definition {
  return {
    id: draft.value.id.trim(), name: draft.value.name.trim(), kind: draft.value.kind,
    goName: draft.value.goName.trim(), eventIds: [...draft.value.eventIds],
    params: parameters.value.map((parameter): Parameter => ({
      name: parameter.name.trim(), type: parameter.type,
      ...(parameter.comment.trim() ? { comment: parameter.comment.trim() } : {}),
      ...(parameter.defaultText.trim() ? { default: parseJSON(parameter.defaultText) } : {}),
      ...(parameter.type === "enum" ? { enum: lines(parameter.enumText) } : {}),
    })),
  };
}

// 读取文件仅填入文本，用户仍须显式解析才能预览和应用。
async function readCatalog(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file || busy.value) return;
  busy.value = true;
  error.value = "";
  resetPreview();
  fileLabel.value = "";
  try {
    importText.value = await file.text();
    fileLabel.value = file.name;
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    busy.value = false;
    input.value = "";
  }
}

// 已有输入时禁用示例按钮，避免无提示地覆盖用户尚未解析的 JSON。
function fillExample() {
  if (busy.value || importText.value.trim()) return;
  importText.value = stringifyJSON({ kind: "behaviortree.catalog", schemaVersion: 4, events: [], catalog: [{ id: "move_to", name: "移动到目标", kind: "action", goName: "MoveTo", params: [{ name: "Speed", type: "float64", default: 1.5, comment: "移动速度" }] }] }, 2);
}

// 显式解析并验证待导入数组，成功后才展示新增、相同和冲突列表。
async function previewCatalog() {
  if (busy.value || props.disabled) return;
  resetPreview();
  const revision = importRevision.value;
  busy.value = true;
  try {
    const validated = await validateCatalog(parseCatalogPackage(parseJSON(importText.value)));
    if (revision !== importRevision.value) return;
    incomingPackage.value = validated;
    incoming.value = validated.catalog;
    previewReady.value = true;
    if (!pendingChoices.value) await validatePreviewMerged();
  } catch (cause) {
    if (revision === importRevision.value) error.value = cause instanceof Error ? cause.message : String(cause);
  } finally { busy.value = false; }
}

// 冲突选择全部明确后验证最终结果，跨 ID 的 Go 名冲突也会阻止应用。
async function validatePreviewMerged() {
  previewValidated.value = false;
  error.value = "";
  if (!previewReady.value || pendingChoices.value) return;
  const revision = importRevision.value;
  const decisions = new Map<string, CatalogChoice>();
  for (const [id, choice] of Object.entries(choices.value)) if (choice) decisions.set(id, choice);
  if (!incomingPackage.value) return;
  const merged = mergeCatalogPackage(props.project, incomingPackage.value, decisions,
    new Map(Object.entries(eventMappings.value).filter(([, id]) => id)), new Map(Object.entries(eventRenames.value).filter(([, name]) => name)), importEnumDescription.value);
  const contentChanged = stringifyJSON(merged.catalog) !== stringifyJSON(props.project.catalog) || stringifyJSON(merged.events) !== stringifyJSON(props.project.events);
  if (!contentChanged) throw new Error("没有采纳任何定义或事件变更；如需单独修改整体注释，请使用事件枚举管理");
  await validateCatalog(exportCatalogPackage({ ...props.project, ...merged }, merged.catalog));
  if (revision === importRevision.value) previewValidated.value = true;
}

// 改变任意冲突选择使旧确认失效，并重新检查合并后约束。
async function changeChoice() {
  acknowledged.value = false;
  busy.value = true;
  try { await validatePreviewMerged(); }
  catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause); }
  finally { busy.value = false; }
}

// 一次提交校验最终合并目录，服务端拒绝时保留表单和原工程，便于纠正。
async function applyCatalog() {
  if (busy.value || props.disabled) return;
  if (mode.value === "import" && (!previewReady.value || !previewValidated.value || pendingChoices.value || (updatesExisting.value && !acknowledged.value))) return;
  error.value = "";
  const revision = importRevision.value;
  try {
    if (mode.value !== "import" && draftRevision.value !== props.projectRevision) throw new Error("工程事件表已变化，请重新打开业务定义表单");
    let candidate = incoming.value;
    const decisions = new Map<string, CatalogChoice>();
    for (const [id, choice] of Object.entries(choices.value)) if (choice) decisions.set(id, choice);
    if (mode.value === "create" || mode.value === "edit") {
      const definition = draftDefinition();
      if (!editingID.value && props.catalog.some((item) => item.id === definition.id)) {
        throw new Error(`ID ${definition.id} 已存在，请在“已有定义”中编辑或使用新的 ID`);
      }
      candidate = parseCatalog([definition]);
      if (editingID.value) decisions.set(editingID.value, "replace");
    }
    const replaced = conflictRows.value.some((row) => decisions.get(row.id) === "replace") || (mode.value !== "import" && catalogConflicts(props.catalog, candidate).some(row => decisions.get(row.id) === "replace"));
    if (replaced && !acknowledged.value) throw new Error("请确认更新定义对已有绑定和手写 Go 函数的影响");
    let merged = mergeCatalog(props.catalog, mode.value === "import" ? mappedIncoming.value : candidate, decisions);
    let mergedEvents = props.project.events;
    let mergedDescription = props.project.eventEnumDescription;
    let mergedNextEventID = props.project.nextEventId;
    if (mode.value === "import") {
      if (!incomingPackage.value) throw new Error("导入预览已失效，请重新解析");
      const result = mergeCatalogPackage(props.project, incomingPackage.value, decisions,
        new Map(Object.entries(eventMappings.value).filter(([, id]) => id)), new Map(Object.entries(eventRenames.value).filter(([, name]) => name)), importEnumDescription.value);
      merged = result.catalog; mergedEvents = result.events; mergedDescription = result.eventEnumDescription; mergedNextEventID = result.nextEventId;
      if (stringifyJSON(merged) === stringifyJSON(props.catalog) && stringifyJSON(mergedEvents) === stringifyJSON(props.project.events)) {
        throw new Error("没有采纳任何定义或事件变更；如需单独修改整体注释，请使用事件枚举管理");
      }
    }
    validateEventRegistry(mergedEvents, merged, mergedDescription);
    busy.value = true;
    const validated = await validateCatalog(exportCatalogPackage({ ...props.project, catalog: merged, events: mergedEvents, eventEnumDescription: mergedDescription, nextEventId: mergedNextEventID }, merged));
    if (revision !== importRevision.value) return;
    const bindID = mode.value === "create" && !editingID.value && bindNew.value && draft.value.kind === props.initialKind
      ? candidate[0]!.id : undefined;
    emit("apply", validated.catalog, bindID, mergedEvents, mergedDescription, mergedNextEventID);
  } catch (cause) {
    if (mode.value === "import") previewValidated.value = false;
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    busy.value = false;
  }
}

// 关闭弹窗不会提交草稿；网络提交期间保留弹窗防止用户误判提交结果。
function close() {
  if (!busy.value && !props.disabled) emit("close");
}

// 将键盘焦点限制在弹窗内，避免快捷键误操作背景画布。
function dialogKeydown(event: KeyboardEvent) {
  event.stopPropagation();
  if (event.key === "Escape") {
    event.preventDefault();
    if (exportMenu.value?.open) { exportMenu.value.open = false; exportMenu.value.querySelector("summary")?.focus(); return; }
    close(); return;
  }
  if (event.key !== "Tab") return;
  const controls = Array.from(dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]') ?? []).filter(control => control.getClientRects().length > 0 && (!control.closest('details:not([open])') || control.tagName === 'SUMMARY'));
  if (!controls?.length) return;
  const first = controls[0]!, last = controls[controls.length - 1]!;
  if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.value)) { event.preventDefault(); last.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
}
let previousFocus: HTMLElement | null = null;
onMounted(() => {
  previousFocus = document.activeElement as HTMLElement | null;
  dialog.value?.focus();
});
onUnmounted(() => { importRevision.value++; previousFocus?.focus(); });
</script>

<template>
  <Teleport to="body">
    <div class="catalog-overlay" @click.self="close">
      <section ref="dialog" class="catalog-dialog" role="dialog" aria-modal="true" aria-labelledby="catalog-title" tabindex="-1" @keydown="dialogKeydown">
        <header><div><h2 id="catalog-title">{{ dialogTitle }}</h2><p>{{ mode === 'edit' ? `正在修改已有${draft.kind === 'action' ? '动作' : '条件'}定义 · ID：${editingID}` : '声明可绑定的动作与条件，业务函数由 Go 实现。' }}</p></div><button v-if="mode === 'manage'" type="button" :disabled="busy || disabled" aria-label="关闭业务定义管理" @click="close">关闭</button></header>
        <nav v-if="mode !== 'manage'" aria-label="定义表单导航">
          <button type="button" :disabled="busy || disabled" @click="changeMode('manage')">← 返回业务定义</button>
        </nav>
        <nav v-else class="catalog-tabs" aria-label="业务节点管理分类">
          <button type="button" :class="{ active: activeTab === 'definitions' }" :aria-pressed="activeTab === 'definitions'" @click="activeTab = 'definitions'">业务定义（{{ catalog.length }}）</button>
          <button type="button" :class="{ active: activeTab === 'directories' }" :aria-pressed="activeTab === 'directories'" @click="activeTab = 'directories'">目录</button>
          <button type="button" :class="{ active: activeTab === 'tags' }" :aria-pressed="activeTab === 'tags'" @click="activeTab = 'tags'">标签</button>
        </nav>
        <div class="catalog-content">
          <div id="catalog-manager-overlays"></div>
          <CatalogBrowser v-if="index" v-show="mode === 'manage' && activeTab === 'directories'" ref="organizationBrowser" presentation="directories" :initial-folder="selectedFolder" :index="index" :revision="revision ?? 0" :collapsed="false" search="" :disabled="Boolean(disabled)" :commit="commitOrganization" :failure-message="failureMessage ?? ''" @select="selectFolder" @inspect="editDefinition" @menu="(event, definition) => emit('menu', event, definition)" />
          <CatalogBrowser v-if="index && mode === 'manage' && activeTab === 'tags'" presentation="tags" :index="index" :revision="revision ?? 0" :collapsed="false" search="" :disabled="Boolean(disabled)" :commit="commitOrganization" :failure-message="failureMessage ?? ''" />
          <div v-if="mode === 'manage' && activeTab === 'definitions'" class="catalog-existing">
            <div class="catalog-toolbar">
              <button type="button" class="catalog-primary" :disabled="disabled" @click="changeMode('create')">新建定义</button>
              <button type="button" :disabled="disabled" @click="changeMode('import')">导入</button>
              <details ref="exportMenu" class="catalog-export"><summary>导出</summary><div class="catalog-export-options"><small>导出全部定义，不受筛选影响</small><button type="button" :disabled="disabled || transferBusy || !catalog.length" @click="transfer('copy')">复制全部 JSON</button><button type="button" :disabled="disabled || transferBusy || !catalog.length" @click="transfer('download')">下载全部 JSON</button></div></details>
            </div>
            <div class="catalog-filters">
              <label>搜索定义<input v-model="query" type="search" placeholder="名称 / ID / Go 函数" /></label>
              <label>种类<select v-model="kindFilter"><option value="">全部</option><option value="action">动作</option><option value="condition">条件</option></select></label>
              <label>目录（直属定义）<select :value="selectedFolder" @change="selectFolder(($event.target as HTMLSelectElement).value)"><option v-for="folder in folderChoices" :key="folder.id" :value="folder.id">{{ folder.name }}</option></select></label>
            </div>
            <p class="catalog-hint">当前显示 {{ filteredDefinitions.length }} 个定义</p>
            <p v-if="!filteredDefinitions.length" class="catalog-empty">{{ catalog.length ? '当前目录下没有匹配的定义，请调整搜索或筛选条件。' : '还没有业务定义。新建或导入定义后，即可在节点属性中选择绑定。' }}</p>
            <article v-for="item in filteredDefinitions" :key="item.id"><div class="catalog-definition-info"><div class="catalog-definition-heading"><strong>{{ item.name }}</strong><span class="catalog-kind">{{ item.kind === 'action' ? '动作' : '条件' }}</span></div><small>ID：{{ item.id }} · Go 函数：{{ item.goName }} · 参数：{{ item.params?.length ?? 0 }} 个</small></div><div class="catalog-row-actions"><button type="button" :disabled="disabled" @click="editDefinition(item)">编辑</button><button type="button" :disabled="disabled" :aria-label="`${item.name}的更多操作`" @click="emit('menu', $event, item)">更多 ⋯</button></div></article>
          </div>
          <form v-else-if="mode === 'create' || mode === 'edit'" id="catalog-form" @submit.prevent="applyCatalog">
            <div class="catalog-grid">
              <label>定义 ID<input v-model="draft.id" :readonly="Boolean(editingID)" :disabled="busy" placeholder="move_to" required /><small>绑定使用的稳定 ID{{ editingID ? '，编辑时保持固定' : '，请使用未占用的标识' }}。</small></label>
              <label>显示名称<input v-model="draft.name" :disabled="busy" placeholder="移动到目标" required /></label>
              <label>种类<select v-model="draft.kind" :disabled="busy"><option value="action">动作</option><option value="condition">条件</option></select></label>
              <label>业务实现函数<input v-model="draft.goName" :disabled="busy" placeholder="MoveTo" required /><small>同包中的手写 Go 函数，目录内名称必须唯一，可被多个节点复用。节点代码名独立保存。</small></label>
            </div>
            <div class="catalog-section-title"><h3>参数声明</h3><button type="button" :disabled="busy" @click="addParameter">添加参数</button></div>
            <p class="catalog-hint">参数名称使用 Go 导出成员名，例如 Target。默认值填写 JSON；字符串写为 "文本"，留空表示未设置。悬浮类型或 ⓘ 查看用途和示例。</p>
            <div v-for="(parameter, index) in parameters" :key="parameter.key" class="catalog-parameter">
              <label>参数名<input v-model="parameter.name" :disabled="busy" placeholder="Target" required /></label>
              <label><span>类型 <span class="catalog-type-info" :title="parameterTypeTooltip(parameter.type)" :aria-label="parameterTypeTooltip(parameter.type)">ⓘ</span></span><select v-model="parameter.type" :disabled="busy" :title="parameterTypeTooltip(parameter.type)" :aria-describedby="`parameter-type-hint-${parameter.key}`"><option v-for="type in valueTypes" :key="type" :value="type" :title="`${type} — ${PARAM_TYPE_META[type].short}`">{{ type }}</option></select></label>
              <label>默认值（JSON）<input v-model="parameter.defaultText" :disabled="busy" :placeholder="PARAM_TYPE_META[parameter.type].placeholder" :title="PARAM_TYPE_META[parameter.type].detail || undefined" /></label>
              <button type="button" :disabled="busy" :aria-label="`移除参数 ${parameter.name || index + 1}`" @click="parameters.splice(index, 1)">移除</button>
              <small :id="`parameter-type-hint-${parameter.key}`" class="catalog-wide catalog-type-hint">{{ PARAM_TYPE_META[parameter.type].short }}：{{ PARAM_TYPE_META[parameter.type].description }} {{ PARAM_TYPE_META[parameter.type].note }}</small>
              <label class="catalog-wide">注释（可留空）<textarea v-model="parameter.comment" :disabled="busy" placeholder="说明参数用途，将生成到 Go 字段注释中" rows="2" /></label>
              <label v-if="parameter.type === 'enum'" class="catalog-wide">允许的枚举值（每行一个）<textarea v-model="parameter.enumText" :disabled="busy" rows="3" /></label>
            </div>
            <EventMultiSelect :events="project.events" :selected="draft.eventIds" :disabled="busy" @change="draft.eventIds = $event" @manage="emit('manageEvents')" />
            <div v-if="editingID" class="catalog-warning"><strong>更新已有定义会影响所有引用它的节点。</strong><p>节点参数会按新定义同步：保留兼容绑定，删除已移除参数，重置不兼容绑定；新增参数采用默认值或保持未绑定。应用后自动校验工程，请按提示配置参数，并同步手写 Go 实现。</p><label class="catalog-check"><input v-model="acknowledged" :disabled="busy" type="checkbox" />我已了解影响，确认更新此定义</label></div>
            <label v-if="initialKind && !editingID && draft.kind === initialKind" class="catalog-check"><input v-model="bindNew" :disabled="busy" type="checkbox" />保存并绑定当前节点</label>
          </form>
          <div v-else-if="mode === 'import'" class="catalog-import">
            <p>粘贴 Schema 4 业务目录交换包，或从文件读取。事件 ID 冲突自动重编号，定义和代码名冲突仍需选择。</p>
            <div class="catalog-import-tools" role="group" aria-label="导入来源">
              <button type="button" :class="{ active: importSource === 'paste' }" :disabled="busy" @click="importSource = 'paste'">粘贴 JSON</button>
              <button type="button" :class="{ active: importSource === 'file' }" :disabled="busy" @click="importSource = 'file'">从文件读取</button>
              <button type="button" :disabled="busy || Boolean(importText.trim())" :title="importText.trim() ? '请先清空输入，再填入示例' : '填入一个可编辑的业务定义示例'" @click="fillExample">填入示例</button>
            </div>
            <label v-if="importSource === 'file'">业务定义文件<input type="file" accept=".json,application/json" :disabled="busy" @change="readCatalog" /></label>
            <p v-if="fileLabel">{{ fileLabel }}</p>
            <label>业务目录 JSON<textarea v-model="importText" :disabled="busy" rows="10" spellcheck="false" placeholder='{"kind":"behaviortree.catalog","schemaVersion":4,"events":[],"catalog":[]}' /></label>
            <button type="button" :disabled="busy || !importText.trim()" @click="previewCatalog">解析预览</button>
            <section v-if="previewReady" class="catalog-preview" aria-label="导入预览">
              <h3>新增 {{ previewCounts.added }} · 相同 {{ previewCounts.same }} · 冲突 {{ previewCounts.conflict }}</h3>
              <p v-if="!previewRows.length" class="catalog-hint">交换包内没有需要导入的定义。</p>
              <ul><li v-for="row in previewRows" :key="row.item.id"><span>{{ row.status }}</span> {{ row.item.name }} · {{ row.item.id }} · {{ row.item.goName }}</li></ul>
            </section>
            <article v-for="row in previewReady ? conflictRows : []" :key="row.id" class="catalog-conflict">
              <strong>定义冲突：{{ row.id }}</strong>
              <div class="catalog-compare"><div><small>当前工程</small><pre>{{ stringifyJSON(row.current, 2) }}</pre></div><div><small>导入定义</small><pre>{{ stringifyJSON(row.incoming, 2) }}</pre></div></div>
              <label>处理方式<select v-model="choices[row.id]" :disabled="busy" @change="changeChoice"><option :value="undefined" disabled>请选择处理方式</option><option value="keep">保留现有定义</option><option value="replace">使用导入定义</option></select></label>
            </article>
            <article v-for="row in previewReady ? eventConflictRows : []" :key="row.incoming.id" class="catalog-conflict"><strong>事件 ID 冲突：{{ row.incoming.name }} · Event{{ row.incoming.codeName }}</strong><p>{{ eventMappings[row.incoming.id] ? '已映射到现有事件 ID' : '将自动分配工程内的新 ID' }} {{ preparedEvents?.eventIDs.get(row.incoming.id) ?? '（待校验）' }}，并同步更新业务定义引用。</p></article>
            <article v-for="row in previewReady ? codeNameCollisionRows : []" :key="row.id" class="catalog-conflict"><strong>代码名冲突：Event{{ row.codeName }}</strong><p>导入事件与当前工程不同 ID 的成员同名，请映射到现有事件或修改导入成员代码名。</p><label>映射到现有事件<select v-model="eventMappings[row.id]" @change="changeChoice"><option value="">不映射</option><option v-for="item in project.events" :key="item.id" :value="item.id">{{ item.name }} · Event{{ item.codeName }}</option></select></label><label>或修改导入代码名<input v-model="eventRenames[row.id]" @change="changeChoice" placeholder="新的代码名" /></label></article>
            <article v-if="previewReady && enumDescriptionConflict" class="catalog-conflict"><strong>枚举功能注释不同</strong><div class="catalog-compare"><div><small>当前工程</small><pre>{{ project.eventEnumDescription || '（空）' }}</pre></div><div><small>导入来源</small><pre>{{ incomingPackage?.eventEnumDescription || '（空；采用将清空）' }}</pre></div></div><label class="catalog-check"><input v-model="importEnumDescription" type="checkbox" @change="changeChoice" />明确采用导入的整体注释{{ incomingPackage?.eventEnumDescription ? '' : '（将清空）' }}</label></article>
            <p v-if="previewReady && !conflictRows.length" class="catalog-hint">没有同 ID 变更；确认后点击“应用到工程”，再保存工程。</p>
            <div v-if="updatesExisting" class="catalog-warning"><p>更新后节点参数会按新定义同步：保留兼容绑定，删除已移除参数，重置不兼容绑定；新增参数采用默认值或保持未绑定。应用后自动校验工程，请按提示配置参数，并同步手写 Go 实现。</p><label class="catalog-check"><input v-model="acknowledged" :disabled="busy" type="checkbox" />确认使用所选导入定义更新已有绑定</label></div>
          </div>
          <p v-if="error" class="catalog-error" role="alert">{{ error }}</p>
        </div>
        <footer><span>{{ mode === 'manage' ? '分类修改即时应用，修改后需保存工程；关闭不会撤销已应用的修改。' : mode === 'import' ? '仅在应用后修改工程；请保存工程以保留导入结果。' : '定义保存后，可在代码面板预览业务函数骨架。' }}</span><button v-if="mode !== 'manage'" type="button" :disabled="busy || disabled" @click="changeMode('manage')">取消</button><button v-if="mode !== 'manage'" class="catalog-primary" type="button" :disabled="busy || (mode === 'import' && (!previewReady || !previewValidated || !incoming.length || pendingChoices || (updatesExisting && !acknowledged)))" @click="applyCatalog">{{ busy ? '校验中…' : mode === 'import' ? '应用到工程' : mode === 'edit' ? '验证并保存修改' : '验证并保存定义' }}</button></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.catalog-import-tools{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:16px}.catalog-import textarea{font-family:ui-monospace,monospace;font-size:12px}.catalog-preview{margin-top:20px;padding:14px;border:1px solid #385361;border-radius:8px}.catalog-preview ul{list-style:none;margin:10px 0 0;padding:0;max-height:220px;overflow:auto}.catalog-preview li{padding:5px 0;overflow-wrap:anywhere}.catalog-preview li span{color:#a3f1d9;margin-right:8px}
.catalog-type-info{color:#94aebb;cursor:help;font-size:12px}.catalog-type-hint{line-height:1.5;overflow-wrap:anywhere}
.catalog-overlay{position:fixed;inset:0;z-index:1000;display:grid;place-items:center;padding:16px;background:#071017b8;backdrop-filter:blur(4px);color:#d8e6ed;font:14px/1.5 system-ui,sans-serif}
.catalog-dialog{width:min(1040px,100%);max-height:calc(100dvh - 32px);display:flex;flex-direction:column;border:1px solid #365260;border-radius:14px;background:#14232d;box-shadow:0 24px 90px #0008;outline:none}
header,footer,nav{display:flex;align-items:center;gap:12px;padding:18px 24px;flex-shrink:0}header{justify-content:space-between}h2,h3,p{margin:0}h2{font-size:20px}h3{font-size:15px}header p,.catalog-hint,small,footer span{color:#94aebb;font-size:12px}nav{padding-top:0;border-bottom:1px solid #2d414d;flex-wrap:wrap}.catalog-content{padding:22px 24px;overflow:auto;min-height:160px}button,input,select,textarea{box-sizing:border-box;font:inherit;color:inherit;border:1px solid #385361;border-radius:6px;background:#10202a}button{padding:8px 12px;cursor:pointer;white-space:nowrap}button:hover{border-color:#86dec1;background:#1b3540}button:disabled{opacity:.5;cursor:wait}button.active,.catalog-primary{color:#a3f1d9;border-color:#64cfae;background:#1c3b3d}input,select,textarea{padding:9px 10px;width:100%;min-width:0}input:read-only{color:#8297a4}input:focus,select:focus,textarea:focus,button:focus-visible{outline:2px solid #86dec1;outline-offset:2px}label{display:flex;flex-direction:column;gap:6px;min-width:0}.catalog-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}.catalog-section-title{display:flex;align-items:center;justify-content:space-between;margin:24px 0 8px}.catalog-parameter{display:grid;grid-template-columns:1fr 130px 1.4fr auto;align-items:end;gap:10px;margin-top:12px;padding:12px;border:1px solid #2e4754;border-radius:8px}.catalog-wide{grid-column:1/-1}.catalog-events{margin-top:20px}.catalog-check{display:flex;flex-direction:row;align-items:flex-start;gap:9px;margin-top:14px}.catalog-check input{width:auto;margin:4px 0 0}.catalog-warning{margin-top:18px;border:1px solid #947039;background:#3c3222;border-radius:8px;padding:14px;color:#f0d7a5}.catalog-warning p{margin-top:5px}.catalog-existing article{display:flex;gap:12px;align-items:center;justify-content:space-between;padding:15px 0;border-bottom:1px solid #2d414d}.catalog-existing small{display:block;margin-top:4px}.catalog-empty{padding:24px;color:#a5becb;border:1px dashed #456170;border-radius:8px}.catalog-import>label,.catalog-import>p{margin-bottom:16px}.catalog-conflict{margin-top:18px;border:1px solid #725c3a;border-radius:8px;padding:16px}.catalog-compare{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin:12px 0}.catalog-compare>div{min-width:0}pre{padding:10px;margin:5px 0 0;max-height:240px;overflow:auto;border-radius:5px;background:#0c1821;font-size:12px}.catalog-error{margin-top:18px;padding:12px;border:1px solid #ad6262;background:#46282d;border-radius:6px;color:#ffc2c2;overflow-wrap:anywhere}footer{border-top:1px solid #2d414d}footer span{margin-right:auto}
@media(max-width:650px){.catalog-overlay{padding:16px}.catalog-dialog{max-height:calc(100dvh - 32px)}header,footer,nav,.catalog-content{padding:14px}.catalog-grid,.catalog-compare{grid-template-columns:1fr}.catalog-parameter{grid-template-columns:1fr 1fr}.catalog-parameter>label:nth-child(3){grid-column:1/-1}footer{flex-wrap:wrap}footer span{width:100%}}
.catalog-dialog{min-width:0;box-sizing:border-box}.catalog-content{min-height:0;flex:1}.catalog-tabs{gap:20px;padding-bottom:0}.catalog-tabs button{border:0;border-radius:0;background:transparent;padding:10px 0;border-bottom:2px solid transparent;color:#94aebb}.catalog-tabs button.active{border-bottom-color:#64cfae;color:#a3f1d9}.catalog-toolbar,.catalog-row-actions,.catalog-definition-heading{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.catalog-toolbar{margin-bottom:18px}.catalog-filters{display:grid;grid-template-columns:minmax(180px,1.5fr) minmax(100px,.6fr) minmax(150px,1fr);gap:12px;margin-bottom:14px}.catalog-definition-info{min-width:0;flex:1;overflow-wrap:anywhere}.catalog-kind{font-size:11px;line-height:1.6;padding:1px 7px;border:1px solid #3c6670;border-radius:4px;color:#a3d8dd;flex-shrink:0}.catalog-row-actions{flex-shrink:0}.catalog-export{position:relative}.catalog-export summary{list-style:none;cursor:pointer;padding:8px 12px;border:1px solid #385361;border-radius:6px;background:#10202a}.catalog-export summary::after{content:' ▾';color:#94aebb}.catalog-export-options{position:absolute;left:0;top:calc(100% + 6px);z-index:2;min-width:210px;padding:10px;display:grid;gap:6px;border:1px solid #385361;border-radius:8px;background:#14232d;box-shadow:0 8px 24px #0008}.catalog-export-options button{text-align:left}.catalog-export-options small{margin:0 0 4px}.catalog-export summary:focus-visible{outline:2px solid #86dec1;outline-offset:2px}header>div{min-width:0;overflow-wrap:anywhere}header>button{flex-shrink:0}button{flex-shrink:0}
@media(max-width:650px){.catalog-filters{grid-template-columns:minmax(0,1fr) minmax(0,1fr)}.catalog-filters>label:first-child{grid-column:1/-1}.catalog-existing article{align-items:flex-start;flex-wrap:wrap}.catalog-definition-info{flex-basis:100%}.catalog-tabs{gap:16px}.catalog-tabs button{padding:6px 0}.catalog-export-options{left:auto;right:0;min-width:190px}}
</style>
