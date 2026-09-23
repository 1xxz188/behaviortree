<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import { clone } from "./project.ts";
import type { EventDefinition, Project } from "./project.ts";
import { allocateEventID, EventRegistryIndex, removeEvent, validateEventRegistry, validateNextEventID } from "./eventRegistry.ts";
import { parseJSON, stringifyJSON } from "./json.ts";

const props = defineProps<{
  project: Project; // 当前工程；各表单只编辑自己的独立草稿。
  index: EventRegistryIndex; // 引用计数共用画布索引。
  revision: number; // 任意事务后使旧草稿失效。
  commit: (change: () => void) => void; // 由父级建立单次撤销事务。
}>();
const emit = defineEmits<{ close: [] }>();
const form = ref<{ name: string; codeName: string } | null>(null);
const editing = shallowRef<EventDefinition | null>(null);
const formEvents = shallowRef(props.project.events);
const formOriginal = shallowRef<EventDefinition | null>(null);
const error = ref("");
const busy = ref(false); // 服务端候选校验期间阻止重复提交。
const deleteTarget = shallowRef<EventDefinition | null>(null);
const deleteEvents = shallowRef(props.project.events);
const deleteOriginal = shallowRef<EventDefinition | null>(null);
const deleteCounts = computed(() => deleteTarget.value ? props.index.counts(deleteTarget.value.id) : null);
const manager = ref<HTMLElement>();
const formDialog = ref<HTMLElement>();
const formNameInput = ref<HTMLInputElement>();
const deleteDialog = ref<HTMLElement>();
const deleteCancelButton = ref<HTMLButtonElement>();
let previousFocus: HTMLElement | null = null;
let formTrigger: HTMLElement | null = null;
let deleteTrigger: HTMLElement | null = null;
let active = true; // 卸载后迟到的候选校验不得提交工程。

// 工程替换关闭旧目标；成员数组或目标内容变化才使旧表单失效。
watch(() => props.project, () => { dismissForm(); dismissDelete(); });
watch(() => props.revision, () => { if (deleteTarget.value && (deleteEvents.value !== props.project.events || props.index.eventByID.get(deleteTarget.value.id) !== deleteTarget.value || !sameEvent(deleteTarget.value, deleteOriginal.value))) dismissDelete(); });

// 固定成员字段，识别同一数组内就地修改的事件。
function sameEvent(current: EventDefinition, original: EventDefinition | null): boolean {
  return !!original && current.id === original.id && current.name === original.name
    && current.codeName === original.codeName && current.description === original.description;
}

// 创建和编辑共用草稿；ID 只在真正保存新成员时分配。
function openForm(target?: EventDefinition): void {
  formTrigger = document.activeElement as HTMLElement | null;
  editing.value = target ?? null;
  form.value = { name: target?.name ?? "", codeName: target?.codeName ?? "" };
  formEvents.value = props.project.events;
  formOriginal.value = target ? { ...target } : null;
  error.value = "";
  void nextTick(() => formNameInput.value?.focus());
}

// 关闭独立表单后将键盘焦点还给打开它的按钮。
function dismissForm(): void {
  form.value = null;
  editing.value = null;
  error.value = "";
  const trigger = formTrigger;
  formTrigger = null;
  void nextTick(() => trigger?.isConnected && trigger.focus());
}

// 两种独立弹窗共用 Tab 焦点约束，避免键盘操作落到背后的管理列表。
function trapDialogTab(dialog: HTMLElement | undefined, event: KeyboardEvent): void {
  if (event.key !== "Tab") return;
  const controls = [...(dialog?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])];
  if (!controls.length) return;
  const first = controls[0]!;
  const last = controls[controls.length - 1]!;
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
}

// 表单弹窗独立处理 Escape 和焦点循环。
function keydownForm(event: KeyboardEvent): void {
  event.stopPropagation();
  if (busy.value) return;
  if (event.key === "Escape") { event.preventDefault(); dismissForm(); return; }
  trapDialogTab(formDialog.value, event);
}

