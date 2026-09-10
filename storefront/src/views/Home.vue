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

    <!-- 公告条（设置文本优先；回落最新公告文章；点击弹公告弹窗，与导航📢同源） -->
    <div v-if="noticeBarText" class="notice-bar" @click="openNoticeModal()">
      <span class="tag">公告</span>
      <span class="notice-title">{{ noticeBarText }}</span>
      <span v-if="latestNotice && !announcementText" class="muted">{{ formatDate(latestNotice.published_at) }}</span>
    </div>

    <!-- 左右布局：PC 左侧多级分类树 + 右侧内容；移动端横向胶囊兜底 -->
    <div id="catalog" class="home-layout">
      <CategoryTree
        v-if="navStyle !== 'grid'"
        :categories="categories" :show-recommended="hasRecommended"
        :model-value="activeCategory"
        @update:model-value="pickCategory"
      />
      <div class="home-content">
        <!-- 搜索 -->
        <form class="search-bar" role="search" @submit.prevent="goSearch">
          <input
            v-model="keyword"
            class="input"
            placeholder="搜索商品名"
            aria-label="搜索商品名"
          />
          <button class="btn btn-primary" type="submit">搜索</button>
        </form>

        <!-- 分类导航：grid=顶部胶囊全断点；list 时 PC 左树，移动端「全部分类」折叠树（含全部层级，替代胶囊） -->
        <div v-if="navStyle === 'grid' && (categories.length || hasRecommended)" class="card cat-chips">
          <button class="mobile-cat-head" type="button" :aria-expanded="chipsExpanded" @click="chipsExpanded = !chipsExpanded">
            <span class="mcb-bar"></span><span class="mcb-title">全部分类</span>
            <span class="mcb-count">{{ categories.length }} 类</span>
            <span class="mcb-toggle-label">{{ chipsExpanded ? '折叠' : '展开' }}</span>
            <span class="mcb-arrow" :class="{ open: chipsExpanded }"></span>
          </button>
          <div class="cat-chips-row" :class="{ expanded: chipsExpanded }">
            <button class="chip" :class="{ active: !activeCategory }" @click="pickCategory(0)"><ThemeIcon name="grid" class="chip-icon" />全部</button>
            <button v-if="hasRecommended" class="chip" :class="{ active: activeCategory === -1 }" @click="pickCategory(-1)">推荐商品</button>
            <button v-for="c in categories.filter((x) => !x.parent_id)" :key="c.id" class="chip" :class="{ active: activeCategory === c.id }" @click="pickCategory(c.id)">
              <CategoryIcon :icon="c.icon" class="chip-icon" />{{ c.name }}
            </button>
          </div>
        </div>
        <div v-else-if="categories.length || hasRecommended" class="card mobile-cat mobile-only">
          <CategoryTree variant="panel" :categories="categories" :show-recommended="hasRecommended" :model-value="activeCategory" @update:model-value="pickCategory" />
        </div>

        <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>

        <CatalogToolbar :title="sectionTitle" :sort="sort" :view="viewMode" :loading="loading" @sort="changeSort" @view="changeView" />

        <!-- 商品列表（网格/列表双视图） -->
        <div ref="catalogItems" :class="[viewMode === 'grid' ? 'product-grid' : 'product-list', { 'catalog-big': viewMode === 'grid' && bigGrid }]"
          :style="viewMode === 'grid' ? gridStyle : undefined">
          <template v-for="(p, index) in products" :key="p.id">
            <ProductCard :p="p" :mode="viewMode" :show-sales="showSales" :show-stock="showStock" />
            <BannerStrip v-if="index + 1 === middleBannerIndex" :banners="middleBanners" catalog
              label="商品列表中部横幅" @open="openBanner" />
          </template>
        </div>
        <div v-if="products.length === 0 && !loading" class="empty-state">
          <div class="empty-icon">📦</div>
          <div class="muted">暂无商品</div>
        </div>

        <!-- 分页器（首页/页码/末页 + 每页条数） -->
        <div v-if="total > defaultPageSize" class="pager">
          <span class="pager-total muted">共 {{ total }} 件</span>
          <div class="pager-btns">
            <button class="pager-btn pager-jump" :disabled="page <= 1" title="首页" @click="goPage(1)">«</button>
            <button class="pager-btn" :disabled="page <= 1" @click="goPage(page - 1)">上一页</button>
            <!-- 手机端页码砖收起后的当前位置指示（桌面隐藏） -->
            <span class="pager-now">{{ page }} / {{ totalPage }}</span>
            <template v-for="p in pageList" :key="p">
              <span v-if="p === 0" class="pager-ellipsis">…</span>
              <button v-else class="pager-btn num" :class="{ active: p === page }" @click="goPage(p)">{{ p }}</button>
            </template>
            <button class="pager-btn" :disabled="page >= totalPage" @click="goPage(page + 1)">下一页</button>
            <button class="pager-btn pager-jump" :disabled="page >= totalPage" title="末页" @click="goPage(totalPage)">»</button>
          </div>
          <div class="pager-size">
            <span class="muted">每页</span>
            <select v-model.number="pageSize" class="pager-select" @change="changePageSize">
              <option v-for="s in pageSizeOptions" :key="s" :value="s">{{ s }}</option>
            </select>
            <span class="muted">条</span>
          </div>
        </div>
      </div>
    </div>
    <BannerStrip :banners="bottomBanners" label="首页底部横幅" @open="openBanner" />
  </div>
