<template>
  <div class="pay-page">
    <!-- 加载态 -->
    <div v-if="phase === 'loading'" class="pay-center-card">
      <div class="pay-loading-icon pulse">⏳</div>
      <div class="pay-loading-title">正在加载订单…</div>
    </div>

    <!-- 已取消 / 已过期 -->
    <div v-else-if="phase === 'closed'" class="pay-center-card">
      <div class="pay-state-icon gray">{{ order?.status === 'canceled' ? '🚫' : '⏰' }}</div>
      <div class="pay-state-title">{{ order?.status === 'canceled' ? '订单已取消' : '订单已超时' }}</div>
      <div class="muted">订单号：{{ orderNo }} · 未完成支付</div>
      <div class="pay-btn-row">
        <router-link class="btn btn-primary" to="/products">重新选购</router-link>
        <router-link class="btn btn-outline" to="/member?tab=orders">我的订单</router-link>
      </div>
    </div>

    <!-- 支付成功 🎉 -->
    <div v-else-if="phase === 'success'" class="pay-center-card">
      <div class="pay-state-icon green pop">✓</div>
      <div class="pay-state-title">支付成功</div>
      <div class="muted" style="margin-bottom: 4px;">订单号：{{ orderNo }}</div>
      <div style="margin-bottom: 6px;">支付金额 <b class="pay-amount">{{ formatMoney(order?.total_cents || 0) }}</b></div>

      <!-- 自动取货：卡密直接展示（会话内记忆查询密码；失败降级提示去取货页） -->
      <div v-if="delivery" class="pay-cards">
        <div class="pay-cards-head">
          <span>您的卡密（{{ delivery.items.length }} 条）</span>
          <button v-if="delivery.items.length > 1" class="pay-copy-all" @click="copyAll">
            {{ copiedAll ? '已复制全部' : '复制全部' }}
          </button>
        </div>
        <div v-for="(it, i) in delivery.items" :key="it.item_id" class="pay-card-row">
          <span class="pay-card-index">#{{ i + 1 }}</span>
          <code class="pay-card-code">{{ it.content }}</code>
          <button class="pay-card-copy" @click="copyOne(it.content, i)">{{ copied === i ? '已复制' : '复制' }}</button>
        </div>
      </div>
      <div v-else class="pay-fetch-hint">
        <span>🎁 商品已发放，凭订单号 + 查询密码领取卡密</span>
        <router-link class="btn btn-primary" :to="`/fetch?order_no=${orderNo}`">前往取货</router-link>
      </div>

      <div class="pay-btn-row">
        <router-link class="btn btn-outline" :to="`/order/${orderNo}`">查看订单详情</router-link>
        <router-link class="btn btn-outline" to="/products">继续购物</router-link>
      </div>
    </div>

    <!-- 等待回调（轮询超时兜底） -->
    <div v-else-if="phase === 'waiting'" class="pay-center-card">
      <div class="pay-state-icon amber pulse">🕒</div>
      <div class="pay-state-title">支付处理中</div>
      <div class="muted">如已完成支付，到账可能有数秒延迟</div>
      <div class="pay-btn-row">
        <button class="btn btn-primary" @click="checkOnce(true)">刷新支付状态</button>
        <router-link class="btn btn-outline" :to="`/fetch?order_no=${orderNo}`">前往取货</router-link>
      </div>
    </div>

    <!-- 扫码支付中 -->
    <div v-else-if="phase === 'qrcode'" class="pay-qr-layout">
      <div class="pay-qr-main">
        <div class="pay-qr-head">
          <span class="pay-channel-icon">{{ payingChannel ? emojiOf(selected.method || payingChannel.code, payingChannel.driver) : '💳' }}</span>
          <div>
            <div class="pay-qr-title">{{ payingChannel?.name || '扫码支付' }}</div>
            <div class="muted">请使用手机扫一扫完成支付</div>
          </div>
          <span v-if="countdownText" class="pay-countdown" :class="{ danger: countdownDanger }">{{ countdownText }}</span>
        </div>
        <div class="pay-qr-box">
          <img v-if="qrDataUrl" :src="qrDataUrl" alt="支付二维码" />
          <div v-else class="pay-qr-loading">生成二维码中…</div>
        </div>
        <div class="pay-qr-amount">支付金额 <b>{{ formatMoney(order?.total_cents || 0) }}</b></div>
        <div class="pay-qr-hint">
          <span class="dot-loader"><span></span><span></span><span></span></span>
          正在检测支付结果，完成后自动展示卡密
        </div>
        <button class="pay-change" @click="backToSelect">← 更换支付方式</button>
      </div>
      <aside class="pay-side">
        <div class="pay-side-row"><span class="muted">订单号</span><span class="pay-mono">{{ orderNo }}</span></div>
        <div class="pay-side-row"><span class="muted">下单时间</span><span>{{ fmtTime(order?.created_at) }}</span></div>
        <div class="pay-side-row"><span class="muted">商品</span><span>{{ itemCount }} 件</span></div>
        <div class="pay-side-row total"><span>应付</span><b class="pay-amount">{{ formatMoney(order?.total_cents || 0) }}</b></div>
      </aside>
    </div>

    <!-- 跳转支付中 -->
    <div v-else-if="phase === 'redirect'" class="pay-center-card">
      <div class="pay-state-icon blue">🚀</div>
      <div class="pay-state-title">正在前往收银台</div>
      <div class="muted">使用{{ payingChannel?.name || '所选渠道' }}完成支付；支付后本页自动检测</div>
      <div class="pay-btn-row">
        <button class="btn btn-primary" @click="openRedirect">重新打开收银台</button>
        <button class="btn btn-outline" @click="copyLink">复制支付链接</button>
        <button class="btn btn-outline" @click="backToSelect">更换支付方式</button>
      </div>
      <div class="pay-qr-hint" style="margin-top: 14px;">
        <span class="dot-loader"><span></span><span></span><span></span></span>
        正在检测支付结果…
      </div>
    </div>

    <!-- 选择支付（默认态） -->
    <div v-else class="pay-select">
      <!-- 订单摘要 -->
      <div class="pay-summary">
        <div class="pay-summary-head">
          <span class="muted">订单号</span>
          <span class="pay-mono">{{ orderNo }}</span>
          <span v-if="countdownText" class="pay-countdown" :class="{ danger: countdownDanger }">⏱ {{ countdownText }}</span>
        </div>
        <div class="pay-summary-amount">
          <span>应付金额</span>
          <b class="pay-amount">{{ formatMoney(order?.total_cents || 0) }}</b>
        </div>
        <div class="pay-summary-meta">
          <span class="muted">下单时间 {{ fmtTime(order?.created_at) }}</span>
          <span class="muted">共 {{ itemCount }} 件商品</span>
        </div>
      </div>

      <!-- 渠道网格（方式级收银台，与充值页共用组件） -->
      <div class="pay-channels">
        <div class="pay-channels-title">选择支付方式</div>
        <PayChannelGrid
          :options="payOptions"
          :channel="selected.channel"
          :method="selected.method"
          @select="(ch, m) => (selected = { channel: ch, method: m })"
        />
      </div>

      <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>

      <button class="pay-submit" :disabled="!selected.channel || submitting" @click="pay">
        {{ submitting ? '创建支付中…' : `立即支付 ${formatMoney(order?.total_cents || 0)}` }}
      </button>
      <div class="pay-assure">🔒 支付过程安全加密 · 支付成功后自动发放卡密</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import QRCode from 'qrcode';