function closeForm(): void { if (!busy.value) dismissForm(); }

// 候选注册表先执行完整校验，成功后才发布一次事务。
async function saveMember(): Promise<void> {
  if (!form.value || busy.value) return;
  try {
    error.value = "";
    if (formEvents.value !== props.project.events || (editing.value && (props.index.eventByID.get(editing.value.id) !== editing.value || !sameEvent(editing.value, formOriginal.value)))) throw new Error("事件表已变化，请重新打开表单");
    const current = editing.value;
    validateNextEventID(props.project.nextEventId, props.project.events);
    const allocated = current ? null : allocateEventID(props.project.nextEventId);
    const member: EventDefinition = {
      id: current?.id ?? allocated!.id,
      name: form.value.name.trim(), codeName: form.value.codeName.trim(),
      ...(current?.description !== undefined ? { description: current.description } : {}),
    };
    const events = current ? props.project.events.map(item => item.id === current.id ? member : item) : [...props.project.events, member];
    validateEventRegistry(events, props.project.catalog, props.project.eventEnumDescription);
    if (current && member.name === current.name && member.codeName === current.codeName) { dismissForm(); return; }
    const owner = props.project;
    const revision = props.revision;
    const candidate = clone(owner);
    candidate.events = events;
    if (allocated) candidate.nextEventId = allocated.nextEventId;
    busy.value = true;
    // Go Decode 校验完整生成符号；树草稿诊断不会阻止合法事件保存。
    const response = await fetch("/api/validate", { method: "POST", headers: { "Content-Type": "application/json" }, body: stringifyJSON(candidate) });
    if (!response.ok) {
      const detail = parseJSON<{ error?: string }>(await response.text());
      throw new Error(detail.error ?? `事件校验失败（${response.status}）`);
    }
    if (!active || props.project !== owner || props.revision !== revision) throw new Error("工程已变化，请重新打开事件表单");
    props.commit(() => { props.project.events = events; if (allocated) props.project.nextEventId = allocated.nextEventId; });
    dismissForm();
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause); }
  finally { busy.value = false; }
}

// 删除确认固定当前成员表；仅成员状态变化使确认失效。
function confirmDelete(target: EventDefinition): void {
  deleteTrigger = document.activeElement as HTMLElement | null;
  deleteTarget.value = target;
  deleteEvents.value = props.project.events;
  deleteOriginal.value = { ...target };
  error.value = "";
  void nextTick(() => deleteCancelButton.value?.focus());
}

// 关闭删除弹窗后还原焦点；已删除的按钮不存在时回到管理窗口。
function dismissDelete(): void {
  deleteTarget.value = null;
  const trigger = deleteTrigger;
  deleteTrigger = null;
  void nextTick(() => (trigger?.isConnected ? trigger : manager.value)?.focus());
}

// 删除弹窗捕获 Escape 和 Tab，避免按键传到画布或管理窗口。
function keydownDelete(event: KeyboardEvent): void {
  event.stopPropagation();
  if (busy.value) return;
  if (event.key === "Escape") { event.preventDefault(); dismissDelete(); return; }
  trapDialogTab(deleteDialog.value, event);
}

// 确认目标仍是同一工程声明，再一次清除注册表和全部定义引用。
function deleteMember(): void {
  const target = deleteTarget.value;
  if (!target || busy.value) return;
  try {
    if (deleteEvents.value !== props.project.events || props.index.eventByID.get(target.id) !== target || !sameEvent(target, deleteOriginal.value)) throw new Error("事件表已变化，请重新确认删除");
    const candidate = removeEvent(props.project, target.id);
    props.commit(() => { props.project.events = candidate.events; props.project.catalog = candidate.catalog; });
    dismissDelete();
    if (editing.value?.id === target.id) dismissForm();
  } catch (cause) { error.value = String(cause); dismissDelete(); }
}