</template>

<script setup lang="ts">
import BannerStrip from '@/components/BannerStrip.vue';
import CatalogToolbar from '@/components/CatalogToolbar.vue';
import ThemeIcon from '@/components/ThemeIcon.vue';
import CategoryIcon from '@/components/CategoryIcon.vue';
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated, inject, watch, nextTick } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { listProducts, listBanners, listPosts, listCategories, fetchAnnouncement, type Product, type Banner, type StorePost, type CategoryItem, type AnnouncementConfig } from '@/api';
import { fetchSiteSeo, applyDefaultSeo, applyVerification } from '@/seo';
import { formatMoney } from '@/api/client';
import ProductCard from '@/components/ProductCard.vue';
import CategoryTree from '@/components/CategoryTree.vue';

const router = useRouter();
const route = useRoute();
const products = ref<Product[]>([]);
const keyword = ref('');
const searchTerm = ref('');
const defaultSort = ref('default');
let appliedDefaultView = '';
let initialized = false;
const loading = ref(false);
const error = ref('');
// 公告弹窗开启动词由 App.vue 提供（与导航📢公告同一弹窗；缺失时回退文章列表页）
const openNoticeModal: () => void = inject('openNotice', () => {
  window.location.href = '/posts?type=notice';
});
const banners = ref<Banner[]>([]);
const middleBanners = ref<Banner[]>([]);
const bottomBanners = ref<Banner[]>([]);
const latestNotice = ref<StorePost | null>(null);
const categories = ref<CategoryItem[]>([]);
const hasRecommended = ref(false); // 推荐分类仅在存在可见推荐商品时显示
const activeCategory = ref(0);
const viewMode = ref<'grid' | 'list'>('grid');
const page = ref(1);
const defaultPageSize = ref(20);
const pageSize = ref(20);
const sort = ref('default');
const total = ref(0);
const announcement = ref<AnnouncementConfig>({ type: 'text', text: '', images: [] });
// 每页选项跟随后台 template.per_page（默认 20 → 20/40/60），前台不再写死档位
const pageSizeOptions = computed(() => [defaultPageSize.value, defaultPageSize.value * 2, defaultPageSize.value * 3]);

const siteName = ref('ZCard 商店');
onMounted(async () => { const cfg = await fetchSiteSeo(); if (cfg.name) siteName.value = cfg.name; });

