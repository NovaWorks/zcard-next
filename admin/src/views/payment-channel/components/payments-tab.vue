<script setup lang="ts">
// 支付单流水（payment:read_detail）+ 补单（payment:capture 超管专属）+ 退款单列表。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NPopconfirm, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchPayments, capturePayment, fetchRefunds } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "PaymentsTab" });

const loading = ref(false);
const payments = ref<any[]>([]);
const nextCursor = ref(0);
const statusFilter = ref<string>("");
const orderNo = ref("");

// 快捷筛选卡片（与 statusTag 同色系）
const statusTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "待支付", value: "pending", type: "warning" as const },
  { label: "成功", value: "success", type: "success" as const },
  { label: "失败", value: "failed", type: "error" as const },
  { label: "已过期", value: "expired", type: "default" as const },
];

function statusTag(s: string) {
  const type = s === "success" ? "success" : s === "pending" ? "warning" : s === "failed" ? "error" : "default";
  const text = s === "success" ? "成功" : s === "pending" ? "待支付" : s === "failed" ? "失败" : s === "expired" ? "已过期" : s;
  return h(NTag, { type, size: "small" }, { default: () => text });
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 70 },
  { title: "订单号", key: "order_no", width: 200 },
  { title: "渠道", key: "channel", width: 90 },
  { title: "渠道单号", key: "channel_order_no", width: 180, ellipsis: true },
  {
    title: "金额",
    key: "amount_cents",
    width: 100,
    render: (row) => formatMoney(row.amount_cents),
  },
  {
    title: "状态",
    key: "status",
    width: 86,
    render: (row) => statusTag(row.status),
  },
  {
    title: "支付时间",
    key: "paid_at",
    width: 160,
    render: (row) => (row.paid_at ? new Date(row.paid_at * 1000).toLocaleString() : "-"),
  },
  {
    title: "创建时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
  {
    title: "操作",
    key: "actions",
    width: 90,
    render: (row) =>
      row.status === "pending" && checkAuth("payment:capture")
        ? h(
            NPopconfirm,
            { onPositiveClick: () => handleCapture(row) },
            {
              trigger: () =>
                h(NButton, { size: "small", type: "primary", secondary: true }, { default: () => "补单" }),
              default: () => "主动向渠道查单并复走回调管线，确定？",
            },
          )
        : null,
  },
];

async function load(reset = true) {
  loading.value = true;
  try {
    const { data, error } = await fetchPayments({
      status: statusFilter.value || undefined,
      order_no: orderNo.value.trim() || undefined,
      cursor: reset ? undefined : nextCursor.value || undefined,
      limit: 20,
    });
    if (!error && data) {
      const list = (data as any).payments || [];
      payments.value = reset ? list : [...payments.value, ...list];
      nextCursor.value = (data as any).next_cursor || 0;
    }
  } finally {
    loading.value = false;
  }
}

async function handleCapture(row: any) {
  const { error } = await capturePayment(row.id);
  if (!error) {
    window.$message?.success("补单已执行（结果以支付单状态为准）");
    load();
  }
}

// ── 退款单列表 ──
const refunds = ref<any[]>([]);
const refundLoading = ref(false);
const refundColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 70 },
  { title: "订单号", key: "order_no", width: 200 },
  { title: "金额", key: "amount_cents", width: 100, render: (row) => formatMoney(row.amount_cents) },
  {
    title: "渠道",
    key: "channel",
    width: 100,
    render: (row) =>
      h(NTag, { size: "small" }, { default: () => ({ wallet: "钱包", gateway: "网关", upstream: "上游" } as any)[row.channel] || row.channel }),
  },
  { title: "状态", key: "status", width: 90 },
  { title: "原因", key: "reason", minWidth: 140, ellipsis: true },
  {
    title: "创建时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
];

async function loadRefunds() {
  refundLoading.value = true;
  try {
    const { data, error } = await fetchRefunds();
    if (!error && data) refunds.value = (data as any).refunds || [];
  } finally {
    refundLoading.value = false;
  }
}

onMounted(() => {
  load();
  loadRefunds();
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <!-- 支付单 -->
    <div>
      <div class="mb-8px text-13px font-500">支付单流水</div>
      <FilterTabs v-model:value="statusFilter" :options="statusTabs" size="small" class="mb-8px" @change="load()" />
      <div class="mb-8px flex items-center gap-8px">
        <NInput
          v-model:value="orderNo"
          size="small"
          placeholder="按订单号搜索"
          clearable
          class="w-220px"
          @keyup.enter="load()"
        />
        <NButton size="small" @click="load()">查询</NButton>
      </div>
      <NDataTable :columns="columns" :data="payments" :loading="loading" size="small" />
      <div v-if="nextCursor" class="mt-8px text-center">
        <NButton size="small" quaternary @click="load(false)">加载更多</NButton>
      </div>
    </div>

    <!-- 退款单 -->
    <div>
      <div class="mb-8px text-13px font-500">退款单</div>
      <NDataTable :columns="refundColumns" :data="refunds" :loading="refundLoading" size="small" />
    </div>
  </div>
</template>
