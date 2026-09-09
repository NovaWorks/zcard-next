<script setup lang="ts">
// 采购单管理（procurement:read / procurement:write）：上游拿货单（客户购买 →
// 上游采购 → 卡密回填链路的运行轨迹）。状态筛选 + 手动重试 / 转人工。
import { h, computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { NButton, NDataTable, NTag, NPopconfirm, NModal, NCard, NSpin, NAlert, NDescriptions, NDescriptionsItem, NPagination } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchProcurement, fetchDeliveries, fetchProcurements, retryProcurement, markProcurementManual } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";
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

function orderStatusText(status: string) {
  return ({ pending_payment: '待支付', paid: '已支付', fulfilling: '履约中', partially_delivered: '部分发货', delivered: '已发货', completed: '已完成', canceled: '已取消', expired: '已过期', refunded: '已退款', refund_pending: '退款中' } as Record<string, string>)[status] || status;
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
  width: 148,
  render: (row: any) =>
    h("div", { class: "flex flex-wrap items-center gap-4px" }, [
      h(NButton, { size: "tiny", type: "primary", quaternary: true, onClick: () => openDetail(row.id) }, { default: () => "查看" }),
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
    // 订单项 ID 为长数字（雪花位），定宽 + 悬停全文防换行
    cols.push({ title: "订单项", key: "order_item_id", width: 110, render: (row: any) => h("span", { class: "whitespace-nowrap", title: String(row.order_item_id ?? "") }, String(row.order_item_id ?? "-")) });
    cols.push({ title: "渠道", key: "connection_id", width: 72, render: (row: any) => `#${row.connection_id}` });
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
    // 数字列（卡密行数可达十万级）加宽 + 右对齐，杜绝逐字换行
    cols.push({ title: "卡密行数", key: "received_cards", width: 88, align: "right" as const, render: (row: any) => h("span", { class: "whitespace-nowrap tabular-nums" }, String(row.received_cards ?? row.received_count ?? "-")) });
    cols.push({ title: "重试", key: "retry_count", width: 64, align: "right" as const, render: (row: any) => h("span", { class: "whitespace-nowrap tabular-nums" }, String(row.retry_count ?? 0)) });
  }
  if (tr !== "compact") cols.push(errorCol());
  if (tr !== "compact") cols.push({ title: "更新时间", key: "updated_at", width: 142, render: (row: any) => fmtTime(row.updated_at || row.created_at) });
  cols.push(actionsCol());
  return cols;
});

const detailVisible = ref(false);
const detailLoading = ref(false);
const detail = ref<any>(null);
const detailError = ref('');
const deliveries = ref<any[]>([]);
const deliveryLoading = ref(false);
const deliveryError = ref('');
const deliveryPage = ref(1);
const deliveryTotal = ref(0);
let detailRequest = 0;
const canViewDelivery = () => checkAuth('order:view_delivery');
const money = (value?: number) => formatMoney(Number(value || 0));
function closeDetail() { ++detailRequest; detailVisible.value = false; detail.value = null; deliveries.value = []; }
async function openDetail(id: number) {
  const seq = ++detailRequest;
  detailVisible.value = true; detailLoading.value = true; detail.value = null;
  detailError.value = ''; deliveries.value = []; deliveryError.value = ''; deliveryTotal.value = 0; deliveryPage.value = 1;
  const { data, error } = await fetchProcurement(id);
  if (seq !== detailRequest) return;
  detailLoading.value = false;
  if (error || !data) { detailError.value = '读取采购详情失败，请关闭后重试'; return; }
  detail.value = data;
  if (canViewDelivery()) void loadDelivery();
}
async function loadDelivery() {
  const seq = detailRequest;
  deliveryLoading.value = true; deliveryError.value = ''; deliveries.value = [];
  const { data, error } = await fetchDeliveries(detail.value.order_no, deliveryPage.value, 20, detail.value.order_item_id);
  if (seq !== detailRequest) return;
  deliveryLoading.value = false;
  if (error) { deliveryError.value = '读取发货内容失败'; return; }
  deliveries.value = (data as any)?.deliveries || [];
  deliveryTotal.value = Number((data as any)?.total || 0);
}
onMounted(load);
</script>

