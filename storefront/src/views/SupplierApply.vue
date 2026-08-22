<template>
  <div>
    <!-- 说明 -->
    <div class="card" style="margin-bottom: 16px;">
      <div style="display: flex; justify-content: space-between; flex-wrap: wrap; gap: 8px; align-items: center;">
        <div>对接申请（供货）</div>
        <span class="tag">审核通过后下发 app_id / app_key</span>
      </div>
      <div class="muted" style="margin-top: 6px; line-height: 1.7;">
        申请通过后，你的第三方站点（acg-faka / dujiao-next / 另一套 ZCard）可把本站作为上游供货方，
        填本站地址 + 下方凭据即可对接，无需改动对方代码。协议在申请时选定，一个账户对应一种面板。
        下游下单从账户「供货余额」扣款，余额不足请先充值。
      </div>
    </div>

    <!-- 申请表单 -->
    <div class="card" style="margin-bottom: 16px;">
      <div style="font-weight: 600; margin-bottom: 12px;">提交新申请</div>
      <div class="protocol-grid" style="display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 10px; margin-bottom: 12px;">
        <label
          v-for="p in protocols"
          :key="p.value"
          class="protocol-option"
          :class="{ active: form.protocol === p.value }"
          style="border: 1px solid #e5e6e8; border-radius: 10px; padding: 10px 12px; cursor: pointer; display: block;"
        >
          <input v-model="form.protocol" type="radio" :value="p.value" style="margin-right: 6px;" />
          <b>{{ p.label }}</b>
          <div class="muted" style="margin-top: 4px; line-height: 1.6;">{{ p.desc }}</div>
        </label>
      </div>
      <div class="form-grid" style="display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 10px;">
        <input v-model="form.display_name" class="input" placeholder="站点/店铺名（必填）" maxlength="100" />
        <input v-model="form.contact" class="input" placeholder="联系方式：QQ / 邮箱 / 手机" maxlength="255" />
      </div>
      <div style="margin-top: 10px;">
        <input v-model="form.notify_url" class="input" placeholder="交付回调地址（选填；直接填域名即可，支持 http/https）" maxlength="500" />
        <div class="muted" style="margin-top: 4px;">示例：shop.example.com/callback 或 http(s)://shop.example.com/callback（不填则无回调）</div>
      </div>
      <textarea v-model="form.apply_reason" class="input" style="margin-top: 10px; width: 100%; min-height: 64px;" placeholder="申请理由（可选，审核时参考）" maxlength="500"></textarea>
      <div class="actions" style="margin-top: 12px;">
        <button class="btn" :disabled="submitting" @click="submit">{{ submitting ? '提交中…' : '提交申请' }}</button>
        <span v-if="formError" style="color: #dc2626; margin-left: 10px; font-size: 13px;">{{ formError }}</span>
      </div>
    </div>

    <!-- 我的对接账户 -->
    <div class="card">
      <div style="font-weight: 600; margin-bottom: 12px;">我的对接账户（{{ accounts.length }}）</div>
      <div v-if="loading" class="muted">加载中…</div>
      <div v-else-if="!accounts.length" class="muted">暂无申请，提交上方表单即可开始对接</div>
      <div v-else class="account-list" style="display: flex; flex-direction: column; gap: 10px;">
        <div v-for="a in accounts" :key="a.id" class="account-item" style="border: 1px solid #e5e6e8; border-radius: 10px; padding: 12px;">
          <div style="display: flex; justify-content: space-between; flex-wrap: wrap; gap: 8px; align-items: center;">
            <div style="display: flex; align-items: center; flex-wrap: wrap; gap: 8px;">
              <b>{{ a.display_name }}</b>
              <span class="tag" style="margin-left: 8px;">{{ protocolLabel(a.protocol) }}</span>
              <span class="tag" :style="statusStyle(a.status)">{{ statusLabel(a.status) }}</span>
            </div>
            <div class="actions" style="gap: 8px; align-items: center;">
              <span v-if="a.status === 'approved'" style="font-size: 14px; margin-right: 4px;">
                余额 <b style="color: #2563eb; font-size: 16px;">{{ formatMoney(a.balance_cache || 0) }}</b>
              </span>
              <button v-if="a.status === 'approved'" class="btn" style="padding: 6px 14px; font-size: 13px;" @click="openRecharge(a)">充值</button>
              <button v-if="a.status === 'applying'" class="btn secondary" @click="cancel(a)">撤销申请</button>
              <button v-if="a.status === 'approved'" class="btn secondary" @click="showCredentials(a)">{{ credOpenId === a.id ? '收起凭据' : '查看凭据' }}</button>
              <button v-if="a.status === 'approved'" class="btn secondary" @click="regenerate(a)">重置密钥</button>
            </div>
          </div>
          <div v-if="a.apply_reason" class="muted" style="margin-top: 6px;">申请理由：{{ a.apply_reason }}</div>
          <div v-if="a.review_note" class="muted" style="margin-top: 4px; color: #b45309;">审核意见：{{ a.review_note }}</div>
          <div class="muted" style="margin-top: 4px;">申请时间：{{ fmt(a.created_at) }}<template v-if="a.reviewed_at"> · 审核时间：{{ fmt(a.reviewed_at) }}</template></div>

          <!-- 凭据 + 对接指引 -->
          <div v-if="a.status === 'approved' && credOpenId === a.id" style="margin-top: 12px; background: #f9fafb; border-radius: 8px; padding: 12px;">
            <div v-if="credLoading" class="muted">加载凭据…</div>
            <template v-else-if="credentials">
              <div style="font-weight: 600; margin-bottom: 8px;">凭据（请妥善保存）</div>
              <div class="cred-row" style="display: flex; gap: 8px; align-items: center; margin-bottom: 8px; flex-wrap: wrap;">
                <span class="muted" style="width: 72px;">app_id</span>
                <code style="flex: 1; background: #fff; border: 1px solid #e5e6e8; border-radius: 6px; padding: 6px 10px; font-size: 13px;">{{ credentials.api_key }}</code>
                <button class="btn secondary" @click="copy(credentials.api_key)">{{ copied === 'key' ? '✓' : '复制' }}</button>
              </div>
              <div class="cred-row" style="display: flex; gap: 8px; align-items: center; margin-bottom: 8px; flex-wrap: wrap;">
                <span class="muted" style="width: 72px;">app_key</span>
                <code style="flex: 1; background: #fff; border: 1px solid #e5e6e8; border-radius: 6px; padding: 6px 10px; font-size: 13px;">{{ credentials.api_secret }}</code>
                <button class="btn secondary" @click="copy(credentials.api_secret)">{{ copied === 'secret' ? '✓' : '复制' }}</button>
              </div>
              <div v-if="regeneratedSecret" style="margin-bottom: 8px; padding: 8px 10px; background: #fef3c7; border-radius: 6px; font-size: 13px;">
                新密钥（旧密钥已失效）：<code>{{ regeneratedSecret }}</code>
              </div>
            </template>

            <!-- IP 白名单（审核通过后可配置；空 = 所有 IP 放行） -->
            <div style="margin-top: 14px; padding-top: 12px; border-top: 1px dashed #e5e6e8;">
              <div style="display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 6px;">
                <b style="font-size: 13px;">🔒 IP 白名单</b>
                <span class="muted" style="font-size: 12px;">{{ (a.ip_whitelist?.length || 0) ? `已限制 ${a.ip_whitelist!.length} 条` : '未限制（所有 IP 可调用）' }}</span>
              </div>
              <div class="muted" style="margin: 6px 0 8px; line-height: 1.7; font-size: 12px;">
                出于安全考虑，可限制只有指定 IP 的服务器才能调用本账户的对接接口。
                <b>不填写 = 默认所有 IP 都可以请求</b>；填写后仅白名单内 IP 可用（支持精确 IP 如 1.2.3.4，或网段如 10.0.0.0/24，最多 20 条）。服务器出口 IP 变更后请及时更新，否则接口将被拒绝。
              </div>
              <div v-if="(a.ip_whitelist || []).length" style="display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 8px;">
                <span v-for="ip in a.ip_whitelist" :key="ip" class="ip-chip">
                  <code>{{ ip }}</code>
                  <button class="ip-chip-x" title="移除" @click="removeWhitelistIP(a, ip)">✕</button>
                </span>
              </div>
              <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                <input
                  v-model="whitelistInput"
                  class="input"
                  style="flex: 1; min-width: 200px;"
                  placeholder="添加 IP 或网段，如 1.2.3.4 / 10.0.0.0/24"
                  @keyup.enter="addWhitelistIP(a)"
                />
                <button class="btn secondary" :disabled="whitelistSaving" @click="addWhitelistIP(a)">添加</button>
              </div>
              <div v-if="whitelistError" style="color: #dc2626; font-size: 12px; margin-top: 6px;">{{ whitelistError }}</div>
            </div>
            <div class="muted" style="margin-top: 10px; line-height: 1.8;">
              <b style="color: #1f2329;">对接方式</b><br />
              <template v-if="a.protocol === 'acg_faka'">
                ① 对方 acg-faka 后台 →「店铺共享 / 共享店铺」→ 新增店铺<br />
                ② 类型选「异次元(原生)」，地址填本站地址（<code>{{ origin }}</code>）<br />
                ③ 商户ID 填上面的 app_id，密钥填 app_key，保存前会自动连通测试
              </template>
              <template v-else-if="a.protocol === 'dujiao_next'">
                ① 对方 dujiao-next 后台 →「站点连接」→ 新增连接<br />
                ② 协议选 dujiao-next，地址填本站地址（<code>{{ origin }}</code>/api/v1/upstream）<br />
                ③ API Key 填上面的 app_id，API Secret 填 app_key
              </template>
              <template v-else>
                ① 对方 ZCard 后台 →「货源渠道」→ 新增连接（协议选 zcard）<br />
                ② 地址填本站地址（<code>{{ origin }}</code>/api/supply），app_id 填上面的 api_key、密钥填 api_secret
              </template>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 充值弹窗（大厂收银风格：余额卡 + 档位网格 + 支付方式 + 主按钮） -->
    <div v-if="rechargeOpen" class="recharge-mask" @click.self="closeRecharge">
      <div class="recharge-modal">
        <div class="recharge-head">
          <div style="font-weight: 700; font-size: 16px;">供货余额充值</div>
          <button class="recharge-close" @click="closeRecharge">✕</button>
        </div>

        <!-- 余额卡 -->
        <div class="balance-hero">
          <div class="muted" style="color: rgba(255,255,255,0.75);">当前余额</div>
          <div style="font-size: 28px; font-weight: 800; letter-spacing: 0.5px;">{{ formatMoney(rechargeTarget?.balance_cache || 0) }}</div>
          <div style="font-size: 12px; opacity: 0.8; margin-top: 4px;">{{ rechargeTarget?.display_name }} · {{ protocolLabel(rechargeTarget?.protocol || '') }}</div>
        </div>

        <div class="recharge-body">
          <!-- 成功反馈 -->
          <div v-if="rechargeDone" class="recharge-done">
            <div style="font-size: 42px;">✅</div>
            <div style="font-weight: 700; margin-top: 8px;">充值成功</div>
            <div class="muted" style="margin-top: 4px;">已到账 {{ formatMoney(rechargeDoneAmount) }}，支付完成后余额自动更新</div>
            <button class="btn" style="margin-top: 16px; width: 100%;" @click="closeRecharge">完成</button>
          </div>

          <template v-else>
            <!-- 金额档位 -->
            <div class="recharge-section">
              <div class="recharge-label">充值金额</div>
              <div class="tier-grid">
                <button
                  v-for="t in presetTiers"
                  :key="t"
                  class="tier-card"
                  :class="{ active: rechargeYuan === t }"
                  @click="rechargeYuan = t; focusCustom = false"
                >
                  {{ formatMoney(t * 100) }}
                </button>
                <button class="tier-card custom" :class="{ active: !presetTiers.includes(rechargeYuan as number) && rechargeYuan !== null }" @click="focusCustom = true">
                  自定义
                </button>
              </div>
              <div v-if="focusCustom" class="custom-input">
                <input class="input" v-model.number="rechargeYuan" type="number" min="1" step="0.01" placeholder="输入金额" autofocus />
              </div>
              <div class="muted" style="margin-top: 6px;">限额 {{ formatMoney(rechargeMeta?.min_amount || 1000) }} ~ {{ formatMoney(rechargeMeta?.max_amount || 500000) }}</div>
            </div>

            <!-- 支付方式 -->
            <div class="recharge-section">
              <div class="recharge-label">支付方式</div>
              <div v-if="rechargeChannels.length === 0" class="muted">暂无可用的支付渠道</div>
              <div v-else class="channel-list">
                <label
                  v-for="c in rechargeChannels"
                  :key="c.code"
                  class="channel-card"
                  :class="{ active: rechargeChannel === c.code }"
                >
                  <input v-model="rechargeChannel" type="radio" :value="c.code" style="display: none;" />
                  <span class="channel-dot" :class="{ on: rechargeChannel === c.code }"></span>
                  <span class="channel-name">{{ c.name }}</span>
                </label>
              </div>
            </div>

            <div v-if="rechargeError" style="color: #dc2626; font-size: 13px; margin: 8px 0;">{{ rechargeError }}</div>

            <!-- 支付跳转 -->
            <div v-if="rechargeRedirect" style="margin-top: 12px;">
              <a class="btn" style="width: 100%; text-align: center; text-decoration: none;" :href="rechargeRedirect" target="_blank">去支付（跳转收银台）</a>
              <div class="muted" style="text-align: center; margin-top: 6px;">支付完成后余额自动到账</div>
            </div>
            <div v-else-if="rechargeQrcode" style="margin-top: 12px; text-align: center;">
              <img :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(rechargeQrcode)}`" alt="支付二维码" style="width: 180px; border-radius: 10px;" />
              <div class="muted" style="margin-top: 6px;">请使用对应 App 扫码支付，完成后余额自动到账</div>
            </div>

            <!-- 主按钮 -->
            <button
              v-if="!rechargeRedirect && !rechargeQrcode"
              class="recharge-submit"
              :disabled="recharging || !rechargeYuan || rechargeYuan <= 0 || !rechargeChannel"
              @click="doRecharge"
            >
              {{ recharging ? '创建支付单…' : `立即充值 ${formatMoney((rechargeYuan ?? 0) * 100)}` }}
            </button>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import {
  listMySupplierAccounts, submitSupplierApplication, getSupplierCredentials,
  regenerateSupplierSecret, cancelSupplierApplication, createSupplierRecharge,
  setSupplierIPWhitelist, fetchPaymentChannels, type ChannelItem,
  type SupplierAccount, type SupplierCredentials,
} from '@/api';
import { api, formatMoney } from '@/api/client';

const protocols = [
  { value: 'zcard', label: 'ZCard', desc: '另一套 ZCard 系统对接（X-Supply 四头签名）' },
  { value: 'dujiao_next', label: 'dujiao-next', desc: '对方 dujiao-next 站对接（Dujiao-Next 三头签名）' },
  { value: 'acg_faka', label: 'acg-faka', desc: '对方 acg-faka 站对接（共享店铺原生协议）' },
];

const accounts = ref<SupplierAccount[]>([]);
const loading = ref(false);
const submitting = ref(false);
const formError = ref('');
const form = ref({ protocol: 'zcard', display_name: '', contact: '', apply_reason: '', notify_url: '' });

const credOpenId = ref<number>(0);
const credLoading = ref(false);
const credentials = ref<SupplierCredentials | null>(null);
const regeneratedSecret = ref('');
const copied = ref('');

// ── IP 白名单管理（审核通过后的账户；空 = 所有 IP 放行）──
const whitelistInput = ref('');
const whitelistSaving = ref(false);
const whitelistError = ref('');

function validIPOrCIDR(v: string): boolean {
  const s = v.trim();
  if (!s) return false;
  if (s.includes('/')) {
    const [ip, mask] = s.split('/');
    const m = Number(mask);
    if (!ip || !Number.isInteger(m) || m < 0 || m > (ip.includes(':') ? 128 : 32)) return false;
    return validIPOrCIDR(ip);
  }
  return /^(\d{1,3}\.){3}\d{1,3}$/.test(s) || s.includes(':'); // IPv4 点分或 IPv6（含冒号粗校验，后端精确校验）
}

async function saveWhitelist(a: SupplierAccount, ips: string[]) {
  whitelistSaving.value = true;
  whitelistError.value = '';
  try {
    const { data, error } = await setSupplierIPWhitelist(a.id, ips);
    if (error) {
      whitelistError.value = error;
      return;
    }
    // 本地同步（列表态 + 展开态）
    a.ip_whitelist = data?.ip_whitelist || ips;
    const row = accounts.value.find((x) => x.id === a.id);
    if (row) row.ip_whitelist = a.ip_whitelist;
    whitelistInput.value = '';
  } finally {
    whitelistSaving.value = false;
  }
}

async function addWhitelistIP(a: SupplierAccount) {
  const ip = whitelistInput.value.trim();
  if (!ip) return;
  if (!validIPOrCIDR(ip)) {
    whitelistError.value = '格式不正确：请填精确 IP（1.2.3.4）或网段（10.0.0.0/24）';
    return;
  }
  const next = [...(a.ip_whitelist || [])];
  if (next.includes(ip)) {
    whitelistError.value = '该 IP 已在白名单中';
    return;
  }
  if (next.length >= 20) {
    whitelistError.value = '白名单最多 20 条';
    return;
  }
  await saveWhitelist(a, [...next, ip]);
}

async function removeWhitelistIP(a: SupplierAccount, ip: string) {
  if (!confirm(`确认从白名单移除 ${ip}？`)) return;
  await saveWhitelist(a, (a.ip_whitelist || []).filter((x) => x !== ip));
}

const origin = typeof location !== 'undefined' ? location.origin : '';

// ── 充值（收银弹窗）──
const rechargeOpen = ref(false);
const rechargeTarget = ref<SupplierAccount | null>(null);
const rechargeYuan = ref<number | null>(null);
const presetTiers = ref<number[]>([100, 200, 500, 1000, 2000]);
const focusCustom = ref(false);
const rechargeChannels = ref<ChannelItem[]>([]);
const rechargeChannel = ref('');
const recharging = ref(false);
const rechargeError = ref('');
const rechargeRedirect = ref('');
const rechargeQrcode = ref('');
const rechargeMeta = ref<{ min_amount: number; max_amount: number } | null>(null);
const rechargeDone = ref(false);
const rechargeDoneAmount = ref(0);

async function openRecharge(a: SupplierAccount) {
  rechargeTarget.value = a;
  rechargeYuan.value = presetTiers.value[1] ?? 100; // 默认第二档
  focusCustom.value = false;
  rechargeError.value = '';
  rechargeRedirect.value = '';
  rechargeQrcode.value = '';
  rechargeDone.value = false;
  rechargeOpen.value = true;
  // 支付渠道 + 供货充值限额（独立配置组 supplier_recharge，与钱包充值隔离）
  const [ch, cfg] = await Promise.all([fetchPaymentChannels(), api.get<{ entries: { key: string; value_json: string }[] }>('/config')]);
  rechargeChannels.value = ch.data?.channels || [];
  rechargeChannel.value = rechargeChannels.value[0]?.code || '';
  const find = (k: string) => cfg.data?.entries?.find((e) => e.key === k)?.value_json;
  const min = find('supplier_recharge.min_amount');
  const max = find('supplier_recharge.max_amount');
  if (min && max) {
    rechargeMeta.value = { min_amount: Number(JSON.parse(min)), max_amount: Number(JSON.parse(max)) };
  }
}

function closeRecharge() {
  rechargeOpen.value = false;
  rechargeTarget.value = null;
}

async function doRecharge() {
  if (!rechargeTarget.value) return;
  if (!rechargeYuan.value || rechargeYuan.value <= 0) {
    rechargeError.value = '请输入充值金额';
    return;
  }
  if (!rechargeChannel.value) {
    rechargeError.value = '请选择支付方式';
    return;
  }
  recharging.value = true;
  rechargeError.value = '';
  rechargeRedirect.value = '';
  rechargeQrcode.value = '';
  const { data, error } = await createSupplierRecharge(rechargeTarget.value.id, {
    amount_cents: Math.round(rechargeYuan.value * 100),
    channel: rechargeChannel.value,
  });
  recharging.value = false;
  if (error || !data) {
    rechargeError.value = error || '创建失败';
    return;
  }
  // 支付载荷三形态（与充值/支付页同构）
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
  // 轮询账户余额（支付完成后刷新）
  pollBalance(rechargeTarget.value.id);
}

let balanceTimer: ReturnType<typeof setInterval> | null = null;
function pollBalance(id: number) {
  if (balanceTimer) clearInterval(balanceTimer);
  const started = Date.now();
  balanceTimer = setInterval(async () => {
    const { data } = await listMySupplierAccounts();
    if (!data) return;
    const acc = data.accounts.find((a) => a.id === id);
    if (acc) {
      const before = rechargeTarget.value?.balance_cache || 0;
      if (acc.balance_cache > before) {
        rechargeDone.value = true;
        rechargeDoneAmount.value = acc.balance_cache - before;
        if (balanceTimer) clearInterval(balanceTimer);
        await load();
      }
    }
    // 5 分钟超时停止轮询
    if (Date.now() - started > 5 * 60 * 1000 && balanceTimer) {
      clearInterval(balanceTimer);
    }
  }, 3000);
}

function protocolLabel(p: string) {
  return ({ zcard: 'ZCard', dujiao_next: 'dujiao-next', acg_faka: 'acg-faka' } as any)[p] || p;
}
function statusLabel(s: string) {
  return ({ applying: '待审核', approved: '已通过', rejected: '已驳回', disabled: '已禁用' } as any)[s] || s;
}
function statusStyle(s: string) {
  return ({
    applying: 'background: #fef3c7; color: #b45309;',
    approved: 'background: #dcfce7; color: #15803d;',
    rejected: 'background: #fee2e2; color: #b91c1c;',
    disabled: 'background: #e5e7eb; color: #4b5563;',
  } as any)[s] || '';
}
function fmt(ts: number) {
  return ts ? new Date(ts * 1000).toLocaleString() : '-';
}
async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    copied.value = text.length > 30 ? 'secret' : 'key';
    setTimeout(() => (copied.value = ''), 1500);
  } catch { /* 剪贴板不可用时忽略 */ }
}

async function load() {
  loading.value = true;
  try {
    const { data } = await listMySupplierAccounts();
    accounts.value = data?.accounts || [];
  } finally {
    loading.value = false;
  }
}

async function submit() {
  formError.value = '';
  if (!form.value.display_name.trim()) {
    formError.value = '请填写站点/店铺名';
    return;
  }
  // 回调地址归一：裸域名自动补 https://；http/https 均支持
  let notifyURL = form.value.notify_url.trim();
  if (notifyURL && !notifyURL.includes('://')) notifyURL = `https://${notifyURL}`;
  if (notifyURL && !/^https?:\/\//.test(notifyURL)) {
    formError.value = '回调地址仅支持 http/https（或直接填域名）';
    return;
  }
  submitting.value = true;
  try {
    const { error } = await submitSupplierApplication({
      protocol: form.value.protocol,
      display_name: form.value.display_name.trim(),
      contact: form.value.contact.trim(),
      apply_reason: form.value.apply_reason.trim(),
      notify_url: notifyURL,
    });
    if (error) {
      formError.value = error;
      return;
    }
    form.value.display_name = '';
    form.value.contact = '';
    form.value.apply_reason = '';
    form.value.notify_url = '';
    await load();
  } finally {
    submitting.value = false;
  }
}

