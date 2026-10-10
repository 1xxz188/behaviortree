<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import type { Definition } from "./project";
import type { DefinitionKind } from "./enums";
import type { CatalogEntry, CatalogOrganizationIndex } from "./catalogOrganization";
import type { CatalogTreeScope, CatalogNodeStatus } from "./catalogVisibility";

const props = defineProps<{
  collapsed: boolean; // 折叠偏好由父级持有，组件重建时仍保留。
  index: CatalogOrganizationIndex; // 工程分类索引，由父级历史边界维护。
  revision: number; // 索引原地更新后驱动可见行重算。
  search: string; // 与内置节点共用搜索输入。
  disabled: boolean; // 工程切换期间禁止修改。
  commit: (change: () => void) => boolean; // 分类变更进入统一撤销边界。
  failureMessage: string; // 父级操作失败的具体原因，在模态框内可见。
  presentation?: "sidebar" | "directories" | "tags"; // 管理页复用目录手势及分类表单，默认作为侧栏。
  initialFolder?: string; // 管理页打开时继承侧栏的当前目录。
  treeScope?: CatalogTreeScope; // 父级保存树范围，默认显示全部树。
  nodeStatus?: CatalogNodeStatus; // 父级保存节点状态，默认显示全部。
  matchesDefinition?: (id: string) => boolean; // 按缓存集合组合范围与状态，逐行常数时间判断。
  kindFilter?: "" | DefinitionKind; // 管理页种类筛选，空值表示全部；侧栏保持原有浏览方式。
}>();
const emit = defineEmits<{
  "update:collapsed": [value: boolean]; // 只切换业务区显示，不修改工程或目录状态。
  "update:treeScope": [value: CatalogTreeScope]; // 管理页与侧栏共享范围显示偏好。
  "update:nodeStatus": [value: CatalogNodeStatus]; // 状态筛选可独立与范围组合。
  create: [folderId: string]; import: [folderId: string]; manage: [];
  inspect: [definition: Definition]; menu: [event: MouseEvent | KeyboardEvent, definition: Definition];
  drag: [event: DragEvent, definition: Definition]; dragend: [];
  select: [folderId: string]; clearSearch: [];
  transfer: [operation: "copy" | "download"];
}>();