<template>
  <div ref="wrapRef">
    <NModal :show="detailVisible" @update:show="(show) => { if (!show) closeDetail(); }">
      <NCard title="采购单详情" closable class="procurement-detail" @close="closeDetail">
        <NSpin :show="detailLoading">
          <NAlert v-if="detailError" type="error">{{ detailError }}</NAlert>
          <template v-if="detail">
            <NDescriptions bordered label-placement="top" :column="tier === 'compact' ? 1 : 3">
              <NDescriptionsItem label="销售订单">{{ detail.order_no }} · {{ orderStatusText(detail.order_status) }}</NDescriptionsItem>
              <NDescriptionsItem label="采购状态">{{ statusText(detail.status) }}</NDescriptionsItem>
              <NDescriptionsItem label="采购单号">{{ detail.id }}</NDescriptionsItem>
              <NDescriptionsItem label="商品" :span="2">{{ detail.product_name }}<template v-if="detail.sku_name"> / {{ detail.sku_name }}</template></NDescriptionsItem>
              <NDescriptionsItem label="数量">{{ detail.item_quantity }}</NDescriptionsItem>
              <NDescriptionsItem label="采购来源">{{ detail.connection_name || `渠道 #${detail.connection_id}（已不可用）` }} · {{ detail.connection_driver }}</NDescriptionsItem>
              <NDescriptionsItem label="上游地址" :span="2">{{ detail.connection_url || '—' }}</NDescriptionsItem>
              <NDescriptionsItem label="上游订单">{{ detail.upstream_order_id || '尚未生成' }}</NDescriptionsItem>
              <NDescriptionsItem label="上游商品编号">{{ detail.upstream_product_code || '—' }}</NDescriptionsItem>
              <NDescriptionsItem label="下单时间">{{ fmtTime(detail.created_at) }}</NDescriptionsItem>
              <NDescriptionsItem label="销售单价">{{ money(detail.sale_unit_cents) }}</NDescriptionsItem>
              <NDescriptionsItem label="商品小计">{{ money(detail.sale_amount_cents) }}</NDescriptionsItem>
              <NDescriptionsItem label="成交金额（优惠分摊）">{{ money(detail.allocated_sale_cents) }}</NDescriptionsItem>
              <NDescriptionsItem label="成本单价（下单快照）">{{ detail.cost_basis === 'order_snapshot' ? money(detail.cost_unit_cents) : '未记录' }}</NDescriptionsItem>
              <NDescriptionsItem label="成本合计">{{ detail.cost_basis === 'order_snapshot' ? money(detail.cost_total_cents) : '未记录' }}</NDescriptionsItem>
              <NDescriptionsItem label="预计毛利">{{ detail.cost_basis === 'order_snapshot' ? money(detail.profit_cents) : '成本缺失，暂无法计算' }}</NDescriptionsItem>
            </NDescriptions>
            <p class="text-12px text-gray-500">成本取下单时的商品成本快照；预计毛利未计退款、手续费及上游实际扣款差额。</p>
            <NAlert v-if="detail.last_error" type="warning" class="my-12px">{{ detail.last_error }}</NAlert>
            <h3>发货内容</h3>
            <p v-if="!canViewDelivery()">当前账号无查看发货内容权限。</p>
            <NSpin v-else :show="deliveryLoading">
              <NAlert v-if="deliveryError" type="error">{{ deliveryError }} <NButton text @click="loadDelivery">重试</NButton></NAlert>
              <p v-else-if="!deliveryLoading && !deliveries.length">此采购商品暂无发货记录。</p>
              <div v-for="row in deliveries" :key="row.id" class="delivery-record"><pre>{{ row.content || row.content_masked || '内容已不可用' }}</pre><small>{{ fmtTime(row.delivered_at) }}</small></div>
              <NPagination v-if="deliveryTotal > 20" v-model:page="deliveryPage" :item-count="deliveryTotal" :page-size="20" :disabled="deliveryLoading" @update:page="loadDelivery" />
            </NSpin>
          </template>
        </NSpin>
      </NCard>
    </NModal>
    <FilterTabs v-model:value="statusFilter" :options="statusTabs" class="mb-12px" @change="onSearch" />
    <NDataTable :columns="columns" :data="rows" :loading="loading" size="small" :row-key="(r: any) => r.id" :max-height="540" :scroll-x="300" />
    <div class="mt-12px flex justify-end">
      <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
    </div>
  </div>
</template>

<style scoped>
.procurement-detail { width: min(960px, calc(100vw - 24px)); max-height:90vh; overflow:auto; overflow-wrap:anywhere; }
.delivery-record { background:var(--n-color-embedded,#f5f7fa); padding:12px; margin:8px 0; border-radius:6px; }
.delivery-record pre { white-space:pre-wrap; overflow-wrap:anywhere; margin:0 0 8px; font-family:monospace; }
</style>