// ── 模板设置（后台 系统设置 → 模板；与商品列表页同源消费，保证全站一致）──
const navStyle = ref('list'); // template.category_nav_style：list=左侧树 | grid=顶部胶囊
const bigGrid = ref(false); // template.default_view=big：大图卡片（更宽的列）
const perRow = ref(0); // template.per_row：每行商品数（2-8 固定列数；0=按容器宽度自适应）
const gridMinPx = computed(() => (bigGrid.value ? 300 : 200));
const gridStyle = computed(() =>
  !bigGrid.value && perRow.value
    ? { gridTemplateColumns: `repeat(${perRow.value}, 1fr)` }
    : { gridTemplateColumns: `repeat(auto-fill, minmax(${gridMinPx.value}px, 1fr))` },
);
// 以实际渲染列数对齐完整商品行，避免横幅前留出半行空白。
const catalogItems = ref<HTMLElement | null>(null);
const catalogColumns = ref(1);
watch([catalogItems, gridStyle, viewMode], ([element], _previous, onCleanup) => {
  if (!element || typeof window === 'undefined') return;
  const measure = () => {
    const tracks = window.getComputedStyle(element).gridTemplateColumns;
    catalogColumns.value = viewMode.value === 'grid' && tracks !== 'none' ? Math.max(1, tracks.split(' ').filter(Boolean).length) : 1;
  };
  measure();
  const observer = new ResizeObserver(measure);
  observer.observe(element);
  onCleanup(() => observer.disconnect());
}, { flush: 'post' });
const middleBannerIndex = computed(() => {
  const count = products.value.length;
  if (!count || !middleBanners.value.length) return 0;
  const columns = viewMode.value === 'grid' ? catalogColumns.value : 1;
  const lastBreak = Math.floor((count - 1) / columns) * columns;
  // 只有一行时放在该行之后，既不挤开商品，也不制造空白行。
  if (!lastBreak) return count;
  return Math.min(lastBreak, Math.max(columns, Math.round(count / 2 / columns) * columns));
});

const showSales = ref(true); // template.show_sales：卡片「已售」显示开关
const showStock = ref(true); // template.show_stock：卡片「库存」显示开关
const topBannerEnabled = ref(true); // promo.top_banner_enabled：顶部横幅（首页 Hero 轮播）开关
const chipsExpanded = ref(false); // 移动端 grid 胶囊：单行横滑 → 展开多行

const categoryTitle = computed(() =>
  activeCategory.value === -1 ? '推荐商品' : activeCategory.value
    ? categories.value.find((c) => c.id === activeCategory.value)?.name || '全部商品'
    : '全部商品',
);
const sectionTitle = computed(() => searchTerm.value ? `${categoryTitle.value} · 搜索结果` : categoryTitle.value);
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
  void navigateCatalog();
}

function changePageSize() {
  // 即使当前已在第一页，也必须按新的每页条数重新请求。
  page.value = 1;
  void navigateCatalog();
}

// Hero 轮播：公告设置 image/carousel 优先；顶部横幅开启时用生效横幅；点击行为跟随来源
const heroItems = computed(() => {
  const items: { key: string; img: string; title: string; onClick?: () => void }[] = [];
  const ann = announcement.value;
  if ((ann.type === 'image' || ann.type === 'carousel') && ann.images.length) {
    ann.images.forEach((img, i) => items.push({ key: `ann-${i}`, img, title: '', onClick: openAnnouncement }));
  } else if (topBannerEnabled.value) {
    banners.value.forEach((b) => items.push({ key: `bn-${b.id}`, img: bannerImg(b), title: b.title, onClick: () => openBanner(b) }));
  }
  return items;
});
// 公告条：设置文本优先，回落最新公告文章标题
const announcementText = computed(() => (announcement.value.type === 'text' ? announcement.value.text : ''));
const noticeBarText = computed(() => (announcementText.value ? (announcement.value.summary ?? announcementText.value) : latestNotice.value?.title) || '');

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
    router.push({ path: '/', query: { category_id: b.link_value }, hash: '#catalog' });
  } else if (b.link_type === 'post' && b.link_value) {
    router.push(`/posts/${b.link_value}`);
  } else if (b.link_type === 'notice') {
    openAnnouncement();
  } else if (b.link_type === 'url' && b.link_value) {
    window.open(b.link_value, '_blank', 'noopener');
  }
}

