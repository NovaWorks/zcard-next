<template>
  <div class="wd-page">
    <h2 class="wd-title">佣金提现</h2>

    <!-- 未开放 -->
    <div v-if="!cfg.enabled && cfgLoaded" class="card wd-center">
      <div class="wd-state-icon gray">🔒</div>
      <div>提现功能暂未开放</div>
      <div class="muted">有疑问请联系客服</div>
    </div>

    <template v-else>
      <!-- 余额区 -->
      <div class="wd-balance-card">
        <div class="wd-balance-main">
          <div class="wd-balance-label">可提佣金（元）</div>
          <div class="wd-balance-num">{{ formatMoney(withdrawable) }}</div>
          <div class="wd-balance-sub muted">冻结中 {{ formatMoney(frozen) }} · 累计已提 {{ formatMoney(my?.withdrawn_cents || 0) }}</div>
        </div>
        <div class="wd-balance-side">
          <div class="wd-rule">最低提现 {{ formatMoney(cfg.minAmountCents) }}</div>
          <div class="wd-rule">手续费 {{ feeDesc }}</div>
        </div>
      </div>

      <div class="wd-layout">
        <!-- 左：申请表单 -->
        <div class="card wd-form-card">
          <h3 class="wd-section-title">申请提现</h3>

          <div class="wd-field">
            <label class="wd-label">提现金额（元）</label>
            <div class="wd-amount-row">
              <input v-model.number="amountYuan" type="number" min="0" :step="0.01" class="input" placeholder="提现金额" />
              <button class="btn secondary" @click="allIn">全部提现</button>
            </div>
            <!-- 实时到账试算 -->
            <div class="wd-preview">
              <span class="muted">预计到账</span>
              <b class="wd-preview-num">{{ formatMoney(credited) }}</b>
              <span class="muted" v-if="feeCents > 0">（手续费 {{ formatMoney(feeCents) }}）</span>
            </div>
          </div>

          <div class="wd-field">
            <label class="wd-label">收款方式</label>
            <div class="wd-methods">
              <button
                v-for="m in cfg.methods"
                :key="m.type"
                class="wd-method"
                :class="{ active: methodType === m.type }"
                @click="methodType = m.type"
              >
                <span class="wd-method-icon">{{ methodIcon(m.type) }}</span>
                <span>{{ m.name }}</span>
              </button>
            </div>
          </div>

          <div class="wd-field">
            <label class="wd-label">{{ methodType === 'usdt_trc20' ? 'TRC20 钱包地址' : '收款账号' }}</label>
            <input
              v-model="account"
              type="text"
              class="input"
              :placeholder="methodType === 'usdt_trc20' ? 'T 开头 34 位 TRC20 地址' : '支付宝账号 / 微信号 / 银行卡号'"
            />
            <div v-if="methodType === 'usdt_trc20'" class="muted" style="margin-top: 4px;">仅支持 TRC20 网络，请核对地址无误</div>
          </div>

          <!-- 收款码上传（微信/支付宝） -->
          <div v-if="methodType === 'wechat' || methodType === 'alipay'" class="wd-field">
            <label class="wd-label">收款二维码（选填，加快打款）</label>
            <div class="wd-qr-upload">
              <img v-if="qrUrl" :src="qrUrl" class="wd-qr-preview" alt="收款码" />
              <label v-else class="wd-qr-placeholder">
                <span>{{ uploading ? '上传中…' : '📷 上传收款码' }}</span>
                <input type="file" accept="image/*" class="wd-file-hidden" @change="onQrPick" />
              </label>
              <button v-if="qrUrl" class="btn secondary wd-qr-repick" @click="qrUrl = ''">重新上传</button>
            </div>
          </div>

          <div v-if="error" class="error" style="margin-bottom: 10px;">{{ error }}</div>
          <div v-if="okMsg" class="success" style="margin-bottom: 10px;">{{ okMsg }}</div>

          <button class="wd-submit" :disabled="submitting || !withdrawable" @click="submit">
            {{ submitting ? '提交中…' : '提交提现申请' }}
          </button>
          <div class="muted" style="margin-top: 8px; text-align: center;">提交后进入人工审核；打款 1-3 个工作日到账</div>
        </div>

        <!-- 右：提现记录 -->
        <div class="card wd-record-card">
          <h3 class="wd-section-title">提现记录</h3>
          <div v-if="records.length" class="wd-records">
            <div v-for="r in records" :key="r.withdrawal_id" class="wd-record">
              <div class="wd-record-head">
                <span class="wd-record-amount">{{ formatMoney(r.amount_cents) }}</span>
                <span :class="statusBadge(r.status)">{{ statusText(r.status) }}</span>
              </div>
              <div class="wd-record-meta muted">
                {{ r.method_name || r.method_type }} · {{ maskAccount(r.account) }}
              </div>
              <div class="wd-record-meta muted">手续费 {{ formatMoney(r.fee_cents) }} · {{ fmtTime(r.created_at) }}</div>
              <div v-if="r.receipt" class="wd-record-receipt">打款回执：{{ r.receipt }}</div>
              <div v-if="r.reject_reason" class="wd-record-reject">驳回原因：{{ r.reject_reason }}</div>
            </div>
          </div>
          <div v-else class="wd-empty muted">暂无提现记录</div>
          <div v-if="recordTotal > recordPageSize" class="wd-pager">
            <button class="btn secondary" :disabled="recordPage <= 1" @click="loadRecords(recordPage - 1)">上一页</button>
            <span class="muted">{{ recordPage }} / {{ Math.ceil(recordTotal / recordPageSize) }}</span>
            <button class="btn secondary" :disabled="recordPage >= Math.ceil(recordTotal / recordPageSize)" @click="loadRecords(recordPage + 1)">下一页</button>
          </div>
        </div>
      </div>

      <!-- 工单入口 -->
      <div class="wd-ticket card">
        <div>
          <b>遇到提现问题？</b>
          <span class="muted">审核超时 / 打款未到账 / 金额疑问，提交工单快速处理</span>
        </div>
        <router-link class="btn secondary" :to="{ path: '/tickets', query: { type: 'withdraw' } }">提交工单</router-link>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import {
  myAffiliate, listMyWithdrawals, createWithdrawal, uploadQrCode,
  fetchWithdrawConfig, type MyAffiliateReply, type MyWithdrawalItem, type WithdrawConfig,
} from '@/api';
import { formatMoney, yuanToFen, centsToYuan } from '@/api/client';

