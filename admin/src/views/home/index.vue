<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <!-- 统计卡片 -->
    <NGrid :x-gap="16" :y-gap="16" cols="s:1 m:3" responsive="screen" item-responsive>
      <NGi>
        <NCard :bordered="false" size="small">
          <div class="text-14px">今日订单数</div>
          <div class="mt-8px text-28px font-bold">{{ today.orders }}</div>
          <div class="mt-4px text-12px opacity-60">已支付 {{ today.paid_orders }} 单</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard :bordered="false" size="small">
          <div class="text-14px">今日营收</div>
          <div class="mt-8px text-28px font-bold">{{ formatMoney(today.revenue) }}</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard :bordered="false" size="small">
          <div class="text-14px">近30天营收</div>
          <div class="mt-8px text-28px font-bold">{{ formatMoney(last30d.revenue) }}</div>
        </NCard>
      </NGi>
    </NGrid>

    <!-- 近7天趋势 -->
    <NCard title="近7天趋势" :bordered="false">
      <div ref="domRef" class="h-360px overflow-hidden"></div>
    </NCard>

    <!-- 商品销量 Top5 -->
    <NCard title="商品销量 Top5" :bordered="false">
      <NDataTable :columns="topColumns" :data="topProducts" :loading="loading" size="small" />
    </NCard>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import type { DataTableColumns } from 'naive-ui';
import { useEcharts } from '@/hooks/common/echarts';
import { fetchDashboard } from '@/service/api';
import { formatMoney, centsToYuan } from '@/utils/money';
import type { DashboardStat, DashboardTopProduct, DashboardTrendPoint } from '@/service/api';

defineOptions({ name: 'Dashboard' });

const loading = ref(false);
const today = ref<DashboardStat>({ orders: 0, revenue: 0, paid_orders: 0 });
const last30d = ref<DashboardStat>({ orders: 0, revenue: 0, paid_orders: 0 });
const topProducts = ref<DashboardTopProduct[]>([]);

const { domRef, updateOptions } = useEcharts(() => ({
  tooltip: {
    trigger: 'axis'
  },
  legend: {
    data: ['订单数', '营收(元)'],
    top: '0'
  },
  grid: {
    left: '3%',
    right: '4%',
    bottom: '3%',
    top: '18%',
    containLabel: true
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: [] as string[]
  },
  yAxis: [
    {
      type: 'value',
      name: '订单数'
    },
    {
      type: 'value',
      name: '营收(元)'
    }
  ],
  series: [
    {
      name: '订单数',
      type: 'line',
      smooth: true,
      data: [] as number[]
    },
    {
      name: '营收(元)',
      type: 'line',
      smooth: true,
      yAxisIndex: 1,
      data: [] as number[]
    }
  ]
}));

function renderTrend(trend: DashboardTrendPoint[]) {
  updateOptions(opts => {
    opts.xAxis.data = trend.map(item => item.date.slice(5));
    opts.series[0].data = trend.map(item => item.orders);
    opts.series[1].data = trend.map(item => centsToYuan(item.revenue));
    return opts;
  });
}

const topColumns: DataTableColumns<DashboardTopProduct> = [
  { title: '排名', key: 'rank', width: 60, render: (_row, index) => index + 1 },
  { title: '商品', key: 'name', minWidth: 160 },
  { title: '销量', key: 'sold_qty', width: 100 },
  { title: '营收', key: 'revenue', width: 120, render: row => formatMoney(row.revenue) }
];

async function loadDashboard() {
  loading.value = true;
  try {
    const { data, error } = await fetchDashboard();
    if (!error && data) {
      const d = data as any;
      today.value = d.today || { orders: 0, revenue: 0, paid_orders: 0 };
      last30d.value = d.last30d || { orders: 0, revenue: 0, paid_orders: 0 };
      topProducts.value = (d.top_products || []).slice(0, 5);
      renderTrend(d.trend || []);
    }
  } finally {
    loading.value = false;
  }
}

onMounted(loadDashboard);
</script>

<style scoped></style>
