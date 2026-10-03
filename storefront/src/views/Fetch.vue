<template>
  <div>
    <!-- Hero 搜索区（深蓝渐变） -->
    <section class="query-hero">
      <h1 class="query-title">{{ $t('订单与交付查询') }}</h1>
      <p class="query-sub">{{ $t('输入订单号查看进度，游客可通过下单邮箱验证找回') }}</p>

      <form class="query-form" @submit.prevent="fetch">
        <div class="query-input-row">
          <span class="query-icon">🔍</span>
          <input
            v-model="orderNo"
            type="text"
            class="query-input"
            :placeholder="$t('订单号 / 邮箱 / 手机号')"
            autocomplete="off"
          />
          <button type="submit" class="query-btn" :disabled="loading">
            {{ loading ? $t('查询中…') : $t('查询') }}
          </button>
        </div>
        <details><summary>{{ $t('虚拟商品或历史订单：使用查询密码') }}</summary><div class="query-pwd-row">
          <span class="query-pwd-label">{{ $t('查询密码') }}</span>
          <input
            v-model="queryPassword"
            type="password"
            class="query-pwd-input"
            :placeholder="$t('下单时设置的查询密码')"
          />
        </div></details>
      </form>

<OrderAccessRecovery :order-no="looksLikeContact(orderNo) ? undefined : orderNo" @recovered="pickOrder" />
      <p v-if="queryPassword" class="query-sub">{{ $t('同一网络下，同一订单连续查询失败 5 次将锁定 30 分钟；验证成功后重新计数。') }}</p>
      <div v-if="error" class="query-error" role="alert">{{ uiText(error) }}</div>
    </section>

    <!-- 未搜索：三步引导 -->
    <div v-if="!result && !error" class="guide-card">
      <div class="guide-title">{{ $t('三步查询订单') }}</div>
      <div class="guide-steps">
        <div class="guide-step">
          <span class="guide-num">1</span>
          <div>
            <b>{{ $t('下单购买') }}</b>
            <span class="muted">{{ $t('选择商品并完成支付，保存订单信息') }}</span>
          </div>
        </div>
        <div class="guide-step">
          <span class="guide-num">2</span>
          <div>
            <b>{{ $t('输入信息') }}</b>
            <span class="muted">{{ $t('使用账户、订单访问链接或邮箱验证码') }}</span>
          </div>
        </div>
        <div class="guide-step">
          <span class="guide-num">3</span>
          <div>
            <b>{{ $t('查看交付结果') }}</b>
            <span class="muted">{{ $t('查看卡密、服务进度或实体商品物流') }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 联系方式模式：订单列表（逐单取货） -->
    <div v-if="guestOrders.length" class="result-wrap">
      <div class="result-card">
        <div class="result-head">
          <div>
            <div class="result-order">{{ $t('找到') }} {{ guestOrders.length }} {{ $t('笔订单') }}</div>
            <div class="result-meta"><span class="muted">{{ $t('输入查询密码后点击对应订单查看') }}</span></div>
          </div>
        </div>
        <div v-for="o in guestOrders" :key="o.order_no" class="guest-order-row">
          <div class="guest-order-info">
            <span class="pay-mono">{{ o.order_no }}</span>
            <span :class="statusBadge(o.status)">{{ statusText(o.status) }}</span>
          </div>
          <span class="guest-order-amount">{{ formatMoney(o.total_cents) }}</span>
          <span class="muted guest-order-time">{{ fmtTime(o.created_at) }}</span>
          <button class="btn btn-primary guest-order-btn" :disabled="loading" @click="pickOrder(o.order_no)">
            {{ loading ? "…" : $t('查看订单') }}
          </button>
        </div>
      </div>
    </div>
    <div v-if="listLoading" class="muted" style="text-align: center; padding: 16px;">{{ $t('查询订单中…') }}</div>

    <!-- 取货结果 -->
    <div v-if="result" class="result-wrap">
      <div class="result-card">
        <div class="result-head">
          <div>
            <div class="result-order">{{ $t('订单号：') }}{{ result.order_no }}</div>
            <div class="result-meta">
              <span :class="statusBadge(result.status)">{{ statusText(result.status) }}</span>
              <span v-if="!physicalOnly" class="muted">{{ $t('已取') }} {{ result.fetch_count || 0 }} {{ $t('次') }}</span>
            </div>
          </div>
        </div>

        <ShippingDetails v-if="shippingOrder" :order="shippingOrder" :password="queryPassword" @refresh="pickOrder(result.order_no)" />
        <div v-if="physicalOnly" class="card-list">{{ result.status === 'pending_payment' ? $t('订单尚未付款，请在订单详情中继续支付。') : ['canceled', 'expired', 'refunded'].includes(result.status) ? $t('订单已关闭或退款，请查看上方配送及取消记录。') : $t('此订单通过快递配送，请查看上方包裹进度。') }}</div>
        <div v-else-if="['paid', 'fulfilling', 'partially_delivered'].includes(result.status)" class="card-list">
          <p>{{ result.items.length ? $t('已付款，部分商品已发货，其余商品正在安排发货。无需再次支付，也无需注册；稍后用此订单号和查询密码刷新取货。') : $t('已付款，商品正在安排发货。无需再次支付，也无需注册；稍后用此订单号和查询密码刷新取货。') }}</p>
          <p>{{ $t('人工服务请在订单详情查看处理进度，需帮助时凭订单号联系客服。') }}</p>
          <button class="btn btn-primary" :disabled="loading" @click="pickOrder(result.order_no)">{{ $t('刷新发货结果') }}</button>
        </div>
        <div v-else-if="result.status === 'pending_payment'" class="card-list">
          <p>{{ $t('订单尚未付款，付款后即可查看交付进度。') }}</p>
          <router-link class="btn btn-primary" :to="`/payment/${result.order_no}`">{{ $t('继续支付') }}</router-link>
        </div>
        <div v-else-if="['canceled', 'expired'].includes(result.status)" class="card-list">{{ $t('订单已关闭，无法继续付款，请重新选购。') }}</div>
        <div v-else-if="!result.items.length" class="card-list">{{ result.status === 'refunded' ? $t('订单已退款，请核对退款记录。') : $t('暂无可领取内容，请查看订单状态或联系客服。') }}</div>
        <DeliveryResults v-if="result.items.length" :items="result.items" />
      </div>

      <div class="result-actions">
        <router-link class="btn btn-outline" :to="`/order/${result.order_no}`">{{ $t('查看订单详情') }}</router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import OrderAccessRecovery from '@/components/OrderAccessRecovery.vue';
import { uiText, t as $t, localeTag } from '@/i18n';

import ShippingDetails from '@/components/ShippingDetails.vue';
import DeliveryResults from '@/components/DeliveryResults.vue';
import { ref, onMounted, computed } from 'vue';
import { useRoute } from 'vue-router';
import { getOrder, getOrderPassword, getOrderAccessToken, consumeOrderAccessLink, rememberOrderPassword, fetchDelivery, listGuestOrders, type FetchDeliveryReply, type GuestOrderItem } from '@/api';
import { getToken, formatMoney } from '@/api/client';
import { looksLikeContact } from '@/utils/commerce';

const route = useRoute();
const orderNo = ref('');
const queryPassword = ref('');
const loading = ref(false);
const error = ref('');
const result = ref<FetchDeliveryReply | null>(null);
const shippingOrder = ref<any>(null);
const physicalOnly = computed(() => shippingOrder.value?.items?.length > 0 && shippingOrder.value.items.every((it: any) => it.goods_type === 'physical'));
const copied = ref<number | null>(null);
const copiedAll = ref(false);
// 联系方式模式：游客按下单邮箱/手机号查订单列表
const guestOrders = ref<GuestOrderItem[]>([]);
const listLoading = ref(false);

// 支付成功/订单列表跳转时预填订单号
onMounted(() => {
  const q = route.query.order_no;
  if (typeof q === 'string' && q) { consumeOrderAccessLink(q); orderNo.value = q; queryPassword.value = getOrderPassword(q); if (queryPassword.value || getToken() || getOrderAccessToken(q)) pickOrder(q); }
});

async function fetch() {
  const ident = orderNo.value.trim();
  if (!ident) {
    error.value = $t("请输入订单号或下单时留的邮箱/手机号");
    return;
  }
  error.value = '';
  result.value = null;
  shippingOrder.value = null;
  guestOrders.value = [];

  // 联系方式模式（邮箱/手机号）：先查订单列表，密码逐单验证取货
  if (looksLikeContact(ident)) {
    listLoading.value = true;
    const { data, error: err } = await listGuestOrders(ident);
    listLoading.value = false;
    if (err) { error.value = err; return; }
    guestOrders.value = data?.orders || [];
    if (!guestOrders.value.length) {
      error.value = $t("未找到用该联系方式下的订单");
    }
    return;
  }

  // 订单号模式：密码 + 直接取货
  if (!queryPassword.value && !getToken() && !getOrderAccessToken(ident)) {
    error.value = $t("请验证下单邮箱或使用订单访问链接");
    return;
  }
  await pickOrder(ident);
}

/** 列表/直连取货：订单号 + 查询密码 → 卡密 */
async function pickOrder(no: string) {
  if (!queryPassword.value && !getToken() && !getOrderAccessToken(no)) {
    error.value = $t("请验证下单邮箱或使用订单访问链接");
    return;
  }
  loading.value = true;
  error.value = '';
  result.value = null;
  shippingOrder.value = null;
  const detail = await getOrder(no, queryPassword.value);
  if (detail.error || !detail.data) { loading.value=false; error.value=detail.error || $t('订单不存在或无权查看'); return; }
  if (detail.data.commerce_version === 1) shippingOrder.value=detail.data;
  if (detail.data.items?.length && detail.data.items.every(i=>i.goods_type==='physical')) {
    result.value={order_no:no,status:detail.data.status,items:[],fetch_count:0};
  } else {
    const { data, error: err }=await fetchDelivery(no,queryPassword.value);
    if(err){loading.value=false;error.value=err;return;}
    result.value=data;
    if(data)rememberOrderPassword(no,queryPassword.value);
  }
  loading.value=false;
  guestOrders.value = []; // 取货成功收起列表
  orderNo.value = no;     // 结果区显示该单号
}

// 复制（navigator.clipboard；按钮文案切换反馈）
async function copyOne(content: string, index: number) {
  try {
    await navigator.clipboard.writeText(content);
    copied.value = index;
    setTimeout(() => { if (copied.value === index) copied.value = null; }, 1500);
  } catch { /* 剪贴板不可用时忽略 */ }
}
async function copyAll() {
  if (!result.value) return;
  try {
    await navigator.clipboard.writeText(result.value.items.map((i) => i.content).join('\n'));
    copiedAll.value = true;
    setTimeout(() => (copiedAll.value = false), 1500);
  } catch { /* 忽略 */ }
}

function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString(localeTag.value) : "";
}

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
</script>