// 可见行保持扁平结构，深层目录不会造成组件递归或调用栈溢出。
interface VisibleRow { entry: CatalogEntry; depth: number; }
// 弹窗草稿独立于工程，取消时不产生历史记录。
interface OrganizationDialog {
  mode: "create" | "rename" | "move" | "tags" | "tag-create" | "tag-rename" | "tag-delete";
  id: string; name: string; parentId: string; entry?: CatalogEntry; tagIds: string[];
}
const selectedFolder = ref(props.initialFolder ?? "");
const selectedDefinition = ref(""); // 当前操作的定义与目录互斥高亮，新增位置仍采用其所属目录。
const expanded = ref(new Set<string>(["", props.initialFolder ?? ""]));
const filteredExpanded = ref(new Set<string>()); // 筛选期间独立折叠，不覆盖普通目录树的展开偏好。
const filters = ref<string[]>([]);
const filtersOpen = ref(false); // 标签筛选默认收起，选择保持到用户主动清除。
// 使用父级视图偏好，折叠或切换黑板后仍恢复同一组筛选条件。
const treeScope = computed({ get: () => props.treeScope ?? "all", set: (value: CatalogTreeScope) => emit("update:treeScope", value) });
const nodeStatus = computed({ get: () => props.nodeStatus ?? "all", set: (value: CatalogNodeStatus) => emit("update:nodeStatus", value) });
const dialog = ref<OrganizationDialog>();
const modal = ref<HTMLDialogElement>();
const dialogBaseline = ref(""); // 打开弹窗时的字段基准，避免未修改的表单误报。
const pendingWarning = ref(""); // 导航拦截时留在当前弹窗提示用户。
// 分类弹窗只比较自身字段，不遍历目录索引。
const hasPending = computed(() => !!dialog.value && JSON.stringify([dialog.value.name, dialog.value.parentId, dialog.value.tagIds]) !== dialogBaseline.value);
// 离开分类表单前把焦点移回其输入区域。
function focusPending() {
  pendingWarning.value = "有未应用更改，请先确定或点击“取消”放弃更改。";
  modal.value?.querySelector<HTMLElement>("input, select")?.focus();
}
const failure = ref("");
const dragged = ref<CatalogEntry>();
const dropHint = ref("");
const folderMenu = ref<{ id: string; x: number; y: number }>();
const menuElement = ref<HTMLElement>();
let menuTrigger: HTMLElement | undefined;
let dialogTrigger: HTMLElement | undefined; // 子弹窗关闭后恢复原按钮或上下文菜单触发位置。
const isFiltered = computed(() => Boolean(props.search.trim() || filters.value.length));
const isManager = computed(() => props.presentation === "directories"); // 统一管理页始终显示层级树。
const isViewFiltered = computed(() => treeScope.value !== "all" || nodeStatus.value !== "all"); // 侧栏范围和状态保持原目录展示及排序。
const isManagementFiltered = computed(() => isManager.value && (isFiltered.value || isViewFiltered.value || Boolean(props.kindFilter))); // 任一管理条件生效时裁剪树并停用排序。
const allTags = computed(() => { props.revision; return [...props.index.tags.values()]; });
const results = computed(() => {
  props.revision;
  const definitions = props.index.search(props.search, filters.value);
  if (!props.kindFilter && !props.matchesDefinition) return definitions;
  return definitions.filter(definition => (!props.kindFilter || definition.kind === props.kindFilter)
    && (!props.matchesDefinition || props.matchesDefinition(definition.id)));
});
// 命中定义只计算一次；共享祖先遇到已标记目录即停止，避免逐定义重复爬完整父链。
const matchingTree = computed(() => {
  if (!isManagementFiltered.value) return undefined;
  const definitions = new Set<string>(); // 当前组合条件命中的定义 ID。
  const folders = new Set<string>(); // 为展示命中定义保留的祖先目录 ID。
  for (const definition of results.value) {
    definitions.add(definition.id);
    let parent = props.index.assignments.get(definition.id)?.folderId ?? "";
    while (parent && !folders.has(parent)) {
      folders.add(parent);
      parent = props.index.folders.get(parent)!.parentId;
    }
  }
  return { definitions, folders };
});
const visibleExpanded = computed(() => isManagementFiltered.value ? filteredExpanded.value : expanded.value); // 清除筛选直接恢复普通树偏好。
// 管理树直接检查命中集合，侧栏沿用缓存范围匹配；折叠目录中的定义不会被误判为空。
const hasMatchingDefinitions = computed(() => {
  props.revision;
  if (isManager.value) return matchingTree.value ? matchingTree.value.definitions.size > 0 : props.index.definitions.size > 0;
  if (!props.matchesDefinition) return props.index.definitions.size > 0;
  for (const id of props.index.definitions.keys()) if (props.matchesDefinition(id)) return true;
  return false;
});
// 拖拽悬停频繁触发，预先索引每个条目的父级及后继，避免每次指针事件扫描同级。
const placements = computed(() => {
  props.revision;
  const locations = new Map<string, { folder: string; next?: CatalogEntry }>();
  for (const [folder, entries] of props.index.children) entries.forEach((entry, position) => {
    locations.set(`${entry.kind}:${entry.id}`, { folder, next: entries[position + 1] });
  });
  return locations;
});
const rows = computed(() => {
  props.revision;
  const result: VisibleRow[] = [];
  const matching = matchingTree.value;
  const openFolders = visibleExpanded.value;
  const stack: VisibleRow[] = props.index.entries("").map(entry => ({ entry, depth: 0 })).reverse();
  while (stack.length) {
    const row = stack.pop()!;
    // 只进入保留目录，按原混合顺序输出；每行用集合常数时间判断是否匹配。
    if (matching) {
      if (!(row.entry.kind === "definition" ? matching.definitions : matching.folders).has(row.entry.id)) continue;
    } else if (row.entry.kind === "definition" && props.matchesDefinition && !props.matchesDefinition(row.entry.id)) continue;
    result.push(row);
    if (row.entry.kind === "folder" && openFolders.has(row.entry.id)) {
      const children = props.index.entries(row.entry.id);
      for (let i = children.length - 1; i >= 0; i--) stack.push({ entry: children[i]!, depth: row.depth + 1 });
    }
  }
  return result;
});
// 清除标签与共享范围、状态，再交由父级清除本地搜索和管理种类，不修改工程分类或当前目录。
function clearFilters() { filters.value = []; treeScope.value = "all"; nodeStatus.value = "all"; emit("clearSearch"); }
const folderChoices = computed(() => {
  props.revision;
  return [{ id: "", name: "根目录" }, ...[...props.index.folders.values()]
    .filter(folder => dialog.value?.entry?.kind !== "folder" || props.index.canMoveFolder(dialog.value.entry.id, folder.id))
    .map(folder => ({ id: folder.id, name: props.index.folderPath(folder.id) }))];
});
const dialogTitle = computed(() => ({ create: "新建目录", rename: "重命名目录", move: "移动到目录", tags: "设置标签", "tag-create": "新建标签", "tag-rename": "重命名标签", "tag-delete": "删除标签" }[dialog.value?.mode ?? "create"]));

// 工程替换或撤销后放弃旧对象上的菜单、拖拽和弹窗，修复失效筛选。
watch(() => props.index, () => {
  folderMenu.value = undefined; dialog.value = undefined; endDrag();
  selectedDefinition.value = "";
  if (!props.index.folders.has(selectedFolder.value)) selectFolder("");
  expanded.value = new Set([...expanded.value].filter(id => !id || props.index.folders.has(id)));
  filteredExpanded.value = new Set([...filteredExpanded.value].filter(id => props.index.folders.has(id)));
  filters.value = filters.value.filter(id => props.index.tags.has(id));
});
watch(() => props.disabled, disabled => { if (disabled) { dialog.value = undefined; folderMenu.value = undefined; endDrag(); } });
let initialFolderSynced = false; // 首次挂载需要展开祖先，之后父级回显不得重新打开手动折叠的目录。
// 仅首次初始化或真实外部目录选择才展开路径；内部选择回显保留当前折叠状态。
watch(() => props.initialFolder, value => {
  const folder = value ?? "";
  if (initialFolderSynced && selectedFolder.value === folder) return;
  initialFolderSynced = true;
  if (selectedFolder.value !== folder) { selectedFolder.value = folder; selectedDefinition.value = ""; }
  let parent = selectedFolder.value;
  while (parent && props.index.folders.has(parent)) { visibleExpanded.value.add(parent); parent = props.index.folders.get(parent)!.parentId; }
}, { immediate: true });
// 条件或命中数据更新才重新展开匹配路径，手动折叠不会反过来触发重新计算。
watch(matchingTree, matching => {
  if (matching) filteredExpanded.value = new Set(matching.folders);
}, { immediate: true, flush: "sync" });
// 中途启用筛选立即取消旧拖拽，避免隐藏条目后仍提交排序。
watch(isManagementFiltered, filtered => { if (filtered && dragged.value) endDrag(); }, { flush: "sync" });
watch(dialog, async value => {
  failure.value = "";
  if (!value) { modal.value?.close(); await nextTick(); dialogTrigger?.focus(); return; }
  await nextTick(); modal.value?.showModal();
  modal.value?.querySelector<HTMLElement>("input, select, button")?.focus();
});