const sorts = ['default', 'sales', 'newest', 'price_asc', 'price_desc'];
function readRouteFilters() {
  const q = route.query;
  const id = Number(q.category_id);
  activeCategory.value = Number.isSafeInteger(id) && id > 0 ? id : 0;
  if (q.recommend_only === '1' || q.recommend_only === 'true') activeCategory.value = -1;
  keyword.value = searchTerm.value = typeof q.keyword === 'string' ? q.keyword : '';
  sort.value = typeof q.sort === 'string' && sorts.includes(q.sort) ? q.sort : defaultSort.value;
  const size = Number(q.page_size);
  pageSize.value = pageSizeOptions.value.includes(size) ? size : defaultPageSize.value;
  const p = Number(q.page);
  page.value = Number.isSafeInteger(p) && p > 0 ? p : 1;
}
function scrollToCatalog() {
  const toolbar = document.querySelector('.catalog-toolbar');
  if (!toolbar) return;
  const headerHeight = document.querySelector('.topbar')?.getBoundingClientRect().height || 0;
  window.scrollTo({ top: Math.max(0, window.scrollY + toolbar.getBoundingClientRect().top - headerHeight - 12) });
}
async function navigateCatalog() {
  const query = { ...route.query };
  for (const k of ['keyword', 'category_id', 'recommend_only', 'sort', 'page', 'page_size']) delete query[k];
  if (searchTerm.value) query.keyword = searchTerm.value;
  if (activeCategory.value > 0) query.category_id = String(activeCategory.value);
  if (activeCategory.value === -1) query.recommend_only = '1';
  query.sort = sort.value;
  if (page.value > 1) query.page = String(page.value);
  if (pageSize.value !== defaultPageSize.value) query.page_size = String(pageSize.value);
  const target = { path: '/', query, hash: '#catalog' };
  if (router.resolve(target).fullPath === route.fullPath) { await load(); scrollToCatalog(); }
  else await router.push(target);
}
function goSearch() {
  searchTerm.value = keyword.value.trim();
  page.value = 1;
  void navigateCatalog();
}
function pickCategory(id: number) {
  activeCategory.value = id;
  page.value = 1;
  chipsExpanded.value = false;
  void navigateCatalog();
}
function changeSort(value: string) {
  if (!sorts.includes(value)) return;
  sort.value = value;
  page.value = 1;
  void navigateCatalog();
}
function changeView(value: 'grid' | 'list') {
  viewMode.value = value;
  if (value === 'grid') bigGrid.value = false;
}
let routeQueryKey = JSON.stringify(route.query);
watch(() => route.fullPath, async (_path, previous) => {
  if (!initialized || route.path !== '/') return;
  const key = JSON.stringify(route.query);
  if (key === routeQueryKey) {
    if (route.hash === '#catalog' && (previous === '/' || previous.startsWith('/?'))) { await nextTick(); scrollToCatalog(); }
    return;
  }
  routeQueryKey = key;
  readRouteFilters();
  await load();
  if (route.path === '/') { await nextTick(); scrollToCatalog(); }
});

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

async function refreshRecommendations() {
  const { data, error: err } = await listProducts({ recommend_only: true, page: 1, page_size: 1 });
  if (err) return; // Temporary failures must not discard the current selection.
  hasRecommended.value = (data?.items?.length || 0) > 0;
  if (!hasRecommended.value && activeCategory.value === -1) {
    activeCategory.value = 0;
    page.value = 1;
  }
}

let loadSequence = 0;
async function load() {
  const sequence = ++loadSequence;
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listProducts({
    keyword: searchTerm.value || undefined,
    category_id: activeCategory.value > 0 ? activeCategory.value : undefined,
    recommend_only: activeCategory.value === -1 || undefined,
    sort: sort.value,
    page: page.value,
    page_size: pageSize.value,
  });
  if (sequence !== loadSequence) return;
  loading.value = false;
  if (err) { error.value = err; return; }
  products.value = data?.items || [];
  total.value = data?.total || 0;
}

let needsRefresh = false;
onActivated(() => {
  startHero();
  if (needsRefresh) {
    needsRefresh = false;
    // 保留浏览状态，同时重新获取价格、库存和首页标题。
    void loadTemplateSettings().then(refreshRecommendations).then(load);
    void fetchSiteSeo().then(applyDefaultSeo);
  }
});
onDeactivated(() => {
  stopHero();
  needsRefresh = true;
});

onMounted(async () => { startHero(); if (route.hash === '#catalog') { await nextTick(); scrollToCatalog(); } });
onUnmounted(stopHero);

// 先读取后台默认值，再应用链接中的筛选条件。
await loadTemplateSettings();
readRouteFilters();
routeQueryKey = JSON.stringify(route.query);
await Promise.all([
  listBanners('middle').then((b) => { middleBanners.value = b?.data?.banners || []; }),
  listBanners('bottom').then((b) => { bottomBanners.value = b?.data?.banners || []; }),
  listBanners('top').then((b) => { banners.value = b?.data?.banners || []; }),
  listPosts('notice', 1, 1).then((n) => { latestNotice.value = n?.data?.posts?.[0] || null; }),
  listCategories().then((c) => { categories.value = c?.data?.categories || []; }),
  refreshRecommendations(),
  fetchAnnouncement().then((a) => { announcement.value = a; }),
  // 首页默认 SEO（仅首页；页面级 SEO 由各自页面组件负责）
  fetchSiteSeo().then((site) => {
    applyDefaultSeo(site);
    applyVerification(site);
  }),
]);
await load();

