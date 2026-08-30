<template>
  <div>
    <!-- 推广中心头部：推广码 + 二维码 + 推广链接 -->
    <div class="card promo-hero" v-if="my">
      <!-- 左列：推广码 + 推广链接（纵向分区） -->
      <div class="promo-left">
        <div class="promo-block">
          <div class="promo-block-title">我的推广码</div>
          <div class="promo-code-row">
            <div class="promo-code">{{ my.promo_code || my.user_id }}</div>
            <button class="btn secondary" @click="copyCode">{{ copiedCode ? '✓ 已复制' : '复制推广码' }}</button>
          </div>
        </div>
        <div class="promo-block">
          <div class="promo-block-title">推广链接</div>
          <div class="actions">
            <input class="input" :value="my.invite_url" readonly style="flex: 1;" />
            <button class="btn secondary" @click="copyLink">{{ copied ? '✓ 已复制' : '复制链接' }}</button>
          </div>
          <div class="muted" style="margin-top: 8px;">好友通过链接注册/下单，你都能获得佣金</div>
        </div>
      </div>
      <!-- 右列：二维码（垂直居中） -->
      <div class="promo-qr">
        <canvas ref="qrCanvas" width="200" height="200"></canvas>
        <div class="muted" style="text-align: center; margin-top: 6px;">扫码进入推广页</div>
        <button class="btn secondary" style="width: 100%; margin-top: 6px;" @click="downloadQr">下载二维码</button>
      </div>
    </div>

    <!-- 收益统计 -->
    <div class="stat-grid" v-if="my">
      <div class="card"><div class="muted">冻结中佣金</div><div class="stat-num">{{ formatMoney(my.pending_cents) }}</div></div>
      <div class="card"><div class="muted">可提现</div><div class="stat-num" style="color: #16a34a;">{{ formatMoney(my.available_cents) }}</div></div>
      <div class="card"><div class="muted">累计佣金</div><div class="stat-num">{{ formatMoney(my.total_cents) }}</div></div>
      <div class="card"><div class="muted">已提现</div><div class="stat-num">{{ formatMoney(my.withdrawn_cents) }}</div></div>
      <div class="card" v-if="my.debt_cents > 0"><div class="muted">负债（退款扣回）</div><div class="stat-num" style="color: #dc2626;">{{ formatMoney(my.debt_cents) }}</div></div>
    </div>

    <!-- 团队概览 + 规则说明 -->
    <div class="card" style="margin-bottom: 16px;" v-if="my">
      <div style="display: flex; justify-content: space-between; flex-wrap: wrap; gap: 8px; align-items: center;">
        <div>团队：直推 {{ my.team_l1 }} 人 · 二级 {{ my.team_l2 }} 人 · 三级 {{ my.team_l3 }} 人</div>
        <span class="badge blue">三级分销</span>
      </div>
      <div class="muted" style="margin-top: 6px;">
        📌 归因规则：好友经你的推广链接访问后 30 天内注册或下单均计入你的团队；佣金按订单金额三级分成（比例由站长配置），冻结期后可提现
      </div>
    </div>

    <div class="tabs">
      <button :class="{ active: tab === 'team' }" @click="switchTab('team')">我的团队</button>
      <button :class="{ active: tab === 'commissions' }" @click="switchTab('commissions')">佣金流水</button>
    </div>

    <!-- 团队 -->
    <div v-if="tab === 'team'" class="card">
      <div class="actions" style="margin-bottom: 8px;">
        <select v-model="teamTier" @change="loadTeam(1)" style="padding: 6px;">
          <option :value="0">全部层级</option>
          <option :value="1">直推（一级）</option>
          <option :value="2">二级</option>
          <option :value="3">三级</option>
        </select>
      </div>
      <table class="list">
        <thead><tr><th>用户</th><th>层级</th><th>加入时间</th></tr></thead>
        <tbody>
          <tr v-for="m in team" :key="m.user_id">
            <td>{{ m.username_masked }}</td>
            <td><span class="badge blue">L{{ m.tier }}</span></td>
            <td class="muted">{{ fmtTime(m.joined_at) }}</td>
          </tr>
          <tr v-if="!team.length"><td colspan="3" class="muted" style="text-align: center;">暂无团队成员</td></tr>
        </tbody>
      </table>
    </div>

    <!-- 佣金流水 -->
    <div v-if="tab === 'commissions'" class="card">
      <table class="list table-desktop">
        <thead><tr><th>时间</th><th>订单</th><th>层级</th><th>基数</th><th>佣金</th><th>状态</th></tr></thead>
        <tbody>
          <tr v-for="c in commissions" :key="c.id">
            <td class="muted">{{ fmtTime(c.created_at) }}</td>
            <td>#{{ c.order_id }}</td>
            <td>L{{ c.tier }}</td>
            <td>{{ formatMoney(c.base_amount) }}</td>
            <td :class="c.amount >= 0 ? 'success' : 'error'">{{ formatSignedMoney(c.amount) }}</td>
            <td><span :class="commissionBadge(c.status)">{{ commissionText(c.status) }}</span></td>
          </tr>
          <tr v-if="!commissions.length"><td colspan="6" class="muted" style="text-align: center;">暂无佣金记录</td></tr>
        </tbody>
      </table>
      <!-- 移动端佣金卡片 -->
      <div class="table-cards">
        <div v-for="c in commissions" :key="c.id" class="mcard">
          <div class="mcard-row">
            <span class="mcard-title">#{{ c.order_id }} · L{{ c.tier }}</span>
            <span :class="commissionBadge(c.status)">{{ commissionText(c.status) }}</span>
          </div>
          <div class="mcard-row">
            <span class="muted">{{ fmtTime(c.created_at) }} · 基数 {{ formatMoney(c.base_amount) }}</span>
            <span :class="c.amount >= 0 ? 'success' : 'error'" style="font-weight: 700;">{{ formatSignedMoney(c.amount) }}</span>
          </div>
        </div>
        <div v-if="!commissions.length" class="muted" style="text-align: center; padding: 16px 0;">暂无佣金记录</div>
      </div>
      <div class="actions" style="margin-top: 12px;" v-if="commissionsTotal > pageSize">
        <button class="btn secondary" :disabled="commissionsPage <= 1" @click="loadCommissions(commissionsPage - 1)">上一页</button>
        <span class="muted">{{ commissionsPage }} / {{ Math.ceil(commissionsTotal / pageSize) }}</span>
        <button class="btn secondary" :disabled="commissionsPage >= Math.ceil(commissionsTotal / pageSize)" @click="loadCommissions(commissionsPage + 1)">下一页</button>
      </div>
    </div>

    <!-- 提现（独立页入口） -->
    <div v-if="tab === 'withdraw'" class="card" style="display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap;">
      <div>
        <b>佣金提现</b>
        <div class="muted" style="margin-top: 4px;">支付宝 / 微信收款码 / USDT TRC20 · 提现记录 · 工单支持</div>
      </div>
      <router-link class="btn btn-primary" to="/withdraw">去提现</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue';