<style scoped>
/* ── Hero 搜索区 ── */
.query-hero {
  background: linear-gradient(135deg, var(--zc-primary-hover), var(--zc-primary), var(--zc-primary-bright));
  border-radius: 14px;
  padding: 36px 24px 40px;
  color: #fff;
  text-align: center;
  margin-bottom: 16px;
}
.query-title { font-size: 26px; font-weight: 800; letter-spacing: 0.5px; }
.query-sub { margin-top: 6px; font-size: 14px; opacity: 0.85; }

.query-form { max-width: 560px; margin: 20px auto 0; }
.query-input-row {
  display: flex; align-items: center; gap: 8px;
  background: #fff; border-radius: 14px; padding: 6px 6px 6px 14px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.15);
}
.query-icon { font-size: 16px; }
.query-input {
  flex: 1; min-width: 0; border: none; outline: none;
  padding: 10px 6px; font-size: 14px; color: #1f2329;
  background: transparent;
}
.query-btn {
  border: none; cursor: pointer;
  background: linear-gradient(90deg, var(--zc-primary), var(--zc-primary-hover));
  color: #fff; font-weight: 600; font-size: 14px;
  padding: 10px 22px; border-radius: 10px;
  transition: all 0.15s; white-space: nowrap;
}
.query-btn:hover:not(:disabled) { box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2); }
.query-btn:disabled { opacity: 0.6; cursor: not-allowed; }

