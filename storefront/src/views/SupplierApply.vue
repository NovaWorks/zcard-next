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
        <input v-model="form.notify_url" class="input" placeholder="交付回调地址（可选，HTTPS）" maxlength="500" />
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
            <div>
              <b>{{ a.display_name }}</b>
              <span class="tag" style="margin-left: 8px;">{{ protocolLabel(a.protocol) }}</span>
              <span class="tag" :style="statusStyle(a.status)">{{ statusLabel(a.status) }}</span>
            </div>
            <div class="actions" style="gap: 8px;">
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
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import {
  listMySupplierAccounts, submitSupplierApplication, getSupplierCredentials,
  regenerateSupplierSecret, cancelSupplierApplication,
  type SupplierAccount, type SupplierCredentials,
} from '@/api';

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

const origin = typeof location !== 'undefined' ? location.origin : '';

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
  if (form.value.notify_url && !/^https:\/\//.test(form.value.notify_url)) {
    formError.value = '回调地址必须 HTTPS';
    return;
  }
  submitting.value = true;
  try {
    const { error } = await submitSupplierApplication({
      protocol: form.value.protocol,
      display_name: form.value.display_name.trim(),
      contact: form.value.contact.trim(),
      apply_reason: form.value.apply_reason.trim(),
      notify_url: form.value.notify_url.trim(),
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
