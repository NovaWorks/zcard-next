<script setup lang="ts">
import { ref, onMounted, h } from "vue";
import {
  NAlert,
  NButton,
  NDataTable,
  NInput,
  NSelect,
  NPagination,
  type DataTableColumns,
} from "naive-ui";
import { request } from "@/service/request";
import { checkAuth } from "@/directives";
const rows = ref<any[]>([]),
  loading = ref(false),
  error = ref(""),
  page = ref(1),
  total = ref(0),
  sales = ref(false),
  reason = ref(""),
  status = ref(""),
  orderNo = ref("");
const phases: Record<string, string> = {
  purchase: "采购确认中",
  query: "同步会话",
  refund: "零售退款中",
  review: "待人工核对",
  done: "已结束",
};
const options = Object.entries(phases).map(([value, label]) => ({
  value,
  label,
}));
const columns: DataTableColumns<any> = [
  { title: "订单号", key: "order_no" },
  { title: "货源", key: "connection_id" },
  {
    title: "任务状态",
    key: "phase",
    render: (r) => phases[r.phase] || r.phase,
  },
  { title: "会话状态", key: "state" },
  { title: "买家退款", key: "refund_status" },
  { title: "诊断", key: "last_error" },
  { title: "尝试次数", key: "attempts" },
  {
    title: "操作",
    key: "actions",
    render: (r) =>
      r.phase !== "done" && checkAuth("procurement:write")
        ? h(
            NButton,
            {
              size: "small",
              disabled: loading.value,
              onClick: () => retry(r.id),
            },
            () => "恢复原任务",
          )
        : null,
  },
];
async function load() {
  loading.value = true;
  const r = await request<any>({
    url: "/api/v1/admin/sms-intents",
    params: {
      page: page.value,
      page_size: 20,
      status: status.value,
      order_no: orderNo.value,
    },
  });
  loading.value = false;
  error.value = r.error ? "接码诊断加载失败，请重试" : "";
  if (r.data) {
    rows.value = r.data.items || [];
    total.value = Number(r.data.total || 0);
    sales.value = !!r.data.sales_enabled;
  }
}
async function retry(id: string) {
  if (reason.value.trim().length < 3) {
    error.value = "请先填写核对和重试原因（至少 3 字）";
    return;
  }
  const r = await request({
    url: `/api/v1/admin/sms-intents/${id}/retry`,
    method: "post",
    data: { reason: reason.value },
  });
  if (!r.error) await load();
}
onMounted(load);
</script>
<template>
  <div class="flex flex-col gap-4">
    <NAlert :type="sales ? 'info' : 'warning'"
      >接码新购{{
        sales ? "已启用" : "已关闭"
      }}。关闭新购不影响已有任务恢复。恢复原任务只重放已保存的购买或操作意图。</NAlert
    ><NAlert v-if="error" type="error">{{ error }}</NAlert>
    <div class="flex flex-wrap gap-2">
      <NInput
        v-model:value="orderNo"
        placeholder="零售订单号"
        class="max-w-64"
      /><NSelect
        v-model:value="status"
        :options="[{ label: '全部', value: '' }, ...options]"
        class="max-w-48"
      /><NButton
        :loading="loading"
        @click="
          page = 1;
          load();
        "
        >查询</NButton
      >
    </div>
    <NInput
      v-if="checkAuth('procurement:write')"
      v-model:value="reason"
      placeholder="恢复任务前的核对说明（3–180 字）"
      :maxlength="180"
    /><NDataTable
      :columns="columns"
      :data="rows"
      :loading="loading"
      :scroll-x="950"
      :row-key="(r) => r.id"
    /><NPagination
      v-model:page="page"
      :page-size="20"
      :item-count="total"
      @update:page="load"
    />
  </div>
</template>
