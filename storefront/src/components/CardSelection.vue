<script setup lang="ts">
import { ref, watch } from 'vue';
import { t as $t, uiText } from '@/i18n';
import { listCardDrafts, type CardDraft } from '@/api';
import { formatMoney } from '@/api/client';

const props = defineProps<{ productId: number; skuId: number; modelValue: string; minimum: number }>();
const emit = defineEmits<{ 'update:modelValue': [string] }>();
const items = ref<CardDraft[]>([]);
const selected = ref<CardDraft>();
const page = ref(1), total = ref(0), loading = ref(false), error = ref('');
const limit = 20;
let generation = 0;

async function load() {
  if (props.minimum > 1) return;
  const current = ++generation;
  loading.value = true;
  error.value = '';
  const result = await listCardDrafts(props.productId, props.skuId, page.value, limit);
  if (current !== generation) return;
  loading.value = false;
  if (result.error) { error.value = result.error; return; }
  items.value = result.data?.items || [];
  total.value = Number(result.data?.total || 0);
}

function choose(item?: CardDraft) {
  selected.value = item;
  emit('update:modelValue', item?.selection || '');
}

watch(() => [props.productId, props.skuId, props.minimum], () => {
  generation++;
  items.value = [];
  selected.value = undefined;
  loading.value = false;
  error.value = '';
  page.value = 1;
  total.value = 0;
  emit('update:modelValue', '');
}, { immediate: true });
watch(() => props.modelValue, value => { if (!value) selected.value = undefined; });
</script>

<template>
  <fieldset class="card-selection">
    <legend>{{ $t('选择卡密（选填）') }}</legend>
    <p class="muted">{{ $t('不选择则随机交付；选定卡密时每单仅购买一份，最终金额以确认报价为准。') }}</p>
    <p v-if="minimum > 1" role="status" class="muted">{{ $t('此商品起购数量大于一，暂不支持单卡预选。') }}</p>
    <template v-else>
      <div v-if="selected" class="card-picked" role="status">
        <span>{{ selected.preview }}</span>
        <span>{{ $t('参考附加价') }} {{ formatMoney(selected.premium_cents || 0) }}</span>
        <button type="button" class="btn secondary" @click="choose()">{{ $t('改为随机交付') }}</button>
      </div>
      <button type="button" class="btn secondary" :disabled="loading" @click="load">{{ loading ? $t('加载中…') : $t('查看可选卡密') }}</button>
      <p v-if="error" role="alert" class="error">{{ uiText(error) }}</p>
      <div v-if="items.length" class="card-picks" role="group" :aria-label="$t('可选卡密')" :aria-busy="loading">
        <button v-for="item in items" :key="item.selection" type="button" class="card-pick" :disabled="loading" :aria-pressed="modelValue === item.selection" @click="choose(item)">
          <span>{{ item.preview }}</span><small>{{ $t('参考附加价') }} {{ formatMoney(item.premium_cents || 0) }}</small>
        </button>
      </div>
      <div v-if="items.length || page > 1" class="card-paging">
        <button type="button" class="btn secondary" :disabled="loading || page <= 1" @click="page--; load()">{{ $t('上一页') }}</button>
        <span>{{ $t('第 {0} 页', [page]) }}</span>
        <button type="button" class="btn secondary" :disabled="loading || page * limit >= total" @click="page++; load()">{{ $t('下一页') }}</button>
      </div>
    </template>
  </fieldset>
</template>

<style scoped>
.card-selection{min-width:0;border:1px solid var(--border-color,#e5e7eb);border-radius:10px;padding:16px;margin:12px 0}.card-selection legend{font-weight:600;padding:0 6px}.card-selection p{line-height:1.6}.card-picks{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,200px),1fr));gap:8px;margin-top:12px}.card-pick{display:grid;gap:6px;min-height:60px;padding:12px;text-align:left;border:1px solid var(--border-color,#e5e7eb);border-radius:8px;background:var(--card-bg,#fff);color:inherit;overflow-wrap:anywhere;cursor:pointer}.card-pick[aria-pressed=true]{border-color:var(--zc-primary,#2563eb);outline:2px solid var(--zc-primary,#2563eb)}.card-pick small,.muted{color:var(--text-muted,#6b7280)}.card-picked{display:grid;gap:8px;margin:12px 0;overflow-wrap:anywhere}.card-paging{display:flex;justify-content:space-between;align-items:center;gap:8px;margin-top:12px}.card-selection .btn{min-height:44px}.error{color:#b42318;line-height:1.6}
</style>
