<script setup lang="ts">
// 收银台支付方式网格（Payment.vue 订单支付 / Member.vue 余额充值 共用）：
// 方式级选项卡片（自定义图标 → emoji 回落）+ 选中勾标，交互同大厂收银台。
import type { PayOption } from '@/composables/pay-options';

defineProps<{
  options: PayOption[];
  channel: string;
  method: string;
}>();

defineEmits<{ select: [channel: string, method: string] }>();
</script>

<template>
  <div v-if="options.length" class="pay-channel-grid">
    <button
      v-for="o in options"
      :key="o.channel + ':' + o.method"
      class="pay-channel"
      :class="{ active: channel === o.channel && method === o.method }"
      @click="$emit('select', o.channel, o.method)"
    >
      <span class="pay-channel-icon">
        <img v-if="o.icon" :src="o.icon" :alt="o.name" class="pay-channel-img" />
        <template v-else>{{ o.emoji }}</template>
      </span>
      <span class="pay-channel-name">{{ o.name }}</span>
      <span class="pay-channel-sub">{{ o.sub }}</span>
      <span v-if="channel === o.channel && method === o.method" class="pay-channel-check">✓</span>
    </button>
  </div>
  <div v-else class="pay-no-channel">
    <slot name="empty">暂无可用的支付渠道，请联系客服</slot>
  </div>
</template>

<style scoped>
.pay-channel-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 10px; }
.pay-channel-img { width: 28px; height: 28px; object-fit: contain; border-radius: 6px; display: block; }
@media (max-width: 520px) { .pay-channel-grid { grid-template-columns: 1fr; } }
.pay-channel {
  position: relative; display: flex; align-items: center; gap: 12px;
  border: 2px solid #e5e7eb; border-radius: 12px; padding: 14px;
  background: #fff; cursor: pointer; text-align: left; transition: all 0.15s;
  font-family: inherit;
}
.pay-channel:hover { border-color: rgba(37, 99, 235, 0.4); }
.pay-channel.active { border-color: #2563eb; background: #eff6ff; box-shadow: 0 2px 8px rgba(37, 99, 235, 0.12); }
.pay-channel-icon {
  width: 38px; height: 38px; border-radius: 10px; flex-shrink: 0;
  background: #f1f5f9; display: inline-flex; align-items: center; justify-content: center; font-size: 18px;
}
.pay-channel-name { font-size: 14px; font-weight: 600; color: #111827; }
.pay-channel-sub { display: block; font-size: 12px; color: #9ca3af; margin-top: 2px; }
.pay-channel-check {
  position: absolute; top: 8px; right: 8px;
  width: 20px; height: 20px; border-radius: 999px;
  background: #2563eb; color: #fff; font-size: 12px;
  display: flex; align-items: center; justify-content: center;
}
.pay-no-channel { padding: 24px; text-align: center; color: #9ca3af; font-size: 14px; }
</style>
