<template>
  <div class="home">
    <!-- Hero 轮播（公告设置 image/carousel 优先；回落横幅；无图回退渐变品牌区） -->
    <div v-if="heroItems.length" class="hero-slider" @mouseenter="stopHero" @mouseleave="startHero">
      <div class="hero-track" :style="{ transform: `translateX(-${heroIndex * 100}%)` }">
        <div v-for="item in heroItems" :key="item.key" class="hero-slide" @click="item.onClick">
          <img :src="item.img" :alt="item.title" />
          <div v-if="item.title" class="hero-title">{{ item.title }}</div>
        </div>
      </div>
      <div v-if="heroItems.length > 1" class="hero-dots">
        <span
          v-for="(item, i) in heroItems"
          :key="item.key"
          class="hero-dot"
          :class="{ active: i === heroIndex }"
          @click="heroIndex = i"
        ></span>
      </div>
    </div>
    <div v-else class="hero-banner">
      <div>
        <h1>{{ siteName }}</h1>
        <p>自动发货 · 正品保障 · 售后无忧</p>
        <div class="hero-points">
          <span>⚡ 即时发货</span>
          <span>🛡️ 正品保障</span>
          <span>💬 在线客服</span>
        </div>
      </div>
      <span class="hero-icon">🎁</span>
    </div>

    <!-- 公告条（设置文本优先；回落最新公告文章） -->
    <div v-if="noticeBarText" class="notice-bar" @click="$router.push('/posts?type=notice')">
      <span class="tag">公告</span>
      <span class="notice-title">{{ noticeBarText }}</span>
      <span v-if="latestNotice && !announcementText" class="muted">{{ formatDate(latestNotice.published_at) }}</span>
    </div>

    <!-- 左右布局：PC 左侧多级分类树 + 右侧内容；移动端横向胶囊兜底 -->
    <div class="home-layout">
      <CategoryTree
        v-if="navStyle !== 'grid'"
        :categories="categories"
        :model-value="activeCategory"
        @update:model-value="pickCategory"
      />
      <div class="home-content">
        <!-- 搜索 -->
        <div class="search-bar">
          <input
            v-model="keyword"
            class="input"
            placeholder="搜索商品名"
            @keyup.enter="goSearch"
          />
          <button class="btn btn-primary" @click="goSearch">搜索</button>
        </div>

        <!-- 分类胶囊：category_nav_style=grid 时全断点显示；list 时仅移动端兜底 -->
        <div v-if="categories.length" class="card cat-chips" :class="{ 'mobile-only': navStyle !== 'grid' }">
          <div style="display: flex; flex-wrap: wrap; gap: 8px; align-items: center;">
            <button class="chip" :class="{ active: !activeCategory }" @click="pickCategory(0)">全部</button>
            <button v-for="c in categories.filter((x) => !x.parent_id)" :key="c.id" class="chip" :class="{ active: activeCategory === c.id }" @click="pickCategory(c.id)">
              {{ c.name }}
            </button>
          </div>
        </div>

        <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>

        <!-- 商品区标题 + 视图切换 -->
        <div class="section-head">
          <h2 class="section-title"><span class="title-bar"></span>{{ sectionTitle }}</h2>
          <div class="view-switcher">
            <button :class="{ active: viewMode === 'grid' }" @click="viewMode = 'grid'" title="网格视图">▦</button>
            <button :class="{ active: viewMode === 'list' }" @click="viewMode = 'list'" title="列表视图">☰</button>
          </div>
        </div>

        <!-- 商品列表（网格/列表双视图） -->
        <div v-if="viewMode === 'grid'" class="product-grid" :style="{ gridTemplateColumns: `repeat(auto-fill, minmax(${gridMinPx}px, 1fr))` }">
          <ProductCard v-for="p in products" :key="p.id" :p="p" mode="grid" />
        </div>
        <div v-else class="product-list">
          <ProductCard v-for="p in products" :key="p.id" :p="p" mode="list" />
        </div>
        <div v-if="products.length === 0 && !loading" class="empty-state">
          <div class="empty-icon">📦</div>
          <div class="muted">暂无商品</div>
        </div>

        <!-- 分页器（首页/页码/末页 + 每页条数） -->
        <div v-if="totalPage > 1" class="pager">
          <span class="pager-total muted">共 {{ total }} 件</span>
          <div class="pager-btns">
            <button class="pager-btn" :disabled="page <= 1" title="首页" @click="goPage(1)">«</button>
            <button class="pager-btn" :disabled="page <= 1" @click="goPage(page - 1)">上一页</button>
            <template v-for="p in pageList" :key="p">
              <span v-if="p === 0" class="pager-ellipsis">…</span>
              <button v-else class="pager-btn" :class="{ active: p === page }" @click="goPage(p)">{{ p }}</button>
            </template>
            <button class="pager-btn" :disabled="page >= totalPage" @click="goPage(page + 1)">下一页</button>
            <button class="pager-btn" :disabled="page >= totalPage" title="末页" @click="goPage(totalPage)">»</button>
          </div>
          <div class="pager-size">
            <span class="muted">每页</span>
            <select v-model.number="pageSize" class="pager-select" @change="goPage(1)">
              <option :value="12">12</option>
              <option :value="24">24</option>
              <option :value="48">48</option>
            </select>
            <span class="muted">条</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import { listProducts, listBanners, listPosts, listCategories, fetchAnnouncement, type Product, type Banner, type StorePost, type CategoryItem, type AnnouncementConfig } from '@/api';