initialized = true;
// Backend defaults apply on first entry; cached user choices remain intact.
async function loadTemplateSettings() {
  try {
    const apiBase = import.meta.env.SSR ? (import.meta.env.VITE_SSG_API || 'http://127.0.0.1:8000') : '';
    const resp = await fetch(`${apiBase}/api/v1/storefront/config`);
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
    const nextView = ['list', 'big'].includes(dv) ? dv : 'grid';
    if (nextView !== appliedDefaultView) {
      bigGrid.value = nextView === 'big';
      viewMode.value = nextView === 'list' ? 'list' : 'grid';
      appliedDefaultView = nextView;
    }
    // 卡片销量/库存显示开关（显式 false 才关闭，兼容旧数据缺省）
    if (val('template.show_sales') === false) showSales.value = false;
    if (val('template.show_stock') === false) showStock.value = false;
    // 每行商品数（2-8）：显式固定列数，替代 auto-fill 的"装几个算几个"
    const pr = val('template.per_row');
    if (typeof pr === 'number' && pr >= 2 && pr <= 8) perRow.value = Math.floor(pr);
    // 每页商品数（防滥用夹在 6~60；与 /products 页同源消费）
    const pp = Number(val('template.per_page'));

    if (Number.isInteger(pp) && pp >= 6 && pp <= 60 && pp !== defaultPageSize.value) {
      defaultPageSize.value = pp;
      if (!route.query.page_size) {
        pageSize.value = pp;
      }
    }
    const sb = val('template.sort_by');
    if (['default', 'newest', 'sales', 'price_asc', 'price_desc'].includes(sb)) defaultSort.value = sb;
    // 顶部横幅开关：关闭时 Hero 回退品牌渐变区（公告图片轮播不受影响）
    if (val('promo.top_banner_enabled') === false) topBannerEnabled.value = false;
  } catch { /* 配置拉取失败保持默认 */ }
}

</script>

<style scoped>
.home { display: flex; flex-direction: column; gap: 16px; }

