<script setup lang="ts">
import { ref, watch } from 'vue';

const props = defineProps<{ type: string; icon?: string }>();
const imageFailed = ref(false);
watch(() => props.icon, () => { imageFailed.value = false; });
</script>

<template>
  <span class="payment-method-icon" aria-hidden="true">
    <img v-if="icon && !imageFailed" :src="icon" alt="" @error="imageFailed = true" />
    <svg v-else-if="type === 'alipay'" viewBox="0 0 32 32">
      <rect width="32" height="32" rx="7" fill="#1677ff" />
      <text x="16" y="24" text-anchor="middle" fill="white" font-size="24" font-family="Arial, sans-serif">支</text>
    </svg>
    <svg v-else-if="type === 'wechat' || type === 'wxpay'" viewBox="0 0 32 32">
      <rect width="32" height="32" rx="7" fill="#07c160" />
      <path d="M14 6C8.5 6 4 9.5 4 14c0 2.5 1.4 4.7 3.8 6.2L7 23l3.3-1.7c1.2.4 2.4.6 3.7.6 5.5 0 10-3.5 10-7.9S19.5 6 14 6Z" fill="white" />
      <path d="M28.5 20c0-3.5-3.5-6.3-7.8-6.3S13 16.5 13 20s3.4 6.3 7.7 6.3c1 0 2-.2 2.9-.5l2.6 1.3-.6-2.3c1.8-1.1 2.9-2.8 2.9-4.8Z" fill="white" stroke="#07c160" />
      <g fill="#07c160"><circle cx="10.5" cy="12" r="1.2" /><circle cx="17.5" cy="12" r="1.2" /><circle cx="18" cy="18.5" r="1" /><circle cx="23.5" cy="18.5" r="1" /></g>
    </svg>
    <svg v-else-if="type.startsWith('usdt')" viewBox="0 0 32 32">
      <circle cx="16" cy="16" r="16" fill="#26a17b" />
      <path d="M7 7h18v4h-7v4.2c4.9.2 8.5 1.1 8.5 2.2S21.9 20 16 20 5.5 18.9 5.5 17.4s3.6-2 8.5-2.2V11H7V7Zm7 9.2c-4 .2-6.5.9-6.5 1.3 0 .5 3.8 1.3 8.5 1.3s8.5-.8 8.5-1.3c0-.4-2.5-1.1-6.5-1.3v1.5h-4v-1.5ZM14 20h4v6h-4v-6Z" fill="white" />
    </svg>
    <svg v-else viewBox="0 0 32 32" fill="none">
      <rect width="32" height="32" rx="7" fill="#eff6ff" />
      <g stroke="#2563eb" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <template v-if="type === 'bank'">
          <path d="m5 12 11-7 11 7H5ZM7 26h18M9 15v8m7-8v8m7-8v8" />
        </template>
        <template v-else>
          <rect x="5" y="8" width="22" height="16" rx="3" /><path d="M5 13h22M9 19h5" />
        </template>
      </g>
    </svg>
  </span>
</template>

<style scoped>
.payment-method-icon { display: inline-flex; width: 28px; height: 28px; flex: none; align-items: center; justify-content: center; }
.payment-method-icon img, .payment-method-icon svg { width: 100%; height: 100%; display: block; object-fit: contain; border-radius: 6px; }
</style>
