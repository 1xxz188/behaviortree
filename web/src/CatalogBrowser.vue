<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import type { Definition } from "./project";
import type { CatalogEntry, CatalogOrganizationIndex } from "./catalogOrganization";

const props = defineProps<{
  collapsed: boolean; // 折叠偏好由父级持有，组件重建时仍保留。
  index: CatalogOrganizationIndex; // 工程分类索引，由父级历史边界维护。
  revision: number; // 索引原地更新后驱动可见行重算。
  search: string; // 与内置节点共用搜索输入。
  disabled: boolean; // 工程切换期间禁止修改。
  commit: (change: () => void) => boolean; // 分类变更进入统一撤销边界。
  failureMessage: string; // 父级操作失败的具体原因，在模态框内可见。
}>();
const emit = defineEmits<{
  "update:collapsed": [value: boolean]; // 只切换业务区显示，不修改工程或目录状态。
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
const selectedFolder = ref("");
const selectedDefinition = ref(""); // 当前操作的定义与目录互斥高亮，新增位置仍采用其所属目录。
const expanded = ref(new Set<string>([""]));
const filters = ref<string[]>([]);
const tagManager = ref(false);
const dialog = ref<OrganizationDialog>();
const modal = ref<HTMLDialogElement>();
const failure = ref("");
const dragged = ref<CatalogEntry>();
const dropHint = ref("");
const folderMenu = ref<{ id: string; x: number; y: number }>();
const menuElement = ref<HTMLElement>();
let menuTrigger: HTMLElement | undefined;
const moreOpen = ref(false);
const isFiltered = computed(() => Boolean(props.search.trim() || filters.value.length));
const allTags = computed(() => { props.revision; return [...props.index.tags.values()]; });
const results = computed(() => { props.revision; return props.index.search(props.search, filters.value); });
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
  const stack: VisibleRow[] = props.index.entries("").map(entry => ({ entry, depth: 0 })).reverse();
  while (stack.length) {
    const row = stack.pop()!;
    result.push(row);
    if (row.entry.kind === "folder" && expanded.value.has(row.entry.id)) {
      const children = props.index.entries(row.entry.id);
      for (let i = children.length - 1; i >= 0; i--) stack.push({ entry: children[i]!, depth: row.depth + 1 });
    }
  }
  return result;
});
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
  if (selectedFolder.value) expanded.value.add(selectedFolder.value);
  filters.value = filters.value.filter(id => props.index.tags.has(id));
});
watch(() => props.disabled, disabled => { if (disabled) { dialog.value = undefined; folderMenu.value = undefined; endDrag(); } });
watch(dialog, async value => {
  failure.value = "";
  if (!value) { modal.value?.close(); return; }
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
  const next = new Set(expanded.value);
  if (next.has(id)) next.delete(id); else next.add(id);
  expanded.value = next;
  selectFolder(id);
}
// 菜单和按钮共用草稿弹窗，避免使用浏览器阻塞式 prompt。
function openDialog(mode: OrganizationDialog["mode"], id = "", entry?: CatalogEntry) {
  if (props.disabled) return;
  folderMenu.value = undefined;
  dialog.value = { mode, id, entry, name: mode.startsWith("tag-") ? props.index.tags.get(id)?.name ?? "" : props.index.folders.get(id)?.name ?? "",
    parentId: mode === "create" ? id : entry?.kind === "folder" ? props.index.folders.get(entry.id)!.parentId : props.index.assignments.get(id)?.folderId ?? "",
    tagIds: [...(props.index.assignments.get(id)?.tagIds ?? [])] };
}
// 业务定义菜单由父级捕获身份，本组件仅接管移动和标签草稿。
function openMoveDefinition(id: string) { openDialog("move", id, { kind: "definition", id, order: 0 }); }
function openDefinitionTags(id: string) { openDialog("tags", id); }
defineExpose({ openMoveDefinition, openDefinitionTags });

// 所有表单提交通过父级事务；校验失败时保留草稿供纠正。
async function submitDialog() {
  const draft = dialog.value;
  if (!draft || props.disabled) return;
  const ok = props.commit(() => {
    switch (draft.mode) {
      case "create": { const folder = props.index.addFolder(draft.name, draft.parentId); expanded.value.add(draft.parentId); selectFolder(folder.id); break; }
      case "rename": props.index.renameFolder(draft.id, draft.name); break;
      case "move": props.index.move(draft.entry!, draft.parentId); expanded.value.add(draft.parentId); break;
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
  folderMenu.value = undefined;
  if (props.commit(() => props.index.deleteFolder(id))) {
    expanded.value.delete(id);
    if (selectedFolder.value === id) selectFolder("");
  }
}
// 过滤结果定位时清空筛选，沿祖先链展开目标所在目录。
async function locate(definition: Definition) {
  const folder = props.index.assignments.get(definition.id)?.folderId ?? "";
  let parent = folder;
  const next = new Set(expanded.value);
  while (parent) { next.add(parent); parent = props.index.folders.get(parent)!.parentId; }
  expanded.value = next; filters.value = []; emit("clearSearch"); selectFolder(folder);
  await nextTick();
  document.getElementById(`catalog-definition-${definition.id}`)?.scrollIntoView({ block: "nearest" });
  document.getElementById(`catalog-definition-${definition.id}`)?.focus();
}
// 分类拖拽与画布复制共用浏览器手势，但保留独立数据类型和落点语义。
function startDrag(event: DragEvent, entry: CatalogEntry) {
  if (props.disabled || !event.dataTransfer) return;
  dragged.value = entry;
  if (entry.kind === "definition") selectDefinition(entry.id); else selectFolder(entry.id);
  if (entry.kind === "definition") emit("drag", event, props.index.definitions.get(entry.id)!);
  else emit("dragend");
  event.dataTransfer.effectAllowed = entry.kind === "definition" ? "copyMove" : "move";
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
  if (!dragged.value || props.disabled || isFiltered.value || !event.dataTransfer) return;
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
  if (!dragged.value || props.disabled || isFiltered.value) return;
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
  emit("select", selectedFolder.value); // 从黑板页返回时，新增默认位置与当前可见选择一致。
});
onBeforeUnmount(() => { document.removeEventListener("pointerdown", outside, true); emit("dragend"); });
</script>

<template>
  <section class="catalog-browser" aria-label="业务节点库">
    <div class="library-section-heading">
      <button type="button" class="library-section-toggle" :aria-expanded="!collapsed" aria-controls="catalog-node-content" @click="emit('update:collapsed', !collapsed)">
        <span aria-hidden="true">{{ collapsed ? '▸' : '▾' }}</span>Go 业务节点
      </button>
      <button class="text-button" :disabled="disabled" @click="emit('manage')">管理定义</button>
    </div>
    <!-- 仅隐藏内容，保留目录展开、标签筛选及当前选择。 -->
    <div id="catalog-node-content" v-show="!collapsed">
    <div class="catalog-tools">
      <button :disabled="disabled" @click="openDialog('create', selectedFolder)">新建目录</button>
      <button :disabled="disabled" @click="emit('create', selectedFolder)">新建定义</button>
      <button :disabled="disabled" @click="emit('import', selectedFolder)">导入业务定义</button>
      <button :disabled="disabled" @click="tagManager = !tagManager">管理标签</button>
      <button :disabled="disabled" aria-label="业务定义更多操作" :aria-expanded="moreOpen" @click="moreOpen = !moreOpen">更多 ⋯</button>
    </div>
    <div v-if="moreOpen" class="catalog-more">
      <button :disabled="disabled || !index.definitions.size" @click="emit('transfer', 'copy'); moreOpen = false">复制全部定义 JSON</button>
      <button :disabled="disabled || !index.definitions.size" @click="emit('transfer', 'download'); moreOpen = false">导出全部定义 JSON</button>
    </div>
    <div v-if="allTags.length" class="tag-filters" aria-label="标签筛选（全部满足）">
      <label v-for="tag in allTags" :key="tag.id"><input v-model="filters" type="checkbox" :value="tag.id" />{{ tag.name }}</label>
    </div>
    <div v-if="tagManager" class="tag-manager">
      <div class="tag-heading"><strong>工程标签</strong><button :disabled="disabled" @click="openDialog('tag-create')">新建标签</button></div>
      <p v-if="!allTags.length" class="muted">暂无标签，可创建后关联业务定义。</p>
      <div v-for="tag in allTags" :key="tag.id" class="tag-row"><span>{{ tag.name }}</span>
        <button :disabled="disabled" :aria-label="`重命名标签 ${tag.name}`" @click="openDialog('tag-rename', tag.id)">改名</button>
        <button :disabled="disabled" :aria-label="`删除标签 ${tag.name}`" @click="openDialog('tag-delete', tag.id)">删除</button>
      </div>
    </div>
    <button class="catalog-root" :class="{ selected: !selectedFolder && !selectedDefinition, 'drop-inside': dropHint === 'root' }" :disabled="disabled"
      @click="selectFolder('')" @dragenter="dragOver($event)" @dragover="dragOver($event)" @dragleave="leaveDropTarget" @drop="drop($event)">▣ 根目录 <small>{{ index.definitions.size }} 个定义</small></button>
    <!-- 根目录也保留说明行，避免拖拽开始时切换选择导致整棵目录树跳动。 -->
    <p class="catalog-current muted" :title="index.folderPath(selectedFolder)">新增位置：{{ index.folderPath(selectedFolder) }}</p>
    <template v-if="isFiltered">
      <div class="search-summary">{{ results.length }} 个结果 <button @click="filters = []; emit('clearSearch')">清除筛选</button></div>
      <div v-for="definition in results" :key="definition.id" class="catalog-result">
        <button class="catalog-definition" :class="{ selected: selectedDefinition === definition.id }" :disabled="disabled" draggable="true" @dragstart="startDrag($event, { kind: 'definition', id: definition.id, order: 0 })" @dragend="endDrag"
          @click="inspectDefinition(definition)" @contextmenu.prevent.stop="definitionMenu($event, definition)" @keydown.shift.f10.prevent.stop="definitionMenu($event, definition)">
          <span>{{ definition.kind === 'action' ? '▶' : '?' }}</span><span><b>{{ definition.name }}</b><small>{{ definition.goName }}</small></span>
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
          :aria-expanded="expanded.has(row.entry.id)" draggable="true" @dragstart="startDrag($event, row.entry)" @dragend="endDrag"
          @click="toggleFolder(row.entry.id)" @contextmenu.prevent.stop="openFolderMenu($event, row.entry.id)" @keydown.shift.f10.prevent.stop="openFolderMenu($event, row.entry.id)">
          <span>{{ expanded.has(row.entry.id) ? '▾' : '▸' }} ▰</span><b>{{ index.folders.get(row.entry.id)!.name }}</b>
        </button>
        <button v-else :id="`catalog-definition-${row.entry.id}`" class="catalog-definition" :class="{ selected: selectedDefinition === row.entry.id }" :disabled="disabled" draggable="true"
          @dragstart="startDrag($event, row.entry)" @dragend="endDrag" @click="inspectDefinition(index.definitions.get(row.entry.id)!)"
          @contextmenu.prevent.stop="definitionMenu($event, index.definitions.get(row.entry.id)!)" @keydown.shift.f10.prevent.stop="definitionMenu($event, index.definitions.get(row.entry.id)!)">
          <span>{{ index.definitions.get(row.entry.id)!.kind === 'action' ? '▶' : '?' }}</span>
          <span><b>{{ index.definitions.get(row.entry.id)!.name }}</b><small>{{ index.definitions.get(row.entry.id)!.goName }}</small></span>
        </button>
      </div>
      <p v-if="!rows.length" class="muted empty-note">新建业务定义，或粘贴 JSON 导入已有定义。</p>
    </div>
    </div>
    <Teleport to="body">
      <div v-if="folderMenu" ref="menuElement" class="folder-context-menu" role="menu" aria-label="目录菜单" :style="{ left: `${folderMenu.x}px`, top: `${folderMenu.y}px` }" @keydown.stop="menuKey" @contextmenu.prevent>
        <button role="menuitem" @click="openDialog('create', folderMenu.id)">新建子目录</button>
        <button role="menuitem" @click="openDialog('rename', folderMenu.id)">重命名</button>
        <button role="menuitem" @click="openDialog('move', folderMenu.id, { kind: 'folder', id: folderMenu.id, order: 0 })">移动到</button>
        <button role="menuitem" :disabled="!index.isFolderEmpty(folderMenu.id)" :title="index.isFolderEmpty(folderMenu.id) ? '删除空目录，可撤销' : '目录中仍有定义或子目录，请先移走内容'" @click="deleteFolder(folderMenu.id)">删除目录</button>
        <small v-if="!index.isFolderEmpty(folderMenu.id)">仅允许删除空目录</small>
      </div>
      <dialog v-if="dialog" ref="modal" class="project-dialog organization-dialog" :aria-label="dialogTitle" @cancel.prevent="dialog = undefined" @keydown.stop>
        <form @submit.prevent="submitDialog">
          <h2>{{ dialogTitle }}</h2>
          <label v-if="['create', 'rename', 'tag-create', 'tag-rename'].includes(dialog.mode)">名称<input v-model="dialog.name" required autofocus /></label>
          <label v-if="dialog.mode === 'move'">目标目录<select v-model="dialog.parentId"><option v-for="folder in folderChoices" :key="folder.id" :value="folder.id">{{ folder.name }}</option></select></label>
          <template v-if="dialog.mode === 'tags'">
            <p class="muted">可选择多个标签；取消勾选即解除关联。</p>
            <label v-for="tag in allTags" :key="tag.id" class="tag-choice"><input v-model="dialog.tagIds" type="checkbox" :value="tag.id" />{{ tag.name }}</label>
            <p v-if="!allTags.length">暂无标签，请先在“管理标签”中创建。</p>
          </template>
          <p v-if="dialog.mode === 'tag-delete'">删除标签“{{ dialog.name }}”？将解除 {{ index.tagMembers.get(dialog.id)?.size ?? 0 }} 个定义的关联，定义本身会保留。此操作可撤销。</p>
          <p v-if="failure" class="identity-error" role="alert">{{ failure }}</p>
          <div class="dialog-actions"><button type="button" @click="dialog = undefined">取消</button><button type="submit">{{ dialog.mode === 'tag-delete' ? '确认删除' : '确定' }}</button></div>
        </form>
      </dialog>
    </Teleport>
  </section>
</template>

<style scoped>
.catalog-tools { display: flex; flex-wrap: wrap; gap: 5px; margin-bottom: 9px; }
.catalog-tools button, .catalog-more button { font-size: 11px; padding: 5px 7px; }
.catalog-more { display: grid; gap: 4px; margin-bottom: 8px; }
.catalog-root, .catalog-folder, .catalog-definition { display: flex; align-items: center; gap: 7px; width: 100%; text-align: left; border: 1px solid transparent; }
.catalog-root { justify-content: space-between; margin-top: 8px; background: #14202a; }
.catalog-root small, .catalog-current { font-size: 10px; }
.catalog-current { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.catalog-folder { padding: 8px 5px; background: transparent; color: #d7dfe5; }
.catalog-folder b { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }
.catalog-definition { background: #2b404c; padding: 7px; margin: 3px 0; min-height: 43px; }
.catalog-definition > span:first-child { color: #85baff; font-size: 19px; }
.catalog-definition > span:last-child { min-width: 0; }
.catalog-definition b, .catalog-definition small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.catalog-definition b { font-size: 12px; font-weight: 500; }
.catalog-definition small { font-size: 10px; color: #9cabb8; }
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
</style>
