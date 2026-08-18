<script setup lang="ts">
import TablePager from "@/components/common/table-pager.vue";
import { ref, onMounted, h } from "vue";
import {
  NButton,
  NTag,
  NSpace,
  NPopconfirm,
  NModal,
  NDescriptions,
  NDescriptionsItem,
  NDivider,
  NSelect,
} from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { checkAuth } from "@/directives";
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

// ── 枚举翻译（P2-09 T5 修复：业务名称化，未知值回显原值）──

function fulfillmentTypeText(t: string) {
  const map: Record<string, string> = {
    auto: "自动发货",
    manual: "手动发货",
    upstream: "上游代发",
  };
  return map[t] || t || "-";
}

function fulfillmentStatusText(s: string) {
  const map: Record<string, string> = {
    pending: "待履约",
    delivering: "履约中",
    delivered: "已履约",
    failed: "履约失败",
  };
  return map[s] || s || "-";
}

function amountTypeText(t: string) {
  const map: Record<string, string> = {
    base_price: "商品价",
    sku_adjust: "SKU 加价",
    member_discount: "会员折扣",
    group_discount: "团购折扣",
    promo_discount: "促销折扣",
    coupon_discount: "优惠券折扣",
    points_discount: "积分抵扣",
    subsite_markup: "分站加价",
    fee: "手续费",
    tax: "税费",
    rounding_adjust: "取整调整",
  };
  return map[t] || t;
}

function sourceTypeText(s: string) {
  const map: Record<string, string> = {
    member_level: "会员等级",
    member_group: "会员分组",
    coupon: "优惠券",
    flash_sale: "限时秒杀",
    points: "积分",
    subsite: "分站",
    manual: "手动",
    gift_card: "礼品卡",
  };
  return map[s] || s || "-";
}

// 状态事件节点颜色（横向时间线：成功绿/取消红/其他蓝）
function eventDotColor(evt: any): string {
  if (["paid", "delivered", "completed", "fulfilled"].includes(evt.event)) return "#18a058";
  if (["canceled", "expired", "refunded"].includes(evt.event)) return "#d03050";
  return "#2080f0";
}

// 事件排序（时间升序——后端已排，防御性再排一次）
function sortedEvents(evts: any[]) {
  return [...(evts || [])].sort((a, b) => (a.created_at || 0) - (b.created_at || 0));
}

function operatorText(op: string) {
  const map: Record<string, string> = {
    system: "系统",
    user: "用户",
    admin: "管理员",
    worker: "工单处理",
  };
  return map[op] || op || "-";
}

function eventText(evt: string) {
  const map: Record<string, string> = {
    created: "创建订单",
    paid: "支付成功",
    fulfilled: "履约完成",
    delivered: "发货完成",
    completed: "订单完成",
    canceled: "订单取消",
    expired: "订单过期",
    refund_requested: "申请退款",
    refunded: "退款完成",
  };
  return map[evt] || evt;
}

function hasDiscount(detail: any) {
  return (detail?.amount_lines || []).some((l: any) => (l.amount_cents || 0) < 0);
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
            row.status === "pending_payment" && checkAuth("order:cancel")
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
  {
    title: "商品",
    key: "name",
    width: 180,
    ellipsis: { tooltip: true },
    render: (row) => (row.name || `#${row.product_id}`) + (row.sku_name ? `（${row.sku_name}）` : ""),
  },
  { title: "数量", key: "quantity", width: 60 },
  {
    title: "单价",
    key: "unit_price_cents",
    width: 90,
    render: (row) => formatMoney(row.unit_price_cents),
  },
  { title: "小计", key: "amount_cents", width: 90, render: (row) => formatMoney(row.amount_cents) },
  { title: "成本", key: "cost_cents", width: 90, render: (row) => formatMoney(row.cost_cents) },
  {
    title: "来源",
    key: "is_self",
    width: 80,
    render: (row) =>
      h(
        NTag,
        { size: "small", type: row.is_self ? "success" : "warning", bordered: false },
        { default: () => (row.is_self ? "自营" : "上游") },
      ),
  },
  {
    title: "上游渠道",
    key: "upstream_source_name",
    width: 120,
    ellipsis: { tooltip: true },
    render: (row) => row.upstream_source_name || "-",
  },
  {
    title: "履约",
    key: "fulfillment",
    width: 150,
    render: (row) =>
      h(
        NSpace,
        { size: 4 },
        {
          default: () => [
            h(NTag, { size: "small", bordered: false }, { default: () => fulfillmentTypeText(row.fulfillment_type) }),
            h(
              NTag,
              {
                size: "small",
                bordered: false,
                type: row.fulfillment_status === "delivered" ? "success" : "default",
              },
              { default: () => fulfillmentStatusText(row.fulfillment_status) },
            ),
          ],
        },
      ),
  },
];

