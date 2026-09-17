<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import type { Definition, Parameter } from "./project";
import type { DefinitionKind, ValueType } from "./enums";
import { valueTypes } from "./enums";
import { PARAM_TYPE_META, parameterTypeTooltip } from "./parameterTypeMeta";
import { parseJSON, stringifyJSON } from "./json";
import { catalogConflicts, mergeCatalog, parseCatalog } from "./catalog";
import type { CatalogChoice } from "./catalog";

const props = withDefaults(defineProps<{
  catalog: Definition[]; // 当前工程目录；提交成功前不修改。
  initialKind?: DefinitionKind; // 从节点属性打开时，预选种类并允许绑定。
  initialMode?: "create" | "edit" | "import" | "manage"; // 新建与编辑使用独立工作页。
  initialDefinition?: Definition; // 右键入口指定的已有定义，表单只编辑其副本。
}>(), { initialMode: "manage" });
const emit = defineEmits<{
  apply: [catalog: Definition[], bindID?: string];
  close: [];
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
const editingID = ref("");
const draft = ref({ id: "", name: "", kind: props.initialKind ?? "action", goName: "", events: "" });
const parameters = ref<ParameterDraft[]>([]);
let parameterKey = 0;
const incoming = ref<Definition[]>([]);
const choices = ref<Record<string, CatalogChoice | "">>(Object.create(null));
const acknowledged = ref(false);
const bindNew = ref(Boolean(props.initialKind));
const error = ref("");
const busy = ref(false);
const fileLabel = ref("");
const dialog = ref<HTMLElement>();
const conflictRows = computed(() => catalogConflicts(props.catalog, incoming.value));
const updatesExisting = computed(() => conflictRows.value.some((row) => choices.value[row.id] === "replace"));
const dialogTitle = computed(() => mode.value === "edit" ? `编辑业务定义：${draft.value.name || editingID.value}`
  : mode.value === "create" ? "新建业务定义" : "Go 业务节点目录"); // 标题明确区分修改已有定义和新增定义。

// 切换页面时清空上次待提交内容，防止误应用其他工作页的导入。
function changeMode(next: "create" | "edit" | "import" | "manage") {
  if (busy.value) return;
  mode.value = next;
  incoming.value = [];
  choices.value = Object.create(null);
  acknowledged.value = false;
  error.value = "";
  editingID.value = "";
  draft.value = { id: "", name: "", kind: props.initialKind ?? "action", goName: "", events: "" };
  parameters.value = [];
  fileLabel.value = "";
  bindNew.value = Boolean(props.initialKind);
}

// 稳定 ID 不允许在编辑表单中改名；参数草稿在提交时由工程入口统一同步。
function editDefinition(item: Definition) {
  changeMode("edit");
  editingID.value = item.id;
  draft.value = { id: item.id, name: item.name, kind: item.kind, goName: item.goName, events: (item.events ?? []).join("\n") };
  parameters.value = (item.params ?? []).map((parameter) => ({
    key: parameterKey++, name: parameter.name, type: parameter.type, comment: parameter.comment ?? "",
    defaultText: parameter.default === undefined ? "" : stringifyJSON(parameter.default),
    enumText: (parameter.enum ?? []).join("\n"),
  }));
  bindNew.value = false;
}

// 直接编辑入口在首次渲染前填充草稿，避免短暂显示空白的新建表单。
if (props.initialMode === "edit") {
  if (props.initialDefinition) editDefinition(props.initialDefinition);
  else changeMode("manage");
}

// 添加独立参数行，不触碰工程中的已绑定参数。
function addParameter() {
  parameters.value.push({ key: parameterKey++, name: "", type: "string", comment: "", defaultText: "", enumText: "" });
}

// 逐行处理枚举和事件列表，空行用于表单排版而不生成空值。
function lines(value: string): string[] {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

// 构造新声明时使用无损 JSON，默认值是否合法由 Go 统一校验。
function draftDefinition(): Definition {
  return {
    id: draft.value.id.trim(), name: draft.value.name.trim(), kind: draft.value.kind,
    goName: draft.value.goName.trim(), events: lines(draft.value.events),
    params: parameters.value.map((parameter): Parameter => ({
      name: parameter.name.trim(), type: parameter.type,
      ...(parameter.comment.trim() ? { comment: parameter.comment.trim() } : {}),
      ...(parameter.defaultText.trim() ? { default: parseJSON(parameter.defaultText) } : {}),
      ...(parameter.type === "enum" ? { enum: lines(parameter.enumText) } : {}),
    })),
  };
}

// 读取文件仅暂存目录，全部冲突处理并验证成功前不写入工程。
async function readCatalog(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file || busy.value) return;
  busy.value = true;
  error.value = "";
  incoming.value = [];
  choices.value = Object.create(null);
  acknowledged.value = false;
  fileLabel.value = "";
  try {
    const parsed = parseCatalog(parseJSON(await file.text()));
    // 先计算冲突以检测当前工程目录结构异常，再将文件放入响应式状态。
    catalogConflicts(props.catalog, parsed);
    incoming.value = parsed;
    fileLabel.value = `${file.name} · ${parsed.length} 个定义`;
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    busy.value = false;
    input.value = "";
  }
}

// 一次提交校验最终合并目录，服务端拒绝时保留表单和原工程，便于纠正。
async function applyCatalog() {
  if (busy.value) return;
  error.value = "";
  try {
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
    const replaced = catalogConflicts(props.catalog, candidate).some((row) => decisions.get(row.id) === "replace");
    if (replaced && !acknowledged.value) throw new Error("请确认更新定义对已有绑定和手写 Go 函数的影响");
    const merged = mergeCatalog(props.catalog, candidate, decisions);
    busy.value = true;
    const response = await fetch("/api/catalog", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: stringifyJSON(merged),
    });
    const result = parseJSON<unknown>(await response.text());
    if (!response.ok) {
      const detail = result as { error?: string };
      throw new Error(detail?.error || `目录校验失败（${response.status}）`);
    }
    const validated = parseCatalog(result);
    const bindID = mode.value === "create" && !editingID.value && bindNew.value && draft.value.kind === props.initialKind
      ? candidate[0]!.id : undefined;
    emit("apply", validated, bindID);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    busy.value = false;
  }
}