// 选中目录只决定新增节点的默认位置，不修改工程。
function selectFolder(id: string) { selectedDefinition.value = ""; selectedFolder.value = id; emit("select", id); }
// 查看或拖动定义时选中该定义，同步新增位置但不再高亮原目录。
function selectDefinition(id: string) {
  selectFolder(props.index.assignments.get(id)?.folderId ?? "");
  selectedDefinition.value = id;
}
// 普通单击只打开定义窗口，创建画布实例继续由拖放入口负责。
function inspectDefinition(definition: Definition) {
  if (props.disabled) return;
  selectDefinition(definition.id); emit("inspect", definition);
}
// 右键与键盘菜单也应选中正在操作的定义。
function definitionMenu(event: MouseEvent | KeyboardEvent, definition: Definition) {
  if (props.disabled) return;
  selectDefinition(definition.id); emit("menu", event, definition);
}
// 折叠状态属于当前页面，不进入工程 JSON 或撤销栈。
function toggleFolder(id: string) {
  const next = new Set(visibleExpanded.value);
  if (next.has(id)) next.delete(id); else next.add(id);
  if (isManagementFiltered.value) filteredExpanded.value = next; else expanded.value = next;
  selectFolder(id);
}
// 菜单和按钮共用草稿弹窗，避免使用浏览器阻塞式 prompt。
function openDialog(mode: OrganizationDialog["mode"], id = "", entry?: CatalogEntry) {
  if (props.disabled) return;
  if (hasPending.value) return focusPending();
  dialogTrigger = folderMenu.value ? menuTrigger : document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
  folderMenu.value = undefined;
  dialog.value = { mode, id, entry, name: mode.startsWith("tag-") ? props.index.tags.get(id)?.name ?? "" : props.index.folders.get(id)?.name ?? "",
    parentId: mode === "create" ? id : entry?.kind === "folder" ? props.index.folders.get(entry.id)!.parentId : props.index.assignments.get(id)?.folderId ?? "",
    tagIds: [...(props.index.assignments.get(id)?.tagIds ?? [])] };
  dialogBaseline.value = JSON.stringify([dialog.value.name, dialog.value.parentId, dialog.value.tagIds]);
  pendingWarning.value = "";
}
// 业务定义菜单由父级捕获身份，本组件仅接管移动和标签草稿。
function openMoveDefinition(id: string) { openDialog("move", id, { kind: "definition", id, order: 0 }); }
function openDefinitionTags(id: string) { openDialog("tags", id); }
// 管理入口允许直接创建标签，仍使用同一个事务表单。
function openTagCreate() { openDialog("tag-create"); }
defineExpose({ openMoveDefinition, openDefinitionTags, openTagCreate, hasPending, focusPending });
// Escape 属于页面离开请求；显式取消按钮才直接放弃草稿。
function cancelDialog() { if (hasPending.value) focusPending(); else dialog.value = undefined; }

