<template>
  <div>
    <div class="tabs">
      <button :class="{ active: tab === 'overview' }" @click="tab = 'overview'">总览</button>
      <button :class="{ active: tab === 'orders' }" @click="switchTab('orders')">我的订单</button>
      <button :class="{ active: tab === 'transactions' }" @click="switchTab('transactions')">余额流水</button>
      <button :class="{ active: tab === 'recharge' }" @click="switchTab('recharge')">充值</button>
      <button :class="{ active: tab === 'giftcard' }" @click="switchTab('giftcard')">礼品卡</button>
      <button :class="{ active: tab === 'security' }" @click="switchTab('security')">账户安全</button>
    </div>

    <!-- 总览：余额四数 + 等级进度 + 快捷入口 -->
    <div v-if="tab === 'overview'">
      <div class="stat-grid">
        <div class="card"><div class="muted">可用余额</div><div class="stat-num">{{ formatMoney(balance?.available_cents ?? 0) }}</div></div>
        <div class="card"><div class="muted">冻结中</div><div class="stat-num">{{ formatMoney(balance?.locked_cents ?? 0) }}</div></div>
        <div class="card"><div class="muted">积分</div><div class="stat-num">{{ level?.points ?? balance?.points ?? 0 }}</div></div>
        <div class="card"><div class="muted">累计消费</div><div class="stat-num">{{ formatMoney(level?.consumed_cents ?? 0) }}</div></div>
      </div>
      <div class="card" v-if="level">
        <div style="display: flex; justify-content: space-between; margin-bottom: 8px;">
          <span>当前等级：<b>{{ level.current?.name || '普通会员' }}</b>
            <span v-if="level.current?.discount" class="tag" style="margin-left: 6px;">{{ (level.current.discount / 100).toFixed(0) }} 折</span>
          </span>
          <span v-if="level.next" class="muted">距 {{ level.next.name }}：
            <template v-if="level.progress?.recharge_gap_cents">还需充值 {{ formatMoney(level.progress.recharge_gap_cents) }}</template>
            <template v-if="level.progress?.recharge_gap_cents && level.progress?.consume_gap_cents"> / </template>
            <template v-if="level.progress?.consume_gap_cents">还需消费 {{ formatMoney(level.progress.consume_gap_cents) }}</template>
          </span>
          <span v-else class="muted">已满级</span>
        </div>
        <div class="progress"><div :style="{ width: `${level.progress?.percent ?? 100}%` }"></div></div>
        <div class="actions" style="margin-top: 16px;">
          <button class="btn" @click="switchTab('recharge')">去充值</button>
          <router-link class="btn secondary" to="/points">积分商城</router-link>
          <router-link class="btn secondary" to="/fetch">去取货</router-link>
          <router-link class="btn secondary" to="/coupons">我的优惠券</router-link>
        </div>
      </div>
      <div v-else class="card muted">加载中…</div>
    </div>

    <!-- 我的订单 -->
    <div v-if="tab === 'orders'" class="card">
      <table class="list">
        <thead><tr><th>订单号</th><th>状态</th><th>金额</th><th>件数</th><th>时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="o in orders" :key="o.order_no">
            <td>{{ o.order_no }}</td>
            <td><span :class="statusBadge(o.status)">{{ statusText(o.status) }}</span></td>
            <td class="price">{{ formatMoney(o.total_cents) }}</td>
            <td>{{ o.item_count }}</td>
            <td class="muted">{{ fmtTime(o.created_at) }}</td>
            <td class="actions">
              <router-link class="btn secondary" :to="`/payment/${o.order_no}`" v-if="o.status === 'pending'">去支付</router-link>
              <router-link class="btn secondary" :to="`/fetch`" v-else>取货</router-link>
              <button class="btn secondary" v-if="o.status === 'pending'" @click="cancel(o.order_no)">取消</button>
            </td>
          </tr>
          <tr v-if="!orders.length"><td colspan="6" class="muted" style="text-align: center;">暂无订单</td></tr>
        </tbody>
      </table>
      <div class="actions" style="margin-top: 12px;" v-if="ordersTotal > ordersPageSize">
        <button class="btn secondary" :disabled="ordersPage <= 1" @click="loadOrders(ordersPage - 1)">上一页</button>
        <span class="muted">{{ ordersPage }} / {{ Math.ceil(ordersTotal / ordersPageSize) }}</span>
        <button class="btn secondary" :disabled="ordersPage >= Math.ceil(ordersTotal / ordersPageSize)" @click="loadOrders(ordersPage + 1)">下一页</button>
      </div>
    </div>

    <!-- 余额流水 -->
    <div v-if="tab === 'transactions'" class="card">
      <table class="list">
        <thead><tr><th>时间</th><th>类型</th><th>金额</th><th>余额</th><th>备注</th></tr></thead>
        <tbody>
          <tr v-for="t in transactions" :key="t.id">
            <td class="muted">{{ fmtTime(t.created_at) }}</td>
            <td>{{ t.type }}</td>
            <td :class="t.amount_cents >= 0 ? 'success' : 'error'">{{ formatSignedMoney(t.amount_cents) }}</td>
            <td>{{ formatMoney(t.balance_after_cents) }}</td>
            <td class="muted">{{ t.remark || t.reference }}</td>
          </tr>
          <tr v-if="!transactions.length"><td colspan="5" class="muted" style="text-align: center;">暂无流水</td></tr>
        </tbody>
      </table>
      <div class="actions" style="margin-top: 12px;" v-if="txTotal > txPageSize">
        <button class="btn secondary" :disabled="txPage <= 1" @click="loadTx(txPage - 1)">上一页</button>
        <span class="muted">{{ txPage }} / {{ Math.ceil(txTotal / txPageSize) }}</span>
        <button class="btn secondary" :disabled="txPage >= Math.ceil(txTotal / txPageSize)" @click="loadTx(txPage + 1)">下一页</button>
      </div>
    </div>

    <!-- 充值（档位可视化 + 自定义金额；服务端裁决；支付载荷三形态同支付页） -->
    <div v-if="tab === 'recharge'" class="card" style="max-width: 480px;">
      <div v-if="giftTiers.length" class="field">
        <label>充值档位</label>
        <div style="display: grid; grid-template-columns: repeat(auto-fill, minmax(128px, 1fr)); gap: 8px;">
          <button v-for="tier in giftTiers" :key="tier.amount" class="btn secondary"
                  :style="rechargeYuan === centsToYuan(tier.amount) ? 'background:#2563eb;color:#fff' : ''"
                  @click="pickTier(tier)">
            <div>{{ formatMoney(tier.amount) }}</div>
            <div v-if="tier.gift_balance" style="font-size: 12px;">送 {{ formatMoney(tier.gift_balance) }}</div>
            <div v-if="tier.gift_points" style="font-size: 12px;">送 {{ tier.gift_points }} 积分</div>
          </button>
        </div>
      </div>
      <div class="field">
        <label>充值金额（元）<span v-if="giftTiers.length" class="muted">（自定义）</span></label>
        <input class="input" v-model.number="rechargeYuan" type="number" min="1" step="0.01" placeholder="10" />
        <div class="muted" v-if="rechargeMeta">限额 {{ formatMoney(rechargeMeta.min_amount) }} ~ {{ formatMoney(rechargeMeta.max_amount) }}
          <template v-if="giftTiers.length && !giftTiers.some((t) => t.gift_balance || t.gift_points)">；充值赠送见支付结果</template>
        </div>
      </div>
      <div class="field">
        <label>支付渠道</label>
        <select v-model="rechargeChannel">
          <option value="alipay">支付宝</option>
          <option value="wechat">微信支付</option>
          <option value="epay">易支付</option>
        <option value="epusdt">USDT（TRC20）</option>
        </select>
      </div>
      <div v-if="rechargeError" class="error" style="margin-bottom: 8px;">{{ rechargeError }}</div>
      <button class="btn" :disabled="recharging" @click="doRecharge">{{ recharging ? '创建中…' : '创建充值单' }}</button>
      <div v-if="rechargeRedirect" style="margin-top: 12px;"><a class="btn" :href="rechargeRedirect" target="_blank">跳转到收银台</a></div>
      <div v-if="rechargeQrcode" style="margin-top: 12px;">
        <img :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(rechargeQrcode)}`" alt="支付二维码" />
        <div class="muted">支付完成后余额自动入账（赠送同步到账）</div>
      </div>
    </div>

    <!-- 礼品卡兑换 -->
    <div v-if="tab === 'giftcard'" class="card" style="max-width: 480px;">
      <div class="field">
        <label>礼品卡兑换码</label>
        <input class="input" v-model="giftCode" type="text" placeholder="卡密兑换码" @keyup.enter="doRedeem" />
        <div class="muted">兑换后余额即时到账；连续失败将临时锁定（防爆破）</div>
      </div>
      <div v-if="giftError" class="error" style="margin-bottom: 8px;">{{ giftError }}</div>
      <div v-if="giftOk" class="success" style="margin-bottom: 8px;">兑换成功：到账 {{ formatMoney(giftOk.amount_cents) }}，当前余额 {{ formatMoney(giftOk.balance_after_cents) }}</div>
      <button class="btn" :disabled="redeeming" @click="doRedeem">{{ redeeming ? '兑换中…' : '兑换' }}</button>
    </div>

    <!-- 账户安全（P3-10：改密吊销全部会话、新 token 保当前；改邮箱唯一校验） -->
    <div v-if="tab === 'security'" style="display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 420px)); gap: 16px;">
      <div class="card">
        <h3 style="margin-bottom: 12px;">修改密码</h3>
        <div class="field">
          <label>当前密码</label>
          <input class="input" v-model="oldPwd" type="password" placeholder="当前密码" />
        </div>
        <div class="field">
          <label>新密码</label>
          <input class="input" v-model="newPwd" type="password" placeholder="至少 6 位" />
        </div>
        <div class="field">
          <label>确认新密码</label>
          <input class="input" v-model="newPwd2" type="password" placeholder="再输入一次" />
        </div>
        <div v-if="pwdError" class="error" style="margin-bottom: 8px;">{{ pwdError }}</div>
        <div v-if="pwdOk" class="success" style="margin-bottom: 8px;">已修改（其他设备将退出登录）</div>
        <button class="btn" :disabled="changingPwd" @click="doChangePwd">{{ changingPwd ? '提交中…' : '修改密码' }}</button>
      </div>
      <div class="card">
        <h3 style="margin-bottom: 12px;">修改邮箱</h3>
        <div class="muted" style="margin-bottom: 8px;">当前：{{ level?.points !== undefined ? '' : '' }}{{ meEmail || '未设置' }}</div>
        <div class="field">
          <label>新邮箱</label>
          <input class="input" v-model="newEmail" type="email" placeholder="you@example.com" />
        </div>
        <div v-if="emailError" class="error" style="margin-bottom: 8px;">{{ emailError }}</div>
        <div v-if="emailOk" class="success" style="margin-bottom: 8px;">邮箱已更新（找回密码将发往新邮箱）</div>
        <button class="btn secondary" :disabled="changingEmail" @click="doChangeEmail">{{ changingEmail ? '提交中…' : '更新邮箱' }}</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import {
  getBalance, getMyLevel, listMyOrders, cancelMyOrder, listTransactions,
  createRecharge, redeemGiftcard, changePassword, updateProfile, me,
  type BalanceReply, type MyLevelReply, type MyOrderItem, type WalletTransaction
} from '@/api';
import { api, formatMoney, formatSignedMoney, setToken, centsToYuan } from '@/api/client';

const tab = ref<'overview' | 'orders' | 'transactions' | 'recharge' | 'giftcard' | 'security'>('overview');
const balance = ref<BalanceReply | null>(null);
const level = ref<MyLevelReply | null>(null);

// 订单
const orders = ref<MyOrderItem[]>([]);
const ordersPage = ref(1);
const ordersTotal = ref(0);
const ordersPageSize = 10;

// 流水
const transactions = ref<WalletTransaction[]>([]);
const txPage = ref(1);
const txTotal = ref(0);
const txPageSize = 15;

// 充值
const rechargeYuan = ref<number | null>(null);
const rechargeChannel = ref('alipay');
const recharging = ref(false);
const rechargeError = ref('');
const rechargeRedirect = ref('');
const rechargeQrcode = ref('');
const rechargeMeta = ref<{ min_amount: number; max_amount: number } | null>(null);
const giftTiers = ref<{ amount: number; gift_balance?: number; gift_points?: number }[]>([]);

// 礼品卡
const giftCode = ref('');
const giftError = ref('');
const giftOk = ref<{ amount_cents: number; balance_after_cents: number } | null>(null);
const redeeming = ref(false);

onMounted(async () => {
  const [b, l] = await Promise.all([getBalance(), getMyLevel()]);
  balance.value = b.data;
  level.value = l.data;
  // 充值档位（T0 公开下发：recharge.enabled/min_amount/max_amount/gift_tiers）
  const cfg = await api.get<{ entries: { key: string; value_json: string }[] }>('/config');
  const find = (k: string) => cfg.data?.entries?.find((e) => e.key === k)?.value_json;
  const min = find('recharge.min_amount');
  const max = find('recharge.max_amount');
  if (min && max) {
    rechargeMeta.value = { min_amount: Number(JSON.parse(min)), max_amount: Number(JSON.parse(max)) };
  }
  const tiers = find('recharge.gift_tiers');
  if (tiers) {
    try { giftTiers.value = JSON.parse(tiers); } catch { /* 非法配置忽略 */ }
  }
});

function switchTab(t: 'overview' | 'orders' | 'transactions' | 'recharge' | 'giftcard' | 'security') {
  tab.value = t;
  if (t === 'orders' && !orders.value.length) loadOrders(1);
  if (t === 'transactions' && !transactions.value.length) loadTx(1);
}

async function loadOrders(page: number) {
  const { data } = await listMyOrders(page, ordersPageSize);
  if (data) {
    orders.value = data.orders || [];
    ordersTotal.value = data.total;
    ordersPage.value = page;
  }
}

async function loadTx(page: number) {
  const { data } = await listTransactions(page, txPageSize);
  if (data) {
    transactions.value = data.transactions || [];
    txTotal.value = data.total;
    txPage.value = page;
  }
}

async function cancel(orderNo: string) {
  if (!confirm(`确认取消订单 ${orderNo}？`)) return;
  const { error } = await cancelMyOrder(orderNo);
  if (error) {
    alert(error);
    return;
  }
  loadOrders(ordersPage.value);
}

function pickTier(tier: { amount: number }) {
  rechargeYuan.value = centsToYuan(tier.amount);
}

async function doRecharge() {
  if (!rechargeYuan.value || rechargeYuan.value <= 0) {
    rechargeError.value = '请输入充值金额';
    return;
  }
  recharging.value = true;
  rechargeError.value = '';
  rechargeRedirect.value = '';
  rechargeQrcode.value = '';
  // 充值单创建（金额分；服务端按档位裁决与赠送计算）
  const { data, error } = await createRecharge(Math.round(rechargeYuan.value * 100), rechargeChannel.value);
  recharging.value = false;
  if (error || !data) {
    rechargeError.value = error || '创建失败';
    return;
  }
  // 支付载荷三形态（与 Payment.vue 同构）
  if (data.type === 'redirect') rechargeRedirect.value = data.payload;
  else if (data.type === 'qrcode') rechargeQrcode.value = data.payload;
  else if (data.type === 'params') {
    try {
      const p = JSON.parse(data.payload);
      rechargeRedirect.value = p.url || '';
      rechargeError.value = p.url ? '' : '支付参数异常';
    } catch {
      rechargeError.value = '支付参数异常';
    }
  }
}

async function doRedeem() {
  if (!giftCode.value.trim()) {
    giftError.value = '请输入兑换码';
    return;
  }
  redeeming.value = true;
  giftError.value = '';
  giftOk.value = null;
  const { data, error } = await redeemGiftcard(giftCode.value.trim());
  redeeming.value = false;
  if (error || !data) {
    giftError.value = error || '卡密无效';
    return;
  }
  giftOk.value = data;
  giftCode.value = '';
  const b = await getBalance();
  balance.value = b.data;
}

// security（P3-10）
const oldPwd = ref('');
const newPwd = ref('');
const newPwd2 = ref('');
const pwdError = ref('');
const pwdOk = ref(false);
const changingPwd = ref(false);
const meEmail = ref('');
const newEmail = ref('');
const emailError = ref('');
const emailOk = ref(false);
const changingEmail = ref(false);

async function loadMe() {
  const { data } = await me();
  meEmail.value = data?.email || '';
}
loadMe();

async function doChangePwd() {
  pwdError.value = '';
  pwdOk.value = false;
  if (newPwd.value.length < 6) { pwdError.value = '新密码至少 6 位'; return; }
  if (newPwd.value !== newPwd2.value) { pwdError.value = '两次输入不一致'; return; }
  changingPwd.value = true;
  const { data, error } = await changePassword({ old_password: oldPwd.value, new_password: newPwd.value });
  changingPwd.value = false;
  if (error || !data) { pwdError.value = error || '当前密码错误'; return; }
  setToken(data.token); // 新 token 保当前会话（其他设备已被吊销）
  pwdOk.value = true;
  oldPwd.value = ''; newPwd.value = ''; newPwd2.value = '';
}

async function doChangeEmail() {
  emailError.value = '';
  emailOk.value = false;
  if (!newEmail.value.includes('@')) { emailError.value = '请输入有效邮箱'; return; }
  changingEmail.value = true;
  const { data, error } = await updateProfile({ email: newEmail.value.trim() });
  changingEmail.value = false;
  if (error || !data) { emailError.value = error || '邮箱可能已被占用'; return; }
  meEmail.value = data.email;
  newEmail.value = '';
  emailOk.value = true;
}

function statusText(s: string): string {
  return ({ pending: '待支付', paid: '已支付', delivered: '已发货', completed: '已完成', cancelled: '已取消', expired: '已过期', refunded: '已退款' } as Record<string, string>)[s] || s;
}
function statusBadge(s: string): string {
  return ({ pending: 'badge orange', paid: 'badge blue', delivered: 'badge green', completed: 'badge green', cancelled: 'badge gray', expired: 'badge gray', refunded: 'badge red' } as Record<string, string>)[s] || 'badge gray';
}
function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '';
}
</script>