import { fetchSiteSeo, applyDefaultSeo, applyVerification } from '@/seo';
import { formatMoney } from '@/api/client';
import ProductCard from '@/components/ProductCard.vue';
import CategoryTree from '@/components/CategoryTree.vue';

const router = useRouter();
const products = ref<Product[]>([]);
const keyword = ref('');
const loading = ref(false);
const error = ref('');
const banners = ref<Banner[]>([]);
const latestNotice = ref<StorePost | null>(null);
const categories = ref<CategoryItem[]>([]);
const activeCategory = ref(0);
const viewMode = ref<'grid' | 'list'>('grid');
const page = ref(1);
const pageSize = ref(12);
const total = ref(0);
const announcement = ref<AnnouncementConfig>({ type: 'text', text: '', images: [] });

const siteName = 'ZCard 商店';

// ── 模板设置（后台 系统设置 → 模板；与商品列表页同源消费，保证全站一致）──
const navStyle = ref('list'); // template.category_nav_style：list=左侧树 | grid=顶部胶囊
const bigGrid = ref(false); // template.default_view=big：大图卡片（更宽的列）
const gridMinPx = computed(() => (bigGrid.value ? 300 : 200));
const sectionTitle = computed(() =>
  activeCategory.value
    ? categories.value.find((c) => c.id === activeCategory.value)?.name || '全部商品'
    : '全部商品',
);
const totalPage = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)));
// 页码列表：当前页 ±2 + 首末页；越界段用 0 占位渲染省略号
const pageList = computed(() => {
  const cur = page.value, last = totalPage.value;
  const set = new Set<number>([1, last, cur - 2, cur - 1, cur, cur + 1, cur + 2]);
  const nums = [...set].filter((p) => p >= 1 && p <= last).sort((a, b) => a - b);
  const out: number[] = [];
  let prev = 0;
  for (const p of nums) {
    if (prev && p - prev > 1) out.push(0); // 省略号
    out.push(p);
    prev = p;
  }
  return out;
});
function goPage(p: number) {
  const target = Math.min(Math.max(1, p), totalPage.value);
  if (target === page.value) return;
  page.value = target;
  load();
  window.scrollTo({ top: 0, behavior: 'smooth' });
}

// Hero 轮播：公告设置 image/carousel 优先；否则用生效横幅；点击行为跟随来源
const heroItems = computed(() => {
  const items: { key: string; img: string; title: string; onClick?: () => void }[] = [];
  const ann = announcement.value;
  if ((ann.type === 'image' || ann.type === 'carousel') && ann.images.length) {
    ann.images.forEach((img, i) => items.push({ key: `ann-${i}`, img, title: '', onClick: openAnnouncement }));
  } else {
    banners.value.forEach((b) => items.push({ key: `bn-${b.id}`, img: bannerImg(b), title: b.title, onClick: () => openBanner(b) }));
  }
  return items;
});
// 公告条：设置文本优先，回落最新公告文章标题
const announcementText = computed(() => (announcement.value.type === 'text' ? announcement.value.text : ''));
const noticeBarText = computed(() => announcementText.value || latestNotice.value?.title || '');

// 点击公告轮播 → 打开全局公告弹窗（App.vue 监听）
function openAnnouncement() {
  window.dispatchEvent(new CustomEvent('zcard-open-notice'));
}

