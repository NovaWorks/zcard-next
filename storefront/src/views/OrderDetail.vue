<template>
  <div class="order-detail">
    <!-- 页头：返回 + 标题 -->
    <div class="od-head">
      <router-link class="od-back" :to="isLoggedIn ? '/member?tab=orders' : '/fetch'">← {{ isLoggedIn ? $t('返回订单列表') : $t('取货查询') }}</router-link>
      <h2 class="od-title">{{ $t('订单详情') }}</h2>
    </div>

    <div v-if="loading" class="card muted" style="padding: 48px; text-align: center;">{{ $t('加载中…') }}</div>
    <div v-else-if="error" class="card" style="padding: 24px;">
      <p class="error">{{ uiText(error) }}</p>
      <OrderAccessRecovery :order-no="orderNo" @recovered="loadOrder" />
      <details><summary>{{ $t('虚拟商品或历史订单：使用查询密码') }}</summary><form @submit.prevent="loadOrder"><label>{{ $t('下单时的查询密码') }}<input v-model="password" class="input" type="password" autocomplete="off" /></label><button class="btn" type="submit">{{ $t('查询订单') }}</button></form></details>
    </div>

    <template v-else-if="order">
      <!-- 状态条：状态徽章 + 订单号 + 下单时间（铺满全宽） -->
      <div class="card od-status-bar">
        <span :class="statusBadge(order.status)" class="od-status-badge">{{ statusText(order.status) }}</span>
        <div class="od-status-meta">
          <div class="od-order-no">{{ $t('订单号：') }}{{ order.order_no }}</div>
          <div class="muted">{{ $t('下单时间：') }}{{ fmtTime(order.created_at) }}</div>
        </div>
      </div>

      <div v-if="!order.items.some(i=>i.delivery_kind==='sms_activation') && ['paid', 'fulfilling', 'partially_delivered'].includes(order.status)" class="card">
        <p>{{ order.status === 'partially_delivered' ? $t('已付款，部分商品已发货，其余商品正在安排发货。请勿重复付款；长时间未发货请凭订单号联系客服。') : $t('已付款，商品正在安排发货。请勿重复付款；长时间未发货请凭订单号联系客服。') }}</p>
        <button class="btn secondary" @click="loadOrder">{{ $t('刷新订单状态') }}</button>
      </div>
      <SMSOrder v-if="order.items.some(i=>i.delivery_kind==='sms_activation') && !['pending_payment','canceled','expired'].includes(order.status)" :order-no="order.order_no" :metadata="order.items.find(i=>i.delivery_kind==='sms_activation')?.sms_product" />
      <ShippingDetails v-if="Number(order.commerce_version)===1" :order="order" :password="password" @refresh="loadOrder" />
      <div class="od-body">
        <!-- 左列：商品清单（grid 行式：PC 四列对齐表头；移动端每行两行块状——大厂订单详情同构） -->
        <div class="card od-items">
          <div class="od-section-title">{{ $t('商品清单（{0}）', [order.items.length]) }}</div>
          <div class="od-item-list">
            <div class="od-item-head">
              <span>{{ $t('商品') }}</span><span class="od-ta-r">{{ $t('单价') }}</span><span class="od-ta-c">{{ $t('数量') }}</span><span class="od-ta-r">{{ $t('小计') }}</span>
            </div>
            <div v-for="(it, i) in order.items" :key="i" class="od-item">
              <div class="od-item-name">{{ it.product_name }}
                <p class="muted">{{it.goods_type === 'physical' ? $t('快递配送') : it.fulfillment_type === 'manual' ? $t('人工服务') : it.fulfillment_type === 'upstream' ? $t('上游交付') : $t('自动交付')}} · {{itemStatus(it.fulfillment_status)}}</p>
                <p v-if="it.goods_type === 'physical'" class="muted">{{ $t('已发') }} {{ it.shipped_quantity || 0 }} {{ $t('件 · 已收') }} {{ it.received_quantity || 0 }} {{ $t('件') }}<span v-if="it.canceled_quantity"> {{ $t('· 已取消') }} {{ it.canceled_quantity }} {{ $t('件') }}</span><span v-if="it.returned_quantity"> {{ $t('· 已退货') }} {{ it.returned_quantity }} {{ $t('件') }}</span></p>
                <p v-else-if="it.canceled_quantity" class="muted">{{ $t('已取消') }} {{ it.canceled_quantity }} {{ $t('件') }}</p>
                <dl v-if="answers(it.form_answers_json).length" class="od-answers"><div v-for="(a,j) in answers(it.form_answers_json)" :key="j"><dt>{{a.name}}</dt><dd>{{a.value}}</dd></div></dl>
              </div>
              <div class="od-item-price">{{ formatMoney(it.unit_price_cents) }}</div>
              <div class="od-item-qty">{{ $t('购买 ×') }}{{ it.quantity }}</div>
              <div class="od-item-sub">{{ formatMoney(Number(order.commerce_version)===1?it.paid_cents || 0:it.amount_cents ?? it.unit_price_cents * it.quantity) }}</div>
            </div>
          </div>
        </div>

        <!-- 右列：订单摘要（金额 + 操作） -->
        <div class="od-side">
          <div class="card">
            <div class="od-section-title">{{ $t('订单金额') }}</div>
            <div class="od-amount-row">
              <span>{{order.status === 'pending_payment' ? $t('订单应付') : ['canceled','expired'].includes(order.status) ? $t('订单金额') : $t('实付合计')}}</span>
              <b class="od-amount">{{ formatMoney(order.paid_total_cents || order.total_cents) }}</b>
              <span v-if="Number(order.paid_fee_cents) > 0" class="muted">{{ $t('含手续费') }} {{ formatMoney(order.paid_fee_cents || 0) }}</span>
            </div>
            <p v-if="Number(order.refunded_cents) > 0 || Number(order.refunded_fee_cents) > 0" class="muted">{{ $t('已退款') }} {{ formatMoney(Number(order.refunded_cents || 0) + Number(order.refunded_fee_cents || 0)) }}{{ $t('（含已退手续费') }} {{formatMoney(order.refunded_fee_cents || 0)}}）</p>
            <p class="muted">{{ $t('商品小计为下单优惠后的金额；手续费与整单优惠以订单金额为准。') }}</p>
          </div>
          <div v-if="!order.items.some(i=>i.delivery_kind==='sms_activation') || order.status==='pending_payment'" class="card">
            <div class="od-section-title">{{ $t('可用操作') }}</div>
            <div class="od-actions">
              <router-link class="btn od-action-btn" :to="`/payment/${order.order_no}`" v-if="order.status === 'pending_payment'">{{ $t('去支付') }}</router-link>
              <router-link
                class="btn secondary od-action-btn"
                :to="`/fetch?order_no=${order.order_no}`"
                v-if="order.items.some(it=>it.goods_type!=='physical' && it.delivery_kind!=='sms_activation') && ['paid', 'fulfilling', 'partially_delivered', 'delivered', 'completed'].includes(order.status)"
              >{{ $t('查看交付结果') }}</router-link>
              <button class="btn secondary od-action-btn" v-if="isLoggedIn && order.status === 'pending_payment'" @click="cancelOrder">{{ $t('取消订单') }}</button>
            </div>
          </div>
        </div>
      </div>
      <div v-if="order.items.every(i=>i.goods_type==='physical') && getOrderAccessToken(orderNo)" class="card"><p>{{ $t('请保存订单访问链接，换设备可通过邮箱验证找回') }}</p><button class="btn secondary" @click="saveAccessLink">{{ copiedLink ? $t('已复制') : $t('复制订单访问链接') }}</button><p v-if="linkError" class="error" role="alert">{{ linkError }}</p></div>
      <OrderReview v-if="isLoggedIn" :order-no="orderNo" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { uiText, t as $t, localeTag } from '@/i18n';

