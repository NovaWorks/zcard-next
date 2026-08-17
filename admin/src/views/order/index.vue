<script setup lang="ts">
import TablePager from "@/components/common/table-pager.vue";
import { ref, onMounted, h } from "vue";
import { NButton, NTag, NSpace, NPopconfirm } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchOrders, fetchOrder, cancelOrder } from "@/service/api";
import { formatMoney, formatSignedMoney } from "@/utils/money";

defineOptions({ name: "OrderManagement" });

const loading = ref(false);
const orders = ref<any[]>([]);
const page = ref(1);
const pageSize = ref(20);
// 游标链：cursors[p-1] = 第 p 页起始游标（next_cursor = 满页时末行 ID；0 = 无更多）
const cursors = ref<number[]>([0]);
const hasMore = ref(false);
const statusFilter = ref<string | null>(null);
const showDetail = ref(false);
const detail = ref<any>(null);

const statusOptions = [
  { label: "待支付", value: "pending_payment" },
  { label: "已支付", value: "paid" },
  { label: "已发货", value: "delivered" },
  { label: "已完成", value: "completed" },
  { label: "已取消", value: "canceled" },
  { label: "已退款", value: "refunded" },
];

function statusText(s: string) {
  const map: Record<string, string> = {
    pending_payment: "待支付",
    paid: "已支付",
    fulfilling: "履约中",
    delivered: "已发货",
    completed: "已完成",
    canceled: "已取消",
    expired: "已过期",
    refunded: "已退款",
    refund_pending: "退款中",
  };
  return map[s] || s;
}

function statusType(s: string): "success" | "error" | "warning" | "info" | "default" {
  if (["paid", "delivered", "completed"].includes(s)) return "success";
  if (["canceled", "expired", "refunded"].includes(s)) return "error";
  if (s === "pending_payment") return "warning";
  return "info";
}

function formatTime(ts?: number) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

const columns: DataTableColumns<any> = [
  { title: "订单号", key: "order_no", width: 200 },
  {
    title: "状态",
    key: "status",
    width: 100,
    render: (row) =>
      h(
        NTag,
        { type: statusType(row.status), size: "small" },
        { default: () => statusText(row.status) },
      ),
  },
  {
    title: "总额",
    key: "total_cents",
    width: 100,
    render: (row) => formatMoney(row.total_cents),
  },
  { title: "联系方式", key: "contact", width: 140, ellipsis: { tooltip: true } },
  {
    title: "创建时间",
    key: "created_at",
    width: 160,
    render: (row) => formatTime(row.created_at),
  },
  {
    title: "操作",
    key: "actions",
    width: 140,
    render: (row) =>
      h(
        NSpace,
        { size: "small" },
        {
          default: () => [
            h(
              NButton,
              { size: "small", onClick: () => handleDetail(row.order_no) },
              { default: () => "详情" },
            ),
            row.status === "pending_payment"
              ? h(
                  NPopconfirm,
                  { onPositiveClick: () => handleCancel(row.order_no) },
                  {
                    trigger: () =>
                      h(NButton, { size: "small", type: "warning" }, { default: () => "取消" }),
                    default: () => "确定取消该订单？",
                  },
                )
              : null,
          ],
        },
      ),
  },
];

const itemColumns: DataTableColumns<any> = [
  { title: "商品ID", key: "product_id", width: 80 },
  { title: "数量", key: "quantity", width: 60 },
  {
    title: "单价",
    key: "unit_price_cents",
    width: 80,
    render: (row) => formatMoney(row.unit_price_cents),
  },
  { title: "小计", key: "amount_cents", width: 80, render: (row) => formatMoney(row.amount_cents) },
  { title: "履约类型", key: "fulfillment_type", width: 80 },
  { title: "履约状态", key: "fulfillment_status", width: 80 },
];

const amountColumns: DataTableColumns<any> = [
  { title: "#", key: "seq", width: 40 },
  { title: "类型", key: "type", width: 140 },
  {
    title: "金额",
    key: "amount_cents",
    width: 80,
    render: (row) => formatSignedMoney(row.amount_cents),
  },
  { title: "来源", key: "source_type", width: 100 },
];