async function showCredentials(a: SupplierAccount) {
  if (credOpenId.value === a.id) {
    credOpenId.value = 0;
    credentials.value = null;
    return;
  }
  credOpenId.value = a.id;
  regeneratedSecret.value = '';
  credLoading.value = true;
  try {
    const { data, error } = await getSupplierCredentials(a.id);
    if (error) {
      alert(error);
      credOpenId.value = 0;
      return;
    }
    credentials.value = data;
  } finally {
    credLoading.value = false;
  }
}

async function regenerate(a: SupplierAccount) {
  if (!confirm(`确认重置「${a.display_name}」的密钥？重置后旧密钥立即失效。`)) return;
  const { data, error } = await regenerateSupplierSecret(a.id);
  if (error) {
    alert(error);
    return;
  }
  credentials.value = { ...credentials.value!, api_secret: data!.api_secret };
  regeneratedSecret.value = data!.api_secret;
}

async function cancel(a: SupplierAccount) {
  if (!confirm(`确认撤销「${a.display_name}」的申请？`)) return;
  const { error } = await cancelSupplierApplication(a.id);
  if (error) {
    alert(error);
    return;
  }
  await load();
}

onMounted(load);
</script>

<style scoped>
/* 充值收银弹窗（大厂交互：蒙层 + 圆角卡片 + 余额卡 + 档位网格 + 主按钮） */
.recharge-mask {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.55);
  backdrop-filter: blur(2px);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}
