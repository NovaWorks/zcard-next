<script setup lang="ts">
const props = defineProps<{ title: string; sort: string; view: 'grid' | 'list'; loading: boolean }>();
const emit = defineEmits<{ sort: [string]; view: ['grid' | 'list'] }>();
const options = [
  { value: 'default', label: '综合排序' }, { value: 'sales', label: '销量优先' },
  { value: 'newest', label: '最新上架' }, { value: 'price_asc', label: '价格从低到高' },
  { value: 'price_desc', label: '价格从高到低' },
];
function selectSort(event: Event) { emit('sort', (event.target as HTMLSelectElement).value); }
</script>

<template>
  <div class="catalog-toolbar">
    <h2 class="catalog-title"><span aria-hidden="true"></span>{{ title }}</h2>
    <div class="catalog-tools">
      <span class="catalog-progress" role="status">{{ loading ? '更新中…' : '' }}</span>
      <div class="catalog-sort-tabs" role="group" aria-label="商品排序">
        <button v-for="option in options.slice(0, 3)" :key="option.value" type="button" :aria-pressed="sort === option.value" @click="emit('sort', option.value)">{{ { default: '综合', sales: '销量', newest: '最新' }[option.value] }}</button>
        <button type="button" :aria-pressed="sort.startsWith('price_')" :aria-label="sort === 'price_asc' ? '价格从低到高，点击改为从高到低' : '价格排序，点击从低到高'" @click="emit('sort', props.sort === 'price_asc' ? 'price_desc' : 'price_asc')">价格 <span aria-hidden="true">{{ sort === 'price_asc' ? '↑' : sort === 'price_desc' ? '↓' : '↕' }}</span></button>
      </div>
      <select class="catalog-sort-select" aria-label="商品排序" :value="sort" @change="selectSort">
        <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
      </select>
      <div class="catalog-views" role="group" aria-label="商品视图">
        <button type="button" title="网格视图" aria-label="网格视图" :aria-pressed="view === 'grid'" @click="emit('view', 'grid')"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg></button>
        <button type="button" title="列表视图" aria-label="列表视图" :aria-pressed="view === 'list'" @click="emit('view', 'list')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h16M4 12h16M4 19h16"/></svg></button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.catalog-toolbar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px 16px; margin: 4px 0 0; }
.catalog-title { display: flex; align-items: baseline; gap: 8px; margin: 0; min-width: 0; overflow-wrap: anywhere; font-size: 17px; font-weight: 700; color: #111827; }
.catalog-title > span { flex: 0 0 4px; height: 16px; background: #ff5722; border-radius: 4px; }
.catalog-tools { display: flex; align-items: center; gap: 10px; margin-left: auto; min-width: 0; }
.catalog-progress { color: #475569; font-size: 12px; }
.catalog-progress:empty { display: none; }
.catalog-sort-tabs, .catalog-views { display: flex; gap: 4px; padding: 3px; background: #fff; border: 1px solid #e2e8f0; border-radius: 9px; }
.catalog-tools button { display: inline-flex; align-items: center; justify-content: center; gap: 5px; height: 32px; padding: 0 12px; border: 0; border-radius: 6px; background: transparent; color: #475569; font: inherit; font-size: 13px; white-space: nowrap; cursor: pointer; }
.catalog-tools button:hover { background: #eff6ff; color: #1d4ed8; }
.catalog-tools button[aria-pressed="true"] { background: #2563eb; color: #fff; }
.catalog-tools button:focus-visible, .catalog-sort-select:focus-visible { outline: 2px solid #2563eb; outline-offset: 3px; }
.catalog-views button { width: 34px; padding: 0; }
.catalog-views svg { width: 18px; height: 18px; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; }
.catalog-sort-select { display: none; min-width: 0; height: 44px; padding: 0 10px; border: 1px solid #cbd5e1; border-radius: 8px; background: #fff; color: #334155; font-size: 14px; }
@media (max-width: 767px) {
  .catalog-title { flex-basis: 100%; }
  .catalog-tools { width: 100%; margin-left: 0; gap: 8px; }
  .catalog-sort-tabs { display: none; }
  .catalog-sort-select { display: block; flex: 1; }
  .catalog-progress { display: none; }
  .catalog-views { padding: 0; gap: 8px; background: transparent; border: 0; }
  .catalog-views button { width: 44px; height: 44px; background: #fff; border: 1px solid #e2e8f0; }
}
</style>
