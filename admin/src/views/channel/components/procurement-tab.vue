<script setup lang="ts">
// 采购单管理（procurement:read / procurement:write）：上游拿货单（客户购买 →
// 上游采购 → 卡密回填链路的运行轨迹）。状态筛选 + 手动重试 / 转人工。
import { h, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { NButton, NDataTable, NTag, NPopconfirm } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchProcurements, retryProcurement, markProcurementManual } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";
import FilterTabs from "@/components/common/filter-tabs.vue";
import TablePager from "@/components/common/table-pager.vue";

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

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 70 },
  { title: "订单项", key: "order_item_id", width: 80 },
  { title: "渠道", key: "connection_id", width: 70, render: (row) => `#${row.connection_id}` },
  { title: "上游单号", key: "upstream_order_id", width: 130, ellipsis: true, render: (row) => row.upstream_order_id || "-" },
  {
    title: "状态",
    key: "status",
    width: 90,
    render: (row) =>
      h(NTag, { size: "small", type: statusTag[row.status] || "default", bordered: false }, { default: () => statusText(row.status) }),
  },
  { title: "卡密行数", key: "received_cards", width: 90, render: (row) => String(row.received_cards ?? row.received_count ?? "-") },
  { title: "重试", key: "retry_count", width: 60 },
  { title: "失败原因", key: "last_error", minWidth: 180, ellipsis: true, render: (row) => row.last_error || "-" },
  { title: "更新时间", key: "updated_at", width: 150, render: (row) => fmtTime(row.updated_at || row.created_at) },
  {
    title: "操作",
    key: "actions",
    width: 140,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        ["pending", "submitted", "polling", "failed"].includes(row.status) && canRetry()
          ? h(NButton, { size: "tiny", type: "primary", quaternary: true, onClick: () => handleRetry(row) }, { default: () => "重试" })
          : null,
        ["pending", "submitted", "polling"].includes(row.status) && canRetry()
          ? h(NPopconfirm, { onPositiveClick: () => handleManual(row) }, { trigger: () => h(NButton, { size: "tiny", quaternary: true }, { default: () => "转人工" }), default: () => "标记人工处理（不再自动重试）？" })
          : null,
      ]),
  },
];

onMounted(load);
</script>

<template>
  <div>
    <FilterTabs v-model:value="statusFilter" :options="statusTabs" class="mb-12px" @change="onSearch" />
    <NDataTable :columns="columns" :data="rows" :loading="loading" size="small" :row-key="(r: any) => r.id" :max-height="540" :scroll-x="1200" />
    <div class="mt-12px flex justify-end">
      <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
    </div>
  </div>
</template>
