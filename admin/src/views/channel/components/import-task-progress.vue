<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { NAlert, NButton, NDataTable, NPagination, NProgress, NSpace, NSwitch } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchSupplySyncTask, fetchSupplyImportItems, retrySupplyImportTask, cancelSupplySyncTask } from "@/service/api";
import { checkAuth } from "@/directives";

const props = defineProps<{ taskId: number }>();
const emit = defineEmits<{ (e: "changed"): void; (e: "configure"): void }>();
const task = ref<any>(null);
const items = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const problemsOnly = ref(false);
const busy = ref(false);
const connectionLost = ref(false);
let generation = 0;
let disposed = false;
let timer: ReturnType<typeof setTimeout> | undefined;
const running = computed(() => ["pending", "processing"].includes(task.value?.status));
const canWrite = computed(() => checkAuth("supply:write"));
const paused = computed(() => ["AUTH_FAILED", "ACCESS_DENIED", "CONNECTION_INVALID", "CONNECTION_DISABLED", "CONFIG_CHANGED"].includes(task.value?.error_code));
const title = computed(() => {
  const t = task.value;
  if (!t) return "正在读取导入进度";
  if (t.cancel_requested_at && running.value) return "正在停止，已导入商品会保留";
  if (paused.value) return "导入已暂停，需要处理货源配置";
  if (t.status === "canceled") return "导入已停止，已导入商品保留";
  if (t.status === "done") return t.error_code ? "处理完成，部分商品需要处理" : "导入完成";
  return t.status === "pending" ? "导入已排队，后台将自动处理" : "正在导入商品";
});
const progress = computed(() => task.value?.total ? Math.min(100, Math.floor(Number(task.value.processed) / Number(task.value.total) * 100)) : 0);
const stateText: Record<string, string> = { pending: "待处理", retry: "等待重试", done: "完成", failed: "需处理", skipped: "已跳过" };
const columns: DataTableColumns<any> = [
  { title: "商品", key: "name", minWidth: 180, ellipsis: { tooltip: true } },
  { title: "商品编号", key: "code", width: 155, ellipsis: { tooltip: true } },
  { title: "进度", key: "state", width: 145, render: r => r.saved && r.stage === "stock" && r.state !== "done" ? `已保存 · 库存${stateText[r.state] || r.state}` : stateText[r.state] || r.state },
  { title: "说明", key: "error_summary", minWidth: 230, render: r => r.error_summary || (r.saved ? "商品已保存" : "等待后台处理") },
];
async function refresh() {
  const version = generation;
  const id = props.taskId;
  const [t, detail] = await Promise.all([
    fetchSupplySyncTask(id),
    fetchSupplyImportItems(id, { page: page.value, page_size: 20, problems_only: problemsOnly.value }),
  ]);
  if (version !== generation) return;
  connectionLost.value = Boolean(t.error || detail.error);
  if (!t.error && t.data) task.value = t.data;
  if (!detail.error && detail.data) { items.value = (detail.data as any).items || []; total.value = Number((detail.data as any).total || 0); }
}
function schedule() {
  if (disposed) return;
  clearTimeout(timer);
  timer = setTimeout(async () => {
    if (!document.hidden) await refresh();
    if (running.value || connectionLost.value || !task.value) schedule();
  }, 3000);
}
watch(() => props.taskId, async () => {
  generation++;
  clearTimeout(timer);
  task.value = null; page.value = 1; problemsOnly.value = false;
  await refresh(); schedule();
}, { immediate: true });
watch([page, problemsOnly], () => { generation++; refresh(); });
onBeforeUnmount(() => { disposed = true; generation++; clearTimeout(timer); });
async function retry(scope: "failed" | "stock" | "remaining", confirmed = false) {
  if (paused.value && !confirmed) {
    window.$dialog?.warning({ title: "确认当前货源配置后继续", content: "未导入商品将使用当前货源配置及原导入定价策略；已保存商品仅补查库存，不重新改价。请先检查账号、汇率及分类映射。", positiveText: "确认并继续", negativeText: "返回检查", onPositiveClick: () => retry(scope, true) });
    return;
  }
  busy.value = true;
  try {
    const { error } = await retrySupplyImportTask(props.taskId, scope, confirmed);
    if (!error) { await refresh(); schedule(); emit("changed"); }
  } finally { busy.value = false; }
}
function stop() {
  window.$dialog?.warning({
    title: "停止后续导入？", content: "已导入商品会保留。当前处理步骤结束后停止，可稍后继续未完成商品。",
    positiveText: "停止后续处理", negativeText: "继续导入",
    onPositiveClick: async () => { await cancelSupplySyncTask(props.taskId); await refresh(); emit("changed"); },
  });
}