const heroIndex = ref(0);
let heroTimer: ReturnType<typeof setInterval> | null = null;
function startHero() {
  stopHero();
  if (heroItems.value.length > 1) {
    heroTimer = setInterval(() => {
      heroIndex.value = (heroIndex.value + 1) % heroItems.value.length;
    }, 4500);
  }
}
function stopHero() {
  if (heroTimer) { clearInterval(heroTimer); heroTimer = null; }
}

function bannerImg(b: Banner): string {
  // SSR 构建期无 window：PC 图（移动端图由客户端水合后按视口切换）
  const isMobile = typeof window !== 'undefined' && window.innerWidth < 640;
  return (isMobile && b.mobile_image) ? b.mobile_image : b.image;
}

// Banner 点击：product → 详情；category → 分类列表；post → 文章详情；notice → 公告弹窗；url → 外链
function openBanner(b: Banner) {
  if (b.link_type === 'product' && b.link_value) {
    router.push(`/product/${b.link_value}`);
  } else if (b.link_type === 'category' && b.link_value) {
    router.push(`/products?category_id=${b.link_value}`);
  } else if (b.link_type === 'post' && b.link_value) {
    router.push(`/posts/${b.link_value}`);
  } else if (b.link_type === 'notice') {
    openAnnouncement();
  } else if (b.link_type === 'url' && b.link_value) {
    window.open(b.link_value, '_blank', 'noopener');
  }
}

function goSearch() {
  router.push({ path: '/products', query: keyword.value ? { keyword: keyword.value } : {} });
}

function pickCategory(id: number) {
  activeCategory.value = id;
  page.value = 1;
  load();
}

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

async function load() {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listProducts({
    keyword: keyword.value || undefined,
    category_id: activeCategory.value || undefined,
    page: page.value,
    page_size: pageSize.value,
  });
  loading.value = false;
  if (err) { error.value = err; return; }
  products.value = data?.items || [];
  total.value = data?.total || 0;
}

// 首页数据预取（setup 顶层：SSG 构建时渲染完整内容；客户端水合复用后由 onMounted 启动轮播）
await Promise.all([
  load(),
  listBanners('top').then((b) => { banners.value = b?.data?.banners || []; }),
  listPosts('notice', 1, 1).then((n) => { latestNotice.value = n?.data?.posts?.[0] || null; }),
  listCategories().then((c) => { categories.value = c?.data?.categories || []; }),
  fetchAnnouncement().then((a) => { announcement.value = a; }),
  // 首页默认 SEO（仅首页；页面级 SEO 由各自页面组件负责）
  fetchSiteSeo().then((site) => {
    applyDefaultSeo(site);
    applyVerification(site);
  }),
]);

// 模板设置读取（onMounted：SSG 构建期不拉取，水合后客户端实时应用）
onMounted(async () => {
  startHero();
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const val = (k: string) => {
      const raw = json?.entries?.find((e: any) => e.key === k)?.value_json;
      if (raw === undefined) return undefined;
      try { return JSON.parse(raw); } catch { return raw; }
    };
    // 分类导航样式：grid=顶部胶囊（隐藏左侧树）
    const ns = val('template.category_nav_style');
    if (ns === 'grid' || ns === 'list') navStyle.value = ns;
    // 商品默认视图：list=列表行 | grid=网格 | big=大图（网格加宽）
    const dv = val('template.default_view');
    if (dv === 'list') viewMode.value = 'list';
    else if (dv === 'big') { viewMode.value = 'grid'; bigGrid.value = true; }
    else viewMode.value = 'grid';
  } catch { /* 配置拉取失败保持默认 */ }
});
onUnmounted(stopHero);
</script>

<style scoped>
.home { display: flex; flex-direction: column; gap: 16px; }

