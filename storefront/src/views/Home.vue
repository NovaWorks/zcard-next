<template>
  <div>
    <div class="card" style="margin-bottom: 16px; display: flex; gap: 8px;">
      <input v-model="keyword" class="input" placeholder="搜索商品名" @keyup.enter="load" style="max-width: 320px;" />
      <button class="btn" @click="load">搜索</button>
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <div class="grid">
      <div v-for="p in products" :key="p.id" class="card" style="cursor: pointer;" @click="$router.push(`/product/${p.id}`)">
        <div class="tag" style="margin-bottom: 8px;">{{ stockTypeLabel(p.stock_type) }}</div>
        <div style="font-weight: 600; margin-bottom: 8px;">{{ p.name }}</div>
        <div class="price">¥{{ fenToYuan(p.price_cents) }}</div>
        <div class="muted">销量 {{ p.sales_count }}</div>
      </div>
    </div>
    <div v-if="products.length === 0 && !loading" class="muted" style="margin-top: 24px; text-align: center;">暂无商品</div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { listProducts, type Product } from '@/api';
import { fenToYuan } from '@/api/client';

const products = ref<Product[]>([]);
const keyword = ref('');
const loading = ref(false);
const error = ref('');

function stockTypeLabel(t: string) {
  return ({ card: '卡密', url: '链接', code: '兑换码' } as Record<string, string>)[t] || t;
}

async function load() {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listProducts({ keyword: keyword.value || undefined, page: 1, page_size: 50 });
  loading.value = false;
  if (err) { error.value = err; return; }
  products.value = data?.items || [];
}

onMounted(load);
</script>