import { createPayment, fetchPaymentChannels, getOrder, fetchDelivery, getOrderPassword, rememberOrderPassword, type ChannelItem, type OrderDetail, type FetchDeliveryReply } from '@/api';
import { formatMoney } from '@/api/client';
import { flattenPayOptions, emojiOf } from '@/composables/pay-options';
import PayChannelGrid from '@/components/PayChannelGrid.vue';

const route = useRoute();
const router = useRouter();
const orderNo = String(route.params.orderNo || '');

// ── 六态：loading → select / qrcode / redirect / success / waiting / closed ──
type Phase = 'loading' | 'select' | 'qrcode' | 'redirect' | 'success' | 'waiting' | 'closed';
const phase = ref<Phase>('loading');

const order = ref<OrderDetail | null>(null);
const channels = ref<ChannelItem[]>([]);
// 方式级选择：channel=渠道码 + method=方式 code（单方式渠道 method 为空串）
const selected = ref<{ channel: string; method: string }>({ channel: '', method: '' });
const payingChannel = ref<ChannelItem | null>(null);
const submitting = ref(false);
const error = ref('');

// 二维码 / 跳转
const qrDataUrl = ref('');
const redirectUrl = ref('');

// 自动取货结果
const delivery = ref<FetchDeliveryReply | null>(null);
const copied = ref<number | null>(null);
const copiedAll = ref(false);

