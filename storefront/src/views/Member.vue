<template>
  <div>
    <MemberTabs :active="tab" />

    <!-- 总览：余额四数 + 等级进度 + 快捷入口 -->
    <div v-if="tab === 'overview'">
      <div class="stat-grid">
        <div class="card"><div class="muted">可用余额</div><div class="stat-num">{{ formatMoney(balance?.available_cents ?? 0) }}</div></div>
        <div class="card"><div class="muted">冻结中</div><div class="stat-num">{{ formatMoney(balance?.locked_cents ?? 0) }}</div></div>
        <div class="card"><div class="muted">积分</div><div class="stat-num">{{ level?.points ?? balance?.points ?? 0 }}</div></div>
        <div class="card"><div class="muted">累计消费</div><div class="stat-num">{{ formatMoney(level?.consumed_cents ?? 0) }}</div></div>
      </div>
      <div class="card lv-card" v-if="level">
        <div class="lv-head">
          <div class="lv-cur">
            <div class="muted lv-label">当前等级</div>
            <div class="lv-name-row">
              <span class="lv-name">{{ level.current?.name || '普通会员' }}</span>
              <span v-if="level.current?.discount" class="tag">{{ (level.current.discount / 100).toFixed(0) }} 折</span>
            </div>
          </div>
          <span v-if="level.next" class="lv-next">距 {{ level.next.name }} <span class="lv-arrow">›</span></span>
          <span v-else class="lv-max">已满级 🏆</span>
        </div>
        <div class="lv-bar">
          <div class="progress"><div :style="{ width: `${levelPercent}%` }"></div></div>
          <span class="lv-percent">{{ levelPercent }}%</span>
        </div>
        <div class="lv-gaps" v-if="rechargeGapCents > 0 || consumeGapCents > 0">
          <span v-if="rechargeGapCents > 0" class="lv-gap">还需充值 <b>{{ formatMoney(rechargeGapCents) }}</b></span>
          <span v-if="consumeGapCents > 0" class="lv-gap">还需消费 <b>{{ formatMoney(consumeGapCents) }}</b></span>
        </div>
        <div class="actions ov-actions" style="margin-top: 16px;">
          <button class="btn" @click="switchTab('recharge')">去充值</button>
          <router-link class="btn secondary" to="/points">积分商城</router-link>
          <router-link class="btn secondary" to="/fetch">去取货</router-link>
          <router-link class="btn secondary" to="/coupons">我的优惠券</router-link>
        </div>
      </div>
      <div v-else class="card muted">加载中…</div>

      <!-- 我的推广码（分销入口；点击切到推广营销 tab） -->
      <div class="card" style="margin-top: 16px; display: flex; gap: 14px; align-items: center; flex-wrap: wrap; cursor: pointer;" @click="switchTab('promo')">
        <div>
          <div class="muted">我的推广码</div>
          <div style="font-family: ui-monospace, Menlo, monospace; font-size: 22px; font-weight: 800; letter-spacing: 2px; color: #2563eb; margin-top: 2px;">{{ myPromoCode || '点击开通' }}</div>
        </div>
        <div style="flex: 1; min-width: 200px;" class="muted">
          分享推广链接给好友，注册/下单均可赚三级佣金 →
        </div>
        <span style="font-size: 22px;">🔗</span>
      </div>
    </div>

    <!-- 我的订单 -->
    <div v-if="tab === 'orders'" class="card">
      <table class="list table-desktop">
        <thead><tr><th>订单号</th><th>状态</th><th>金额</th><th>件数</th><th>时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="o in orders" :key="o.order_no">
            <td>{{ o.order_no }}</td>
            <td><span :class="statusBadge(o.status)">{{ statusText(o.status) }}</span></td>
            <td class="price">{{ formatMoney(o.total_cents) }}</td>
            <td>{{ o.item_count }}</td>
            <td class="muted">{{ fmtTime(o.created_at) }}</td>
            <td class="actions">
              <router-link class="btn secondary" :to="`/payment/${o.order_no}`" v-if="o.status === 'pending_payment'">去支付</router-link>
              <router-link
                class="btn secondary"
                :to="`/fetch?order_no=${o.order_no}`"
                v-if="['paid', 'fulfilling', 'partially_delivered', 'delivered', 'completed'].includes(o.status)"
              >取货</router-link>
              <router-link class="btn secondary" :to="`/order/${o.order_no}`">详情</router-link>
              <button class="btn secondary" v-if="o.status === 'pending_payment'" @click="cancel(o.order_no)">取消</button>
            </td>
          </tr>
          <tr v-if="!orders.length"><td colspan="6" class="muted" style="text-align: center;">暂无订单</td></tr>
        </tbody>
      </table>
      <!-- 移动端订单卡片（大厂「我的订单」样式） -->
      <div class="table-cards">
        <div v-for="o in orders" :key="o.order_no" class="mcard">
          <div class="mcard-row">
            <span class="mcard-title">{{ o.order_no }}</span>
            <span :class="statusBadge(o.status)">{{ statusText(o.status) }}</span>
          </div>
          <div class="mcard-row">
            <span class="muted">{{ fmtTime(o.created_at) }} · {{ o.item_count }} 件</span>
            <span class="price">{{ formatMoney(o.total_cents) }}</span>
          </div>
          <div class="mcard-row">
            <span></span>
            <span class="actions">
              <router-link class="btn secondary" :to="`/payment/${o.order_no}`" v-if="o.status === 'pending_payment'">去支付</router-link>
              <router-link
                class="btn secondary"
                :to="`/fetch?order_no=${o.order_no}`"
                v-if="['paid', 'fulfilling', 'partially_delivered', 'delivered', 'completed'].includes(o.status)"
              >取货</router-link>
              <router-link class="btn secondary" :to="`/order/${o.order_no}`">详情</router-link>
              <button class="btn secondary" v-if="o.status === 'pending_payment'" @click="cancel(o.order_no)">取消</button>
            </span>
          </div>
        </div>
        <div v-if="!orders.length" class="muted" style="text-align: center; padding: 16px 0;">暂无订单</div>
      </div>
      <div class="actions" style="margin-top: 12px;" v-if="ordersTotal > ordersPageSize">
        <button class="btn secondary" :disabled="ordersPage <= 1" @click="loadOrders(ordersPage - 1)">上一页</button>
        <span class="muted">{{ ordersPage }} / {{ Math.ceil(ordersTotal / ordersPageSize) }}</span>
        <button class="btn secondary" :disabled="ordersPage >= Math.ceil(ordersTotal / ordersPageSize)" @click="loadOrders(ordersPage + 1)">下一页</button>
      </div>
    </div>

    <!-- 余额流水 -->
    <div v-if="tab === 'transactions'" class="card">
      <table class="list table-desktop">
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
      <!-- 移动端流水卡片 -->
      <div class="table-cards">
        <div v-for="t in transactions" :key="t.id" class="mcard">
          <div class="mcard-row">
            <span class="mcard-title">{{ t.type }}</span>
            <span :class="t.amount_cents >= 0 ? 'success' : 'error'" style="font-weight: 700;">{{ formatSignedMoney(t.amount_cents) }}</span>
          </div>
          <div class="mcard-row">
            <span class="muted">{{ fmtTime(t.created_at) }}</span>
            <span class="muted">余额 {{ formatMoney(t.balance_after_cents) }}</span>
          </div>
          <div class="mcard-row" v-if="t.remark || t.reference">
            <span class="muted" style="word-break: break-all;">{{ t.remark || t.reference }}</span>
          </div>
        </div>
        <div v-if="!transactions.length" class="muted" style="text-align: center; padding: 16px 0;">暂无流水</div>
      </div>
      <div class="actions" style="margin-top: 12px;" v-if="txTotal > txPageSize">
        <button class="btn secondary" :disabled="txPage <= 1" @click="loadTx(txPage - 1)">上一页</button>
        <span class="muted">{{ txPage }} / {{ Math.ceil(txTotal / txPageSize) }}</span>
        <button class="btn secondary" :disabled="txPage >= Math.ceil(txTotal / txPageSize)" @click="loadTx(txPage + 1)">下一页</button>
      </div>
    </div>

    <!-- 充值（方式级收银台：余额条 + 档位/自定义金额 + 支付方式网格 + 结果态；服务端裁决） -->
    <div v-if="tab === 'recharge'" class="recharge-page">
      <!-- 余额概览条 -->
      <div class="card rc-balance">
        <div>
          <div class="muted rc-label">当前可用余额</div>
          <div class="rc-balance-num">{{ formatMoney(balance?.available_cents ?? 0) }}</div>
        </div>
        <div class="rc-balance-side muted">
          <span>冻结中 {{ formatMoney(balance?.locked_cents ?? 0) }}</span>
          <span>积分 {{ level?.points ?? balance?.points ?? 0 }}</span>
        </div>
      </div>

      <!-- 表单态 -->
      <div v-if="rechargePhase === 'form'" class="rc-layout">
        <!-- 左：充值金额 -->
        <div class="card rc-panel">
          <div class="rc-title">充值金额</div>
          <div v-if="giftTiers.length" class="rc-tiers">
            <button v-for="tier in giftTiers" :key="tier.amount" type="button" class="rc-tier"
                    :class="{ active: pickedTier === tier.amount }" @click="pickTier(tier)">
              <span class="rc-tier-amount">{{ formatMoney(tier.amount) }}</span>
              <!-- 余额/积分赠送并列展示（此前 v-else-if 两者都配时积分被吞） -->
              <span v-if="tier.gift_balance" class="rc-tier-gift">送 {{ formatMoney(tier.gift_balance) }}</span>
              <span v-if="tier.gift_points" class="rc-tier-gift">送 {{ tier.gift_points }} 积分</span>
            </button>
          </div>
          <div class="rc-custom">
            <span class="rc-yen">¥</span>
            <input v-model.number="rechargeYuan" type="number" min="1" step="0.01" placeholder="输入充值金额" @input="pickedTier = 0" />
            <button v-if="rechargeYuan" type="button" class="rc-clear" title="清空" @click="clearAmount">×</button>
          </div>
          <div v-if="rechargeMeta" class="muted rc-limit">单笔限额 {{ formatMoney(rechargeMeta.min_amount) }} ~ {{ formatMoney(rechargeMeta.max_amount) }}<template v-if="giftTiers.length && !giftTiers.some((t) => t.gift_balance || t.gift_points)">；充值赠送见支付结果</template></div>
          <!-- 到账预览（命中赠送档位时） -->
          <div v-if="giftPreview" class="rc-gift-preview">
            <span>🧧 到账</span><b>{{ formatMoney(giftPreview.total) }}</b>
            <span v-if="giftPreview.gift_balance">含赠送 {{ formatMoney(giftPreview.gift_balance) }}</span>
            <span v-if="giftPreview.gift_points">含赠送 {{ giftPreview.gift_points }} 积分</span>
          </div>
        </div>

        <!-- 右：支付方式 + 提交 -->
        <div class="card rc-panel">
          <div class="rc-title">支付方式</div>
          <PayChannelGrid :options="payOptions" :channel="rechargeChannel" :method="rechargeMethod"
                          @select="(ch, m) => { rechargeChannel = ch; rechargeMethod = m; }">
            <template #empty>暂无可用的充值渠道，请联系客服</template>
          </PayChannelGrid>
          <div v-if="rechargeError" class="error" style="margin-top: 12px;">{{ rechargeError }}</div>
          <button class="rc-submit" :disabled="!rechargeChannel || !rechargeYuan || recharging" @click="doRecharge">
            {{ recharging ? '创建充值单…' : rechargeYuan ? `立即充值 ${formatMoney(Math.round(rechargeYuan * 100))}` : '请输入充值金额' }}
          </button>
          <div class="rc-assure">🔒 支付过程安全加密 · 支付成功后余额与赠送自动到账</div>
        </div>
      </div>

      <!-- 扫码支付态（本地生成二维码，不依赖第三方服务） -->
      <div v-else-if="rechargePhase === 'qrcode'" class="card rc-result">
        <div class="rc-qr-head">
          <span class="rc-qr-icon">{{ selectedOption?.emoji || '💳' }}</span>
          <div>
            <div class="rc-qr-title">{{ selectedOption?.name || '扫码支付' }}</div>
            <div class="muted">请使用手机扫一扫完成支付</div>
          </div>
        </div>
        <div class="rc-qr-box">
          <img v-if="rechargeQrcode" :src="rechargeQrcode" alt="支付二维码" />
          <div v-else class="muted" style="padding: 60px 0;">生成二维码中…</div>
        </div>
        <div class="rc-qr-amount">充值金额 <b>{{ formatMoney(pendingAmountCents) }}</b></div>
        <div class="muted rc-qr-hint">支付完成后余额与赠送自动到账（如未到账请刷新本页）</div>
        <button class="rc-change" @click="backToForm">← 更换支付方式</button>
      </div>

      <!-- 跳转收银台态 -->
      <div v-else class="card rc-result">
        <div class="rc-redirect-icon">🚀</div>
        <div class="rc-qr-title">正在前往收银台</div>
        <div class="muted">使用{{ selectedOption?.name || '所选渠道' }}完成支付；充值单金额 {{ formatMoney(pendingAmountCents) }}</div>
        <div class="rc-btn-row">
          <button class="btn" @click="openRedirect">重新打开收银台</button>
          <button class="btn secondary" @click="copyLink">复制支付链接</button>
          <button class="btn secondary" @click="backToForm">更换支付方式</button>
        </div>
      </div>
    </div>

    <!-- 礼品卡兑换（与充值页同构：余额条 + 主卡 + 说明，视觉节奏一致） -->
    <div v-if="tab === 'giftcard'" class="recharge-page">
      <div class="card rc-balance">
        <div>
          <div class="muted rc-label">当前可用余额</div>
          <div class="rc-balance-num">{{ formatMoney(balance?.available_cents ?? 0) }}</div>
        </div>
        <div class="rc-balance-side muted">
          <span>冻结中 {{ formatMoney(balance?.locked_cents ?? 0) }}</span>
          <span>积分 {{ level?.points ?? balance?.points ?? 0 }}</span>
        </div>
      </div>
      <div class="card gc-card">
        <div class="gc-title">礼品卡兑换</div>
        <div class="field">
          <label>礼品卡兑换码</label>
          <input class="input gc-input" v-model="giftCode" type="text" placeholder="输入卡密兑换码" @keyup.enter="doRedeem" />
          <div class="muted">兑换后余额即时到账；连续失败将临时锁定（防爆破）</div>
        </div>
        <div v-if="giftError" class="error" style="margin-bottom: 8px;">{{ giftError }}</div>
        <div v-if="giftOk" class="success" style="margin-bottom: 8px;">兑换成功：到账 {{ formatMoney(giftOk.amount_cents) }}，当前余额 {{ formatMoney(giftOk.balance_after_cents) }}</div>
        <button class="btn gc-submit" :disabled="redeeming" @click="doRedeem">{{ redeeming ? '兑换中…' : '立即兑换' }}</button>
      </div>
      <div class="card gc-tips">
        <div class="rc-title">兑换说明</div>
        <ul class="gc-tips-list">
          <li>在「礼品卡/卡密」渠道购买后获得兑换码，粘贴到上方输入框即可兑换</li>
          <li>兑换金额即时进入账户余额，可用于下单与充值</li>
          <li>兑换码连续输错将临时锁定；遇到问题请联系在线客服处理</li>
        </ul>
      </div>
    </div>

    <!-- 推广营销（内嵌推广中心：推广码/二维码/团队/佣金） -->
    <div v-if="tab === 'promo'">
      <Affiliate />
    </div>

    <!-- 对接申请（内嵌供货申请中心：提交申请/审核状态/凭据管理） -->
    <div v-if="tab === 'supplier'">
      <SupplierApply />
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
        <div class="muted" style="margin-bottom: 8px;">当前：{{ meEmail || '未设置' }}</div>
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
import { ref, computed, watch, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import QRCode from 'qrcode';
import {
  getBalance, getMyLevel, listMyOrders, cancelMyOrder, listTransactions,
  createRecharge, redeemGiftcard, changePassword, updateProfile, me,
  fetchPaymentChannels, type ChannelItem,
  type BalanceReply, type MyLevelReply, type MyOrderItem, type WalletTransaction
} from '@/api';
import { api, formatMoney, formatSignedMoney, setToken, centsToYuan } from '@/api/client';
import { flattenPayOptions } from '@/composables/pay-options';
import PayChannelGrid from '@/components/PayChannelGrid.vue';
import Affiliate from './Affiliate.vue';
import SupplierApply from './SupplierApply.vue';
import MemberTabs from '@/components/MemberTabs.vue';

// tab 由 ?tab= 查询参数驱动（MemberTabs 导航跳转 /member?tab=xxx）——
// 从 /withdraw 等独立页返回时能直接恢复到对应区块。
type Tab = 'overview' | 'orders' | 'transactions' | 'recharge' | 'giftcard' | 'promo' | 'supplier' | 'security';
const TAB_KEYS: readonly string[] = ['overview', 'orders', 'transactions', 'recharge', 'giftcard', 'promo', 'supplier', 'security'];
function normalizeTab(v: unknown): Tab {
  return TAB_KEYS.includes(v as string) ? (v as Tab) : 'overview';
}
const route = useRoute();
const tab = ref<Tab>(normalizeTab(route.query.tab));
watch(() => route.query.tab, (v) => switchTab(normalizeTab(v)));
const balance = ref<BalanceReply | null>(null);
const level = ref<MyLevelReply | null>(null);

// 等级进度（proto3 零值省略/-1 未配置均归一为不展示；>0 才渲染差额胶囊）
const levelPercent = computed(() => level.value?.progress?.percent ?? 100);
const rechargeGapCents = computed(() => Math.max(level.value?.progress?.recharge_gap_cents ?? 0, 0));
const consumeGapCents = computed(() => Math.max(level.value?.progress?.consume_gap_cents ?? 0, 0));

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

// 充值（方式级收银台：表单 → 扫码/跳转结果态）
type RechargePhase = 'form' | 'qrcode' | 'redirect';
const rechargePhase = ref<RechargePhase>('form');
const rechargeYuan = ref<number | null>(null);
const pickedTier = ref(0); // 命中的赠送档位（amount 分）；自定义输入时清零
const rechargeChannels = ref<ChannelItem[]>([]);
const rechargeChannel = ref('');
const rechargeMethod = ref('');
const recharging = ref(false);
const rechargeError = ref('');
const rechargeRedirect = ref('');
const rechargeQrcode = ref(''); // 二维码 data URL（本地生成，不依赖第三方服务）
const pendingAmountCents = ref(0);
const rechargeMeta = ref<{ min_amount: number; max_amount: number } | null>(null);
const giftTiers = ref<{ amount: number; gift_balance?: number; gift_points?: number }[]>([]);

// 充值渠道：过滤余额渠道（服务端拒绝 wallet 充值）后展平为方式级选项
const rechargePayChannels = computed(() => rechargeChannels.value.filter((c) => c.driver !== 'wallet'));
const payOptions = computed(() => flattenPayOptions(rechargePayChannels.value));
const selectedOption = computed(() =>
  payOptions.value.find((o) => o.channel === rechargeChannel.value && o.method === rechargeMethod.value) || null,
);
// 到账预览：命中赠送档位时显示本金+赠送合计
const giftPreview = computed(() => {
  if (!rechargeYuan.value || rechargeYuan.value <= 0) return null;
  const cents = Math.round(rechargeYuan.value * 100);
  const tier = giftTiers.value.find((t) => t.amount === cents);
  if (!tier || (!tier.gift_balance && !tier.gift_points)) return null;
  return { total: cents + (tier.gift_balance || 0), gift_balance: tier.gift_balance, gift_points: tier.gift_points };
});

// 礼品卡
const giftCode = ref('');
const giftError = ref('');
const giftOk = ref<{ amount_cents: number; balance_after_cents: number } | null>(null);
const redeeming = ref(false);

onMounted(async () => {
  // 直接带 ?tab= 进入（如从提现页返回）：触发对应区块懒加载
  if (tab.value === 'orders' && !orders.value.length) loadOrders(1);
  if (tab.value === 'transactions' && !transactions.value.length) loadTx(1);
  const [b, l] = await Promise.all([getBalance(), getMyLevel()]);
  balance.value = b.data;
  level.value = l.data;
  // 充值档位（T0 公开下发：recharge.enabled/min_amount/max_amount/gift_tiers）
  const cfg = await api.get<{ entries: { key: string; value_json: string }[] }>('/config');
  const find = (k: string) => cfg.data?.entries?.find((e) => e.key === k)?.value_json;
  const min = find('recharge.min_amount');
  const max = find('recharge.max_amount');
  if (min && max) {
    try {
      const mn = Number(JSON.parse(min)), mx = Number(JSON.parse(max));
      if (Number.isFinite(mn) && Number.isFinite(mx) && mx > 0) {
        rechargeMeta.value = { min_amount: mn, max_amount: mx };
      }
    } catch { /* 非法配置忽略 */ }
  }
  const tiers = find('recharge.gift_tiers');
  if (tiers) {
    // 目录默认值为 JSON null：parse 得 null 不能直接赋值（render 读 .length 会崩）
    try {
      const parsed = JSON.parse(tiers);
      if (Array.isArray(parsed) && parsed.length) giftTiers.value = parsed;
    } catch { /* 非法配置忽略 */ }
  }
  loadRechargeChannels();
});

function switchTab(t: 'overview' | 'orders' | 'transactions' | 'recharge' | 'giftcard' | 'promo' | 'supplier' | 'security') {
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
  pickedTier.value = tier.amount;
}

function clearAmount() {
  rechargeYuan.value = null;
  pickedTier.value = 0;
  rechargeError.value = '';
}

async function loadRechargeChannels() {
  const { data, error } = await fetchPaymentChannels();
  if (!error && data) {
    rechargeChannels.value = data.channels || [];
    const first = payOptions.value[0];
    rechargeChannel.value = first?.channel || '';
    rechargeMethod.value = first?.method || '';
  }
}

function backToForm() {
  rechargePhase.value = 'form';
  rechargeQrcode.value = '';
  rechargeRedirect.value = '';
}

async function doRecharge() {
  const cents = rechargeYuan.value ? Math.round(rechargeYuan.value * 100) : 0;
  if (!cents || cents <= 0) {
    rechargeError.value = '请输入充值金额';
    return;
  }
  if (rechargeMeta.value && (cents < rechargeMeta.value.min_amount || cents > rechargeMeta.value.max_amount)) {
    rechargeError.value = `充值金额需在 ${formatMoney(rechargeMeta.value.min_amount)} ~ ${formatMoney(rechargeMeta.value.max_amount)} 之间`;
    return;
  }
  if (!rechargeChannel.value) {
    rechargeError.value = '请选择支付方式';
    return;
  }
  recharging.value = true;
  rechargeError.value = '';
  pendingAmountCents.value = cents;
  // 充值单创建（金额分 + 方式级 channel/method；服务端按档位裁决与赠送计算）
  const { data, error: err } = await createRecharge(cents, rechargeChannel.value, rechargeMethod.value);
  recharging.value = false;
  if (err || !data) {
    rechargeError.value = err || '创建失败';
    return;
  }
  // 支付载荷三形态（与 Payment.vue 同构）：qrcode 本地渲染；params 自动表单提交
  const payload = data.payload || '';
  if (data.type === 'qrcode') {
    let content = payload;
    try { const parsed = JSON.parse(payload); content = parsed.code_url || payload; } catch { /* 原文即内容 */ }
    rechargeQrcode.value = await makeQr(content);
    rechargePhase.value = 'qrcode';
  } else if (data.type === 'params') {
    try {
      const p = JSON.parse(payload);
      if (p.url) {
        submitForm(p.url, p.params || {});
        rechargeRedirect.value = p.url;
        rechargePhase.value = 'redirect';
      } else {
        rechargeError.value = '支付参数异常';
      }
    } catch {
      rechargeError.value = '支付参数异常';
    }
  } else {
    let url = payload;
    try { const parsed = JSON.parse(payload); url = parsed.url || payload; } catch { /* 原文即 URL */ }
    rechargeRedirect.value = url;
    rechargePhase.value = 'redirect';
    openRedirect();
  }
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
  if (!rechargeRedirect.value) return;
  // 新窗口打开 + noopener 防反向控制（与支付页同款纪律）
  window.open(rechargeRedirect.value, '_blank', 'noopener');
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
  if (!rechargeRedirect.value) return;
  try {
    await navigator.clipboard.writeText(rechargeRedirect.value);
    window.alert('支付链接已复制');
  } catch { /* 忽略 */ }
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
  myPromoCode.value = data?.promo_code || '';
}
const myPromoCode = ref('');
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
/* ── 移动端表格→卡片（订单/流水；桌面表格不受影响）── */
.table-cards { display: none; }
.mcard {
  border: 1px solid #f3f4f6; border-radius: 10px; padding: 12px;
  display: flex; flex-direction: column; gap: 8px; background: #fafbfc;
}
.mcard-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.mcard-title { font-weight: 600; font-size: 14px; color: #111827; word-break: break-all; text-align: left; }
.mcard .btn { padding: 6px 12px; font-size: 13px; }
@media (max-width: 768px) {
  .table-desktop { display: none; }
  .table-cards { display: flex; flex-direction: column; gap: 10px; }
}

/* ── 会员等级卡（移动端优先：等级名独占一行、进度条满宽带百分比、差额拆独立胶囊）── */
.lv-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; margin-bottom: 12px; }
.lv-cur { min-width: 0; }
.lv-label { font-size: 13px; margin-bottom: 4px; }
.lv-name-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.lv-name { font-size: 20px; font-weight: 800; color: #111827; line-height: 1.25; }
.lv-next {
  flex-shrink: 0; display: inline-flex; align-items: center; gap: 2px; white-space: nowrap;
  font-size: 13px; font-weight: 600; color: #2563eb; background: #eff6ff;
  padding: 4px 10px; border-radius: 999px; margin-top: 2px;
}
.lv-arrow { font-size: 15px; line-height: 1; }
.lv-max {
  flex-shrink: 0; white-space: nowrap; font-size: 13px; font-weight: 600;
  color: #b45309; background: #fef3c7; padding: 4px 12px; border-radius: 999px; margin-top: 2px;
}
.lv-bar { display: flex; align-items: center; gap: 10px; }
.lv-bar .progress { flex: 1; min-width: 0; }
.lv-percent { flex-shrink: 0; font-size: 13px; font-weight: 700; color: #2563eb; font-variant-numeric: tabular-nums; }
.lv-gaps { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 10px; }
.lv-gap {
  font-size: 13px; color: #6b7280; background: #f9fafb; border: 1px solid #f3f4f6;
  padding: 5px 12px; border-radius: 999px;
}
.lv-gap b { color: #ff5722; font-weight: 700; }

/* ── 充值（方式级收银台，与支付页同视觉语言）── */
.recharge-page { max-width: 760px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px; }
.rc-balance {
  display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap;
  background: linear-gradient(135deg, #eff6ff, #fff);
}
.rc-label { font-size: 13px; }
.rc-balance-num { font-size: 26px; font-weight: 800; color: #111827; margin-top: 2px; }
.rc-balance-side { display: flex; flex-direction: column; gap: 4px; font-size: 13px; text-align: right; }

.rc-layout { display: grid; grid-template-columns: 1fr; gap: 16px; align-items: start; }
@media (min-width: 768px) { .rc-layout { grid-template-columns: 1fr 1fr; } }
.rc-panel { padding: 18px; }
.rc-title { font-size: 15px; font-weight: 700; color: #111827; margin-bottom: 14px; }

/* 档位网格 */
.rc-tiers { display: grid; grid-template-columns: repeat(2, 1fr); gap: 10px; margin-bottom: 14px; }
.rc-tier {
  position: relative; display: flex; flex-direction: column; align-items: center; gap: 3px;
  border: 2px solid #e5e7eb; border-radius: 12px; padding: 12px 8px;
  background: #fff; cursor: pointer; transition: all 0.15s; font-family: inherit;
}
.rc-tier:hover { border-color: rgba(37, 99, 235, 0.4); }
.rc-tier.active { border-color: #2563eb; background: #eff6ff; box-shadow: 0 2px 8px rgba(37, 99, 235, 0.12); }
.rc-tier-amount { font-size: 16px; font-weight: 700; color: #111827; }
.rc-tier-gift {
  font-size: 12px; color: #b45309; background: #fef3c7;
  padding: 1px 8px; border-radius: 999px;
}

/* 自定义金额输入（¥ 前缀 + 清空） */
.rc-custom {
  display: flex; align-items: center; gap: 6px;
  border: 1px solid #e5e7eb; border-radius: 10px; padding: 0 12px;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.rc-custom:focus-within { border-color: #2563eb; box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.15); }
.rc-yen { font-size: 18px; font-weight: 700; color: #6b7280; }
.rc-custom input {
  flex: 1; border: none; outline: none; padding: 12px 0;
  font-size: 16px; font-weight: 600; color: #111827; background: none; min-width: 0;
}
.rc-clear {
  border: none; background: #f3f4f6; color: #6b7280; cursor: pointer;
  width: 22px; height: 22px; border-radius: 999px; font-size: 14px; line-height: 1; flex-shrink: 0;
}
.rc-clear:hover { background: #e5e7eb; color: #374151; }
.rc-limit { font-size: 12px; margin-top: 8px; }

/* 到账预览 */
.rc-gift-preview {
  margin-top: 12px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  background: #fffbeb; border: 1px solid #fde68a; border-radius: 10px;
  padding: 10px 12px; font-size: 13px; color: #92400e;
}
.rc-gift-preview b { font-size: 15px; }

/* 提交 */
.rc-submit {
  width: 100%; margin-top: 16px; padding: 14px 0; border: none; cursor: pointer;
  border-radius: 12px; font-size: 16px; font-weight: 700; color: #fff;
  background: linear-gradient(90deg, #2563eb, #1d4ed8);
  box-shadow: 0 6px 18px rgba(37, 99, 235, 0.3); transition: all 0.15s;
  font-family: inherit;
}
.rc-submit:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 8px 24px rgba(37, 99, 235, 0.4); }
.rc-submit:disabled { opacity: 0.5; cursor: not-allowed; }
.rc-assure { text-align: center; font-size: 12px; color: #9ca3af; margin-top: 10px; }

/* 结果态（扫码/跳转） */
.rc-result { padding: 28px 20px; text-align: center; }
.rc-qr-head { display: flex; align-items: center; gap: 10px; text-align: left; justify-content: center; margin-bottom: 16px; }
.rc-qr-icon {
  width: 38px; height: 38px; border-radius: 10px; flex-shrink: 0;
  background: #f1f5f9; display: inline-flex; align-items: center; justify-content: center; font-size: 18px;
}
.rc-qr-title { font-size: 15px; font-weight: 700; color: #111827; }
.rc-qr-box {
  width: 244px; margin: 0 auto; background: #fff; border: 1px solid #e5e7eb;
  border-radius: 12px; padding: 12px; box-shadow: 0 4px 12px rgba(15, 23, 42, 0.06);
}
.rc-qr-box img { width: 100%; display: block; }
.rc-qr-amount { margin-top: 14px; font-size: 14px; }
.rc-qr-amount b { color: #ff5722; font-size: 20px; }
.rc-qr-hint { margin-top: 8px; font-size: 13px; }
.rc-change { margin-top: 14px; border: none; background: none; color: #2563eb; font-size: 13px; cursor: pointer; }
.rc-change:hover { text-decoration: underline; }
.rc-redirect-icon { font-size: 40px; margin-bottom: 10px; }
.rc-btn-row { display: flex; gap: 10px; justify-content: center; margin-top: 18px; flex-wrap: wrap; }

/* ── 礼品卡兑换（嵌于 recharge-page 容器内与充值页同构：余额条 + 主卡 + 说明）── */
.gc-card { padding: 26px 28px; }
.gc-title { font-size: 17px; font-weight: 700; color: #111827; margin-bottom: 16px; }
.gc-input { height: 48px; font-size: 16px; letter-spacing: 1px; }
.gc-tips-list { margin: 0; padding-left: 18px; color: #6b7280; font-size: 13px; line-height: 2; }
.gc-submit {
  width: 100%; margin-top: 18px; padding: 13px 0; border: none; cursor: pointer;
  border-radius: 12px; font-size: 16px; font-weight: 700; color: #fff;
  background: linear-gradient(90deg, #2563eb, #1d4ed8);
  box-shadow: 0 6px 18px rgba(37, 99, 235, 0.3); transition: all 0.15s;
  font-family: inherit;
}
.gc-submit:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 8px 24px rgba(37, 99, 235, 0.4); }
.gc-submit:disabled { opacity: 0.5; cursor: not-allowed; }

/* 总览快捷入口：移动端两列等宽（对齐大厂宫格按钮） */
@media (max-width: 768px) {
  .ov-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .ov-actions .btn { width: 100%; text-align: center; margin: 0; padding: 11px 0; }
  .gc-card { padding: 20px 16px; }
}
</style>