import QRCode from 'qrcode';
import {
  myAffiliate, listTeam, listCommissions, createWithdrawal,
  type MyAffiliateReply, type TeamMember, type CommissionItem
} from '@/api';
import { formatMoney, formatSignedMoney } from '@/api/client';

const tab = ref<'team' | 'commissions' | 'withdraw'>('team');
const my = ref<MyAffiliateReply | null>(null);
const copied = ref(false);
const copiedCode = ref(false);
const qrCanvas = ref<HTMLCanvasElement | null>(null);

const team = ref<TeamMember[]>([]);
const teamTier = ref(0);
const commissions = ref<CommissionItem[]>([]);
const commissionsPage = ref(1);
const commissionsTotal = ref(0);
const pageSize = 15;

const withdrawYuan = ref<number | null>(null);
const withdrawMethod = ref('alipay');
const withdrawAccount = ref('');
const withdrawError = ref('');
const withdrawOk = ref<{ withdrawal_id: number; amount_cents: number; fee_cents: number; credited_cents: number } | null>(null);
const withdrawing = ref(false);

onMounted(async () => {
  const { data } = await myAffiliate();
  my.value = data;
  loadTeam(1);
  // 推广链接二维码（canvas 渲染；链接为空跳过）
  if (data?.invite_url) {
    await nextTick();
    if (qrCanvas.value) {
      try {
        await QRCode.toCanvas(qrCanvas.value, data.invite_url, { width: 200, margin: 2 });
      } catch { /* 渲染失败忽略——复制链接兜底 */ }
    }
  }
});

