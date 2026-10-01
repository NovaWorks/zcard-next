<template>
  <div>
    <!-- 兑换码领取 -->
    <div class="card" style="margin-bottom: 16px;">
      <h2 style="margin-bottom: 12px;">{{ $t('领取优惠券') }}</h2>
      <div class="actions">
        <input class="input" v-model="code" type="text" :placeholder="$t('输入兑换码')" style="flex: 1; max-width: 320px;" @keyup.enter="doRedeem" />
        <button class="btn" :disabled="redeeming" @click="doRedeem">{{ redeeming ? $t('领取中…') : $t('领取') }}</button>
      </div>
      <div v-if="redeemError" class="error" style="margin-top: 8px;">{{ uiText(redeemError) }}</div>
      <div v-if="redeemOk" class="success" style="margin-top: 8px;">{{ $t('领取成功，下单时输入券码即可抵扣') }}</div>
    </div>

    <!-- 我的券 -->
    <div class="card">
      <h2 style="margin-bottom: 12px;">{{ $t('我的优惠券') }}</h2>
      <p v-if="loading" class="muted" role="status">{{ $t('正在加载优惠券…') }}</p>
      <p v-if="loadError" class="error" role="alert">{{ uiText(loadError) }} <button class="btn secondary" :disabled="loading" @click="load">{{ $t('重试') }}</button></p>
      <div class="grid">
        <div v-for="c in availableCoupons" :key="c.id" class="card" style="border: 1px dashed var(--zc-primary-border);">
          <div style="font-size: 18px; font-weight: 700; color: #e11d48;">
            <template v-if="c.type === 'fixed'">{{ formatMoney(c.value) }}</template>
            <template v-else-if="c.type === 'percent'">{{ couponDiscountLabel(c.value) }}</template>
            <template v-else>{{ $t('优惠以结算页为准') }}</template>
          </div>
          <div style="font-weight: 600; margin: 6px 0;">{{ c.name }}</div>
          <div class="muted">{{ $t('适用：') }}{{ scopeText(c.scope_json) }}</div>
          <div class="muted" style="margin-top: 4px;">{{ Number(c.expire_at) > 0 ? $t('有效期至 {0}', [fmtTime(c.expire_at)]) : $t('长期有效') }}</div>
          <div class="muted" style="margin-top: 6px;">{{ $t('券码：') }}<code>{{ c.code }}</code></div>
        </div>
      </div>
      <div v-if="!loading && !loadError && !availableCoupons.length" class="muted" style="text-align: center;">{{ $t('暂无可用优惠券') }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { uiText, t as $t, localeTag } from '@/i18n';

import { ref, computed, onMounted } from 'vue';
import { listMyCoupons, redeemCoupon, type MyCoupon } from '@/api';
import { formatMoney } from '@/api/client';
import { couponDiscountLabel } from '@/utils/commerce';

const code = ref('');
const redeeming = ref(false);
const redeemError = ref('');
const redeemOk = ref(false);
const coupons = ref<MyCoupon[]>([]);
const loading = ref(false);
const loadError = ref('');
const availableCoupons = computed(() => coupons.value.filter(c => !(Number(c.expire_at) > 0 && Number(c.expire_at) <= Date.now() / 1000)));

onMounted(load);

async function load() {
  if (loading.value) return;
  loading.value = true;
  loadError.value = '';
  const { data, error } = await listMyCoupons();
  loading.value = false;
  if (error) { loadError.value = error; return; }
  coupons.value = data?.coupons || [];
}

async function doRedeem() {
  if (!code.value.trim()) {
    redeemError.value = $t("请输入兑换码");
    return;
  }
  redeeming.value = true;
  redeemError.value = '';
  redeemOk.value = false;
  const { error } = await redeemCoupon(code.value.trim());
  redeeming.value = false;
  if (error) {
    redeemError.value = error;
    return;
  }
  redeemOk.value = true;
  code.value = '';
  load();
}

// scope_json：{"type":"all"} 全场 | {"type":"products","ids":[1,2]} | {"type":"categories","ids":[3]}
function scopeText(scopeJson: string): string {
  try {
    const s = JSON.parse(scopeJson || '{}');
    if (s.type === 'all' || !s.type) return $t("全部商品");
    if (s.type === 'products') return $t("指定商品 ×{0}", [(s.ids || []).length]);
    if (s.type === 'categories') return $t("指定分类 ×{0}", [(s.ids || []).length]);
    return $t("以结算页为准");
  } catch {
    return $t("以结算页为准");
  }
}

function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleDateString(localeTag.value) : '';
}
</script>