const my = ref<MyAffiliateReply | null>(null);
const cfg = ref<WithdrawConfig>({ enabled: false, minAmountCents: 1000, feeType: 'fixed', feeValue: 0, methods: [] });
const cfgLoaded = ref(false);

const amountYuan = ref<number | null>(null);
const methodType = ref('');
const account = ref('');
const qrUrl = ref('');
const uploading = ref(false);
const submitting = ref(false);
const error = ref('');
const okMsg = ref('');

const records = ref<MyWithdrawalItem[]>([]);
const recordPage = ref(1);
const recordTotal = ref(0);
const recordPageSize = 8;

// 可提 = available − 冻结（服务端同口径）
const frozen = ref(0);
const withdrawable = computed(() => Math.max(0, (my.value?.available_cents || 0) - frozen.value));

// 手续费试算
const feeCents = computed(() => {
  const amount = yuanToFen(amountYuan.value || 0);
  if (amount <= 0) return 0;
  return cfg.value.feeType === 'percent' ? Math.floor(amount * cfg.value.feeValue / 10000) : cfg.value.feeValue;
});
const credited = computed(() => Math.max(0, yuanToFen(amountYuan.value || 0) - feeCents.value));
const feeDesc = computed(() =>
  cfg.value.feeType === 'percent' ? `按 ${(cfg.value.feeValue / 100).toFixed(2)}% 收取` : `固定 ${formatMoney(cfg.value.feeValue)}`
);

