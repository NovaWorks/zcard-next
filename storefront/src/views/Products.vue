<template>
  <div class="products-layout">
    <!-- PC 左侧多级分类树（template.category_nav_style=list 时显示） -->
    <CategoryTree v-if="navStyle !== 'grid'" :categories="categories" :model-value="categoryId" @update:model-value="pickCategory" />
    <div class="products-content">
      <!-- 分类胶囊：category_nav_style=grid 时全断点显示（顶部横向导航）；list 时仅移动端 -->
      <div v-if="categories.length" class="card category-chips" :class="{ 'mobile-only': navStyle !== 'grid' }" style="margin-bottom: 12px;">
        <div style="display: flex; flex-wrap: wrap; gap: 8px; align-items: center;">
          <button class="chip" :class="{ active: !categoryId }" @click="pickCategory(0)">全部</button>
          <button v-for="c in categories.filter((x) => !x.parent_id)" :key="c.id" class="chip" :class="{ active: categoryId === c.id }" @click="pickCategory(c.id)">
            {{ c.name }}
          </button>
        </div>
      </div>

      <!-- 排序 + 搜索 + 视图切换 -->
      <div class="card" style="margin-bottom: 16px;">
        <div style="display: flex; flex-wrap: wrap; gap: 8px; align-items: center;">
          <input v-model="keyword" class="input" placeholder="搜索商品名" @keyup.enter="onSearch" style="max-width: 240px;" />
          <button class="btn secondary" @click="onSearch">搜索</button>
          <span class="muted" style="font-size: 13px;">排序：</span>
          <select v-model="sort" class="input" style="max-width: 160px;" @change="onSearch">
            <option value="default">综合排序</option>
            <option value="sales">销量优先</option>
            <option value="newest">最新上架</option>
            <option value="price_asc">价格从低到高</option>
            <option value="price_desc">价格从高到低</option>
          </select>
          <span style="flex: 1;"></span>
          <div class="view-toggle">
            <button class="vt-btn" :class="{ active: viewMode === 'grid' }" title="网格视图" @click="viewMode = 'grid'">▦</button>
            <button class="vt-btn" :class="{ active: viewMode === 'list' }" title="列表视图" @click="viewMode = 'list'">☰</button>
          </div>
        </div>
      </div>

      <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>

      <div v-if="viewMode === 'grid'" class="grid" :style="{ gridTemplateColumns: `repeat(auto-fill, minmax(${gridMinPx}px, 1fr))` }">
        <ProductCard v-for="p in products" :key="p.id" :p="p" mode="grid" :show-sales="showSales" :show-stock="showStock" />
      </div>
      <div v-else class="list-rows">
        <ProductCard v-for="p in products" :key="p.id" :p="p" mode="list" :show-sales="showSales" :show-stock="showStock" />
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
import { ref, computed, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { listProducts, listCategories, type Product, type CategoryItem } from '@/api';
import { fetchSiteSeo, applySeo } from '@/seo';
import ProductCard from '@/components/ProductCard.vue';
import CategoryTree from '@/components/CategoryTree.vue';

const route = useRoute();
const products = ref<Product[]>([]);
const categories = ref<CategoryItem[]>([]);
const keyword = ref('');
const categoryId = ref(0);
// 排序：default=综合（运营权重）| sales | newest | price_asc | price_desc（与后台 sort_by 同值域）
const sort = ref('default');
const page = ref(1);
const pageSize = ref(12);
const total = ref(0);
const loading = ref(false);
const error = ref('');

// ── 模板设置（后台 系统设置 → 模板；公开配置下发，客户端生效）──
const viewMode = ref<'grid' | 'list'>('grid'); // template.default_view（big 归入网格+更宽卡片）
const bigGrid = ref(false); // default_view=big：大图卡片（更宽的列）
const navStyle = ref('list'); // template.category_nav_style：list=左侧树 | grid=顶部胶囊
const gridMinPx = computed(() => (bigGrid.value ? 300 : 200)); // per_row 微调列宽
const showSales = ref(true); // template.show_sales：卡片「已售」显示开关
const showStock = ref(true); // template.show_stock：卡片「库存」显示开关（叠加商品级 stock_visible）

onMounted(async () => {
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const val = (k: string) => {
      const raw = json?.entries?.find((e: any) => e.key === k)?.value_json;
      if (raw === undefined) return undefined;
      try { return JSON.parse(raw); } catch { return raw; }
    };
    // 商品默认视图：list=列表行 | grid=网格 | big=大图（网格加宽）
    const dv = val('template.default_view');
    if (dv === 'list') viewMode.value = 'list';
    else if (dv === 'big') { viewMode.value = 'grid'; bigGrid.value = true; }
    else viewMode.value = 'grid';
    // 每页商品数（防滥用夹在 6~60）
    const pp = Number(val('template.per_page'));
    if (Number.isInteger(pp) && pp >= 6 && pp <= 60) {
      pageSize.value = pp;
      load();
    }
    // 默认排序方式（与后台 sort_by 同值域；default=综合）
    const sb = val('template.sort_by');
    if (['default', 'newest', 'sales', 'price_asc', 'price_desc'].includes(sb) && !route.query.sort) {
      sort.value = sb;
      load();
    }
    // 卡片销量/库存显示开关（显式 false 才关闭，兼容旧数据缺省）
    if (val('template.show_sales') === false) showSales.value = false;
    if (val('template.show_stock') === false) showStock.value = false;
    // 分类导航样式：grid=顶部胶囊（隐藏左侧树）
    const ns = val('template.category_nav_style');
    if (ns === 'grid' || ns === 'list') navStyle.value = ns;
  } catch { /* 配置拉取失败保持默认 */ }
});