// 所有表单提交通过父级事务；校验失败时保留草稿供纠正。
async function submitDialog() {
  const draft = dialog.value;
  if (!draft || props.disabled) return;
  const ok = props.commit(() => {
    switch (draft.mode) {
      case "create": { const folder = props.index.addFolder(draft.name, draft.parentId); visibleExpanded.value.add(draft.parentId); selectFolder(folder.id); break; }
      case "rename": props.index.renameFolder(draft.id, draft.name); break;
      case "move": props.index.move(draft.entry!, draft.parentId); visibleExpanded.value.add(draft.parentId); break;
      case "tags": props.index.setTags(draft.id, draft.tagIds); break;
      case "tag-create": props.index.addTag(draft.name); break;
      case "tag-rename": props.index.renameTag(draft.id, draft.name); break;
      case "tag-delete": props.index.deleteTag(draft.id); filters.value = filters.value.filter(id => id !== draft.id); break;
    }
  });
  if (ok) {
    if (draft.mode === "move" && draft.entry?.kind === "definition") selectDefinition(draft.id);
    dialog.value = undefined;
  }
  else { await nextTick(); failure.value = props.failureMessage || "操作未完成，请修正后重试。"; }
}
// 空目录可直接删除，子目录和直属定义由索引常数时间判断。
function deleteFolder(id: string) {
  if (props.disabled || !id) return;
  folderMenu.value = undefined;
  if (props.commit(() => props.index.deleteFolder(id))) {
    expanded.value.delete(id);
    filteredExpanded.value.delete(id);
    if (selectedFolder.value === id) selectFolder("");
  }
}
// 侧栏平铺结果定位时清空本地筛选；管理树保留条件，并使用各自的展开集合和 DOM 身份。
async function locate(definition: Definition) {
  const folder = props.index.assignments.get(definition.id)?.folderId ?? "";
  let parent = folder;
  const next = new Set(visibleExpanded.value);
  while (parent) { next.add(parent); parent = props.index.folders.get(parent)!.parentId; }
  if (isManagementFiltered.value) filteredExpanded.value = next; else expanded.value = next;
  if (!isManager.value) clearFilters();
  selectFolder(folder);
  await nextTick();
  const element = document.getElementById(`${isManager.value ? 'catalog-managed-definition' : 'catalog-definition'}-${definition.id}`);
  element?.scrollIntoView({ block: "nearest" });
  element?.focus();
}
// 分类拖拽与画布复制共用浏览器手势，但保留独立数据类型和落点语义。
function startDrag(event: DragEvent, entry: CatalogEntry) {
  if (isManagementFiltered.value) { event.preventDefault(); endDrag(); return; }
  if (props.disabled || !event.dataTransfer) return;
  dragged.value = entry;
  if (entry.kind === "definition") selectDefinition(entry.id); else selectFolder(entry.id);
  if (entry.kind === "definition" && (!props.presentation || props.presentation === "sidebar")) emit("drag", event, props.index.definitions.get(entry.id)!);
  else emit("dragend");
  event.dataTransfer.effectAllowed = entry.kind === "definition" && (!props.presentation || props.presentation === "sidebar") ? "copyMove" : "move";
  event.dataTransfer.setData("application/x-behaviortree-catalog", `${entry.kind}:${entry.id}`);
}
// 拖拽取消与松手都清理本地状态，只有有效 drop 才提交工程修改。
function endDrag() { dragged.value = undefined; dropHint.value = ""; emit("dragend"); }
// 目录中间区域代表移入，顶部和底部代表插入同级；定义只有前后位置。
function dropLocation(event: DragEvent, entry?: CatalogEntry) {
  if (!entry) return { folder: "", before: undefined, hint: "root" };
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect();
  const ratio = (event.clientY - bounds.top) / bounds.height;
  if (entry.kind === "folder" && ratio >= .25 && ratio <= .75) return { folder: entry.id, before: undefined, hint: `in:${entry.id}` };
  const location = placements.value.get(`${entry.kind}:${entry.id}`)!;
  const after = ratio >= .5;
  return { folder: location.folder, before: after ? location.next : entry, hint: `${after ? "after" : "before"}:${entry.kind}:${entry.id}` };
}
// 进入及悬停落点都接收本地拖拽，确保快速跨行后立即松手也有效；筛选时停用排序。
function dragOver(event: DragEvent, entry?: CatalogEntry) {
  event.stopPropagation(); dropHint.value = "";
  if (!dragged.value || props.disabled || isFiltered.value || isManagementFiltered.value || !event.dataTransfer) return;
  const target = dropLocation(event, entry);
  if (dragged.value.kind === "folder" && !props.index.canMoveFolder(dragged.value.id, target.folder)) return;
  event.preventDefault(); event.dataTransfer.dropEffect = "move"; dropHint.value = target.hint;
}
// 离开落点及其子元素后清除提示，避免空白区或无效目标残留旧排序线。
function leaveDropTarget(event: DragEvent) {
  const target = event.currentTarget as HTMLElement;
  if (!(event.relatedTarget instanceof Node) || !target.contains(event.relatedTarget)) dropHint.value = "";
}
// 每次拖放只提交一次分类事务，嵌套落点不会冒泡重复移动。
function drop(event: DragEvent, entry?: CatalogEntry) {
  event.stopPropagation();
  if (!dragged.value || props.disabled || isFiltered.value || isManagementFiltered.value) { if (dragged.value) endDrag(); return; }
  event.preventDefault();
  const target = dropLocation(event, entry), source = dragged.value;
  if (source.kind === entry?.kind && source.id === entry.id) { endDrag(); return; }
  if (props.commit(() => props.index.move(source, target.folder, target.before))) {
    expanded.value.add(target.folder);
    if (source.kind === "definition") selectDefinition(source.id);
  }
  endDrag();
}
// 菜单键盘入口和鼠标入口共享视口边界定位。
async function openFolderMenu(event: MouseEvent | KeyboardEvent, id: string) {
  if (props.disabled) return;
  selectFolder(id);
  menuTrigger = event.currentTarget as HTMLElement;
  const bounds = menuTrigger.getBoundingClientRect();
  folderMenu.value = { id, x: "clientX" in event ? event.clientX : bounds.left, y: "clientY" in event ? event.clientY : bounds.bottom };
  await nextTick();
  const rect = menuElement.value!.getBoundingClientRect();
  folderMenu.value.x = Math.max(8, Math.min(folderMenu.value.x, window.innerWidth - rect.width - 8));
  folderMenu.value.y = Math.max(8, Math.min(folderMenu.value.y, window.innerHeight - rect.height - 8));
  menuElement.value?.querySelector<HTMLButtonElement>("button")?.focus();
}
// 关闭菜单时恢复触发位置，方向键只在可用项目之间移动。
function menuKey(event: KeyboardEvent) {
  if (event.key === "Escape" || event.key === "Tab") { folderMenu.value = undefined; menuTrigger?.focus(); return; }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const buttons = [...menuElement.value!.querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
  buttons[event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (current + (event.key === "ArrowUp" ? -1 : 1) + buttons.length) % buttons.length]?.focus();
}
// 外部点击关闭上下文菜单，不拦截正常页面操作。
function outside(event: PointerEvent) { if (!menuElement.value?.contains(event.target as Node)) folderMenu.value = undefined; }
onMounted(() => {
  document.addEventListener("pointerdown", outside, true);
  if (props.presentation !== "tags") emit("select", selectedFolder.value); // 从黑板页返回时，新增默认位置与当前可见选择一致。
});
onBeforeUnmount(() => { document.removeEventListener("pointerdown", outside, true); emit("dragend"); });
</script>

<template>
  <section class="catalog-browser" :class="{ 'catalog-management': presentation && presentation !== 'sidebar' }" aria-label="业务节点库">
    <div v-if="!presentation || presentation === 'sidebar'" class="library-section-heading">
      <button type="button" class="library-section-toggle" :aria-expanded="!collapsed" aria-controls="catalog-node-content" @click="emit('update:collapsed', !collapsed)">
        <span aria-hidden="true">{{ collapsed ? '▸' : '▾' }}</span>Go 业务节点
      </button>
      <button class="text-button" :disabled="disabled" @click="emit('manage')">管理</button>
    </div>
    <!-- 仅隐藏内容，保留目录展开、标签筛选及当前选择。 -->
    <div :id="!presentation || presentation === 'sidebar' ? 'catalog-node-content' : undefined" v-show="presentation === 'tags' || presentation === 'directories' || !collapsed">
    <template v-if="!presentation || presentation === 'sidebar' || isManager">
      <div v-if="!isManager" class="catalog-view-filters">
        <label>范围<select v-model="treeScope" aria-label="业务节点树范围" title="仅当前树显示该树引用的定义，以及工程中尚未使用的公共定义。"><option value="all">全部树</option><option value="current">仅当前树</option></select></label>
        <label>状态<select v-model="nodeStatus" aria-label="业务节点状态" title="有效：所选范围内有根节点可达的实例引用；无效：仅草稿引用或尚未使用。"><option value="all">全部</option><option value="valid">只看有效</option><option value="invalid">只看无效</option></select></label>
      </div>
      <div class="filter-heading">
        <button class="text-button" :aria-expanded="filtersOpen" @click="filtersOpen = !filtersOpen">{{ filtersOpen ? '▾' : '▸' }} 标签筛选（{{ filters.length }}）</button>
        <button v-if="filters.length" class="text-button" @click="filters = []">清除</button>
      </div>
      <div v-if="filtersOpen" class="tag-filters" aria-label="标签筛选（全部满足）">
        <label v-for="tag in allTags" :key="tag.id"><input v-model="filters" type="checkbox" :value="tag.id" />{{ tag.name }}</label>
        <p v-if="!allTags.length" class="muted">暂无标签，可在管理中创建。</p>
      </div>
    </template>
    <div v-if="presentation === 'tags'" class="tag-manager">
      <!-- 标题与说明集中在工具栏内，通过底色和边框区分下方标签列表。 -->
      <div class="tag-heading">
        <div class="tag-heading-copy"><strong>工程标签</strong><p class="muted">删除标签仅解除关联，保留业务定义。</p></div>
        <button :disabled="disabled" @click="openDialog('tag-create')">新建标签</button>
      </div>
      <div class="tag-list" role="list" aria-label="工程标签列表">
        <p v-if="!allTags.length" class="muted tag-empty">暂无标签，可创建后关联业务定义。</p>
        <div v-for="tag in allTags" :key="tag.id" class="tag-row" role="listitem"><span class="tag-details"><b>{{ tag.name }}</b><small>{{ index.tagMembers.get(tag.id)?.size ?? 0 }} 个关联定义</small></span>
          <div class="tag-actions">
            <button :disabled="disabled" :aria-label="`重命名标签 ${tag.name}`" @click="openDialog('tag-rename', tag.id)">重命名</button>
            <button :disabled="disabled" :aria-label="`删除标签 ${tag.name}`" @click="openDialog('tag-delete', tag.id)">删除</button>
          </div>
        </div>
      </div>
    </div>
    <template v-else>
    <p class="catalog-current muted" :title="index.folderPath(selectedFolder)">当前目录：{{ index.folderPath(selectedFolder) }}</p>
    <div v-if="presentation === 'directories'" class="directory-tools" aria-label="当前目录操作">
      <button :disabled="disabled" @click="openDialog('create', selectedFolder)">新建子目录</button>
      <button :disabled="disabled || !selectedFolder" @click="openDialog('rename', selectedFolder)">重命名</button>
      <button :disabled="disabled || !selectedFolder" @click="openDialog('move', selectedFolder, { kind: 'folder', id: selectedFolder, order: 0 })">移动到</button>
      <button :disabled="disabled || !selectedFolder || !index.isFolderEmpty(selectedFolder)" title="仅允许删除空目录" @click="deleteFolder(selectedFolder)">删除空目录</button>
    </div>
    <button class="catalog-root" :class="{ selected: !selectedFolder && !selectedDefinition, 'drop-inside': dropHint === 'root' }" :disabled="disabled"
      @click="selectFolder('')" @dragenter="dragOver($event)" @dragover="dragOver($event)" @dragleave="leaveDropTarget" @drop="drop($event)">▣ 根目录 <small>{{ index.definitions.size }} 个定义</small></button>
    <div v-if="isManagementFiltered" class="search-summary">匹配 {{ results.length }} / 总计 {{ index.definitions.size }} 个定义 <button @click="clearFilters">清除全部筛选</button></div>
    <template v-if="isFiltered && !isManager">
      <div class="search-summary">{{ results.length }} 个结果 <button @click="clearFilters">清除筛选</button></div>
      <div v-for="definition in results" :key="definition.id" class="catalog-result">
        <button class="catalog-definition" :class="{ selected: selectedDefinition === definition.id }" :disabled="disabled" draggable="true" @dragstart="startDrag($event, { kind: 'definition', id: definition.id, order: 0 })" @dragend="endDrag"
          @click="inspectDefinition(definition)" @contextmenu.prevent.stop="definitionMenu($event, definition)" @keydown.shift.f10.prevent.stop="definitionMenu($event, definition)">
          <span class="catalog-kind-icon" :title="definition.kind === 'action' ? '动作' : '条件'" :aria-label="definition.kind === 'action' ? '动作' : '条件'">{{ definition.kind === 'action' ? '▶' : '?' }}</span><span><b>{{ definition.name }}</b><small>{{ definition.goName }}</small></span>
        </button>
        <div class="result-path"><span>{{ index.definitionPath(definition.id) || '根目录' }}</span><button @click="locate(definition)">定位</button></div>
      </div>
      <p v-if="!results.length" class="muted">没有符合条件的业务定义。</p>
    </template>
    <div v-else class="catalog-tree" :class="{ 'drop-end': dropHint === 'root' }" aria-label="业务目录树"
      @dragenter="dragOver($event)" @dragover="dragOver($event)" @dragleave="leaveDropTarget" @drop="drop($event)">
      <div v-for="row in rows" :key="`${row.entry.kind}:${row.entry.id}`" class="catalog-row"
        :class="{ 'drop-before': dropHint === `before:${row.entry.kind}:${row.entry.id}`, 'drop-after': dropHint === `after:${row.entry.kind}:${row.entry.id}`, 'drop-inside': row.entry.kind === 'folder' && dropHint === `in:${row.entry.id}` }"
        :style="{ paddingLeft: `${Math.min(row.depth, 10) * 12}px` }" @dragenter="dragOver($event, row.entry)" @dragover="dragOver($event, row.entry)" @dragleave="leaveDropTarget" @drop="drop($event, row.entry)">
        <button v-if="row.entry.kind === 'folder'" class="catalog-folder" :class="{ selected: !selectedDefinition && selectedFolder === row.entry.id }" :disabled="disabled"
          :aria-expanded="visibleExpanded.has(row.entry.id)" :draggable="!isManagementFiltered" @dragstart="startDrag($event, row.entry)" @dragend="endDrag"
          @click="toggleFolder(row.entry.id)" @contextmenu.prevent.stop="openFolderMenu($event, row.entry.id)" @keydown.shift.f10.prevent.stop="openFolderMenu($event, row.entry.id)">
          <span>{{ visibleExpanded.has(row.entry.id) ? '▾' : '▸' }} ▰</span><b>{{ index.folders.get(row.entry.id)!.name }}</b>
        </button>
        <div v-else class="catalog-definition-row" :class="{ 'managed-definition': isManager, selected: isManager && selectedDefinition === row.entry.id }">
        <button :id="`${isManager ? 'catalog-managed-definition' : 'catalog-definition'}-${row.entry.id}`" class="catalog-definition" :class="{ selected: selectedDefinition === row.entry.id }" :disabled="disabled" :draggable="!isManagementFiltered"
          @dragstart="startDrag($event, row.entry)" @dragend="endDrag" @click="inspectDefinition(index.definitions.get(row.entry.id)!)"
          @contextmenu.prevent.stop="definitionMenu($event, index.definitions.get(row.entry.id)!)" @keydown.shift.f10.prevent.stop="definitionMenu($event, index.definitions.get(row.entry.id)!)">
          <span class="catalog-kind-icon" :title="index.definitions.get(row.entry.id)!.kind === 'action' ? '动作' : '条件'" :aria-label="index.definitions.get(row.entry.id)!.kind === 'action' ? '动作' : '条件'">{{ index.definitions.get(row.entry.id)!.kind === 'action' ? '▶' : '?' }}</span>
          <span><b>{{ index.definitions.get(row.entry.id)!.name }}</b>
            <small v-if="isManager">ID：{{ row.entry.id }} · Go 函数：{{ index.definitions.get(row.entry.id)!.goName }} · 参数：{{ index.definitions.get(row.entry.id)!.params?.length ?? 0 }} 个</small>
            <small v-else>{{ index.definitions.get(row.entry.id)!.goName }}</small>
          </span>
        </button>
        <div v-if="isManager" class="definition-actions">
          <button type="button" :disabled="disabled" :aria-label="`编辑 ${index.definitions.get(row.entry.id)!.name}`" @click="inspectDefinition(index.definitions.get(row.entry.id)!)">编辑</button>
          <button type="button" :disabled="disabled" :aria-label="`${index.definitions.get(row.entry.id)!.name}的更多操作`" @click="definitionMenu($event, index.definitions.get(row.entry.id)!)">更多 ⋯</button>
        </div>
        </div>
      </div>
      <p v-if="isManager || isViewFiltered ? !hasMatchingDefinitions : !rows.length" class="muted empty-note">{{ isManagementFiltered || isViewFiltered ? '没有符合条件的业务定义。' : '新建业务定义，或粘贴 JSON 导入已有定义。' }}</p>
    </div>
    </template>
    </div>
    <!-- 管理弹窗与承载节点在同一轮挂载，延迟解析目标以避免首次打开时目标尚未进入 DOM。 -->
    <Teleport defer :to="presentation === 'directories' || presentation === 'tags' ? '#catalog-manager-overlays' : 'body'">
      <div v-if="folderMenu" ref="menuElement" class="folder-context-menu" role="menu" aria-label="目录菜单" :style="{ left: `${folderMenu.x}px`, top: `${folderMenu.y}px` }" @keydown.stop="menuKey" @contextmenu.prevent>
        <button role="menuitem" @click="openDialog('create', folderMenu.id)">新建子目录</button>
        <button role="menuitem" @click="openDialog('rename', folderMenu.id)">重命名</button>
        <button role="menuitem" @click="openDialog('move', folderMenu.id, { kind: 'folder', id: folderMenu.id, order: 0 })">移动到</button>
        <button role="menuitem" :disabled="!index.isFolderEmpty(folderMenu.id)" :title="index.isFolderEmpty(folderMenu.id) ? '删除空目录，可撤销' : '目录中仍有定义或子目录，请先移走内容'" @click="deleteFolder(folderMenu.id)">删除目录</button>
        <small v-if="!index.isFolderEmpty(folderMenu.id)">仅允许删除空目录</small>
      </div>
      <dialog v-if="dialog" ref="modal" class="project-dialog organization-dialog" :aria-label="dialogTitle" @cancel.prevent.stop="cancelDialog" @keydown.stop>
        <form :class="{ 'pending-organization': hasPending }" @submit.prevent="submitDialog">
          <h2>{{ dialogTitle }}</h2>
          <label v-if="['create', 'rename', 'tag-create', 'tag-rename'].includes(dialog.mode)">名称<input v-model="dialog.name" required autofocus /></label>
          <label v-if="dialog.mode === 'move'">目标目录<select v-model="dialog.parentId"><option v-for="folder in folderChoices" :key="folder.id" :value="folder.id">{{ folder.name }}</option></select></label>
          <template v-if="dialog.mode === 'tags'">
            <p class="muted">可选择多个标签；取消勾选即解除关联。</p>
            <label v-for="tag in allTags" :key="tag.id" class="tag-choice"><input v-model="dialog.tagIds" type="checkbox" :value="tag.id" />{{ tag.name }}</label>
            <p v-if="!allTags.length">暂无标签，请先在管理窗口的“标签”页中创建。</p>
          </template>
          <p v-if="dialog.mode === 'tag-delete'">删除标签“{{ dialog.name }}”？将解除 {{ index.tagMembers.get(dialog.id)?.size ?? 0 }} 个定义的关联，定义本身会保留。此操作可撤销。</p>
          <p v-if="failure" class="identity-error" role="alert">{{ failure }}</p>
          <p v-if="pendingWarning" class="identity-error" role="alert">{{ pendingWarning }}</p>
          <div class="dialog-actions"><button type="button" @click="dialog = undefined">取消</button><button type="submit" :class="{ 'pending-apply': hasPending }" :disabled="disabled">{{ dialog.mode === 'tag-delete' ? '确认删除' : '确定' }}{{ hasPending ? ' · 未应用' : '' }}</button></div>
        </form>
      </dialog>
    </Teleport>
  </section>
</template>

<style scoped>
.pending-organization input,.pending-organization select{border-color:#e9aa43;box-shadow:0 0 0 2px #e9aa4333}.pending-apply{border-color:#e9aa43;background:#66451f;color:#fff0c7;font-weight:700}
.filter-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin: 6px 0; }
.catalog-view-filters { display: grid; grid-template-columns: repeat(auto-fit, minmax(85px, 1fr)); gap: 6px; margin: 8px 0; }
.catalog-view-filters label { display: grid; gap: 4px; min-width: 0; font-size: 11px; color: #9cabb8; }
.catalog-view-filters select { width: 100%; min-width: 0; box-sizing: border-box; padding: 5px; font-size: 11px; }
.directory-tools { display: flex; flex-wrap: wrap; gap: 8px; margin: 12px 0; }
.directory-tools button, .tag-row button { flex-shrink: 0; }
.catalog-browser { min-width: 0; }
.catalog-management .catalog-current { font-size: 13px; }
.catalog-management .tag-manager { margin: 0; padding: 0; border: 1px solid #304653; border-radius: 8px; background: #12212b; }
.catalog-management .tag-heading { margin: 0; padding: 12px 14px; gap: 12px; font-size: 14px; background: #1b2e39; border-bottom: 1px solid #38505e; border-radius: 7px 7px 0 0; }
.tag-heading-copy { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.tag-heading-copy strong { display: block; line-height: 1.4; }
.tag-heading-copy p { margin: 4px 0 0; font-size: 12px; line-height: 1.5; }
.catalog-management .tag-row { margin: 0; padding: 10px 14px; gap: 12px; font-size: 14px; border-bottom: 1px solid #2b404d; }
.catalog-management .tag-row:last-child { border-bottom: 0; }
.catalog-management .tag-details { min-width: 0; }
.catalog-management .tag-row small { display: block; font-size: 12px; color: #8fa5b6; margin-top: 3px; }
.tag-actions { display: flex; flex-shrink: 0; align-items: center; gap: 8px; }
.catalog-management .tag-heading button, .catalog-management .tag-row button { flex-shrink: 0; font-size: 13px; padding: 7px 11px; }
.tag-empty { margin: 0; padding: 18px 14px; font-size: 13px; line-height: 1.5; }
.catalog-root, .catalog-folder, .catalog-definition { display: flex; align-items: center; gap: 7px; width: 100%; text-align: left; border: 1px solid transparent; }
.catalog-root { justify-content: space-between; margin-top: 8px; background: #14202a; }
.catalog-root small, .catalog-current { font-size: 10px; }
.catalog-current { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.catalog-folder { padding: 8px 5px; background: transparent; color: #d7dfe5; }
.catalog-folder b { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }
.catalog-definition { background: #2b404c; padding: 7px; margin: 3px 0; min-height: 43px; }
.catalog-kind-icon { color: #85baff; font-size: 19px; width: 18px; flex-shrink: 0; text-align: center; }
.catalog-definition > span:last-child { min-width: 0; }
.catalog-definition b, .catalog-definition small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.catalog-definition b { font-size: 12px; font-weight: 500; }
.catalog-definition small { font-size: 10px; color: #9cabb8; }
.managed-definition { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin: 3px 0; padding: 7px; border: 1px solid transparent; border-radius: 6px; background: #2b404c; }
.managed-definition .catalog-definition { flex: 1 1 200px; min-width: 0; width: auto; border: 0; margin: 0; padding: 5px; background: transparent; }
.managed-definition .catalog-definition b { font-size: 14px; font-weight: 600; }
.managed-definition .catalog-definition small { margin-top: 4px; font-size: 11px; white-space: normal; overflow-wrap: anywhere; }
.definition-actions { display: flex; flex-shrink: 0; gap: 8px; margin-left: auto; }
.definition-actions button { font-size: 13px; padding: 8px 12px; }
.selected { border-color: #54b6ae; background: #213a41; }
.catalog-tree { position: relative; min-height: 80px; padding-bottom: 40px; }
.catalog-tree.drop-end::after { content: ""; position: absolute; bottom: 38px; left: 0; right: 0; border-top: 2px solid #68dfc6; pointer-events: none; }
.catalog-row { border-top: 2px solid transparent; border-bottom: 2px solid transparent; }
.drop-before { border-top-color: #68dfc6; }
.drop-after { border-bottom-color: #68dfc6; }
.drop-inside { outline: 2px solid #68dfc6; outline-offset: -2px; background: #274f49; }
.tag-filters { display: flex; flex-wrap: wrap; gap: 5px; font-size: 11px; }
.tag-filters label { display: flex; gap: 3px; align-items: center; background: #243541; padding: 3px 5px; border-radius: 4px; }
.tag-filters input, .tag-choice input { width: auto; margin: 0; }
.tag-manager { margin: 8px 0; padding: 8px; background: #101d27; border: 1px solid #384d5b; border-radius: 5px; }
.tag-heading, .tag-row { display: flex; align-items: center; gap: 4px; margin: 4px 0; font-size: 11px; }
.tag-heading strong, .tag-row span { flex: 1; overflow-wrap: anywhere; }
.tag-heading button, .tag-row button, .result-path button, .search-summary button { font-size: 10px; padding: 3px 5px; }
.search-summary, .result-path { display: flex; justify-content: space-between; align-items: center; font-size: 11px; gap: 5px; padding: 5px 0; }
.result-path { color: #8fa5b6; font-size: 10px; padding: 0 4px 6px; }
.result-path span { overflow-wrap: anywhere; }
.folder-context-menu { position: fixed; z-index: 2000; padding: 6px; min-width: 170px; background: #18242e; border: 1px solid #49606c; border-radius: 8px; box-shadow: 0 12px 32px #0008; }
.folder-context-menu button { display: block; width: 100%; text-align: left; background: transparent; border-color: transparent; }
.folder-context-menu small { display: block; color: #a1afba; padding: 6px; }
.organization-dialog label { display: grid; gap: 8px; }
.organization-dialog .tag-choice { display: flex; align-items: center; margin: 10px 0; }
.organization-dialog select { width: 100%; }
.organization-dialog { max-width: min(560px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); overflow-y: auto; box-sizing: border-box; }
.organization-dialog form { min-width: 0; }
.organization-dialog h2, .organization-dialog p, .organization-dialog label { overflow-wrap: anywhere; }
</style>