function methodIcon(t: string) {
  return ({ alipay: '🅰️', wechat: '💬', usdt_trc20: '₮', bank: '🏦' } as Record<string, string>)[t] || '💳';
}

function allIn() {
  amountYuan.value = centsToYuan(withdrawable.value);
}

async function onQrPick(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0];
  (e.target as HTMLInputElement).value = '';
  if (!file) return;
  if (file.size > 2 * 1024 * 1024) {
    error.value = '收款码图片不能超过 2MB';
    return;
  }
  uploading.value = true;
  error.value = '';
  const reader = new FileReader();
  reader.onload = async () => {
    const base64 = String(reader.result).split(',')[1] || '';
    const { data, error: err } = await uploadQrCode(base64);
    uploading.value = false;
    if (err || !data) { error.value = err || '上传失败'; return; }
    qrUrl.value = data.url;
  };
  reader.readAsDataURL(file);
}

async function submit() {
  const amount = yuanToFen(amountYuan.value || 0);
  if (amount < cfg.value.minAmountCents) {
    error.value = `最低提现 ${formatMoney(cfg.value.minAmountCents)}`;
    return;
  }
  if (amount > withdrawable.value) {
    error.value = '超过可提佣金';
    return;
  }
  if (!methodType.value) {
    error.value = '请选择收款方式';
    return;
  }
  if (!account.value.trim()) {
    error.value = '请填写收款账号';
    return;
  }
  if (methodType.value === 'usdt_trc20' && !/^T[1-9A-HJ-NP-Za-km-z]{33}$/.test(account.value.trim())) {
    error.value = 'TRC20 地址格式不正确（T 开头 34 位）';
    return;
  }
  submitting.value = true;
  error.value = '';
  okMsg.value = '';
  const { data, error: err } = await createWithdrawal({
    amount_cents: amount,
    method_type: methodType.value,
    account: account.value.trim(),
    qr_code_url: qrUrl.value || undefined,
  });
  submitting.value = false;
  if (err || !data) {
    error.value = err || '提交失败';
    return;
  }
  okMsg.value = `申请成功 #${data.withdrawal_id}：${formatMoney(data.amount_cents)}，预计到账 ${formatMoney(data.credited_cents)}（等待审核）`;
  amountYuan.value = null;
  account.value = '';
  qrUrl.value = '';
  refresh();
}

async function refresh() {
  const { data: m } = await myAffiliate();
  my.value = m;
  frozen.value = m ? (m as any).frozen_cents || 0 : 0;
  loadRecords(1);
}

async function loadRecords(page: number) {
  const { data } = await listMyWithdrawals(page, recordPageSize);
  if (data) {
    records.value = data.withdrawals || [];
    recordTotal.value = data.total;
    recordPage.value = page;
  }
}

onMounted(async () => {
  cfg.value = await fetchWithdrawConfig();
  cfgLoaded.value = true;
  if (cfg.value.enabled) {
    methodType.value = cfg.value.methods[0]?.type || '';
    refresh();
  }
});

function statusText(s: string): string {
  return ({ pending: '审核中', approved: '已通过', paid: '已打款', rejected: '已驳回' } as Record<string, string>)[s] || s;
}
function statusBadge(s: string): string {
  return ({ pending: 'badge orange', approved: 'badge blue', paid: 'badge green', rejected: 'badge red' } as Record<string, string>)[s] || 'badge gray';
}
function maskAccount(a: string): string {
  if (!a) return '-';
  if (a.length <= 6) return a.slice(0, 2) + '****';
  return a.slice(0, 4) + '****' + a.slice(-4);
}
function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '';
}
</script>

