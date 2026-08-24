<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { useRouter } from "vue-router";
import { NRadioButton, NRadioGroup, NSpin } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { useEcharts } from "@/hooks/common/echarts";
import { fetchDashboard, fetchTraffic } from "@/service/api";
import { formatMoney, centsToYuan } from "@/utils/money";
import type { DashboardData, DashboardStat, DashboardTopChannel, DashboardTopProduct, TrafficPoint } from "@/service/api";

defineOptions({ name: "Dashboard" });

const router = useRouter();
const loading = ref(false);
const data = ref<DashboardData | null>(null);

// KPI 时间窗（今日/近7天/近30天，切换联动 4 张指标卡 + 环比基准）
const rangeOptions = [
  { label: "今日", value: "today" },
  { label: "近7天", value: "7d" },
  { label: "近30天", value: "30d" },
];
const range = ref<"today" | "7d" | "30d">("today");

// 趋势图天数（图表卡内独立切换，重新请求后端）
const trendDaysOptions = [
  { label: "7天", value: 7 },
  { label: "14天", value: 14 },
  { label: "30天", value: 30 },
];
const trendDays = ref(7);

const EMPTY: DashboardStat = { orders: 0, revenue: 0, paid_orders: 0, cost: 0, profit: 0, new_users: 0 };

// proto3 零值字段不输出 → 字段级兜底补 0（杜绝 undefined/NaN）
const statOf = (d: DashboardData | null): DashboardStat => {
  if (!d) return EMPTY;
  const s = range.value === "today" ? d.today : range.value === "7d" ? d.last7d : d.last30d;
  return { ...EMPTY, ...(s || {}) };
};
const prevOf = (d: DashboardData | null): DashboardStat => {
  if (!d) return EMPTY;
  const s = range.value === "today" ? d.yesterday : range.value === "7d" ? d.prev7d : d.prev30d;
  return { ...EMPTY, ...(s || {}) };
};

const cur = computed(() => statOf(data.value));
const prev = computed(() => prevOf(data.value));
const prevLabel = computed(() => (range.value === "today" ? "较昨日" : range.value === "7d" ? "较前7天" : "较前30天"));

/** 环比（%）；无基数/非法值返回 null 显示 — */
function ratio(cur: number, prev: number): number | null {
  if (!Number.isFinite(cur) || !Number.isFinite(prev) || prev === 0) return null;
  return ((cur - prev) / prev) * 100;
}

const kpiCards = computed(() => {
  const c = cur.value;
  const p = prev.value;
  return [
    { key: "revenue", label: "营收", value: formatMoney(c.revenue), ratio: ratio(c.revenue, p.revenue), sparkline: true },
    { key: "orders", label: "订单数", value: String(c.orders), sub: `已支付 ${c.paid_orders} 单`, ratio: ratio(c.orders, p.orders), sparkline: false },
    { key: "profit", label: "利润", value: formatMoney(c.profit), ratio: ratio(c.profit, p.profit), sparkline: false },
    { key: "users", label: "新增用户", value: String(c.new_users), ratio: ratio(c.new_users, p.new_users), sparkline: false },
  ];
});

// 营收卡迷你趋势线（复用趋势数据）
const { domRef: sparkRef, updateOptions: updateSpark } = useEcharts(() => ({
  grid: { left: 0, right: 0, top: 2, bottom: 0 },
  xAxis: { type: "category", show: false, boundaryGap: false, data: [] as string[] },
  yAxis: { type: "value", show: false, min: "dataMin" },
  series: [
    {
      type: "line",
      smooth: true,
      showSymbol: false,
      lineStyle: { width: 1.5, color: "#2080f0" },
      areaStyle: { color: "rgba(32,128,240,0.12)" },
      data: [] as number[],
    },
  ],
}));
function setSparkDom(el: unknown) {
  sparkRef.value = (el as HTMLElement | null) ?? null;
}

// 销售趋势主图（营收面积 + 订单柱，双轴）
const { domRef: mainRef, updateOptions: updateMain } = useEcharts(() => ({
  tooltip: { trigger: "axis" },
  legend: { data: ["营收(元)", "订单数"], top: "0" },
  grid: { left: "3%", right: "4%", bottom: "3%", top: "18%", containLabel: true },
  xAxis: { type: "category", boundaryGap: false, data: [] as string[] },
  yAxis: [
    { type: "value", name: "营收(元)", splitLine: { lineStyle: { type: "dashed" } } },
    { type: "value", name: "订单数", splitLine: { show: false } },
  ],
  series: [
    {
      name: "营收(元)",
      type: "line",
      smooth: true,
      showSymbol: false,
      itemStyle: { color: "#2080f0" },
      lineStyle: { width: 2 },
      areaStyle: { opacity: 0.12 },
      data: [] as number[],
    },
    {
      name: "订单数",
      type: "bar",
      yAxisIndex: 1,
      barMaxWidth: 16,
      itemStyle: { color: "rgba(32,128,240,0.25)", borderRadius: [3, 3, 0, 0] },
      data: [] as number[],
    },
  ],
}));

