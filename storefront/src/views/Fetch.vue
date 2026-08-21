<template>
  <div>
    <!-- Hero 搜索区（深蓝渐变） -->
    <section class="query-hero">
      <h1 class="query-title">卡密取货查询</h1>
      <p class="query-sub">输入订单号 或 下单时留的邮箱/手机号，凭查询密码领取卡密</p>

      <form class="query-form" @submit.prevent="fetch">
        <div class="query-input-row">
          <span class="query-icon">🔍</span>
          <input
            v-model="orderNo"
            type="text"
            class="query-input"
            placeholder="订单号 / 邮箱 / 手机号"
            autocomplete="off"
          />
          <button type="submit" class="query-btn" :disabled="loading">
            {{ loading ? '查询中…' : '取货' }}
          </button>
        </div>
        <div class="query-pwd-row">
          <span class="query-pwd-label">查询密码</span>
          <input
            v-model="queryPassword"
            type="password"
            class="query-pwd-input"
            placeholder="下单时设置的查询密码"
          />
        </div>
      </form>

      <div v-if="error" class="query-error">{{ error }}</div>
    </section>

    <!-- 未搜索：三步引导 -->
    <div v-if="!result && !error" class="guide-card">
      <div class="guide-title">三步完成取货</div>
      <div class="guide-steps">
        <div class="guide-step">
          <span class="guide-num">1</span>
          <div>
            <b>下单购买</b>
            <span class="muted">选择商品并完成支付，下单时设置查询密码</span>
          </div>
        </div>
        <div class="guide-step">
          <span class="guide-num">2</span>
          <div>
            <b>输入信息</b>
            <span class="muted">订单号（或下单邮箱/手机号查订单）+ 查询密码</span>
          </div>
        </div>
        <div class="guide-step">
          <span class="guide-num">3</span>
          <div>
            <b>领取卡密</b>
            <span class="muted">复制卡密即可使用，支持一键复制全部</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 联系方式模式：订单列表（逐单取货） -->
    <div v-if="guestOrders.length" class="result-wrap">
      <div class="result-card">
        <div class="result-head">
          <div>
            <div class="result-order">找到 {{ guestOrders.length }} 笔订单</div>
            <div class="result-meta"><span class="muted">输入查询密码后点击对应订单取货</span></div>
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
            {{ loading ? "…" : "取货" }}
          </button>
        </div>
      </div>
    </div>
    <div v-if="listLoading" class="muted" style="text-align: center; padding: 16px;">查询订单中…</div>

    <!-- 取货结果 -->
    <div v-if="result" class="result-wrap">
      <div class="result-card">
        <div class="result-head">
          <div>
            <div class="result-order">订单号：{{ result.order_no }}</div>
            <div class="result-meta">
              <span :class="statusBadge(result.status)">{{ statusText(result.status) }}</span>
              <span class="muted">已取 {{ result.fetch_count || 0 }} 次</span>
            </div>
          </div>
        </div>

        <div v-if="result.items.length" class="card-list">
          <div class="card-list-title">
            <span>卡密列表</span>
            <button v-if="result.items.length > 1" class="copy-all" @click="copyAll">
              {{ copiedAll ? '已复制全部' : '复制全部' }}
            </button>
          </div>
          <div v-for="(it, i) in result.items" :key="it.item_id" class="card-row">
            <span class="card-index">#{{ i + 1 }}</span>
            <div class="card-content">
              <template v-if="it.masked">
                <span class="card-masked">{{ it.content }}</span>
                <span class="card-mask-tip">内容已掩码（仅首次查询可见）</span>
              </template>
              <code v-else class="card-code">{{ it.content }}</code>
            </div>
            <button class="card-copy" @click="copyOne(it.content, i)">{{ copied === i ? '已复制' : '复制' }}</button>
          </div>
        </div>
      </div>

      <div class="result-actions">
        <router-link class="btn btn-outline" to="/member?tab=orders">查看我的订单</router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { fetchDelivery, listGuestOrders, type FetchDeliveryReply, type GuestOrderItem } from '@/api';
import { formatMoney } from '@/api/client';

const route = useRoute();
const orderNo = ref('');
const queryPassword = ref('');
const loading = ref(false);
const error = ref('');
const result = ref<FetchDeliveryReply | null>(null);
const copied = ref<number | null>(null);
const copiedAll = ref(false);
// 联系方式模式：游客按下单邮箱/手机号查订单列表
const guestOrders = ref<GuestOrderItem[]>([]);
const listLoading = ref(false);

/** 输入像联系方式（邮箱含@ / 11 位手机号）→ 列表模式 */
function looksLikeContact(v: string): boolean {
  const t = v.trim();
  return t.includes("@") || /^1\d{10}$/.test(t);
}

// 支付成功/订单列表跳转时预填订单号
onMounted(() => {
  const q = route.query.order_no;
  if (typeof q === 'string' && q) orderNo.value = q;
});

async function fetch() {
  const ident = orderNo.value.trim();
  if (!ident) {
    error.value = '请输入订单号或下单时留的邮箱/手机号';
    return;
  }
  error.value = '';
  result.value = null;
  guestOrders.value = [];

  // 联系方式模式（邮箱/手机号）：先查订单列表，密码逐单验证取货
  if (looksLikeContact(ident)) {
    listLoading.value = true;
    const { data, error: err } = await listGuestOrders(ident);
    listLoading.value = false;
    if (err) { error.value = err; return; }
    guestOrders.value = data?.orders || [];
    if (!guestOrders.value.length) {
      error.value = '未找到用该联系方式下的订单';
    }
    return;
  }

  // 订单号模式：密码 + 直接取货
  if (!queryPassword.value) {
    error.value = '请填写查询密码';
    return;
  }
  await pickOrder(ident);
}

/** 列表/直连取货：订单号 + 查询密码 → 卡密 */
async function pickOrder(no: string) {
  if (!queryPassword.value) {
    error.value = '请填写查询密码';
    return;
  }
  loading.value = true;
  error.value = '';
  result.value = null;
  const { data, error: err } = await fetchDelivery(no, queryPassword.value);
  loading.value = false;
  if (err) { error.value = err; return; }
  result.value = data;
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
  return ts ? new Date(ts * 1000).toLocaleString() : "";
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
</script>

<style scoped>
/* ── Hero 搜索区 ── */
.query-hero {
  background: linear-gradient(135deg, #1d4ed8, #2563eb, #3b82f6);
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
  background: linear-gradient(90deg, #2563eb, #1d4ed8);
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
  background: #2563eb; color: #fff; font-size: 13px; font-weight: 700;
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
  font-size: 13px; color: #2563eb;
}
.copy-all:hover { text-decoration: underline; }

.card-row {
  display: flex; align-items: center; gap: 10px;
  background: #f8fafc; border: 1px solid #f1f5f9; border-radius: 8px;
  padding: 10px 12px; margin-bottom: 8px;
}
.card-row:hover { border-color: rgba(37, 99, 235, 0.3); }
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
  font-size: 12px; color: #2563eb; padding: 4px 8px; border-radius: 6px;
  opacity: 0.55; transition: all 0.15s;
}
.card-row:hover .card-copy { opacity: 1; }
.card-copy:hover { background: #eff6ff; }

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
