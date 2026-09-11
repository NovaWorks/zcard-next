<script setup lang="ts">
import { NSwitch } from 'naive-ui';
type UsageKey = 'allow_purchase' | 'allow_member_recharge' | 'allow_supply_recharge';
const props = defineProps<{ value: Partial<Record<UsageKey, boolean>>; parent?: Partial<Record<UsageKey, boolean>>; wallet?: boolean; compact?: boolean }>();
const emit = defineEmits<{ (e: 'change', key: UsageKey, value: boolean): void }>();
const uses: { key: UsageKey; label: string; help: string }[] = [
  { key: 'allow_purchase', label: '商品购买', help: '商城订单结算时可用' },
  { key: 'allow_member_recharge', label: '会员充值', help: '个人中心充值到会员余额' },
  { key: 'allow_supply_recharge', label: '供货账号充值', help: '下游对接商充值到采购余额' },
];
function blocked(key: UsageKey) { return props.parent?.[key] === false || (!!props.wallet && key !== 'allow_purchase'); }
</script>
<template>
  <div class="usage-switches" :class="{ compact }">
    <div v-for="use in uses" :key="use.key" class="usage-row">
      <div><div class="usage-label">{{ use.label }}</div><div v-if="!compact" class="usage-help">{{ use.help }}</div></div>
      <NSwitch :aria-label="use.label" :value="!blocked(use.key) && value[use.key] !== false" :disabled="blocked(use.key)" :aria-disabled="blocked(use.key)" @update:value="emit('change', use.key, $event)" />
    </div>
  </div>
</template>
<style scoped>
.usage-switches { width:100%; border:1px solid var(--n-border-color, #e5e7eb); border-radius:10px; padding:0 14px; }
.usage-row { display:flex; align-items:center; justify-content:space-between; gap:16px; min-height:64px; padding:8px 0; }
.usage-row + .usage-row { border-top:1px solid var(--n-border-color, #e5e7eb); }
.usage-label { font-weight:500; }
.usage-help { font-size:12px; color:var(--n-text-color-3, #64748b); margin-top:3px; }
.compact .usage-row { min-height:44px; }
@media(min-width:640px) { .compact { display:flex; gap:16px; } .compact .usage-row { flex:1; } .compact .usage-row + .usage-row { border:0; } }
</style>