function renderCharts(d: DashboardData) {
  updateMain((opts) => {
    opts.xAxis.data = d.trend.map((t) => t.date.slice(5));
    opts.series[0].data = d.trend.map((t) => centsToYuan(t.revenue || 0));
    opts.series[1].data = d.trend.map((t) => t.paid_count || 0);
    return opts;
  });
  updateSpark((opts) => {
    opts.xAxis.data = d.trend.map((t) => t.date);
    opts.series[0].data = d.trend.map((t) => centsToYuan(t.revenue || 0));
    return opts;
  });
}

// 流量趋势图（PV 柱 + UV 线；跟随 trendDays 天数）
const { domRef: trafficRef, updateOptions: updateTraffic } = useEcharts(() => ({
  tooltip: { trigger: "axis" },
  legend: { data: ["PV", "UV"], top: "0" },
  grid: { left: "3%", right: "3%", bottom: "3%", top: "18%", containLabel: true },
  xAxis: { type: "category", boundaryGap: false, data: [] as string[] },
  yAxis: { type: "value", splitLine: { lineStyle: { type: "dashed" } } },
  series: [
    {
      name: "PV",
      type: "bar",
      barMaxWidth: 16,
      itemStyle: { color: "rgba(32,128,240,0.25)", borderRadius: [3, 3, 0, 0] },
      data: [] as number[],
    },
    {
      name: "UV",
      type: "line",
      smooth: true,
      showSymbol: false,
      itemStyle: { color: "#18a058" },
      lineStyle: { width: 2 },
      data: [] as number[],
    },
  ],
}));

function renderTraffic(points: TrafficPoint[]) {
  updateTraffic((opts) => {
    opts.xAxis.data = points.map((p) => p.date.slice(5));
    opts.series[0].data = points.map((p) => p.pv || 0);
    opts.series[1].data = points.map((p) => p.uv || 0);
    return opts;
  });
}

const topProducts = computed(() => (data.value?.top_products || []).slice(0, 5));
const onlineUsers = computed(() => data.value?.online_users || 0);

const topColumns: DataTableColumns<DashboardTopProduct> = [
  { title: "排名", key: "rank", width: 60, render: (_row, index) => index + 1 },
  { title: "商品", key: "name", minWidth: 140, ellipsis: true },
  { title: "销量", key: "sold_qty", width: 90, render: (row) => row.sold_qty || 0 },
  { title: "营收", key: "revenue", width: 110, render: (row) => formatMoney(row.revenue) },
];

// ── 支付渠道排行 ──
const channels = computed(() => (data.value?.top_channels || []).slice(0, 5));

const channelNames: Record<string, string> = {
  alipay: "支付宝",
  wechat: "微信支付",
  epay: "易支付",
  epusdt: "EPUSDT",
  stripe: "Stripe",
  paypal: "PayPal",
  wallet: "余额",
};
const channelName = (ch: string) => channelNames[ch] || ch;

function channelRate(c: DashboardTopChannel) {
  return c.total_count ? Math.round((c.success_count / c.total_count) * 1000) / 10 : 0;
}
function rateColor(r: number) {
  return r >= 90 ? "#18a058" : r >= 70 ? "#2080f0" : "#d03050";
}

// ── 待办事项（点击跳转对应管理页）──
const todos = computed(() => {
  const p = data.value?.pending;
  if (!p) return [];
  return [
    // 待审对接申请 → 渠道管理·供货账号 tab（自动筛待审核）
    { label: "待审对接申请", value: p.pending_supplier_applications, path: { path: "/channel", query: { tab: "suppliers", status: "applying" } }, warn: true },
    { label: "待审核提现", value: p.pending_withdrawals, path: "/wallet", warn: true },
    { label: "待处理退款", value: p.pending_refunds, path: "/order", warn: true },
    { label: "履约中订单", value: p.fulfilling_orders, path: "/order", warn: false },
    // 库存预警 → 商品管理并自动过滤库存不足商品（商品页读 query 启用筛选）
    { label: "库存预警", value: p.low_stock_products, path: { path: "/product", query: { low_stock: "1" } }, warn: true },
  ];
});

async function loadDashboard() {
  loading.value = true;
  try {
    const { data: d, error } = await fetchDashboard(trendDays.value);
    if (!error && d) {
      data.value = d;
      renderCharts(d);
    }
    const { data: t, error: tErr } = await fetchTraffic(trendDays.value);
    if (!tErr && t) renderTraffic(t.points || []);
  } finally {
    loading.value = false;
  }
}

watch(trendDays, loadDashboard);
onMounted(loadDashboard);
</script>