async function load() {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listProducts({
    keyword: keyword.value || undefined,
    category_id: categoryId.value || undefined,
    sort: sort.value,
    page: page.value,
    page_size: pageSize.value,
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

// 列表数据预取（setup 顶层：SSG 静态化列表页 + 输出 SEO head）
{
  const q = route.query;
  if (q.category_id) categoryId.value = Number(q.category_id);
  if (typeof q.keyword === 'string') keyword.value = q.keyword;
  if (typeof q.sort === 'string') sort.value = q.sort;
  const { data } = await listCategories();
  categories.value = data?.categories || [];
  await load();
  await applyListSeo();
}

/** 列表页 SEO：分类名进 title；canonical 恒为 /products（分类筛选是同一列表的
 变体，服务端静态页与爬虫视图均为 /products——水合后保持一致避免规范信号打架） */
async function applyListSeo() {
  const site = await fetchSiteSeo();
  const origin = typeof window !== "undefined" ? window.location.origin : site.url;
  const catName = categories.value.find((c) => c.id === categoryId.value)?.name;
  const title = catName ? `${catName} - ${site.name}` : `全部商品 - ${site.name}`;
  applySeo({ title, canonical: `${origin}/products`, ogType: 'website' }, site);
}
</script>

<style scoped>
.products-layout { display: flex; gap: 16px; align-items: flex-start; }
.products-content { flex: 1; min-width: 0; }
.mobile-only { display: flex; }
@media (min-width: 768px) {
  .mobile-only { display: none; }
}
.chip {
  padding: 6px 16px; border-radius: 999px; font-size: 14px; color: #374151;
  background: #f3f4f6; border: 1px solid transparent; cursor: pointer; transition: all .15s;
}
.chip:hover { border-color: #2563eb; color: #2563eb; }
.chip.active { background: #2563eb; color: #fff; }
.view-toggle { display: inline-flex; border: 1px solid #e5e7eb; border-radius: 8px; overflow: hidden; }
.vt-btn {
  border: none; background: #fff; color: #6b7280; font-size: 14px;
  padding: 6px 12px; cursor: pointer; transition: all .15s;
}
.vt-btn + .vt-btn { border-left: 1px solid #e5e7eb; }
.vt-btn:hover { color: #2563eb; }
.vt-btn.active { background: #2563eb; color: #fff; }
.list-rows { display: flex; flex-direction: column; gap: 10px; }
</style>
