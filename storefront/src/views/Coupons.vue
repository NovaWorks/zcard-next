<template>
  <div>
    <!-- 兑换码领取 -->
    <div class="card" style="margin-bottom: 16px;">
      <h2 style="margin-bottom: 12px;">领取优惠券</h2>
      <div class="actions">
        <input class="input" v-model="code" type="text" placeholder="输入兑换码" style="flex: 1; max-width: 320px;" @keyup.enter="doRedeem" />
        <button class="btn" :disabled="redeeming" @click="doRedeem">{{ redeeming ? '领取中…' : '领取' }}</button>
      </div>
      <div v-if="redeemError" class="error" style="margin-top: 8px;">{{ redeemError }}</div>
      <div v-if="redeemOk" class="success" style="margin-top: 8px;">领取成功，下单时输入券码即可抵扣</div>
    </div>

    <!-- 我的券 -->
    <div class="card">
      <h2 style="margin-bottom: 12px;">我的优惠券</h2>
      <div class="grid">
        <div v-for="c in coupons" :key="c.id" class="card" style="border: 1px dashed #c7d2fe;">
          <div style="font-size: 18px; font-weight: 700; color: #e11d48;">
            <template v-if="c.type === 'fixed'">{{ formatMoney(c.value) }}</template>
            <template v-else>{{ (c.value / 100).toFixed(1) }} 折</template>
          </div>
          <div style="font-weight: 600; margin: 6px 0;">{{ c.name }}</div>
          <div class="muted">适用：{{ scopeText(c.scope_json) }}</div>
          <div class="muted" style="margin-top: 4px;">有效期至 {{ fmtTime(c.expire_at) }}</div>
          <div class="muted" style="margin-top: 6px;">券码：<code>{{ c.code }}</code></div>
        </div>
      </div>
      <div v-if="!coupons.length" class="muted" style="text-align: center;">暂无可用优惠券</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { listMyCoupons, redeemCoupon, type MyCoupon } from '@/api';
import { formatMoney } from '@/api/client';

const code = ref('');
const redeeming = ref(false);
const redeemError = ref('');
const redeemOk = ref(false);
const coupons = ref<MyCoupon[]>([]);

onMounted(load);

async function load() {
  const { data } = await listMyCoupons();
  coupons.value = data?.coupons || [];
}

async function doRedeem() {
  if (!code.value.trim()) {
    redeemError.value = '请输入兑换码';
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
    if (s.type === 'all' || !s.type) return '全部商品';
    if (s.type === 'products') return `指定商品 ×${(s.ids || []).length}`;
    if (s.type === 'categories') return `指定分类 ×${(s.ids || []).length}`;
    return '以结算页为准';
  } catch {
    return '以结算页为准';
  }
}

function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleDateString() : '';
}
</script>