<template>
  <NSpin :show="loading">
    <div class="flex flex-col gap-16px">
      <!-- 头部：标题 + 在线用户 + KPI 时间窗 -->
      <div class="flex items-center justify-between">
        <span class="flex items-center gap-8px">
          <span class="text-16px font-semibold">工作台</span>
          <span class="flex items-center gap-4px text-13px text-gray-500">
            <span class="h-8px w-8px rounded-full bg-green-500"></span>
            在线 {{ onlineUsers }} 人
          </span>
        </span>
        <NRadioGroup v-model:value="range" size="small">
          <NRadioButton v-for="o in rangeOptions" :key="o.value" :value="o.value">{{ o.label }}</NRadioButton>
        </NRadioGroup>
      </div>

      <!-- KPI 指标卡（营收带迷你趋势线；环比红涨绿跌） -->
      <NGrid :x-gap="16" :y-gap="16" cols="s:1 m:2 l:4" responsive="screen">
        <NGi v-for="card in kpiCards" :key="card.key">
          <NCard :bordered="false" size="small" class="h-full">
            <div class="flex items-center justify-between">
              <span class="text-13px text-gray-500">{{ card.label }}</span>
              <span v-if="card.sub" class="text-12px text-gray-400">{{ card.sub }}</span>
            </div>
            <div class="mt-6px text-26px font-bold leading-tight">{{ card.value }}</div>
            <div class="mt-6px flex items-center gap-4px text-12px">
              <span
                v-if="card.ratio !== null"
                :class="card.ratio >= 0 ? 'text-red-500' : 'text-green-500'"
                class="font-medium"
              >
                {{ card.ratio >= 0 ? "↑" : "↓" }}{{ Math.abs(card.ratio).toFixed(1) }}%
              </span>
              <span v-else class="text-gray-400">—</span>
              <span class="text-gray-400">{{ prevLabel }}</span>
            </div>
            <div v-if="card.sparkline" :ref="setSparkDom" class="mt-8px h-36px"></div>
          </NCard>
        </NGi>
      </NGrid>

      <!-- 中部：销售趋势 + 支付渠道排行 -->
      <NGrid :x-gap="16" :y-gap="16" cols="s:1 l:3" responsive="screen">
        <NGi span="2">
          <NCard :bordered="false">
            <template #header>
              <div class="flex items-center justify-between">
                <span>销售趋势</span>
                <NRadioGroup v-model:value="trendDays" size="small">
                  <NRadioButton v-for="o in trendDaysOptions" :key="o.value" :value="o.value">{{ o.label }}</NRadioButton>
                </NRadioGroup>
              </div>
            </template>
            <div ref="mainRef" class="h-320px"></div>
          </NCard>
        </NGi>
        <NGi>
          <div class="flex flex-col gap-16px">
            <NCard title="支付渠道排行（近30天）" :bordered="false">
              <div v-if="channels.length" class="flex flex-col gap-14px">
                <div v-for="c in channels" :key="c.channel">
                  <div class="flex items-center justify-between text-13px">
                    <span>{{ channelName(c.channel) }}</span>
                    <span class="text-gray-400">
                      {{ c.total_count || 0 }} 笔 ·
                      <span :style="{ color: rateColor(channelRate(c)) }" class="font-medium">{{ channelRate(c) }}%</span>
                    </span>
                  </div>
                  <div class="mt-6px h-6px overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
                    <div
                      class="h-full rounded-full"
                      :style="{ width: channelRate(c) + '%', backgroundColor: rateColor(channelRate(c)) }"
                    ></div>
                  </div>
                </div>
              </div>
              <div v-else class="py-20px text-center text-13px text-gray-400">近30天暂无支付数据</div>
            </NCard>
            <NCard title="流量趋势（PV/UV）" :bordered="false">
              <div ref="trafficRef" class="h-180px"></div>
            </NCard>
          </div>
        </NGi>
      </NGrid>

      <!-- 底部：商品销量 Top5 + 待办事项 -->
      <NGrid :x-gap="16" :y-gap="16" cols="s:1 l:3" responsive="screen">
        <NGi span="2">
          <NCard title="商品销量 Top5（近30天）" :bordered="false">
            <NDataTable :columns="topColumns" :data="topProducts" size="small" :bordered="false"  :max-height="540" />
          </NCard>
        </NGi>
        <NGi>
          <NCard title="待办事项" :bordered="false">
            <div class="flex flex-col">
              <div
                v-for="t in todos"
                :key="t.label"
                class="flex cursor-pointer items-center justify-between rounded-4px border-b border-gray-100 px-4px py-10px last:border-none hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800"
                @click="router.push(t.path)"
              >
                <span class="text-13px">{{ t.label }}</span>
                <span
                  v-if="t.value > 0"
                  :class="t.warn ? 'bg-red-500' : 'bg-blue-500'"
                  class="rounded-full px-8px py-2px text-12px text-white"
                >{{ t.value }}</span>
                <span v-else class="text-12px text-gray-400">无</span>
              </div>
            </div>
          </NCard>
        </NGi>
      </NGrid>
    </div>
  </NSpin>
</template>