// 轮询（4s；3 分钟超时进等待回调态）
const PAID_STATES = ['paid', 'fulfilling', 'partially_delivered', 'delivered', 'completed'];
let pollTimer: ReturnType<typeof setInterval> | null = null;
let pollCount = 0;
const POLL_MAX = 45;

// 倒计时
const countdown = ref<number | null>(null);
let cdTimer: ReturnType<typeof setInterval> | null = null;
const countdownText = computed(() => {
  if (countdown.value === null) return '';
  if (countdown.value <= 0) return '已超时';
  const m = Math.floor(countdown.value / 60);
  const s = countdown.value % 60;
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
});
const countdownDanger = computed(() => countdown.value !== null && countdown.value <= 300);

const itemCount = computed(() => order.value?.items?.reduce((s, i) => s + i.quantity, 0) || 0);

// 渠道 → 收银台方式级选项（共享逻辑见 composables/pay-options.ts）
const payOptions = computed(() => flattenPayOptions(channels.value));

onMounted(async () => {
  // 支付回跳/分享兜底：?pwd= 直填会话记忆（不落 URL 历史——replace 清参）
  const qpwd = route.query.pwd;
  if (typeof qpwd === 'string' && qpwd) {
    rememberOrderPassword(orderNo, qpwd);
    router.replace({ path: `/payment/${orderNo}` });
  }
  await refreshOrder();
  // 订单加载失败（不存在/密码缺失）：进关闭态
  if (!order.value) {
    phase.value = 'closed';
    return;
  }
  const { data } = await fetchPaymentChannels();
  channels.value = data?.channels || [];
  const first = payOptions.value[0];
  selected.value = first ? { channel: first.channel, method: first.method } : { channel: '', method: '' };
  if (phase.value === 'success') loadDelivery();
  if (phase.value === 'select' || phase.value === 'qrcode' || phase.value === 'redirect') startPolling();
});

onUnmounted(() => { stopPolling(); stopCountdown(); });

// ── 订单加载与状态分发 ──
// 游客订单查询需带下单时密码（会话记忆）；登录本人订单免密——两参数都传由后端裁决
async function refreshOrder() {
  const pwd = getOrderPassword(orderNo);
  const { data } = await getOrder(orderNo, pwd || undefined).catch(() => ({ data: null }));
  if (!data) {
    // 登录态首次可能 me 未就绪：不立即判 closed，仅记录错误等渠道加载后重试
    error.value = '订单加载失败，请刷新重试';
    return;
  }
  error.value = '';
  order.value = data;
  if (data.expires_at) startCountdown(data.expires_at);
  decidePhase();
}

function decidePhase() {
  const st = order.value?.status;
  if (!st) return;
  if (PAID_STATES.includes(st)) phase.value = 'success';
  else if (st === 'canceled' || st === 'expired') phase.value = 'closed';
  else if (phase.value === 'loading') phase.value = 'select';
}

// ── 轮询 ──
function startPolling() {
  stopPolling();
  pollCount = 0;
  pollTimer = setInterval(async () => {
    pollCount += 1;
    const prev = order.value?.status;
    await refreshOrder();
    if (!order.value) return; // 单次查询失败（网络抖动）不切态，等下一轮
    const st = order.value.status;
    if (st && PAID_STATES.includes(st) && (!prev || !PAID_STATES.includes(prev))) {
      stopPolling();
      phase.value = 'success';
      loadDelivery();
      return;
    }
    if (st === 'canceled' || st === 'expired') {
      stopPolling();
      phase.value = 'closed';
      return;
    }
    if (pollCount >= POLL_MAX) {
      stopPolling();
      phase.value = 'waiting';
    }
  }, 4000);
}
function stopPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
}