.query-pwd-row {
  display: flex; align-items: center; gap: 8px;
  background: #fff; border-radius: 999px;
  padding: 6px 16px; margin: 10px auto 0;
  max-width: 360px; box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
}
.query-pwd-label { font-size: 13px; font-weight: 600; color: #1f2329; white-space: nowrap; }
.query-pwd-input {
  flex: 1; min-width: 0; border: none; outline: none;
  padding: 8px 4px; font-size: 14px; background: transparent; color: #1f2329;
}

.query-error {
  display: inline-block;
  margin-top: 12px; padding: 5px 16px;
  background: rgba(239, 68, 68, 0.9); color: #fff;
  font-size: 13px; border-radius: 999px;
}

/* ── 引导卡 ── */
.guide-card {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 24px;
}
.guide-title { font-size: 16px; font-weight: 700; margin-bottom: 18px; }
.guide-steps { display: flex; flex-direction: column; gap: 16px; }
.guide-step { display: flex; gap: 12px; align-items: flex-start; }
.guide-num {
  width: 26px; height: 26px; border-radius: 999px; flex-shrink: 0;
  background: var(--zc-primary); color: #fff; font-size: 13px; font-weight: 700;
  display: flex; align-items: center; justify-content: center;
}
.guide-step b { display: block; font-size: 14px; margin-bottom: 2px; }

/* ── 结果区 ── */
.result-wrap { display: flex; flex-direction: column; gap: 14px; }
.result-card {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; overflow: hidden;
}
.result-head { padding: 16px 18px; border-bottom: 1px solid #f3f4f6; }
.result-order { font-size: 14px; font-weight: 600; color: #1f2329; margin-bottom: 8px; word-break: break-all; }
.result-meta { display: flex; align-items: center; gap: 10px; }

.card-list { padding: 14px 18px 18px; }
.card-list-title {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 14px; font-weight: 700; margin-bottom: 10px;
}
.copy-all {
  border: none; background: none; cursor: pointer;
  font-size: 13px; color: var(--zc-primary);
}
.copy-all:hover { text-decoration: underline; }

.card-row {
  display: flex; align-items: center; gap: 10px;
  background: #f8fafc; border: 1px solid #f1f5f9; border-radius: 8px;
  padding: 10px 12px; margin-bottom: 8px;
}
.card-row:hover { border-color: color-mix(in srgb, var(--zc-primary) 30%, transparent); }
.card-index { font-size: 11px; color: #9ca3af; width: 22px; flex-shrink: 0; }
.card-content { flex: 1; min-width: 0; }
.card-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px; color: #1f2329; word-break: break-all; user-select: all;
}
.card-masked { font-size: 13px; color: #6b7280; word-break: break-all; }
.card-mask-tip { display: block; font-size: 11px; color: #f59e0b; margin-top: 2px; }
.card-copy {
  border: none; background: none; cursor: pointer; flex-shrink: 0;
  font-size: 12px; color: var(--zc-primary); padding: 4px 8px; border-radius: 6px;
  opacity: 0.55; transition: all 0.15s;
}
.card-row:hover .card-copy { opacity: 1; }
.card-copy:hover { background: var(--zc-primary-soft); }

.result-actions { display: flex; justify-content: center; }
</style>

<style scoped>
.guest-order-row {
  display: flex; align-items: center; gap: 12px;
  padding: 12px 16px; border-top: 1px solid #f3f4f6; flex-wrap: wrap;
}
.guest-order-info { display: flex; gap: 8px; align-items: center; flex: 1; min-width: 220px; }
.guest-order-amount { font-weight: 700; color: #ff5722; }
.guest-order-time { font-size: 12px; }
.guest-order-btn { padding: 6px 16px; }
</style>
