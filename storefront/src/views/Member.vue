<template>
  <div class="card">
    <h2 style="margin-bottom: 12px;">会员中心</h2>
    <div v-if="balance">
      <div style="margin-bottom: 8px;">可用余额：{{ formatMoney(balance.available_cents) }}</div>
      <div style="margin-bottom: 8px;">冻结：{{ formatMoney(balance.locked_cents) }}</div>
      <div style="margin-bottom: 8px;">总额：{{ formatMoney(balance.total_cents) }}</div>
      <div style="margin-bottom: 12px;">积分：{{ balance.points }}</div>
    </div>
    <div v-else class="muted">登录后可见余额（M1 游客模式下会员中心为取货/订单入口）</div>
    <div style="margin-top: 12px;">
      <router-link class="btn secondary" to="/fetch">去取货</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { getBalance, type BalanceReply } from '@/api';
import { formatMoney } from '@/api/client';

const balance = ref<BalanceReply | null>(null);

onMounted(async () => {
  const { data } = await getBalance();
  balance.value = data;
});
</script>
