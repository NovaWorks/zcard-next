<script setup lang="ts">
// 收银台支付方式网格（Payment.vue 订单支付 / Member.vue 余额充值 共用）：
// 方式级选项卡片（自定义图标 → emoji 回落）：图标居左、名称+描述上下两行、
// 勾选标右侧居中；列数按容器实际宽度自适应（充值右栏窄 → 整行列表，订单页宽 → 两列）。
import type { PayOption } from '@/composables/pay-options';

defineProps<{
  options: PayOption[];
  channel: string;
  method: string;
}>();

defineEmits<{ select: [channel: string, method: string] }>();
</script>

<template>
  <div v-if="options.length" class="pay-grid-wrap">
    <div class="pay-channel-grid">
      <button
        v-for="o in options"
        :key="o.channel + ':' + o.method"
        type="button"
        :disabled="o.disabled"
        :aria-pressed="channel === o.channel && method === o.method"
        class="pay-channel"
        :class="{ active: channel === o.channel && method === o.method }"
        @click="$emit('select', o.channel, o.method)"
      >
        <span class="pay-channel-icon">
          <img v-if="o.icon" :src="o.icon" :alt="o.name" class="pay-channel-img" />
          <template v-else>{{ o.emoji }}</template>
        </span>
        <span class="pay-channel-info">
          <span class="pay-channel-heading"><span class="pay-channel-name">{{ o.name }}</span><span v-if="o.recommended" class="pay-recommend">{{ o.recommendLabel }}</span></span>
          <span class="pay-channel-sub">{{ o.feeText }}</span><span v-if="o.availability" class="pay-channel-description" aria-live="polite">{{ o.availability }}</span><span v-if="o.recommended && o.recommendDescription" class="pay-channel-description">{{ o.recommendDescription }}</span>
        </span>
        <span class="pay-channel-check" aria-hidden="true">✓</span>
      </button>
    </div>
  </div>
  <div v-else class="pay-no-channel">
    <slot name="empty">暂无可用的支付渠道，请联系客服</slot>
  </div>
</template>

<style scoped>
/* 容器查询按实际宽度切换列数：窄面板（充值右栏/手机）整行列表，宽容器（订单页）两列 */
.pay-grid-wrap { container-type: inline-size; }
.pay-channel-grid { display: grid; grid-template-columns: 1fr; gap: 10px; }
@container (min-width: 460px) {
  .pay-channel-grid { grid-template-columns: repeat(2, 1fr); }
}
.pay-channel {
  display: flex; align-items: center; gap: 10px; min-width: 0;
  border: 2px solid #e5e7eb; border-radius: 12px; padding: 11px 12px;
  background: #fff; cursor: pointer; text-align: left; transition: all 0.15s;
  font-family: inherit;
}
.pay-channel:disabled { cursor: not-allowed; background: #f8fafc; border-color: #e5e7eb; }
.pay-channel:hover:not(:disabled) { border-color: color-mix(in srgb, var(--zc-primary) 40%, transparent); }
.pay-channel.active { border-color: var(--zc-primary); background: var(--zc-primary-soft); box-shadow: 0 2px 8px color-mix(in srgb, var(--zc-primary) 12%, transparent); }
.pay-channel-icon {
  width: 36px; height: 36px; border-radius: 10px; flex-shrink: 0;
  background: #f1f5f9; display: inline-flex; align-items: center; justify-content: center; font-size: 18px;
}
.pay-channel-img { width: 26px; height: 26px; object-fit: contain; border-radius: 6px; display: block; }
.pay-channel-heading { display:flex; align-items:center; gap:6px; flex-wrap:wrap; }
.pay-recommend { flex-shrink:0; white-space:nowrap; border-radius:5px; padding:2px 6px; font-size:11px; line-height:1.5; color:#9a4b08; background:#fff1dc; font-weight:600; }
.pay-channel-description { font-size:12px; line-height:1.5; color:#64748b; overflow-wrap:anywhere; }
.pay-channel:focus-visible { outline:2px solid var(--zc-primary); outline-offset:3px; }
.pay-channel-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.pay-channel-name { font-size: 14px; font-weight: 600; color: #111827; line-height: 1.3; }
.pay-channel-sub {
  font-size: 12px; color: #64748b; line-height: 1.5;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
/* 勾选标常驻占位（对齐不跳），未选中透明缩小、选中放大浮现 */
.pay-channel-check {
  flex-shrink: 0; width: 20px; height: 20px; border-radius: 999px;
  background: var(--zc-primary); color: #fff; font-size: 12px;
  display: flex; align-items: center; justify-content: center;
  opacity: 0; transform: scale(0.6); transition: opacity 0.15s, transform 0.15s;
}
.pay-channel.active .pay-channel-check { opacity: 1; transform: scale(1); }
.pay-no-channel { padding: 24px; text-align: center; color: #9ca3af; font-size: 14px; }
</style>