import OrderAccessRecovery from '@/components/OrderAccessRecovery.vue';
import SMSOrder from '@/components/SMSOrder.vue';
import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { getOrder, getOrderPassword, getOrderAccessToken, orderAccessLink, rememberOrderPassword, cancelMyOrder, type OrderDetail } from '@/api';
import ShippingDetails from "@/components/ShippingDetails.vue";
import OrderReview from '@/components/OrderReview.vue';
import { getToken, formatMoney } from '@/api/client';

const route = useRoute();
const orderNo = String(route.params.orderNo || '');
const password = ref(getOrderPassword(orderNo));
const isLoggedIn = !!getToken();
const loading = ref(false);
const error = ref('');
const order = ref<OrderDetail | null>(null);
const copiedLink = ref(false); const linkError=ref('');
async function saveAccessLink(){try{await navigator.clipboard.writeText(orderAccessLink(orderNo)); copiedLink.value=true;}catch{linkError.value=$t('无法复制链接，请允许剪贴板权限后重试');}}

onMounted(loadOrder);
async function loadOrder() {
  loading.value = true;
  const { data, error: err } = await getOrder(orderNo, password.value || undefined).catch(() => ({ data: null, get error() { return $t("订单不存在或无权查看"); } }));
  loading.value = false;
  if (err) { error.value = err; return; }
  order.value = data;
  error.value = '';
  if (data) rememberOrderPassword(orderNo, password.value);
}

