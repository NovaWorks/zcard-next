<template>
  <div class="order-detail">
    <!-- 页头：返回 + 标题 -->
    <div class="od-head">
      <router-link class="od-back" to="/member?tab=orders">← 返回订单列表</router-link>
      <h2 class="od-title">订单详情</h2>
    </div>

    <div v-if="loading" class="card muted" style="padding: 48px; text-align: center;">加载中…</div>
    <div v-else-if="error" class="card error" style="padding: 48px; text-align: center;">{{ error }}</div>

    <template v-else-if="order">
      <!-- 状态条：状态徽章 + 订单号 + 下单时间（铺满全宽） -->
      <div class="card od-status-bar">
        <span :class="statusBadge(order.status)" class="od-status-badge">{{ statusText(order.status) }}</span>
        <div class="od-status-meta">
          <div class="od-order-no">订单号：{{ order.order_no }}</div>
          <div class="muted">下单时间：{{ fmtTime(order.created_at) }}</div>
        </div>
      </div>

      <div class="od-body">
        <!-- 左列：商品清单（grid 行式：PC 四列对齐表头；移动端每行两行块状——大厂订单详情同构） -->
        <div class="card od-items">
          <div class="od-section-title">商品清单（{{ order.items.length }}）</div>
          <div class="od-item-list">
            <div class="od-item-head">
              <span>商品</span><span class="od-ta-r">单价</span><span class="od-ta-c">数量</span><span class="od-ta-r">小计</span>
            </div>
            <div v-for="(it, i) in order.items" :key="i" class="od-item">
              <div class="od-item-name">{{ it.product_name }}</div>
              <div class="od-item-price">{{ formatMoney(it.unit_price_cents) }}</div>
              <div class="od-item-qty">×{{ it.quantity }}</div>
              <div class="od-item-sub">{{ formatMoney(it.unit_price_cents * it.quantity) }}</div>
            </div>
          </div>
        </div>

        <!-- 右列：订单摘要（金额 + 操作） -->
        <div class="od-side">
          <div class="card">
            <div class="od-section-title">订单金额</div>
            <div class="od-amount-row">
              <span>实付合计</span>
              <b class="od-amount">{{ formatMoney(order.total_cents) }}</b>
            </div>
          </div>
          <div class="card">
            <div class="od-section-title">可用操作</div>
            <div class="od-actions">
              <router-link class="btn od-action-btn" :to="`/payment/${order.order_no}`" v-if="order.status === 'pending_payment'">去支付</router-link>
              <router-link
                class="btn secondary od-action-btn"
                :to="`/fetch?order_no=${order.order_no}`"
                v-if="['paid', 'fulfilling', 'partially_delivered', 'delivered', 'completed'].includes(order.status)"
              >取货</router-link>
              <button class="btn secondary od-action-btn" v-if="order.status === 'pending_payment'" @click="cancelOrder">取消订单</button>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { getOrder, cancelMyOrder, type OrderDetail } from '@/api';
import { formatMoney } from '@/api/client';

const route = useRoute();
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

<style scoped>
.order-detail { display: flex; flex-direction: column; gap: 16px; }

/* 页头 */
.od-head { display: flex; align-items: center; gap: 14px; }
.od-back { font-size: 13px; color: #6b7280; text-decoration: none; padding: 4px 8px; border-radius: 6px; transition: all 0.15s; }
.od-back:hover { color: #2563eb; background: #eff6ff; }
.od-title { font-size: 20px; margin: 0; color: #111827; }

/* 状态条：全宽卡片 */
.od-status-bar { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
.od-status-badge { font-size: 13px; padding: 3px 12px; }
.od-status-meta { display: flex; flex-direction: column; gap: 2px; }
.od-order-no { font-size: 14px; font-weight: 600; color: #1f2329; word-break: break-all; }

/* 主体：左清单 + 右摘要（大厂订单详情经典两栏；窄屏堆叠） */
.od-body { display: flex; gap: 16px; align-items: flex-start; }
.od-items { flex: 1; min-width: 0; }
.od-side { width: 280px; flex-shrink: 0; display: flex; flex-direction: column; gap: 16px; }
@media (max-width: 860px) {
  .od-body { flex-direction: column; }
  .od-side { width: 100%; order: -1; } /* 摘要+操作置顶：先看到金额与操作再看清单（大厂移动端订单详情） */
}

.od-section-title { font-size: 14px; font-weight: 700; color: #111827; margin-bottom: 12px; }

/* 商品清单：行式 grid（PC 四列带表头；表头列宽与商品行同模板保证对齐） */
.od-item-head, .od-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 96px 64px 104px;
  gap: 8px; align-items: center;
}
.od-item-head {
  font-size: 12px; color: #6b7280;
  padding: 8px 12px; background: #f9fafb; border-radius: 8px 8px 0 0;
}
.od-item { padding: 14px 12px; border-bottom: 1px solid #f3f4f6; }
.od-item:last-child { border-bottom: none; }
.od-item-name { font-size: 14px; font-weight: 500; color: #1f2329; line-height: 1.5; min-width: 0; }
.od-item-price { text-align: right; color: #6b7280; font-size: 13px; font-variant-numeric: tabular-nums; }
.od-item-qty { text-align: center; color: #6b7280; font-size: 13px; }
.od-item-sub {
  text-align: right; font-weight: 700; color: #111827; font-size: 15px;
  font-variant-numeric: tabular-nums;
}
.od-ta-r { text-align: right; }
.od-ta-c { text-align: center; }

/* 移动端：表头隐藏，每行两行块状（名称+数量 / 单价+小计——大厂订单详情商品行） */
@media (max-width: 768px) {
  .od-item-head { display: none; }
  .od-item {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas:
      "name qty"
      "price sub";
    row-gap: 6px; padding: 12px 2px;
  }
  .od-item-name { grid-area: name; }
  .od-item-qty { grid-area: qty; text-align: right; color: #9ca3af; }
  .od-item-price { grid-area: price; text-align: left; }
  .od-item-sub { grid-area: sub; }
}

/* 摘要卡 */
.od-amount-row {
  display: flex; align-items: baseline; justify-content: space-between;
  padding: 4px 0 8px; font-size: 13px; color: #6b7280;
}
.od-amount { font-size: 24px; font-weight: 800; color: #ff5722; font-variant-numeric: tabular-nums; }
.od-actions { display: flex; flex-direction: column; gap: 10px; }
.od-action-btn { text-align: center; }
</style>