// 快捷键停留在管理层，不会删除画布选中节点。
function keydown(event: KeyboardEvent): void {
  event.stopPropagation();
  if (busy.value) return;
  if (event.key === "Escape") { event.preventDefault(); emit("close"); }
}
function close(): void { if (!busy.value) emit("close"); }
onMounted(() => { previousFocus = document.activeElement as HTMLElement | null; manager.value?.focus(); });
onUnmounted(() => { active = false; previousFocus?.focus(); });
</script>

<template>
  <Teleport to="body">
    <div class="event-manager-backdrop" @click.self="close">
      <section ref="manager" class="event-manager" role="dialog" :aria-modal="form || deleteTarget ? undefined : 'true'" :aria-hidden="form || deleteTarget ? 'true' : undefined" :inert="!!form || !!deleteTarget" aria-labelledby="event-manager-title" tabindex="-1" @keydown="keydown">
        <header><div><h2 id="event-manager-title">事件管理</h2><p>管理当前工程的事件，修改后将用于所有行为树和业务定义。</p></div><button type="button" :disabled="busy" @click="close">关闭</button></header>
        <div class="event-manager-body">
          <section class="event-manager-section"><div class="event-manager-heading"><h3>事件（{{ project.events.length }}）</h3><button type="button" :disabled="busy" @click="openForm()">新增事件</button></div>
            <p v-if="!project.events.length" class="event-manager-empty">尚无事件。点击“新增事件”创建。</p>
            <article v-for="item in project.events" :key="item.id"><div class="event-manager-item"><strong>{{ item.name }}</strong><small>Event{{ item.codeName }} · ID {{ item.id }}</small><small>{{ index.counts(item.id).definitions }} 个定义 · {{ index.counts(item.id).nodes }} 个节点</small></div><div class="event-manager-actions"><button type="button" :disabled="busy" @click="openForm(item)">编辑</button><button type="button" :disabled="busy" @click="confirmDelete(item)">删除</button></div></article>
          </section>
          <p v-if="error && !form" role="alert" class="event-manager-error">{{ error }}</p>
        </div>
      </section>
      <div v-if="form" class="event-form-backdrop" @click.self="closeForm">
        <section ref="formDialog" class="event-form-dialog" role="dialog" aria-modal="true" aria-labelledby="event-form-title" tabindex="-1" @keydown="keydownForm">
          <form @submit.prevent="saveMember">
            <header><div><h2 id="event-form-title">{{ editing ? '编辑事件' : '新增事件' }}</h2><p>设置事件名称与代码名，保存后立即应用到当前工程。</p></div><button type="button" :disabled="busy" aria-label="关闭事件表单" @click="closeForm">×</button></header>
            <div class="event-form-content"><div class="event-manager-fields"><label>显示名称<input ref="formNameInput" v-model="form.name" :disabled="busy" required /></label><label>事件代码名<input v-model="form.codeName" :disabled="busy" required pattern="[A-Z][A-Za-z0-9_]{0,79}" /></label><label>事件 ID<input :value="editing?.id ?? project.nextEventId" readonly /><small v-if="!editing">保存时分配</small></label></div><p v-if="error" role="alert" class="event-manager-error">{{ error }}</p><div class="event-manager-actions"><button type="button" :disabled="busy" @click="closeForm">取消</button><button type="submit" :disabled="busy">{{ busy ? '校验中…' : '保存事件' }}</button></div></div>
          </form>
        </section>
      </div>
      <div v-if="deleteTarget" class="event-form-backdrop" @click.self="dismissDelete">
        <section ref="deleteDialog" class="event-form-dialog event-delete-dialog" role="alertdialog" aria-modal="true" aria-labelledby="event-delete-title" aria-describedby="event-delete-description" tabindex="-1" @keydown="keydownDelete">
          <header><div><h2 id="event-delete-title">删除“{{ deleteTarget.name }}”（Event{{ deleteTarget.codeName }}）？</h2></div><button type="button" aria-label="关闭删除确认" @click="dismissDelete">×</button></header>
          <div class="event-form-content"><p id="event-delete-description">将从整个工程的 {{ deleteCounts?.definitions }} 个业务定义、{{ deleteCounts?.trees }} 棵树的 {{ deleteCounts?.nodes }} 个节点中清除此事件依赖。不会删除节点、定义或业务代码。无其他依赖的节点将不再因该事件重评。该操作可撤销。</p><div class="event-manager-actions"><button ref="deleteCancelButton" type="button" @click="dismissDelete">取消</button><button type="button" @click="deleteMember">确认删除</button></div></div>
        </section>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.event-manager-backdrop{position:fixed;inset:0;z-index:1100;background:#071017b8;display:grid;place-items:center;padding:16px;color:#d8e6ed;font:14px/1.5 system-ui,sans-serif}.event-manager,.event-form-dialog{width:min(760px,100%);max-height:calc(100dvh - 32px);background:#14232d;border:1px solid #365260;border-radius:14px;display:flex;flex-direction:column;box-shadow:0 24px 90px #0008;outline:none}.event-manager header,.event-form-dialog header{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:18px 24px;border-bottom:1px solid #365260}.event-manager h2,.event-manager h3,.event-manager p,.event-form-dialog h2,.event-form-dialog p{margin:0}.event-manager h2,.event-form-dialog h2{font-size:20px}.event-manager h3{font-size:16px}.event-manager header p,.event-manager small,.event-form-dialog header p,.event-form-dialog small{color:#9bb4c1;font-size:12px}.event-manager-body{padding:20px 24px;overflow:auto}.event-manager-section{display:grid;gap:12px;margin-bottom:18px;padding:16px;border:1px solid #365260;border-radius:8px}.event-manager-section article{display:flex;align-items:center;justify-content:space-between;gap:14px;border-top:1px solid #365260;padding-top:12px}.event-manager-item{min-width:0;display:grid;gap:2px}.event-manager-item strong,.event-manager-item small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.event-manager-empty{color:#9bb4c1}.event-form-dialog label{display:grid;gap:6px}.event-manager-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.event-manager-fields label:last-child{grid-column:1/-1}.event-manager-heading,.event-manager-actions{display:flex;align-items:center;justify-content:space-between;gap:8px}.event-manager-actions{justify-content:flex-end;flex:none}.event-manager button,.event-form-dialog button,.event-form-dialog input{font:inherit;color:inherit;border:1px solid #456170;border-radius:6px;background:#10202a;padding:8px 10px}.event-manager button,.event-form-dialog button{cursor:pointer}.event-manager button:hover,.event-form-dialog button:hover{border-color:#86dec1}.event-manager button:disabled,.event-form-dialog button:disabled{opacity:.55;cursor:default}.event-form-dialog input{width:100%;box-sizing:border-box}.event-manager button:focus-visible,.event-form-dialog button:focus-visible,.event-form-dialog input:focus{outline:2px solid #86dec1;outline-offset:2px}.event-delete-dialog{border-color:#b58b49}.event-delete-dialog #event-delete-description{line-height:1.7}.event-manager-error{color:#ffc2c2;overflow-wrap:anywhere}.event-form-backdrop{position:fixed;inset:0;z-index:1;background:#071017cf;display:grid;place-items:center;padding:16px}.event-form-dialog{width:min(520px,100%)}.event-form-dialog form{display:flex;flex-direction:column;min-height:0}.event-form-content{padding:20px 24px;overflow:auto;display:grid;gap:18px}
@media(max-width:560px){.event-manager header,.event-manager-body,.event-form-dialog header,.event-form-content{padding:14px}.event-manager-fields{grid-template-columns:1fr}.event-manager-section article{align-items:flex-start;flex-direction:column}.event-manager-item{max-width:100%;width:100%}.event-manager-actions{align-self:flex-end}}
</style>
