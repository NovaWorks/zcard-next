<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { NAlert, NButton, NDataTable, NModal, NSelect, NSpace } from "naive-ui";
import { manageProductListing } from "@/service/api";
const props = defineProps<{
  show: boolean;
  ids: number[];
  filter: Record<string, unknown>;
  initialAction: string;
  scopeLabel: string;
}>();
const emit = defineEmits<{
  (e: "update:show", v: boolean): void;
  (e: "saved"): void;
}>();
const action = ref("enable");
const busy = ref(false);
const error = ref("");
const result = ref<any>(null);
const applied = ref(false);
const processed = ref(0);
const actions = [
  { label: "开启自动上下架", value: "enable" },
  { label: "暂停自动上下架", value: "disable" },
  { label: "人工上架", value: "on" },
  { label: "人工下架", value: "off" },
  { label: "人工隐藏", value: "hide" },
];
const ready = computed(
  () => result.value?.items?.filter((p: any) => p.result === "ready") || [],
);
const summary = ref("");
const labels: Record<string, string> = {
  ready: "可处理",
  locked: "已锁定 · 跳过",
  unsupported: "不支持 · 跳过",
  conflict: "已变化 · 请重新预览",
  updated: "已完成",
  failed: "失败 · 可重新预览",
};
const columns = [
  { title: "商品", key: "name", minWidth: 160 },
  {
    title: "当前状态",
    key: "status",
    width: 90,
    render: (p: any) =>
      p.status === 1 ? "已上架" : p.status === 2 ? "已隐藏" : "已下架",
  },
  {
    title: "处理结果",
    key: "result",
    width: 160,
    render: (p: any) => labels[p.result] || p.result,
  },
  { title: "说明", key: "message", minWidth: 230 },
];
async function preview() {
  busy.value = true;
  error.value = "";
  result.value = null;
  applied.value = false;
  summary.value = "";
  processed.value = 0;
  try {
    const r = await manageProductListing({
      action: action.value,
      ids: props.ids.length ? props.ids : undefined,
      filter: props.ids.length ? undefined : props.filter,
    });
    if (r.error) {
      error.value =
        (r.error as any).response?.data?.message || "预览失败，请重试";
      return;
    }
    result.value = r.data;
  } finally {
    busy.value = false;
  }
}
watch(
  () => props.show,
  (show) => {
    if (show) {
      action.value = props.initialAction;
      void preview();
    }
  },
);
async function apply() {
  const targets = [...ready.value];
  if (!targets.length || result.value?.truncated) return;
  busy.value = true;
  error.value = "";
  let changed = 0,
    locked = Number(result.value.skipped_locked || 0),
    conflicts = 0,
    unsupported = Number(result.value.unsupported || 0),
    failed = 0;
  try {
    for (let start = 0; start < targets.length; start += 100) {
      const chunk = targets.slice(start, start + 100);
      const r = await manageProductListing({
        action: action.value,
        apply: true,
        revisions: Object.fromEntries(
          chunk.map((p: any) => [p.id, p.revision]),
        ),
      });
      if (r.error) {
        error.value =
          "本批结果未能确认，已完成部分会保留。重新预览后可继续，系统会检查商品是否发生变化。";
        break;
      }
      const d = r.data;
      changed += Number(d.changed || 0);
      locked += Number(d.skipped_locked || 0);
      conflicts += Number(d.conflicts || 0);
      unsupported += Number(d.unsupported || 0);
      failed += Number(d.failed || 0);
      const byId = new Map(d.items.map((p: any) => [String(p.id), p]));
      result.value.items = result.value.items.map(
        (p: any) => byId.get(String(p.id)) || p,
      );
      processed.value += chunk.length;
    }
    applied.value = true;
    summary.value = `已完成 ${changed} 件，锁定跳过 ${locked} 件，状态变化 ${conflicts} 件，不支持 ${unsupported} 件，失败 ${failed} 件。`;
    emit("saved");
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <NModal
    :show="show"
    preset="card"
    title="管理商品上下架"
    style="width: 960px; max-width: 96vw"
    :closable="!busy"
    :mask-closable="false"
    :close-on-esc="!busy"
    @update:show="emit('update:show', $event)"
  >
    <NSpace vertical :size="12">
      <div>范围：{{ scopeLabel }}。先预览，再应用；锁定商品自动跳过。</div>
      <NSelect
        v-model:value="action"
        :options="actions"
        :disabled="busy"
        aria-label="上下架操作"
        @update:value="preview"
      />
      <NAlert v-if="action === 'enable'" type="info" :bordered="false"
        >仅支持全部规格由上游发货的商品。开启后后台约每 5
        分钟调度一次完整检查，大目录需排队；两次独立确认缺货才下架。当前已下架商品也会在确认有货后上架，原本因缺货下架的隐藏商品恢复隐藏。人工上下架会暂停自动管理。</NAlert
      >
      <NAlert v-else type="info" :bordered="false">{{
        action === "disable"
          ? "暂停自动上下架，保留当前销售状态。"
          : "本次人工设置会暂停所选商品的自动上下架。"
      }}</NAlert>
      <NAlert v-if="error" type="error" role="alert"
        >{{ error }}
        <NButton :disabled="busy" size="small" @click="preview"
          >重新预览</NButton
        ></NAlert
      >
      <NAlert v-if="result?.truncated" type="warning"
        >匹配超过 5000 件，请按货源、分类等缩小范围后再操作。</NAlert
      >
      <div v-if="result">
        匹配 {{ result.matched }} 件 · 锁定跳过
        {{ result.skipped_locked || 0 }} 件 · 不支持
        {{ result.unsupported || 0 }} 件 ·
        {{ applied ? "结果见下表" : `可处理 ${ready.length} 件` }}
      </div>
      <div v-if="busy" role="status" aria-live="polite">
        {{ result ? `正在处理，已检查 ${processed} 件…` : "正在读取预览…" }}
      </div>
      <NAlert v-if="summary" type="info" role="status"
        >{{ summary
        }}<span v-if="action === 'enable'"
          >自动管理设置生效后，实际上下架等待后台检查。</span
        ></NAlert
      >
      <NDataTable
        :columns="columns"
        :data="result?.items || []"
        :row-key="(p: any) => p.id"
        :pagination="{ pageSize: 50 }"
        :max-height="420"
        :scroll-x="660"
      />
    </NSpace>
    <template #footer
      ><NSpace justify="end"
        ><NButton :disabled="busy" @click="emit('update:show', false)"
          >关闭</NButton
        ><NButton v-if="applied" :disabled="busy" @click="preview"
          >重新预览</NButton
        ><NButton
          v-else
          type="primary"
          :loading="busy"
          :disabled="busy || !ready.length || result?.truncated"
          @click="apply"
          >应用到 {{ ready.length }} 件商品</NButton
        ></NSpace
      ></template
    >
  </NModal>
</template>