<style scoped>
.wd-page { max-width: 980px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px; }
.wd-title { font-size: 20px; font-weight: 800; color: #111827; }
.wd-center { text-align: center; padding: 48px 20px; }
.wd-state-icon { font-size: 40px; margin-bottom: 10px; }

/* 余额区 */
.wd-balance-card {
  display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap;
  background: linear-gradient(135deg, #1d4ed8, #2563eb, #3b82f6);
  border-radius: 14px; padding: 22px 26px; color: #fff;
  box-shadow: 0 6px 20px rgba(37, 99, 235, 0.25);
}
.wd-balance-label { font-size: 13px; opacity: 0.85; }
.wd-balance-num { font-size: 34px; font-weight: 800; margin: 4px 0; }
.wd-balance-sub { color: rgba(255, 255, 255, 0.75); font-size: 12px; }
.wd-balance-side { display: flex; flex-direction: column; gap: 6px; }
.wd-rule {
  background: rgba(255, 255, 255, 0.15); border-radius: 999px;
  padding: 5px 14px; font-size: 12px;
}

/* 布局 */
.wd-layout { display: grid; grid-template-columns: 1fr; gap: 16px; }
@media (min-width: 860px) { .wd-layout { grid-template-columns: 1.2fr 1fr; } }
.wd-section-title { font-size: 15px; font-weight: 700; color: #111827; margin-bottom: 14px; }

/* 表单 */
.wd-field { margin-bottom: 14px; }
.wd-label { display: block; font-size: 13px; font-weight: 600; color: #4b5563; margin-bottom: 6px; }
.wd-amount-row { display: flex; gap: 8px; }
.wd-preview { margin-top: 6px; font-size: 13px; display: flex; align-items: baseline; gap: 6px; }
.wd-preview-num { color: #16a34a; font-size: 16px; }

.wd-methods { display: flex; gap: 8px; flex-wrap: wrap; }
.wd-method {
  display: flex; align-items: center; gap: 8px;
  border: 2px solid #e5e7eb; border-radius: 10px; padding: 10px 16px;
  background: #fff; cursor: pointer; font-size: 13px; transition: all 0.15s;
}
.wd-method:hover { border-color: rgba(37, 99, 235, 0.4); }
.wd-method.active { border-color: #2563eb; background: #eff6ff; }
.wd-method-icon { font-size: 16px; }

/* 收款码 */
.wd-qr-upload { display: flex; gap: 12px; align-items: center; }
.wd-qr-preview { width: 120px; height: 120px; object-fit: cover; border-radius: 10px; border: 1px solid #e5e7eb; }
.wd-qr-placeholder {
  width: 120px; height: 120px; border: 2px dashed #d1d5db; border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  color: #9ca3af; font-size: 13px; cursor: pointer; transition: border-color 0.15s;
}
.wd-qr-placeholder:hover { border-color: #2563eb; color: #2563eb; }
.wd-file-hidden { display: none; }

.wd-submit {
  width: 100%; padding: 12px 0; border: none; cursor: pointer;
  border-radius: 10px; font-size: 15px; font-weight: 700; color: #fff;
  background: linear-gradient(90deg, #2563eb, #1d4ed8);
  box-shadow: 0 4px 12px rgba(37, 99, 235, 0.3); transition: all 0.15s;
}
.wd-submit:hover:not(:disabled) { transform: translateY(-1px); }
.wd-submit:disabled { opacity: 0.5; cursor: not-allowed; }

/* 记录 */
.wd-records { display: flex; flex-direction: column; }
.wd-record { padding: 12px 0; border-bottom: 1px solid #f3f4f6; }
.wd-record:last-child { border-bottom: none; }
.wd-record-head { display: flex; justify-content: space-between; align-items: center; }
.wd-record-amount { font-size: 16px; font-weight: 700; color: #111827; }
.wd-record-meta { margin-top: 4px; font-size: 12px; }
.wd-record-reject { margin-top: 4px; font-size: 12px; color: #dc2626; }
.wd-record-receipt { margin-top: 4px; font-size: 12px; color: #16a34a; }
.wd-empty { text-align: center; padding: 32px 0; }
.wd-pager { display: flex; gap: 10px; align-items: center; justify-content: center; margin-top: 10px; }

/* 工单入口 */
.wd-ticket {
  display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap;
  background: #fffbeb; border-color: #fde68a;
}
.wd-ticket b { display: block; font-size: 14px; margin-bottom: 2px; }
.wd-ticket .muted { font-size: 12px; }
</style>