.recharge-modal {
  width: 100%;
  max-width: 420px;
  background: #fff;
  border-radius: 16px;
  box-shadow: 0 24px 64px rgba(15, 23, 42, 0.25);
  overflow: hidden;
  animation: recharge-in 0.18s ease-out;
}
@keyframes recharge-in {
  from { opacity: 0; transform: translateY(12px) scale(0.98); }
  to { opacity: 1; transform: translateY(0) scale(1); }
}
.recharge-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 18px 0;
}
.recharge-close {
  border: none;
  background: #f3f4f6;
  color: #6b7280;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
}
.recharge-close:hover { background: #e5e7eb; }
.balance-hero {
  margin: 14px 18px 0;
  border-radius: 12px;
  background: linear-gradient(135deg, #1e40af, #2563eb 60%, #3b82f6);
  color: #fff;
  padding: 16px 18px;
}
.recharge-body { padding: 14px 18px 18px; }
.recharge-section { margin-bottom: 14px; }
.recharge-label {
  font-size: 13px;
  font-weight: 600;
  color: #374151;
  margin-bottom: 8px;
}
.tier-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
}
.tier-card {
  border: 1px solid #e5e6e8;
  background: #fff;
  border-radius: 10px;
  padding: 10px 4px;
  cursor: pointer;
  font-size: 15px;
  font-weight: 700;
  color: #1f2329;
  transition: all 0.12s;
}
.tier-card:hover { border-color: #93c5fd; }
.tier-card.active {
  border-color: #2563eb;
  background: #eff6ff;
  color: #2563eb;
  box-shadow: 0 0 0 1px #2563eb inset;
}
.tier-symbol { font-size: 12px; font-weight: 500; margin-right: 2px; }
.custom-input { margin-top: 8px; }
.channel-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.channel-card {
  display: flex;
  align-items: center;
  gap: 10px;
  border: 1px solid #e5e6e8;
  border-radius: 10px;
  padding: 10px 12px;
  cursor: pointer;
  transition: all 0.12s;
}
.channel-card:hover { border-color: #93c5fd; }
.channel-card.active {
  border-color: #2563eb;
  background: #eff6ff;
}
.channel-dot {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 2px solid #d1d5db;
  position: relative;
  flex-shrink: 0;
}
.channel-dot.on { border-color: #2563eb; }
.channel-dot.on::after {
  content: '';
  position: absolute;
  inset: 2px;
  border-radius: 50%;
  background: #2563eb;
}
.channel-name { font-size: 14px; font-weight: 500; }
.recharge-submit {
  width: 100%;
  margin-top: 6px;
  padding: 13px;
  border: none;
  border-radius: 10px;
  background: #2563eb;
  color: #fff;
  font-size: 15px;
  font-weight: 700;
  cursor: pointer;
  transition: background 0.15s;
}
.recharge-submit:hover:not(:disabled) { background: #1d4ed8; }
.recharge-submit:disabled { opacity: 0.5; cursor: not-allowed; }
.recharge-done { text-align: center; padding: 18px 0 6px; }

/* IP 白名单条目 chip */
.ip-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  border-radius: 6px;
  padding: 3px 4px 3px 10px;
  font-size: 13px;
}
.ip-chip code { font-size: 12.5px; color: #1d4ed8; }
.ip-chip-x {
  border: none;
  background: #dbeafe;
  color: #1d4ed8;
  width: 18px;
  height: 18px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 11px;
  line-height: 1;
}
.ip-chip-x:hover { background: #93c5fd; }
</style>