/* 左右布局：PC 左侧分类树 + 右侧内容 */
.home-layout { display: flex; gap: 16px; align-items: flex-start; }
.home-content { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 16px; }
.mobile-only { display: flex; }
@media (min-width: 768px) {
  .mobile-only { display: none; }
}
/* 分类胶囊（category_nav_style=grid 顶部导航 / list 移动端兜底） */
.cat-chips { padding: 12px 14px; }
.chip {
  padding: 6px 16px; border-radius: 999px; font-size: 14px; color: #374151;
  background: #f3f4f6; border: 1px solid transparent; cursor: pointer; transition: all .15s;
}
.chip:hover { border-color: #2563eb; color: #2563eb; }
.chip.active { background: #2563eb; color: #fff; }

/* ── Hero ── */
.hero-slider {
  position: relative;
  border-radius: 14px;
  overflow: hidden;
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.08);
}
.hero-track { display: flex; transition: transform 0.5s ease; }
.hero-slide { flex: 0 0 100%; position: relative; cursor: pointer; }
.hero-slide img { width: 100%; height: clamp(240px, 32vw, 360px); object-fit: cover; display: block; }
.hero-title {
  position: absolute; left: 0; right: 0; bottom: 0;
  padding: 30px 22px 16px;
  background: linear-gradient(180deg, transparent, rgba(0, 0, 0, 0.65));
  color: #fff; font-size: clamp(17px, 2.4vw, 26px); font-weight: 700;
  letter-spacing: 0.5px; text-shadow: 0 1px 8px rgba(0, 0, 0, 0.5);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.hero-dots {
  position: absolute; bottom: 10px; right: 16px; display: flex; gap: 6px;
}
.hero-dot {
  width: 8px; height: 8px; border-radius: 999px; background: rgba(255,255,255,0.5);
  cursor: pointer; transition: all 0.2s;
}
.hero-dot.active { background: #fff; width: 20px; }

.hero-banner {
  display: flex; align-items: center; justify-content: space-between;
  padding: 36px 32px; border-radius: 14px; color: #fff;
  background: linear-gradient(135deg, #1d4ed8, #2563eb, #3b82f6);
  box-shadow: 0 4px 16px rgba(37, 99, 235, 0.25);
}
.hero-banner h1 { font-size: 28px; margin-bottom: 8px; }
.hero-banner p { opacity: 0.9; margin-bottom: 14px; }
.hero-points { display: flex; gap: 14px; font-size: 13px; }
.hero-icon { font-size: 72px; opacity: 0.35; }

/* ── 公告 ── */
.notice-bar {
  display: flex; align-items: center; gap: 10px;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 10px 16px; cursor: pointer; transition: border-color 0.2s;
}
.notice-bar:hover { border-color: rgba(37, 99, 235, 0.45); }
.notice-title {
  flex: 1; font-size: 14px; color: #1f2329;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}

/* ── 搜索 ── */
.search-bar {
  display: flex; gap: 10px; background: #fff; border: 1px solid #e5e7eb;
  border-radius: 12px; padding: 10px 14px;
}
.search-bar .input { flex: 1; border: none; outline: none; padding: 8px 12px; font-size: 14px; }
.search-bar .input:focus { box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.2); border-radius: 8px; }

/* ── 商品区 ── */
.section-head {
  display: flex; align-items: center; justify-content: space-between;
  margin-top: 4px;
}
.section-title {
  display: flex; align-items: center; gap: 8px; font-size: 17px; font-weight: 700; color: #111827;
}
.title-bar { width: 4px; height: 18px; border-radius: 999px; background: #ff5722; display: inline-block; }
.view-switcher { display: flex; gap: 4px; background: #fff; border: 1px solid #e5e7eb; border-radius: 8px; padding: 3px; }
.view-switcher button {
  width: 30px; height: 26px; border: none; background: none; border-radius: 6px;
  cursor: pointer; font-size: 14px; color: #9ca3af; transition: all 0.15s;
}
.view-switcher button.active { background: #2563eb; color: #fff; }

.product-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 14px;
}
.product-list { display: flex; flex-direction: column; gap: 10px; }

.empty-state { text-align: center; padding: 48px 0; }
.empty-icon { font-size: 44px; margin-bottom: 8px; }

.pager {
  display: flex; align-items: center; justify-content: center; gap: 14px; flex-wrap: wrap;
  padding: 14px 0 6px;
}
.pager-btns { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.pager-btn {
  min-width: 34px; height: 34px; padding: 0 10px;
  border: 1px solid #e5e7eb; border-radius: 8px; background: #fff;
  font-size: 13px; color: #4b5563; cursor: pointer; transition: all 0.15s;
}
.pager-btn:hover:not(:disabled):not(.active) { border-color: #2563eb; color: #2563eb; }
.pager-btn.active { background: #2563eb; border-color: #2563eb; color: #fff; font-weight: 600; }
.pager-btn:disabled { opacity: 0.45; cursor: not-allowed; }
.pager-ellipsis { color: #9ca3af; padding: 0 2px; }
.pager-size { display: flex; align-items: center; gap: 6px; font-size: 13px; }
.pager-select {
  padding: 5px 8px; border: 1px solid #d1d5db; border-radius: 8px;
  font-size: 13px; outline: none; background: #fff;
}
.pager-total { font-size: 13px; }
</style>