// 关闭弹窗不会提交草稿；网络提交期间保留弹窗防止用户误判提交结果。
function close() {
  if (!busy.value) emit("close");
}

// 将键盘焦点限制在弹窗内，避免快捷键误操作背景画布。
function dialogKeydown(event: KeyboardEvent) {
  event.stopPropagation();
  if (event.key === "Escape") { event.preventDefault(); close(); return; }
  if (event.key !== "Tab") return;
  const controls = dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]');
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
onUnmounted(() => previousFocus?.focus());
</script>

<template>
  <Teleport to="body">
    <div class="catalog-overlay" @click.self="close">
      <section ref="dialog" class="catalog-dialog" role="dialog" aria-modal="true" aria-labelledby="catalog-title" tabindex="-1" @keydown="dialogKeydown">
        <header><div><h2 id="catalog-title">{{ dialogTitle }}</h2><p>{{ mode === 'edit' ? `正在修改已有${draft.kind === 'action' ? '动作' : '条件'}定义 · ID：${editingID}` : '声明可绑定的动作与条件，业务函数由 Go 实现。' }}</p></div><button type="button" :disabled="busy" aria-label="关闭目录管理" @click="close">关闭</button></header>
        <nav v-if="mode === 'edit'" aria-label="编辑定义导航">
          <button type="button" :disabled="busy" @click="changeMode('manage')">返回已有定义</button>
          <span>编辑定义</span>
        </nav>
        <nav v-else aria-label="目录操作">
          <button type="button" :class="{ active: mode === 'manage' }" :disabled="busy" @click="changeMode('manage')">已有定义（{{ catalog.length }}）</button>
          <button type="button" :class="{ active: mode === 'create' }" :disabled="busy" @click="changeMode('create')">新建业务定义</button>
          <button type="button" :class="{ active: mode === 'import' }" :disabled="busy" @click="changeMode('import')">导入目录</button>
        </nav>
        <div class="catalog-content">
          <div v-if="mode === 'manage'" class="catalog-existing">
            <p v-if="!catalog.length" class="catalog-empty">目录为空。新建业务定义，或导入由 model.ExportCatalog 导出的 JSON 数组，即可在节点属性中选择绑定。</p>
            <article v-for="item in catalog" :key="item.id"><div><strong>{{ item.name }}</strong><small>{{ item.kind === 'action' ? '动作' : '条件' }} · {{ item.id }} · {{ item.goName }} · {{ item.params?.length ?? 0 }} 个参数</small></div><button type="button" @click="editDefinition(item)">编辑定义</button></article>
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
            <label class="catalog-events">宿主事件（每行一个，可留空）<textarea v-model="draft.events" :disabled="busy" placeholder="movement_completed" rows="2" /></label>
            <div v-if="editingID" class="catalog-warning"><strong>更新已有定义会影响所有引用它的节点。</strong><p>节点参数会按新定义同步：保留兼容绑定，删除已移除参数，重置不兼容绑定；新增参数采用默认值或保持未绑定。应用后自动校验工程，请按提示配置参数，并同步手写 Go 实现。</p><label class="catalog-check"><input v-model="acknowledged" :disabled="busy" type="checkbox" />我已了解影响，确认更新此定义</label></div>
            <label v-if="initialKind && !editingID && draft.kind === initialKind" class="catalog-check"><input v-model="bindNew" :disabled="busy" type="checkbox" />保存并绑定当前节点</label>
          </form>
          <div v-else class="catalog-import">
            <p>选择 JSON 目录数组。新 ID 会追加；相同 ID 的变更由你逐项决定，现有目录中的其他定义会保留。</p>
            <label>目录文件<input type="file" accept=".json,application/json" :disabled="busy" @change="readCatalog" /></label>
            <p v-if="fileLabel">{{ fileLabel }}</p>
            <article v-for="row in conflictRows" :key="row.id" class="catalog-conflict">
              <strong>定义冲突：{{ row.id }}</strong>
              <div class="catalog-compare"><div><small>当前工程</small><pre>{{ stringifyJSON(row.current, 2) }}</pre></div><div><small>导入定义</small><pre>{{ stringifyJSON(row.incoming, 2) }}</pre></div></div>
              <label>处理方式<select v-model="choices[row.id]" :disabled="busy" @change="acknowledged = false"><option :value="undefined" disabled>请选择处理方式</option><option value="keep">保留现有定义</option><option value="replace">使用导入定义</option></select></label>
            </article>
            <p v-if="fileLabel && !conflictRows.length" class="catalog-hint">没有同 ID 变更；应用时将验证合并后的整个目录。</p>
            <div v-if="updatesExisting" class="catalog-warning"><p>更新后节点参数会按新定义同步：保留兼容绑定，删除已移除参数，重置不兼容绑定；新增参数采用默认值或保持未绑定。应用后自动校验工程，请按提示配置参数，并同步手写 Go 实现。</p><label class="catalog-check"><input v-model="acknowledged" :disabled="busy" type="checkbox" />确认使用所选导入定义更新已有绑定</label></div>
          </div>
          <p v-if="error" class="catalog-error" role="alert">{{ error }}</p>
        </div>
        <footer><span>定义保存后，可在代码面板预览业务函数骨架。</span><button type="button" :disabled="busy" @click="close">取消</button><button v-if="mode !== 'manage'" class="catalog-primary" type="button" :disabled="busy || (mode === 'import' && !fileLabel)" @click="applyCatalog">{{ busy ? '校验中…' : mode === 'import' ? '验证并合并目录' : mode === 'edit' ? '验证并保存修改' : '验证并保存定义' }}</button></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.catalog-type-info{color:#94aebb;cursor:help;font-size:12px}.catalog-type-hint{line-height:1.5;overflow-wrap:anywhere}
.catalog-overlay{position:fixed;inset:0;z-index:1000;display:grid;place-items:center;padding:24px;background:#071017b8;backdrop-filter:blur(4px);color:#d8e6ed;font:14px/1.5 system-ui,sans-serif}
.catalog-dialog{width:min(940px,100%);max-height:calc(100dvh - 48px);display:flex;flex-direction:column;border:1px solid #365260;border-radius:14px;background:#14232d;box-shadow:0 24px 90px #0008;outline:none}
header,footer,nav{display:flex;align-items:center;gap:12px;padding:18px 24px;flex-shrink:0}header{justify-content:space-between}h2,h3,p{margin:0}h2{font-size:20px}h3{font-size:15px}header p,.catalog-hint,small,footer span{color:#94aebb;font-size:12px}nav{padding-top:0;border-bottom:1px solid #2d414d;flex-wrap:wrap}.catalog-content{padding:22px 24px;overflow:auto;min-height:160px}button,input,select,textarea{box-sizing:border-box;font:inherit;color:inherit;border:1px solid #385361;border-radius:6px;background:#10202a}button{padding:8px 12px;cursor:pointer;white-space:nowrap}button:hover{border-color:#86dec1;background:#1b3540}button:disabled{opacity:.5;cursor:wait}button.active,.catalog-primary{color:#a3f1d9;border-color:#64cfae;background:#1c3b3d}input,select,textarea{padding:9px 10px;width:100%;min-width:0}input:read-only{color:#8297a4}input:focus,select:focus,textarea:focus,button:focus-visible{outline:2px solid #86dec1;outline-offset:2px}label{display:flex;flex-direction:column;gap:6px;min-width:0}.catalog-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}.catalog-section-title{display:flex;align-items:center;justify-content:space-between;margin:24px 0 8px}.catalog-parameter{display:grid;grid-template-columns:1fr 130px 1.4fr auto;align-items:end;gap:10px;margin-top:12px;padding:12px;border:1px solid #2e4754;border-radius:8px}.catalog-wide{grid-column:1/-1}.catalog-events{margin-top:20px}.catalog-check{display:flex;flex-direction:row;align-items:flex-start;gap:9px;margin-top:14px}.catalog-check input{width:auto;margin:4px 0 0}.catalog-warning{margin-top:18px;border:1px solid #947039;background:#3c3222;border-radius:8px;padding:14px;color:#f0d7a5}.catalog-warning p{margin-top:5px}.catalog-existing article{display:flex;gap:12px;align-items:center;justify-content:space-between;padding:15px 0;border-bottom:1px solid #2d414d}.catalog-existing small{display:block;margin-top:4px}.catalog-empty{padding:24px;color:#a5becb;border:1px dashed #456170;border-radius:8px}.catalog-import>label,.catalog-import>p{margin-bottom:16px}.catalog-conflict{margin-top:18px;border:1px solid #725c3a;border-radius:8px;padding:16px}.catalog-compare{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin:12px 0}.catalog-compare>div{min-width:0}pre{padding:10px;margin:5px 0 0;max-height:240px;overflow:auto;border-radius:5px;background:#0c1821;font-size:12px}.catalog-error{margin-top:18px;padding:12px;border:1px solid #ad6262;background:#46282d;border-radius:6px;color:#ffc2c2;overflow-wrap:anywhere}footer{border-top:1px solid #2d414d}footer span{margin-right:auto}
@media(max-width:650px){.catalog-overlay{padding:8px}.catalog-dialog{max-height:calc(100dvh - 16px)}header,footer,nav,.catalog-content{padding:14px}.catalog-grid,.catalog-compare{grid-template-columns:1fr}.catalog-parameter{grid-template-columns:1fr 1fr}.catalog-parameter>label:nth-child(3){grid-column:1/-1}footer{flex-wrap:wrap}footer span{width:100%}}
</style>
