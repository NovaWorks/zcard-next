<template>
  <!-- 顶部货币切换器（多币种才显示；展示层换算——结算仍以站点基准货币） -->
  <div v-if="options.length > 1" ref="rootEl" class="currency-switch">
    <button class="cs-btn" type="button" title="切换展示货币（结算以基准货币）" @click="open = !open">
      <span class="cs-symbol">{{ current.symbol }}</span>
      <span class="cs-code">{{ current.code }}</span>
      <span class="cs-caret">▾</span>
    </button>
    <div v-if="open" class="cs-menu">
      <button
        v-for="c in options"
        :key="c.code"
        type="button"
        class="cs-item"
        :class="{ active: c.code === current.code }"
        @click="pick(c.code)"
      >
        <span class="cs-item-symbol">{{ c.symbol }}</span>
        <span class="cs-item-code">{{ c.code }}</span>
        <span v-if="c.code === current.code" class="cs-item-check">✓</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { getCurrency, initCurrency, listCurrencies, selectCurrency, type CurrencyMeta } from '@/api/client';

const open = ref(false);
const options = ref<CurrencyMeta[]>([]);
const current = ref<CurrencyMeta>(getCurrency());
const rootEl = ref<HTMLElement | null>(null);

onMounted(async () => {
  await initCurrency();
  options.value = listCurrencies();
  current.value = getCurrency();
});

function pick(code: string) {
  open.value = false;
  selectCurrency(code);
}

function onDocClick(e: MouseEvent) {
  if (open.value && rootEl.value && !rootEl.value.contains(e.target as Node)) open.value = false;
}
onMounted(() => document.addEventListener('click', onDocClick));
onUnmounted(() => document.removeEventListener('click', onDocClick));
</script>

<style scoped>
.currency-switch { position: relative; }
.cs-btn {
  display: inline-flex; align-items: center; gap: 5px; height: 34px; padding: 0 10px;
  border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; cursor: pointer;
  font-size: 13px; color: #374151; font-family: inherit; transition: border-color 0.15s;
}
.cs-btn:hover { border-color: #2563eb; color: #2563eb; }
.cs-symbol { font-weight: 700; }
.cs-code { font-weight: 600; letter-spacing: 0.3px; }
.cs-caret { font-size: 10px; color: #9ca3af; }
.cs-menu {
  position: absolute; right: 0; top: calc(100% + 6px); z-index: 60; min-width: 140px;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 10px;
  box-shadow: 0 10px 30px rgba(15, 23, 42, 0.12); padding: 6px; display: flex; flex-direction: column;
}
.cs-item {
  display: flex; align-items: center; gap: 8px; width: 100%;
  padding: 8px 10px; border: none; border-radius: 8px; background: none;
  cursor: pointer; font-size: 13px; color: #374151; font-family: inherit; text-align: left;
}
.cs-item:hover { background: #f0f6ff; color: #2563eb; }
.cs-item.active { color: #2563eb; font-weight: 600; }
.cs-item-symbol { width: 18px; text-align: center; font-weight: 700; }
.cs-item-code { flex: 1; }
.cs-item-check { font-size: 12px; }
@media (max-width: 768px) {
  .cs-code { display: none; }
  .cs-btn { height: 30px; padding: 0 8px; }
}
</style>