/* 左右布局：PC 左侧分类树 + 右侧内容 */
.home-layout { scroll-margin-top: 100px; display: flex; gap: 16px; align-items: flex-start; }
.home-content { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 16px; }
.mobile-only { display: flex; }
@media (min-width: 768px) {
  .mobile-only { display: none; }
}
/* 分类胶囊（category_nav_style=grid 顶部导航 / list 移动端兜底）——饱满大尺寸 */
.cat-chips { padding: 0; overflow: hidden; }
.cat-chips-row.expanded { flex-wrap: wrap; overflow-x: visible; }
.cat-chips-row .chip { flex-shrink: 0; }
.cat-chips-row { display: flex; flex-wrap: nowrap; overflow-x: auto; gap: 10px; align-items: center; padding: 12px 16px; }
.chip {
  padding: 9px 22px; border-radius: 999px; font-size: 15px; font-weight: 500; color: #374151;
  background: #f3f4f6; border: 1px solid transparent; cursor: pointer; transition: all .15s;
}
.chip:hover { border-color: #2563eb; color: #2563eb; background: #eff6ff; }
.chip.active { background: #2563eb; color: #fff; font-weight: 600; box-shadow: 0 3px 10px rgba(37, 99, 235, 0.3); }
.chip-icon { margin-right: 5px; }
.chip-icon-img { width: 16px; height: 16px; object-fit: contain; border-radius: 3px; }

/* ── 移动端「全部分类」折叠面板（PC 左树的移动端等价物；含全部层级） ── */
/* mobile-only 会给容器 display:flex，这里必须转纵向，防止头/体横排挤压树体 */
.mobile-cat { padding: 0; overflow: hidden; flex-direction: column; }
.mobile-cat-head {
  width: 100%; display: flex; align-items: center; gap: 8px;
  padding: 13px 16px; background: #f8fafc; border: none;
  cursor: pointer; font-family: inherit; text-align: left;
}
.mcb-bar { width: 4px; height: 16px; border-radius: 999px; background: #ff5722; flex-shrink: 0; }
.mcb-title { font-size: 15px; font-weight: 700; color: #111827; letter-spacing: 0.5px; }
.mcb-toggle-label { font-size: 12px; color: #6b7280; }
.mcb-count {
  margin-left: auto; font-size: 12px; color: #2563eb;
  background: rgba(37, 99, 235, 0.08); padding: 2px 9px; border-radius: 999px;
  font-variant-numeric: tabular-nums; white-space: nowrap;
}
.mcb-arrow {
  width: 7px; height: 7px; flex-shrink: 0; margin-left: 8px;
  border-right: 1.5px solid #6b7280; border-bottom: 1.5px solid #6b7280;
  transform: rotate(45deg); transition: transform 0.2s;
}
.mcb-arrow.open { transform: rotate(-135deg); }
.mobile-cat-body { border-top: 1px solid #e5e7eb; padding: 6px; }

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
.hero-banner > div { min-width: 0; }
.hero-banner h1 { font-size: 28px; margin-bottom: 8px; overflow-wrap: anywhere; }
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
.product-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 14px;
}
.product-list { display: flex; flex-direction: column; gap: 10px; }
@media (max-width: 640px) {
  .product-list { gap: 0; background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; overflow: hidden; }
  .product-list :deep(.product-card) { border: none; border-radius: 0; border-bottom: 1px solid #e5e7eb; }
  .product-list :deep(.banner-strip--catalog) { margin: 12px; }
  .product-list :deep(.product-card:last-child) { border-bottom: none; }
  .product-list :deep(.product-card:hover) { transform: none; box-shadow: none; }
}

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
/* 手机端当前位置指示（桌面隐藏，页码砖即位置） */
.pager-now { display: none; align-items: center; justify-content: center; min-width: 56px; height: 34px; font-size: 14px; font-weight: 600; color: #374151; font-variant-numeric: tabular-nums; }
.pager-size { display: flex; align-items: center; gap: 6px; font-size: 13px; white-space: nowrap; flex-shrink: 0; }
.pager-select {
  padding: 5px 8px; border: 1px solid #d1d5db; border-radius: 8px;
  font-size: 13px; outline: none; background: #fff;
}
.pager-total { font-size: 13px; }

/* ── 移动端（≤768px）：轮播限高、分类胶囊单行横滑、商品双列 ── */
@media (max-width: 768px) {
  .home { gap: 12px; }
  .home-layout { scroll-margin-top: 100px; gap: 12px; }
  /* 轮播高度按视口收窄（桌面 clamp 240-360px 在手机上过高） */
  .hero-slide img { height: clamp(110px, 30vw, 170px); }
  .hero-title { padding: 22px 14px 10px; font-size: 15px; }
  /* 回退品牌横幅（无图时）移动端收紧：省首屏空间——隐藏大图标、压行距 */
  .hero-banner { padding: 14px 16px; }
  .hero-banner h1 { font-size: 18px; margin-bottom: 4px; }
  .hero-banner p { margin-bottom: 8px; font-size: 12px; }
  .hero-points { font-size: 12px; gap: 10px; flex-wrap: wrap; }
  .hero-icon { display: none; }
  /* 分类展开后自动换行，长分类名不撑出屏幕。 */
  .cat-chips-row { gap: 8px; padding: 10px 12px; }
  .chip { max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
  /* 商品网格：双列瀑布（auto-fill minmax(200px) 在手机只能出 1 列） */
  .product-grid { grid-template-columns: repeat(2, 1fr) !important; gap: 10px; }
  /* 分页器：手机紧凑单行（上一页 · 当前/总页 · 下一页）——页码砖/省略号/首末跳转收起，
     flex 不折行保证一行放得下；「每页 N 条」独占一行居中。淡灰文字压深一档保可读 */
  .pager { gap: 10px; }
  .pager-btns { flex-wrap: nowrap; justify-content: center; }
  .pager-jump, .pager-ellipsis { display: none; }
  .pager-btn.num { display: none; }
  .pager-now { display: inline-flex; }
  .pager-total, .pager-size .muted { color: #6b7280; }
  .pager-size { justify-content: center; white-space: nowrap; }
}

@media (max-width: 767px) { .catalog-big { grid-template-columns: minmax(0, 1fr) !important; } }
</style>
