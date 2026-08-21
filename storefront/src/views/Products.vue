<template>
  <div class="products-layout">
    <!-- PC 左侧多级分类树 -->
    <CategoryTree :categories="categories" :model-value="categoryId" @update:model-value="pickCategory" />
    <div class="products-content">
      <!-- 移动端分类胶囊 -->
      <div v-if="categories.length" class="card mobile-only" style="margin-bottom: 12px;">
        <div style="display: flex; flex-wrap: wrap; gap: 8px; align-items: center;">
          <button class="chip" :class="{ active: !categoryId }" @click="pickCategory(0)">全部</button>
          <button v-for="c in categories.filter((x) => x.parent_id === 0)" :key="c.id" class="chip" :class="{ active: categoryId === c.id }" @click="pickCategory(c.id)">
            {{ c.name }}
          </button>
        </div>
      </div>

      <!-- 排序 + 搜索 -->
      <div class="card" style="margin-bottom: 16px;">
        <div style="display: flex; flex-wrap: wrap; gap: 8px; align-items: center;">
          <input v-model="keyword" class="input" placeholder="搜索商品名" @keyup.enter="onSearch" style="max-width: 240px;" />
          <button class="btn secondary" @click="onSearch">搜索</button>
          <span class="muted" style="font-size: 13px;">排序：</span>
          <select v-model="sort" class="input" style="max-width: 160px;" @change="onSearch">
            <option value="newest">综合排序</option>
            <option value="sales">销量优先</option>
            <option value="price_asc">价格从低到高</option>
            <option value="price_desc">价格从高到低</option>
          </select>
        </div>
      </div>

      <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>

      <div class="grid" style="grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));">
        <ProductCard v-for="p in products" :key="p.id" :p="p" mode="grid" />
      </div>
      <div v-if="products.length === 0 && !loading" class="muted" style="margin-top: 24px; text-align: center;">暂无商品</div>

      <!-- 分页 -->
      <div v-if="total > pageSize" class="actions" style="margin-top: 20px; justify-content: center;">
        <button class="btn secondary" :disabled="page <= 1" @click="go(page - 1)">上一页</button>
        <span class="muted">{{ page }} / {{ Math.ceil(total / pageSize) }}</span>
        <button class="btn secondary" :disabled="page >= Math.ceil(total / pageSize)" @click="go(page + 1)">下一页</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { listProducts, listCategories, type Product, type CategoryItem } from '@/api';
import ProductCard from '@/components/ProductCard.vue';
import CategoryTree from '@/components/CategoryTree.vue';

const route = useRoute();
const router = useRouter();
const products = ref<Product[]>([]);
const categories = ref<CategoryItem[]>([]);
const keyword = ref('');
const categoryId = ref(0);
const sort = ref('newest');
const page = ref(1);
const pageSize = 12;
const total = ref(0);
const loading = ref(false);
const error = ref('');

async function load() {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listProducts({
    keyword: keyword.value || undefined,
    category_id: categoryId.value || undefined,
    sort: sort.value,
    page: page.value,
    page_size: pageSize,
  });
  loading.value = false;
  if (err) { error.value = err; return; }
  products.value = data?.items || [];
  total.value = data?.total || 0;
}

function go(p: number) {
  page.value = p;
  load();
  window.scrollTo({ top: 0 });
}

function pickCategory(id: number) {
  categoryId.value = id;
  onSearch();
}

function onSearch() {
  page.value = 1;
  load();
}

onMounted(async () => {
  // 路由参数（分类/关键词/排序）
  const q = route.query;
  if (q.category_id) categoryId.value = Number(q.category_id);
  if (typeof q.keyword === 'string') keyword.value = q.keyword;
  if (typeof q.sort === 'string') sort.value = q.sort;
  const { data } = await listCategories();
  categories.value = data?.categories || [];
  load();
});
</script>

<style scoped>
.products-layout { display: flex; gap: 16px; align-items: flex-start; }
.products-content { flex: 1; min-width: 0; }
.mobile-only { display: flex; }
@media (min-width: 768px) {
  .mobile-only { display: none; }
}
.chip {
  padding: 4px 14px; border-radius: 999px; font-size: 13px; color: #374151;
  background: #f3f4f6; border: 1px solid transparent; cursor: pointer; transition: all .15s;
}
.chip:hover { border-color: #2563eb; color: #2563eb; }
.chip.active { background: #2563eb; color: #fff; }
</style>
