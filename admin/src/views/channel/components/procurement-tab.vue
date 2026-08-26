<script setup lang="ts">
// 采购单管理（procurement:read / procurement:write）：上游拿货单（客户购买 →
// 上游采购 → 卡密回填链路的运行轨迹）。状态筛选 + 手动重试 / 转人工。
import { h, computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { NButton, NDataTable, NTag, NPopconfirm } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchProcurements, retryProcurement, markProcurementManual } from "@/service/api";
import { checkAuth } from "@/directives";
import FilterTabs from "@/components/common/filter-tabs.vue";
import TablePager from "@/components/common/table-pager.vue";
import { useResponsiveTier, type TableTier } from "./use-responsive-tier";

defineOptions({ name: "ProcurementTab" });

const { te, t } = useI18n();

/** 状态枚举 → 按当前语言渲染业务名称，未收录的状态回显原值 */
function statusText(s?: string) {
  if (!s) return "-";
  const key = `procurement.status.${s}`;
  return te(key) ? t(key) : s;
}

const loading = ref(false);
const rows = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
const statusFilter = ref("");

const statusTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: statusText("pending"), value: "pending", type: "warning" as const },
  { label: statusText("submitted"), value: "submitted", type: "info" as const },
  { label: statusText("polling"), value: "polling", type: "info" as const },
  { label: statusText("fulfilled"), value: "fulfilled", type: "success" as const },
  { label: statusText("rejected"), value: "rejected", type: "error" as const },
  { label: statusText("refunded"), value: "refunded", type: "default" as const },
  { label: statusText("manual"), value: "manual", type: "warning" as const },
];

const statusTag: Record<string, "success" | "error" | "warning" | "info" | "default"> = {
  fulfilled: "success", rejected: "error", manual: "warning",
  submitted: "info", polling: "info", refunding: "warning", refunded: "default", pending: "warning",
};

const canRetry = () => checkAuth("procurement:write");

// ── 容器宽分档（full ≥1080 / mid ≥720 / compact）：任意屏宽下操作列完整可见，不依赖横向滚动 ──
const wrapRef = ref<HTMLElement | null>(null);
const { tier } = useResponsiveTier(wrapRef);

function fmtTime(ts?: number) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchProcurements({
      page: page.value,
      page_size: pageSize.value,
      status: statusFilter.value || undefined,
    });
    if (!error && data) {
      rows.value = (data as any).orders || (data as any).procurements || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

function onSearch() {
  page.value = 1;
  load();
}

async function handleRetry(row: any) {
  const { error } = await retryProcurement(row.id);
  if (!error) {
    window.$message?.success("已重新提交");
    load();
  }
}

async function handleManual(row: any) {
  const { error } = await markProcurementManual(row.id, "管理员手动标记完成");
  if (!error) {
    window.$message?.success("已转人工终态");
    load();
  }
}

// ── 响应式列集：compact 只留 状态/上游单号/操作；mid 去掉订单项/渠道/卡密/重试；full 全列 ──
const orderNoCol = (tr: TableTier) => ({
  title: "上游单号",
  key: "upstream_order_id",
  minWidth: tr === "compact" ? 96 : 110,
  maxWidth: tr === "compact" ? 180 : 220,
  ellipsis: { tooltip: true },
  render: (row: any) => row.upstream_order_id || "-",
});

const errorCol = () => ({
  title: "失败原因",
  key: "last_error",
  minWidth: 140,
  maxWidth: 420,
  ellipsis: { tooltip: true },
  render: (row: any) => row.last_error || "-",
});

const actionsCol = () => ({
  title: "操作",
  key: "actions",
  width: 104,
  render: (row: any) =>
    h("div", { class: "flex flex-wrap items-center gap-4px" }, [
      ["pending", "submitted", "polling", "failed"].includes(row.status) && canRetry()
        ? h(NButton, { size: "tiny", type: "primary", quaternary: true, onClick: () => handleRetry(row) }, { default: () => "重试" })
        : null,
      ["pending", "submitted", "polling"].includes(row.status) && canRetry()
        ? h(NPopconfirm, { onPositiveClick: () => handleManual(row) }, { trigger: () => h(NButton, { size: "tiny", quaternary: true }, { default: () => "转人工" }), default: () => "标记人工处理（不再自动重试）？" })
        : null,
    ]),
});

const columns = computed<DataTableColumns<any>>(() => {
  const tr = tier.value;
  const cols: DataTableColumns<any> = [];
  if (tr !== "compact") cols.push({ title: "ID", key: "id", width: 48 });
  if (tr === "full") {
    cols.push({ title: "订单项", key: "order_item_id", width: 64 });
    cols.push({ title: "渠道", key: "connection_id", width: 56, render: (row: any) => `#${row.connection_id}` });
  }
  cols.push(orderNoCol(tr));
  cols.push({
    title: "状态",
    key: "status",
    width: 84,
    render: (row: any) =>
      h(NTag, { size: "small", type: statusTag[row.status] || "default", bordered: false }, { default: () => statusText(row.status) }),
  });
  if (tr === "full") {
    cols.push({ title: "卡密行数", key: "received_cards", width: 64, render: (row: any) => String(row.received_cards ?? row.received_count ?? "-") });
    cols.push({ title: "重试", key: "retry_count", width: 52 });
  }
  if (tr !== "compact") cols.push(errorCol());
  if (tr !== "compact") cols.push({ title: "更新时间", key: "updated_at", width: 142, render: (row: any) => fmtTime(row.updated_at || row.created_at) });
  cols.push(actionsCol());
  return cols;
});

onMounted(load);
</script>

<template>
  <div ref="wrapRef">
    <FilterTabs v-model:value="statusFilter" :options="statusTabs" class="mb-12px" @change="onSearch" />
    <NDataTable :columns="columns" :data="rows" :loading="loading" size="small" :row-key="(r: any) => r.id" :max-height="540" :scroll-x="300" />
    <div class="mt-12px flex justify-end">
      <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
    </div>
  </div>
</template>
