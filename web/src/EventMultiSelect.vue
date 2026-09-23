<script setup lang="ts">
import { computed, ref } from "vue";
import type { EventDefinition } from "./project.ts";

const props = defineProps<{ events: EventDefinition[]; selected: string[]; disabled?: boolean }>();
const emit = defineEmits<{ change: [ids: string[]]; manage: [] }>();
const query = ref("");
const options = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase();
  return props.events.filter(event => !needle || `${event.name}\n${event.codeName}\n${event.id}`.toLocaleLowerCase().includes(needle));
});
const byID = computed(() => new Map(props.events.map(event => [event.id, event])));
// 只能切换当前注册表内的 ID；旧草稿所含的已删成员不会再提交。
function toggle(id: string): void {
  if (!byID.value.has(id) || props.disabled) return;
  emit("change", props.selected.includes(id) ? props.selected.filter(value => value !== id) : [...props.selected, id]);
}
</script>

<template>
  <section class="event-multiselect" aria-label="事件依赖"><strong>事件依赖</strong><p>从工程统一定义的事件中选择。宿主更新业务状态后使用对应枚举通知；所有绑定此业务定义的节点共用这些依赖。</p>
    <div v-if="!events.length">尚未定义事件，请先进入事件枚举管理。<button type="button" @click="emit('manage')">管理事件枚举</button></div>
    <template v-else><input v-model="query" aria-label="搜索可选事件" placeholder="搜索显示名称、代码名或 ID" :disabled="disabled" /><div class="event-multiselect-options"><label v-for="event in options" :key="event.id"><input type="checkbox" :checked="selected.includes(event.id)" :disabled="disabled" @change="toggle(event.id)" /><span>{{ event.name }} <small>Event{{ event.codeName }} · ID {{ event.id }}</small></span></label><p v-if="!options.length">没有匹配的事件</p></div><div v-if="selected.length" class="event-multiselect-selected"><span v-for="id in selected" :key="id">{{ byID.get(id)?.name ?? '已删除事件' }} · Event{{ byID.get(id)?.codeName ?? '?' }} · ID {{ id }} <button type="button" :disabled="disabled" :aria-label="`移除${byID.get(id)?.name ?? id}依赖`" @click="toggle(id)">×</button></span></div></template>
  </section>
</template>

<style scoped>
.event-multiselect{display:grid;gap:8px;margin-top:20px}.event-multiselect p{margin:0;color:#94aebb;font-size:12px}.event-multiselect input[type=text],.event-multiselect>input{box-sizing:border-box;width:100%;padding:8px}.event-multiselect-options{max-height:170px;overflow:auto;border:1px solid #385361;border-radius:6px;padding:6px}.event-multiselect-options label{display:flex;flex-direction:row;align-items:center;gap:7px;padding:5px}.event-multiselect-options input{width:auto}.event-multiselect-options small{color:#9fb9c3}.event-multiselect-selected{display:flex;flex-wrap:wrap;gap:5px}.event-multiselect-selected span{border:1px solid #54717a;border-radius:5px;padding:3px 6px}.event-multiselect-selected button{margin-left:4px;padding:0 4px}
</style>