async function cancelOrder() {
  if (!confirm($t("确认取消订单 {0}？", [orderNo]))) return;
  const { error: err } = await cancelMyOrder(orderNo);
  if (err) { error.value = err; return; }
  // 重新加载：状态变 canceled 后按钮消失
  const { data } = await getOrder(orderNo, password.value || undefined).catch(() => ({ data: null }));
  order.value = data;
}

function answers(raw?:string):{name:string;value:string}[]{try{const x=JSON.parse(raw || '[]');return Array.isArray(x)?x:[]}catch{return []}}
function itemStatus(s?:string){return ({get shipped() { return $t("已发货"); },get received() { return $t("已收货"); },get pending() { return $t("待处理"); },get delivering() { return $t("处理中"); },get manual() { return $t("待人工核对"); },get failed() { return $t("处理异常"); },get delivered() { return $t("已完成交付"); },get refunded() { return $t("已退款"); }} as Record<string,string>)[s || 'pending'] || s;}
function statusText(s: string): string {
  return ({
    get pending_payment() { return $t("待支付"); }, get paid() { return $t("已支付"); }, get fulfilling() { return $t("履约中"); }, get partially_delivered() { return $t("部分发货"); },
    get delivered() { return $t("已发货"); }, get completed() { return $t("已完成"); }, get canceled() { return $t("已取消"); }, get expired() { return $t("已过期"); },
    get refund_pending() { return $t("退款中"); }, get refunded() { return $t("已退款"); }, get manual_pending() { return $t("待人工发货"); },
  } as Record<string, string>)[s] || s;
}
function statusBadge(s: string): string {
  return ({
    pending_payment: 'badge orange', paid: 'badge blue', fulfilling: 'badge blue', partially_delivered: 'badge green',
    delivered: 'badge green', completed: 'badge green', canceled: 'badge gray', expired: 'badge gray',
    refund_pending: 'badge orange', refunded: 'badge red', manual_pending: 'badge orange',
  } as Record<string, string>)[s] || 'badge gray';
}
function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString(localeTag.value) : '';
}
</script>

<style scoped>
.od-answers{margin:8px 0;font-size:13px}.od-answers>div{display:flex;flex-wrap:wrap;gap:6px;margin-top:6px}.od-answers dt{font-weight:600}.od-answers dd{margin:0;overflow-wrap:anywhere;white-space:pre-wrap;min-width:0}
.order-detail { display: flex; flex-direction: column; gap: 16px; }

/* 页头 */
.od-head { display: flex; align-items: center; gap: 14px; }
.od-back { font-size: 13px; color: #6b7280; text-decoration: none; padding: 4px 8px; border-radius: 6px; transition: all 0.15s; }
.od-back:hover { color: var(--zc-primary); background: var(--zc-primary-soft); }
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
