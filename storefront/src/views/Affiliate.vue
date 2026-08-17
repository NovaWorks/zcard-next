<template>
  <div>
    <!-- 推广码 + 五数统计 -->
    <div class="stat-grid" v-if="my">
      <div class="card"><div class="muted">冻结中佣金</div><div class="stat-num">{{ formatMoney(my.pending_cents) }}</div></div>
      <div class="card"><div class="muted">可提现</div><div class="stat-num" style="color: #16a34a;">{{ formatMoney(my.available_cents) }}</div></div>
      <div class="card"><div class="muted">累计佣金</div><div class="stat-num">{{ formatMoney(my.total_cents) }}</div></div>
      <div class="card"><div class="muted">已提现</div><div class="stat-num">{{ formatMoney(my.withdrawn_cents) }}</div></div>
      <div class="card" v-if="my.debt_cents > 0"><div class="muted">负债（退款扣回）</div><div class="stat-num" style="color: #dc2626;">{{ formatMoney(my.debt_cents) }}</div></div>
    </div>

    <div class="card" style="margin-bottom: 16px;" v-if="my">
      <div style="display: flex; gap: 12px; align-items: center; flex-wrap: wrap;">
        <div>
          <div class="muted">我的推广码</div>
          <div style="font-size: 20px; font-weight: 700; margin: 4px 0;">{{ my.user_id }}</div>
        </div>
        <div style="flex: 1; min-width: 260px;">
          <div class="muted">邀请链接</div>
          <div class="actions" style="margin-top: 4px;">
            <input class="input" :value="my.invite_url" readonly style="flex: 1;" />
            <button class="btn secondary" @click="copyLink">复制</button>
          </div>
        </div>
      </div>
      <div class="muted" style="margin-top: 8px;">
        团队：直推 {{ my.team_l1 }} 人 · 二级 {{ my.team_l2 }} 人 · 三级 {{ my.team_l3 }} 人
      </div>
      <div v-if="copied" class="success" style="margin-top: 6px;">已复制到剪贴板</div>
    </div>

    <div class="tabs">
      <button :class="{ active: tab === 'team' }" @click="switchTab('team')">我的团队</button>
      <button :class="{ active: tab === 'commissions' }" @click="switchTab('commissions')">佣金流水</button>
      <button :class="{ active: tab === 'withdraw' }" @click="switchTab('withdraw')">提现</button>
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
      <table class="list">
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
      <div class="actions" style="margin-top: 12px;" v-if="commissionsTotal > pageSize">
        <button class="btn secondary" :disabled="commissionsPage <= 1" @click="loadCommissions(commissionsPage - 1)">上一页</button>
        <span class="muted">{{ commissionsPage }} / {{ Math.ceil(commissionsTotal / pageSize) }}</span>
        <button class="btn secondary" :disabled="commissionsPage >= Math.ceil(commissionsTotal / pageSize)" @click="loadCommissions(commissionsPage + 1)">下一页</button>
      </div>
    </div>

    <!-- 提现 -->
    <div v-if="tab === 'withdraw'" class="card" style="max-width: 480px;">
      <div class="muted" style="margin-bottom: 12px;">
        可提现 {{ formatMoney(my?.available_cents || 0) }}；提现经人工审核后打款，手续费从金额中扣除
      </div>
      <div class="field">
        <label>提现金额（元）</label>
        <input class="input" v-model.number="withdrawYuan" type="number" min="1" step="0.01" />
      </div>
      <div class="field">
        <label>收款方式</label>
        <select v-model="withdrawMethod">
          <option value="alipay">支付宝</option>
          <option value="wechat">微信</option>
          <option value="bank">银行转账</option>
        </select>
      </div>
      <div class="field">
        <label>收款账号</label>
        <input class="input" v-model="withdrawAccount" type="text" placeholder="支付宝账号 / 微信号 / 银行卡号" />
      </div>
      <div v-if="withdrawError" class="error" style="margin-bottom: 8px;">{{ withdrawError }}</div>
      <div v-if="withdrawOk" class="success" style="margin-bottom: 8px;">
        申请成功 #{{ withdrawOk.withdrawal_id }}：金额 {{ formatMoney(withdrawOk.amount_cents) }}，手续费 {{ formatMoney(withdrawOk.fee_cents) }}，实际到账 {{ formatMoney(withdrawOk.credited_cents) }}（等待审核）
      </div>
      <button class="btn" :disabled="withdrawing" @click="doWithdraw">{{ withdrawing ? '提交中…' : '申请提现' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import {
  myAffiliate, listTeam, listCommissions, createWithdrawal,
  type MyAffiliateReply, type TeamMember, type CommissionItem
} from '@/api';
import { formatMoney, formatSignedMoney } from '@/api/client';

const tab = ref<'team' | 'commissions' | 'withdraw'>('team');
const my = ref<MyAffiliateReply | null>(null);
const copied = ref(false);

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