async function checkOnce(manual = false) {
  await refreshOrder();
  decidePhase();
  if (phase.value === 'success') { loadDelivery(); return; }
  if (manual && phase.value === 'waiting') window.alert('暂未检测到支付，请稍后再试');
  if (phase.value === 'qrcode' || phase.value === 'redirect' || phase.value === 'waiting') startPolling();
}

// ── 倒计时 ──
function startCountdown(expiresAt: number) {
  stopCountdown();
  const tick = () => { countdown.value = Math.max(0, expiresAt - Math.floor(Date.now() / 1000)); };
  tick();
  cdTimer = setInterval(tick, 1000);
}
function stopCountdown() {
  if (cdTimer) { clearInterval(cdTimer); cdTimer = null; }
}

// ── 支付创建 ──
async function pay() {
  if (!selected.value.channel) return;
  submitting.value = true;
  error.value = '';
  qrDataUrl.value = '';
  redirectUrl.value = '';
  payingChannel.value = channels.value.find((c) => c.code === selected.value.channel) || null;
  const { data, error: err } = await createPayment(orderNo, selected.value.channel, selected.value.method);
  submitting.value = false;
  if (err || !data) { error.value = err || '创建支付失败'; return; }

  const payload = data.payload || '';
  // 余额支付：同步扣款即完成，不走收银台跳转（后端 payload 为本页地址，避免弹窗）
  if (selected.value.channel === 'wallet') {
    await refreshOrder();
    decidePhase();
    if (phase.value === 'success') loadDelivery();
    return;
  }
  if (data.type === 'qrcode') {
    let content = payload;
    try { const parsed = JSON.parse(payload); content = parsed.code_url || payload; } catch { /* 原文即内容 */ }
    qrDataUrl.value = await makeQr(content);
    phase.value = 'qrcode';
  } else if (data.type === 'params') {
    // 易支付表单 POST：自动提交
    try {
      const p = JSON.parse(payload);
      submitForm(p.url || '', p.params || {});
      phase.value = 'redirect';
      redirectUrl.value = p.url || '';
    } catch { error.value = '支付参数异常'; }
  } else {
    let url = payload;
    try { const parsed = JSON.parse(payload); url = parsed.url || payload; } catch { /* 原文即 URL */ }
    redirectUrl.value = url;
    phase.value = 'redirect';
    openRedirect();
  }
  startPolling();
}

async function makeQr(content: string): Promise<string> {
  if (content.startsWith('data:image') || content.startsWith('http')) return content;
  try {
    return await QRCode.toDataURL(content, { width: 220, margin: 1, errorCorrectionLevel: 'M' });
  } catch {
    return '';
  }
}

function openRedirect() {
  if (!redirectUrl.value) return;
  // 新窗口打开 + noopener 防反向控制（大厂同款纪律）
  window.open(redirectUrl.value, '_blank', 'noopener');
}

function submitForm(url: string, params: Record<string, string>) {
  const form = document.createElement('form');
  form.action = url;
  form.method = 'POST';
  form.target = '_blank';
  form.rel = 'noopener';
  for (const [k, v] of Object.entries(params)) {
    const input = document.createElement('input');
    input.type = 'hidden';
    input.name = k;
    input.value = String(v);
    form.appendChild(input);
  }
  document.body.appendChild(form);
  form.submit();
}

async function copyLink() {
  try {
    await navigator.clipboard.writeText(redirectUrl.value);
    window.alert('支付链接已复制');
  } catch { /* 忽略 */ }
}

function backToSelect() {
  stopPolling();
  phase.value = 'select';
  qrDataUrl.value = '';
  redirectUrl.value = '';
}

// ── 支付成功：自动取货（会话内记忆的查询密码；失败降级去取货页）──
async function loadDelivery() {
  const pwd = getOrderPassword(orderNo);
  if (!pwd) return; // 无密码记忆：模板降级显示「前往取货」
  const { data } = await fetchDelivery(orderNo, pwd).catch(() => ({ data: null }));
  if (data) delivery.value = data;
}

