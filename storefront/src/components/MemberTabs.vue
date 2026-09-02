<template>
  <!-- 大厂个人中心分段式导航：白卡容器 + 胶囊选中态（与首页卡片视觉同语言） -->
  <nav class="member-tabs" aria-label="会员中心导航">
    <button
      v-for="t in tabs"
      :key="t.key"
      :class="{ active: active === t.key }"
      @click="go(t.key)"
    >{{ t.label }}</button>
  </nav>
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
  { key: 'points', label: '积分商城' },
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
  // 积分商城同为独立路由页（/points），不占 /member 页内 tab
  if (key === 'points') {
    router.push('/points');
    return;
  }
  router.push(key === 'overview' ? '/member' : { path: '/member', query: { tab: key } });
}
</script>

<style scoped>
.member-tabs {
  display: flex; align-items: center; gap: 2px;
  background: #fff; border: 1px solid #e5e6e8; border-radius: 12px;
  padding: 6px; margin-bottom: 16px;
  overflow-x: auto; -webkit-overflow-scrolling: touch; scrollbar-width: none;
}
.member-tabs::-webkit-scrollbar { display: none; }
.member-tabs button {
  flex-shrink: 0; padding: 8px 16px; border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 14px; color: #4b5563; font-family: inherit;
  transition: background 0.15s, color 0.15s; white-space: nowrap;
}
.member-tabs button:hover:not(.active) { color: #2563eb; background: #f0f6ff; }
.member-tabs button.active { background: #2563eb; color: #fff; font-weight: 600; }
</style>
