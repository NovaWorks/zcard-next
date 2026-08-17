<template>
  <div>
    <!-- 公告条（notice 最新一条） -->
    <div v-if="latestNotice" class="card notice-bar" @click="$router.push('/posts?type=notice')">
      <span class="tag">公告</span>
      <span class="notice-title">{{ latestNotice.title }}</span>
      <span class="muted">{{ formatDate(latestNotice.published_at) }}</span>
    </div>

    <!-- 横幅（生效时间窗内） -->
    <div v-if="banners.length" class="banners">
      <div v-for="b in banners" :key="b.id" class="banner card" @click="openBanner(b)">
        <img :src="bannerImg(b)" :alt="b.title" loading="lazy" />
        <div v-if="b.title" class="banner-title">{{ b.title }}</div>
      </div>
    </div>

    <div class="card" style="margin-bottom: 16px; display: flex; gap: 8px;">
      <input v-model="keyword" class="input" placeholder="搜索商品名" @keyup.enter="load" style="max-width: 320px;" />
      <button class="btn" @click="load">搜索</button>
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <div class="grid">
      <div v-for="p in products" :key="p.id" class="card" style="cursor: pointer;" @click="$router.push(`/product/${p.id}`)">
        <div class="tag" style="margin-bottom: 8px;">{{ stockTypeLabel(p.stock_type) }}</div>
        <div style="font-weight: 600; margin-bottom: 8px;">{{ p.name }}</div>
        <div class="price">{{ formatMoney(p.price_cents) }}</div>
        <div class="muted">销量 {{ p.sales_count }}</div>
      </div>
    </div>
    <div v-if="products.length === 0 && !loading" class="muted" style="margin-top: 24px; text-align: center;">暂无商品</div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { listProducts, listBanners, listPosts, type Product, type Banner, type StorePost } from '@/api';
import { formatMoney } from '@/api/client';

const products = ref<Product[]>([]);
const keyword = ref('');
const loading = ref(false);
const error = ref('');
const banners = ref<Banner[]>([]);
const latestNotice = ref<StorePost | null>(null);

function stockTypeLabel(t: string) {
  return ({ card: '卡密', url: '链接', code: '兑换码' } as Record<string, string>)[t] || t;
}

// 移动端图回落（窄屏）
function bannerImg(b: Banner): string {
  const isMobile = window.innerWidth < 640;
  return (isMobile && b.mobile_image) ? b.mobile_image : b.image;
}

function openBanner(b: Banner) {
  if (b.link_type === 'url' && b.link_value) {
    window.open(b.link_value, '_blank', 'noopener');
  }
}

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

async function loadBanners() {
  const { data } = await listBanners('top');
  banners.value = data?.banners || [];
}

async function loadNotice() {
  const { data } = await listPosts('notice', 1, 1);
  latestNotice.value = data?.posts?.[0] || null;
}

async function load() {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listProducts({ keyword: keyword.value || undefined, page: 1, page_size: 50 });
  loading.value = false;
  if (err) { error.value = err; return; }
  products.value = data?.items || [];
}

onMounted(() => {
  load();
  loadBanners();
  loadNotice();
});
</script>

<style scoped>
.notice-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 16px;
  cursor: pointer;
  padding: 10px 16px;
}
.notice-title { flex: 1; font-size: 14px; color: #1f2329; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.banners { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 16px; margin-bottom: 16px; }
.banner { cursor: pointer; overflow: hidden; padding: 0; position: relative; }
.banner img { width: 100%; height: 160px; object-fit: cover; display: block; }
.banner-title { position: absolute; left: 0; right: 0; bottom: 0; padding: 8px 12px; background: rgba(0,0,0,0.45); color: #fff; font-size: 14px; }
</style>