const amountColumns: DataTableColumns<any> = [
  { title: "#", key: "seq", width: 40 },
  { title: "类型", key: "type", width: 140, render: (row) => amountTypeText(row.type) },
  {
    title: "金额",
    key: "amount_cents",
    width: 90,
    render: (row) =>
      h(
        "span",
        { style: { color: (row.amount_cents || 0) < 0 ? "#18a058" : undefined } },
        { default: () => formatSignedMoney(row.amount_cents) },
      ),
  },
  { title: "来源", key: "source_type", width: 100, render: (row) => sourceTypeText(row.source_type) },
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
          <NDescriptionsItem label="状态">{{ statusText(detail.status) }}</NDescriptionsItem>
          <NDescriptionsItem label="总额">{{ formatMoney(detail.total_cents) }}</NDescriptionsItem>
          <NDescriptionsItem label="成本">{{ formatMoney(detail.cost_cents) }}</NDescriptionsItem>
          <NDescriptionsItem label="联系方式">{{ detail.contact || detail.guest_contact || "-" }}</NDescriptionsItem>
          <NDescriptionsItem label="IP">{{ detail.client_ip || "-" }}</NDescriptionsItem>
          <NDescriptionsItem label="订单属性">
            <NSpace :size="4">
              <NTag v-if="hasDiscount(detail)" size="small" type="success" :bordered="false">已用折扣</NTag>
              <NTag v-if="(detail.items || []).every((it) => it.is_self)" size="small" type="info" :bordered="false">自营</NTag>
              <NTag v-else-if="(detail.items || []).some((it) => !it.is_self)" size="small" type="warning" :bordered="false">含上游商品</NTag>
            </NSpace>
          </NDescriptionsItem>
        </NDescriptions>

        <!-- 上游货源信息（老项目同款信息区） -->
        <template v-if="(detail.items || []).some((it) => it.upstream_source_name)">
          <NDivider>上游货源</NDivider>
          <NDescriptions :column="2" bordered size="small">
            <NDescriptionsItem label="上游渠道">{{
              (detail.items || []).find((it) => it.upstream_source_name)?.upstream_source_name || "-"
            }}</NDescriptionsItem>
            <NDescriptionsItem label="上游驱动">{{
              (detail.items || []).find((it) => it.upstream_driver)?.upstream_driver || "-"
            }}</NDescriptionsItem>
            <NDescriptionsItem label="上游商品编码" :span="2">
              {{ (detail.items || []).find((it) => it.upstream_product_code)?.upstream_product_code || "-" }}
            </NDescriptionsItem>
            <NDescriptionsItem label="上游地址" :span="2">
              <a
                v-if="(detail.items || []).find((it) => it.upstream_url)"
                :href="(detail.items || []).find((it) => it.upstream_url)?.upstream_url"
                target="_blank"
                rel="noopener noreferrer"
                class="text-blue-500 hover:underline break-all"
              >
                {{ (detail.items || []).find((it) => it.upstream_url)?.upstream_url }} ↗
              </a>
              <span v-else>-</span>
            </NDescriptionsItem>
          </NDescriptions>
        </template>
        <NDivider>商品明细</NDivider>
        <NDataTable :data="detail.items || []" :columns="itemColumns" size="small" />
        <NDivider>金额明细（{{ (detail.amount_lines || []).length }} 行）</NDivider>
        <NDataTable :data="detail.amount_lines || []" :columns="amountColumns" size="small" />
        <NDivider>状态事件（{{ (detail.status_events || []).length }} 条）</NDivider>
        <!-- 横向时间线（大厂订单详情风格：圆点节点 + 连线 + 时间，可横向滚动） -->
        <div style="display:flex;align-items:flex-start;overflow-x:auto;padding:4px 4px 8px;">
          <div
            v-for="(evt, i) in [...(detail.status_events || [])].sort((a, b) => (a.created_at || 0) - (b.created_at || 0))"
            :key="i"
            style="display:flex;align-items:flex-start;"
          >
            <div v-if="i > 0" style="width:28px;height:2px;margin-top:13px;background:#d0d7de;" />
            <div style="min-width:96px;padding:0 4px;text-align:center;">
              <div
                style="width:12px;height:12px;border-radius:50%;border:2px solid #fff;box-shadow:0 0 0 1px #d0d7de;margin:0 auto;"
                :style="{ background: ['paid', 'delivered', 'completed', 'fulfilled'].includes(evt.event) ? '#18a058' : ['canceled', 'expired', 'refunded'].includes(evt.event) ? '#d03050' : '#2080f0' }"
              />
              <div
                class="mt-8px text-12px font-500"
                :style="{ color: ['paid', 'delivered', 'completed', 'fulfilled'].includes(evt.event) ? '#18a058' : ['canceled', 'expired', 'refunded'].includes(evt.event) ? '#d03050' : '#2080f0' }"
              >
                {{ eventText(evt.event) }}
              </div>
              <div class="text-11px opacity-50 mt-2px">
                {{ evt.from_status && evt.to_status ? statusText(evt.from_status) + " → " + statusText(evt.to_status) : "" }}
              </div>
              <div class="text-11px opacity-60 mt-2px">{{ formatTime(evt.created_at) }}</div>
              <div class="text-11px opacity-50 mt-2px">
                {{ operatorText(evt.operator) }}{{ evt.reason ? "：" + evt.reason : "" }}
              </div>
            </div>
          </div>
          <div v-if="(detail.status_events || []).length === 0" class="text-12px opacity-50" style="padding:8px 0;">
            暂无状态事件
          </div>
        </div>
      </template>
    </NModal>
  </div>
</template>
