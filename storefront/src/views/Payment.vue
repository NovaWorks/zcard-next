<template>
  <div class="card">
    <h2 style="margin-bottom: 12px;">选择支付方式</h2>
    <div class="field">
      <label>支付渠道</label>
      <select v-model="channel">
        <option value="alipay">支付宝</option>
        <option value="wechat">微信支付</option>
        <option value="epay">易支付</option>
        <option value="epusdt">USDT（TRC20）</option>
        <option value="wallet">余额支付</option>
      </select>
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <button class="btn" :disabled="submitting" @click="pay">{{ submitting ? '处理中…' : '去支付' }}</button>

    <div v-if="redirectUrl" style="margin-top: 16px;">
      <a class="btn" :href="redirectUrl" target="_blank">跳转到收银台</a>
    </div>
    <div v-if="qrcode" style="margin-top: 16px;">
      <div style="font-weight: 600; margin-bottom: 8px;">请用微信扫码支付</div>
      <img :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(qrcode)}`" alt="支付二维码" />
      <div class="muted">支付完成后前往「取货」页输入订单号 + 查询密码</div>
    </div>
    <div v-if="paramsUrl" style="margin-top: 16px;">
      <div style="margin-bottom: 8px;">易支付参数（POST 提交到以下地址）</div>
      <form :action="paramsUrl" method="POST" target="_blank">
        <div v-for="(v, k) in paramsData" :key="k" style="display: none;">
          <input :name="k" :value="v" />
        </div>
        <button class="btn" type="submit">跳转易支付</button>
      </form>
    </div>
    <div v-if="paid" style="margin-top: 16px;" class="success">支付成功，请前往「取货」页领取卡密</div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useRoute } from 'vue-router';
import { createPayment } from '@/api';

const route = useRoute();
const orderNo = String(route.params.orderNo || '');
const channel = ref('alipay');
const submitting = ref(false);
const error = ref('');
const redirectUrl = ref('');
const qrcode = ref('');
const paramsUrl = ref('');
const paramsData = ref<Record<string, string>>({});
const paid = ref(false);

async function pay() {
  submitting.value = true;
  error.value = '';
  redirectUrl.value = '';
  qrcode.value = '';
  paramsUrl.value = '';
  paramsData.value = {};
  const { data, error: err } = await createPayment(orderNo, channel.value);
  submitting.value = false;
  if (err) { error.value = err; return; }
  const type = data?.type;
  const payload = data?.payload || '';
  try {
    const parsed = JSON.parse(payload);
    if (type === 'redirect') {
      redirectUrl.value = parsed.url || payload;
    } else if (type === 'qrcode') {
      qrcode.value = parsed.code_url || payload;
    } else if (type === 'params') {
      paramsUrl.value = parsed.url || '';
      paramsData.value = parsed.params || {};
    } else {
      redirectUrl.value = payload;
    }
  } catch {
    redirectUrl.value = payload;
  }
  if (channel.value === 'wallet') {
    paid.value = true;
  }
}
</script>