async function copyOne(content: string, index: number) {
  try {
    await navigator.clipboard.writeText(content);
    copied.value = index;
    setTimeout(() => { if (copied.value === index) copied.value = null; }, 1500);
  } catch { /* 忽略 */ }
}
async function copyAll() {
  if (!delivery.value) return;
  try {
    await navigator.clipboard.writeText(delivery.value.items.map((i) => i.content).join('\n'));
    copiedAll.value = true;
    setTimeout(() => (copiedAll.value = false), 1500);
  } catch { /* 忽略 */ }
}

function fmtTime(ts?: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '-';
}
</script>

<style scoped>
.pay-page { max-width: 760px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px; }

/* ── 居中态卡（成功/等待/关闭/跳转）── */
.pay-center-card {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 14px;
  padding: 36px 24px; text-align: center;
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.05);
}
.pay-state-icon {
  width: 64px; height: 64px; margin: 0 auto 14px;
  border-radius: 999px; display: flex; align-items: center; justify-content: center;
  font-size: 30px;
}
.pay-state-icon.green { background: #dcfce7; color: #16a34a; font-size: 34px; font-weight: 800; }
.pay-state-icon.amber { background: #fef3c7; }
.pay-state-icon.gray { background: #f3f4f6; }
.pay-state-icon.blue { background: #dbeafe; }
.pay-state-title { font-size: 20px; font-weight: 800; color: #111827; margin-bottom: 6px; }
.pay-loading-icon { font-size: 36px; margin-bottom: 10px; }
.pay-loading-title { color: #6b7280; }
.pulse { animation: pulse 1.6s ease-in-out infinite; }
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }
.pop { animation: pop 0.45s cubic-bezier(0.34, 1.56, 0.64, 1); }
@keyframes pop { 0% { transform: scale(0.4); opacity: 0; } 100% { transform: scale(1); opacity: 1; } }

.pay-amount { color: #ff5722; font-size: 24px; font-weight: 800; }
.pay-btn-row { display: flex; gap: 10px; justify-content: center; margin-top: 18px; flex-wrap: wrap; }
.pay-mono { font-family: ui-monospace, Menlo, monospace; font-size: 13px; word-break: break-all; }

/* ── 成功态卡密 ── */
.pay-cards { text-align: left; background: #f8fafc; border: 1px solid #f1f5f9; border-radius: 12px; padding: 14px; margin-top: 18px; }
.pay-cards-head { display: flex; justify-content: space-between; align-items: center; font-size: 13px; font-weight: 700; margin-bottom: 10px; }
.pay-copy-all { border: none; background: none; color: #2563eb; font-size: 13px; cursor: pointer; }
.pay-copy-all:hover { text-decoration: underline; }
.pay-card-row {
  display: flex; align-items: center; gap: 10px;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 8px;
  padding: 10px 12px; margin-bottom: 8px;
}
.pay-card-row:last-child { margin-bottom: 0; }
.pay-card-index { font-size: 11px; color: #9ca3af; width: 22px; flex-shrink: 0; }
.pay-card-code { flex: 1; font-family: ui-monospace, Menlo, monospace; font-size: 13px; word-break: break-all; user-select: all; }
.pay-card-copy {
  border: none; background: none; cursor: pointer; flex-shrink: 0;
  font-size: 12px; color: #2563eb; padding: 4px 8px; border-radius: 6px; opacity: 0.55; transition: all 0.15s;
}
.pay-card-row:hover .pay-card-copy { opacity: 1; }
.pay-card-copy:hover { background: #eff6ff; }
.pay-fetch-hint {
  display: flex; align-items: center; justify-content: center; gap: 12px; flex-wrap: wrap;
  background: #eff6ff; border: 1px solid #dbeafe; border-radius: 10px; padding: 14px; margin-top: 18px; font-size: 13px;
}

/* ── 扫码态 ── */
.pay-qr-layout { display: grid; grid-template-columns: 1fr; gap: 16px; }
@media (min-width: 768px) { .pay-qr-layout { grid-template-columns: 1.6fr 1fr; } }
.pay-qr-main { background: #fff; border: 1px solid #e5e7eb; border-radius: 14px; padding: 20px; text-align: center; }
.pay-qr-head { display: flex; align-items: center; gap: 10px; text-align: left; margin-bottom: 16px; }
.pay-qr-title { font-size: 15px; font-weight: 700; color: #111827; }
.pay-countdown {
  margin-left: auto; font-family: ui-monospace, Menlo, monospace; font-size: 13px; font-weight: 700;
  background: #dcfce7; color: #15803d; padding: 3px 10px; border-radius: 999px;
}
.pay-countdown.danger { background: #fee2e2; color: #b91c1c; }
.pay-channel-icon {
  width: 38px; height: 38px; border-radius: 10px; flex-shrink: 0;
  background: #f1f5f9; display: inline-flex; align-items: center; justify-content: center; font-size: 18px;
}
.pay-qr-box {
  width: 244px; margin: 0 auto; background: #fff; border: 1px solid #e5e7eb;
  border-radius: 12px; padding: 12px; box-shadow: 0 4px 12px rgba(15, 23, 42, 0.06);
}
.pay-qr-box img { width: 100%; display: block; }
.pay-qr-loading { padding: 96px 0; color: #9ca3af; font-size: 13px; }
.pay-qr-amount { margin-top: 14px; font-size: 14px; }
.pay-qr-hint { margin-top: 12px; font-size: 13px; color: #6b7280; display: flex; align-items: center; justify-content: center; gap: 8px; }
.pay-change {
  margin-top: 14px; border: none; background: none; color: #2563eb; font-size: 13px; cursor: pointer;
}
.pay-change:hover { text-decoration: underline; }

/* 点动画（检测中） */
.dot-loader { display: inline-flex; gap: 4px; }
.dot-loader span {
  width: 6px; height: 6px; border-radius: 999px; background: #2563eb;
  animation: bounce 1.2s infinite ease-in-out;
}
.dot-loader span:nth-child(2) { animation-delay: 0.15s; }
.dot-loader span:nth-child(3) { animation-delay: 0.3s; }
@keyframes bounce { 0%, 80%, 100% { transform: scale(0.6); opacity: 0.4; } 40% { transform: scale(1); opacity: 1; } }

/* ── 侧栏摘要 ── */
.pay-side {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 14px; padding: 16px;
  display: flex; flex-direction: column; gap: 12px; align-self: start;
}
.pay-side-row { display: flex; justify-content: space-between; align-items: center; gap: 8px; font-size: 13px; }
.pay-side-row.total {
  border-top: 1px solid #f3f4f6; padding-top: 12px; font-weight: 700; font-size: 14px;
  justify-content: space-between;
}

/* ── 选择态 ── */
.pay-select { display: flex; flex-direction: column; gap: 16px; }
.pay-summary { background: linear-gradient(135deg, #eff6ff, #fff); border: 1px solid #dbeafe; border-radius: 14px; padding: 20px; }
.pay-summary-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.pay-summary-head .pay-countdown { margin-left: auto; }
.pay-summary-amount { display: flex; align-items: baseline; gap: 10px; margin-top: 10px; font-size: 14px; color: #374151; }
.pay-summary-meta { display: flex; gap: 14px; margin-top: 8px; flex-wrap: wrap; }

.pay-channels { background: #fff; border: 1px solid #e5e7eb; border-radius: 14px; padding: 18px; }
.pay-channels-title { font-size: 15px; font-weight: 700; color: #111827; margin-bottom: 14px; }

.pay-submit {
  width: 100%; padding: 14px 0; border: none; cursor: pointer;
  border-radius: 12px; font-size: 16px; font-weight: 700; color: #fff;
  background: linear-gradient(90deg, #2563eb, #1d4ed8);
  box-shadow: 0 6px 18px rgba(37, 99, 235, 0.3); transition: all 0.15s;
}
.pay-submit:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 8px 24px rgba(37, 99, 235, 0.4); }
.pay-submit:disabled { opacity: 0.5; cursor: not-allowed; }
.pay-assure { text-align: center; font-size: 12px; color: #9ca3af; }
</style>
