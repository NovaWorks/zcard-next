<template>
  <label v-if="enabledLocales.length > 1" class="language-switch">
    <span class="sr-only">{{ t('选择语言') }}</span>
    <select :value="locale" :aria-label="t('选择语言')" @change="change">
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  </label>
</template>
<script setup lang="ts">
import { computed } from 'vue';
import { enabledLocales, locale, selectLocale, SUPPORTED_LOCALES, t, type Locale } from '@/i18n';
const options = computed(() => SUPPORTED_LOCALES.filter(option => enabledLocales.value.includes(option.value)));
function change(event: Event) { selectLocale((event.target as HTMLSelectElement).value as Locale); }
</script>
<style scoped>
.language-switch select { max-width: 130px; height: 34px; padding: 0 6px; border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; color: #374151; font: inherit; font-size: 13px; cursor: pointer; }
.language-switch select:focus-visible { outline: 2px solid var(--zc-primary); outline-offset: 2px; }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
@media (max-width: 768px) { .language-switch select { height: 30px; max-width: 105px; font-size: 12px; } }
</style>