</script>

<template>
  <section class="flex flex-col gap-16px" aria-label="商品导入进度">
    <div role="status" aria-live="polite">
      <div class="mb-8px text-16px font-medium">{{ title }} · #{{ taskId }}</div>
      <NProgress type="line" :percentage="progress" :status="task?.error_code ? 'warning' : task?.status === 'done' ? 'success' : 'default'" />
      <div v-if="task" class="mt-10px flex flex-wrap gap-x-16px gap-y-6px text-14px">
        <span>共 {{ task.total || 0 }} 件</span>
        <span>已保存 {{ Number(task.created || 0) + Number(task.updated || 0) }}</span>
        <span>已跳过 {{ task.manual_skipped || 0 }}</span>
        <span>等待重试 {{ task.retrying_count || 0 }}</span>
        <span>待处理 {{ task.pending_count || 0 }}</span>
        <span>导入失败 {{ task.failed_count || 0 }}</span>
      </div>
      <p v-if="Number(task?.stock_pending_count)" class="mt-8px text-13px">
        已保存商品中，{{ task.stock_pending_count }} 件库存待确认<span v-if="Number(task.stock_failed_count)">，其中 {{ task.stock_failed_count }} 件需处理</span>。
        新商品在库存确认前暂不上架。
      </p>
    </div>
    <NAlert v-if="connectionLost" type="warning" :show-icon="false">暂时无法读取进度，正在重新连接。已提交的任务会在后台继续，请勿重复导入。</NAlert>
    <NAlert v-if="task?.error_context" :type="task.error_code ? 'warning' : 'info'">{{ task.error_context }}</NAlert>
    <p v-if="running" class="text-13px text-gray-500">可以关闭页面，后台会继续处理。临时失败会自动重试，稍后可在货源「任务」中查看结果。</p>
    <NSpace v-if="canWrite">
      <NButton v-if="running" :disabled="!!task.cancel_requested_at" @click="stop">停止任务</NButton>
      <template v-else-if="task">
        <NButton v-if="paused" @click="emit('configure')">检查货源配置</NButton>
        <NButton v-if="task.error_code === 'CONFIG_CHANGED'" type="primary" :loading="busy" @click="retry('remaining')">确认配置后继续</NButton>
        <NButton v-else-if="['canceled', 'failed'].includes(task.status)" type="primary" :loading="busy" @click="retry('remaining')">继续未完成商品</NButton>
        <NButton v-else-if="Number(task.failed_count)" type="primary" :loading="busy" @click="retry('failed')">重试失败商品</NButton>
        <NButton v-if="Number(task.stock_failed_count) && task.error_code !== 'CONFIG_CHANGED'" :loading="busy" @click="retry('stock')">仅重试库存</NButton>
      </template>
    </NSpace>
    <div class="flex items-center gap-8px"><NSwitch aria-label="仅看需处理和已跳过商品" v-model:value="problemsOnly" size="small" @update:value="page = 1" /><span>仅看需处理和已跳过商品</span></div>
    <div class="import-item-cards">
      <article v-for="item in items" :key="item.code" class="import-item-card">
        <strong>{{ item.name }}</strong>
        <span class="text-12px text-gray-500">{{ item.code }}</span>
        <span>{{ item.saved && item.stage === 'stock' && item.state !== 'done' ? `已保存 · 库存${stateText[item.state] || item.state}` : stateText[item.state] }}</span>
        <p>{{ item.error_summary || (item.saved ? '商品已保存' : '等待后台处理') }}</p>
      </article>
      <p v-if="!items.length" class="text-13px text-gray-500">{{ problemsOnly ? '没有需要处理的商品' : '暂无商品明细' }}</p>
    </div>
    <NDataTable class="import-item-table" :columns="columns" :data="items" :scroll-x="710" :max-height="380" size="small" />
    <NPagination v-model:page="page" :page-size="20" :item-count="total" :simple="true" />
  </section>
</template>

<style scoped>
.import-item-cards { display: none; }
@media (max-width: 600px) {
  .import-item-table { display: none; }
  .import-item-cards { display: grid; gap: 12px; }
  .import-item-card { display: grid; gap: 6px; padding: 12px; border: 1px solid var(--n-border-color, #d9dce3); border-radius: 8px; overflow-wrap: anywhere; }
}
</style>
