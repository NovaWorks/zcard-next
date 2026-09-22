<script setup lang="ts">
import type { PaymentQuote } from '@/api';
import { formatMoney } from '@/api/client';
defineProps<{ quote: PaymentQuote | null; loading?: boolean; error?: string; recharge?: boolean }>();
defineEmits<{ retry: [] }>();
</script>
<template>
  <div class="payment-breakdown" role="status" aria-live="polite" aria-atomic="true">
    <span v-if="loading" class="payment-note">正在计算支付金额…</span>
    <div v-else-if="error" class="payment-quote-error">{{ error }} <button type="button" @click="$emit('retry')">重试</button></div>
    <template v-else-if="quote">
      <div><span>{{ recharge ? '充值本金' : '订单金额' }}</span><span>{{ formatMoney(quote.base_cents) }}</span></div>
      <div><span>支付手续费<template v-if="quote.fee_bearer === 'user' && quote.fee_type === 'percent'">（{{ Number(quote.fee_rate) / 100 }}%）</template></span><span>{{ Number(quote.fee_cents) ? `+${formatMoney(quote.fee_cents)}` : '免手续费' }}</span></div>
      <div class="payment-total"><span>实付金额</span><strong>{{ formatMoney(quote.total_cents) }}</strong></div>
      <p v-if="recharge" class="payment-note">手续费不计入余额，赠送按充值本金计算。</p>
    </template>
  </div>
</template>
<style scoped>
.payment-breakdown { padding: 14px 16px; border: 1px solid #e5e7eb; background: #fff; border-radius: 12px; color: #475569; font-size: 13px; }
.payment-breakdown > div:not(.payment-quote-error) { display:flex; justify-content:space-between; align-items:baseline; gap:16px; padding:4px 0; }
.payment-total { margin-top:6px; border-top:1px solid #edf0f3; color:#111827; }
.payment-total strong { font-size:20px; color:var(--zc-primary); }
.payment-note { font-size:12px; line-height:1.5; color:#64748b; margin:8px 0 0; }
.payment-quote-error { color:#b91c1c; }
.payment-quote-error button { font:inherit; padding:8px 12px; border:1px solid currentColor; border-radius:6px; background:transparent; color:inherit; cursor:pointer; }
</style>