async function loadOrders() {
  loading.value = true;
  try {
    const cur = cursors.value[page.value - 1] || 0;
    const { data, error } = await fetchOrders({
      status: statusFilter.value || undefined,
      cursor: cur || undefined,
      limit: pageSize.value,
    });
    if (!error && data) {
      orders.value = (data as any).orders || [];
      const next = Number((data as any).next_cursor || 0);
      hasMore.value = next > 0;
      if (next > 0) cursors.value[page.value] = next;
    }
  } finally {
    loading.value = false;
  }
}

// 筛选变化：游标链重置回第 1 页
function resetOrderList() {
  page.value = 1;
  cursors.value = [0];
  hasMore.value = false;
  loadOrders();
}

// 分页回调：回第 1 页（首页/改条数）时旧游标链作废，随翻页重建
function onPagerChange(p: number) {
  if (p === 1) cursors.value = [0];
  loadOrders();
}

async function handleDetail(orderNo: string) {
  const { data, error } = await fetchOrder(orderNo);
  if (!error && data) {
    detail.value = data;
    showDetail.value = true;
  }
}

async function handleCancel(orderNo: string) {
  const { error } = await cancelOrder(orderNo, "管理员取消");
  if (!error) {
    window.$message?.success("取消成功");
    loadOrders();
  }
}

onMounted(loadOrders);
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="订单管理" class="flex-1">
      <div class="mb-16px flex items-center gap-12px">
        <NSelect
          v-model:value="statusFilter"
          :options="statusOptions"
          placeholder="全部状态"
          clearable
          class="w-160px"
          @update:value="resetOrderList"
        />
        <NButton @click="loadOrders">刷新</NButton>
      </div>

      <NDataTable :columns="columns" :data="orders" :loading="loading" />

      <TablePager
        v-model:page="page"
        v-model:page-size="pageSize"
        mode="cursor"
        :has-more="hasMore"
        @change="onPagerChange"
      />
    </NCard>

    <!-- 订单详情弹窗 -->
    <NModal v-model:show="showDetail" preset="card" title="订单详情" style="width: 720px">
      <template v-if="detail">
        <NDescriptions :column="2" bordered size="small">
          <NDescriptionsItem label="订单号">{{ detail.order_no }}</NDescriptionsItem>
          <NDescriptionsItem label="状态">
            <NTag :type="statusType(detail.status)" size="small">{{
              statusText(detail.status)
            }}</NTag>
          </NDescriptionsItem>
          <NDescriptionsItem label="总额">{{ formatMoney(detail.total_cents) }}</NDescriptionsItem>
          <NDescriptionsItem label="成本">{{ formatMoney(detail.cost_cents) }}</NDescriptionsItem>
          <NDescriptionsItem label="联系方式">{{
            detail.contact || detail.guest_contact || "-"
          }}</NDescriptionsItem>
          <NDescriptionsItem label="IP">{{ detail.client_ip || "-" }}</NDescriptionsItem>
          <NDescriptionsItem label="创建时间">{{
            formatTime(detail.created_at)
          }}</NDescriptionsItem>
          <NDescriptionsItem label="支付时间">{{
            detail.paid_at ? formatTime(detail.paid_at) : "-"
          }}</NDescriptionsItem>
        </NDescriptions>

        <NDivider>商品明细</NDivider>
        <NDataTable :data="detail.items || []" :columns="itemColumns" size="small" />

        <NDivider>金额明细（{{ (detail.amount_lines || []).length }} 行）</NDivider>
        <NDataTable :data="detail.amount_lines || []" :columns="amountColumns" size="small" />

        <NDivider>状态事件（{{ (detail.status_events || []).length }} 条）</NDivider>
        <NTimeline>
          <NTimelineItem
            v-for="(evt, i) in detail.status_events || []"
            :key="i"
            :type="evt.event === 'paid' ? 'success' : evt.event === 'canceled' ? 'error' : 'info'"
            :title="`${evt.from_status || '创建'} → ${evt.to_status}`"
            :content="`${evt.operator}${evt.reason ? '：' + evt.reason : ''}`"
            :time="formatTime(evt.created_at)"
          />
        </NTimeline>
      </template>
    </NModal>
  </div>
</template>
