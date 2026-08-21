<template>
  <div class="tabs">
    <button
      v-for="t in tabs"
      :key="t.key"
      :class="{ active: active === t.key }"
      @click="go(t.key)"
    >{{ t.label }}</button>
  </div>
</template>

<script setup lang="ts">
// 个人中心共用导航（Member 与 Withdraw 两页共享）：
// 提现是独立路由页（/withdraw），其余为 /member 页内 tab（?tab= 查询参数驱动）。

import { useRouter } from 'vue-router';

defineProps<{ active?: string }>();

const router = useRouter();

const tabs = [
  { key: 'overview', label: '总览' },
  { key: 'orders', label: '我的订单' },
  { key: 'transactions', label: '余额流水' },
  { key: 'recharge', label: '充值' },
  { key: 'giftcard', label: '礼品卡' },
  { key: 'promo', label: '推广营销' },
  { key: 'supplier', label: '对接申请' },
  { key: 'withdraw', label: '提现' },
  { key: 'security', label: '账户安全' },
];

function go(key: string) {
  if (key === 'withdraw') {
    router.push('/withdraw');
    return;
  }
  router.push(key === 'overview' ? '/member' : { path: '/member', query: { tab: key } });
}
</script>