function switchTab(t: 'team' | 'commissions' | 'withdraw') {
  tab.value = t;
  if (t === 'commissions' && !commissions.value.length) loadCommissions(1);
}

async function loadTeam(page: number) {
  const { data } = await listTeam(teamTier.value || undefined, page, pageSize);
  if (data) team.value = data.members || [];
}

async function loadCommissions(page: number) {
  const { data } = await listCommissions(page, pageSize);
  if (data) {
    commissions.value = data.commissions || [];
    commissionsTotal.value = data.total;
    commissionsPage.value = page;
  }
}

async function copyLink() {
  if (!my.value?.invite_url) return;
  try {
    await navigator.clipboard.writeText(my.value.invite_url);
    copied.value = true;
    setTimeout(() => (copied.value = false), 2000);
  } catch {
    /* 剪贴板不可用（HTTP 环境）——手选复制 */
  }
}

async function copyCode() {
  const code = my.value?.promo_code || String(my.value?.user_id || '');
  if (!code) return;
  try {
    await navigator.clipboard.writeText(code);
    copiedCode.value = true;
    setTimeout(() => (copiedCode.value = false), 2000);
  } catch { /* 忽略 */ }
}

function downloadQr() {
  if (!qrCanvas.value) return;
  try {
    const link = document.createElement('a');
    link.download = `promo-qrcode-${my.value?.promo_code || my.value?.user_id}.png`;
    link.href = qrCanvas.value.toDataURL('image/png');
    link.click();
  } catch { /* 忽略 */ }
}

async function doWithdraw() {
  if (!withdrawYuan.value || withdrawYuan.value <= 0) {
    withdrawError.value = '请输入提现金额';
    return;
  }
  if (!withdrawAccount.value.trim()) {
    withdrawError.value = '请填写收款账号';
    return;
  }
  withdrawing.value = true;
  withdrawError.value = '';
  withdrawOk.value = null;
  const { data, error } = await createWithdrawal({
    amount_cents: Math.round(withdrawYuan.value * 100),
    method_type: withdrawMethod.value,
    account: withdrawAccount.value.trim()
  });
  withdrawing.value = false;
  if (error || !data) {
    withdrawError.value = error || '申请失败（可提余额不足？）';
    return;
  }
  withdrawOk.value = data;
  withdrawYuan.value = null;
  withdrawAccount.value = '';
  const m = await myAffiliate();
  my.value = m.data;
}

function commissionText(s: string): string {
  return ({ pending_confirm: '冻结中', available: '可提现', withdrawn: '已提现', reversed: '已回冲' } as Record<string, string>)[s] || s;
}
function commissionBadge(s: string): string {
  return ({ pending_confirm: 'badge orange', available: 'badge green', withdrawn: 'badge blue', reversed: 'badge red' } as Record<string, string>)[s] || 'badge gray';
}
function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '';
}
</script>

<style scoped>
/* ── 移动端表格→卡片（佣金流水；桌面表格不受影响）── */
.table-cards { display: none; }
.mcard {
  border: 1px solid #f3f4f6; border-radius: 10px; padding: 12px;
  display: flex; flex-direction: column; gap: 8px; background: #fafbfc;
}
.mcard-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.mcard-title { font-weight: 600; font-size: 14px; color: #111827; word-break: break-all; text-align: left; }
@media (max-width: 768px) {
  .table-desktop { display: none; }
  .table-cards { display: flex; flex-direction: column; gap: 10px; }
}

/* 推广头部：左（码+链接）/ 右（二维码）经典两栏；窄屏纵向堆叠 */
.promo-hero {
  display: flex; gap: 24px; align-items: center;
  background: linear-gradient(135deg, #eff6ff, #fff);
  border-color: #dbeafe;
  flex-wrap: wrap;
}
@media (max-width: 640px) { .promo-hero { flex-direction: column-reverse; } }
.promo-left {
  flex: 1; min-width: 260px;
  display: flex; flex-direction: column; gap: 18px;
}
.promo-block + .promo-block { border-top: 1px dashed #dbeafe; padding-top: 16px; }
.promo-block-title { font-size: 13px; font-weight: 600; color: #6b7280; margin-bottom: 8px; }
.promo-code-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.promo-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 32px; font-weight: 800; letter-spacing: 4px; color: #2563eb;
}
.promo-qr {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 12px; flex-shrink: 0;
}
.promo-qr canvas { display: block; border-radius: 6px; }
</style>
