<template>
  <div class="card" style="max-width: 640px;">
    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px;">
      <h2 style="margin: 0;">订单详情</h2>
      <router-link class="btn secondary" to="/member?tab=orders">返回订单列表</router-link>
    </div>

    <div v-if="loading" class="muted">加载中…</div>
    <div v-else-if="error" class="error">{{ error }}</div>

    <template v-else-if="order">
      <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 16px;">
        <span :class="statusBadge(order.status)">{{ statusText(order.status) }}</span>
        <span class="muted">订单号：{{ order.order_no }}</span>
      </div>

      <table class="list" style="margin-bottom: 16px;">
        <thead><tr><th>商品</th><th>单价</th><th>数量</th><th>小计</th></tr></thead>
        <tbody>
          <tr v-for="(it, i) in order.items" :key="i">
            <td>{{ it.product_name }}</td>
            <td class="price">{{ formatMoney(it.unit_price_cents) }}</td>
            <td>{{ it.quantity }}</td>
            <td class="price">{{ formatMoney(it.unit_price_cents * it.quantity) }}</td>
          </tr>
        </tbody>
      </table>

      <div style="display: flex; justify-content: flex-end; margin-bottom: 16px;">
        <div>合计：<b>{{ formatMoney(order.total_cents) }}</b></div>
      </div>

      <div class="muted" style="margin-bottom: 16px;">下单时间：{{ fmtTime(order.created_at) }}</div>

      <div class="actions">
        <router-link class="btn" :to="`/payment/${order.order_no}`" v-if="order.status === 'pending_payment'">去支付</router-link>
        <router-link
          class="btn secondary"
          :to="`/fetch?order_no=${order.order_no}`"
          v-if="['paid', 'fulfilling', 'partially_delivered', 'delivered', 'completed'].includes(order.status)"
        >取货</router-link>
        <button class="btn secondary" v-if="order.status === 'pending_payment'" @click="cancelOrder">取消订单</button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getOrder, cancelMyOrder, type OrderDetail } from '@/api';
import { formatMoney } from '@/api/client';

const route = useRoute();
const router = useRouter();
const orderNo = String(route.params.orderNo || '');
const loading = ref(false);
const error = ref('');
const order = ref<OrderDetail | null>(null);

onMounted(async () => {
  loading.value = true;
  const { data, error: err } = await getOrder(orderNo).catch(() => ({ data: null, error: '订单不存在或无权查看' }));
  loading.value = false;
  if (err) { error.value = err; return; }
  order.value = data;
});

async function cancelOrder() {
  if (!confirm(`确认取消订单 ${orderNo}？`)) return;
  const { error: err } = await cancelMyOrder(orderNo);
  if (err) { error.value = err; return; }
  // 重新加载：状态变 canceled 后按钮消失
  const { data } = await getOrder(orderNo).catch(() => ({ data: null }));
  order.value = data;
}

function statusText(s: string): string {
  return ({
    pending_payment: '待支付', paid: '已支付', fulfilling: '履约中', partially_delivered: '部分发货',
    delivered: '已发货', completed: '已完成', canceled: '已取消', expired: '已过期',
    refund_pending: '退款中', refunded: '已退款',
  } as Record<string, string>)[s] || s;
}
function statusBadge(s: string): string {
  return ({
    pending_payment: 'badge orange', paid: 'badge blue', fulfilling: 'badge blue', partially_delivered: 'badge green',
    delivered: 'badge green', completed: 'badge green', canceled: 'badge gray', expired: 'badge gray',
    refund_pending: 'badge orange', refunded: 'badge red',
  } as Record<string, string>)[s] || 'badge gray';
}
function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '';
}
</script>
