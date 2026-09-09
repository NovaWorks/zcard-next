<template>
  <div class="posts-page">
    <div class="card" style="display: flex; align-items: center; gap: 12px; margin-bottom: 16px;">
      <div style="display: flex; gap: 8px;">
        <button :class="['btn', type === '' ? '' : 'secondary']" @click="switchType('')">全部</button>
        <button :class="['btn', type === 'notice' ? '' : 'secondary']" @click="switchType('notice')">公告</button>
        <button :class="['btn', type === 'blog' ? '' : 'secondary']" @click="switchType('blog')">博客</button>
      </div>
    </div>
    <!-- 栏目筛选：横向滚动胶囊（选中高亮） -->
    <div v-if="categories.length" class="cat-nav" style="margin-bottom: 16px;">
      <button :class="['cat-chip', { active: !categoryId }]" @click="pickCategory(0)">全部</button>
      <button
        v-for="c in categories"
        :key="c.id"
        :class="['cat-chip', { active: categoryId === c.id }]"
        @click="pickCategory(c.id)"
      >{{ c.name }}</button>
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <div class="post-list">
      <router-link v-for="p in posts" :key="p.id" class="card post-item" :to="`/posts/${p.slug}`">
        <div class="tag">{{ typeLabel(p.type) }}</div>
        <div class="post-title">{{ p.title }}</div>
        <div v-if="p.category_id" class="tag tag-category">{{ categoryName(p.category_id) }}</div>
        <div class="muted post-date">{{ formatDate(p.published_at) }}</div>
      </router-link>
    </div>
    <div v-if="posts.length === 0 && !loading" class="card post-empty" role="status">暂无内容</div>
    <div v-if="total > pageSize" style="display: flex; gap: 8px; justify-content: center; margin-top: 16px;">
      <button class="btn secondary" :disabled="page <= 1" @click="load(page - 1)">上一页</button>
      <span class="muted" style="align-self: center;">{{ page }} / {{ Math.ceil(total / pageSize) }}</span>
      <button class="btn secondary" :disabled="page * pageSize >= total" @click="load(page + 1)">下一页</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { listPosts, listPostCategories, type StorePost, type PostCategory } from '@/api';
import { fetchSiteSeo, applySeo } from '@/seo';

const route = useRoute();
const router = useRouter();
const posts = ref<StorePost[]>([]);
const categories = ref<PostCategory[]>([]);
const type = ref<string>((route.query.type as string) || '');
const categoryId = ref<number>(Number(route.query.category) || 0);
const page = ref(1);
const pageSize = 20;
const total = ref(0);
const loading = ref(false);
const error = ref('');

function typeLabel(t: string) {
  return ({ notice: '公告', blog: '博客' } as Record<string, string>)[t] || t;
}

function categoryName(id: number): string {
  return categories.value.find((c) => c.id === id)?.name || '';
}

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

function switchType(t: string) {
  type.value = t;
  syncQuery();
  load(1);
}

function pickCategory(id: number) {
  categoryId.value = id;
  syncQuery();
  load(1);
}

function syncQuery() {
  const q: Record<string, string> = {};
  if (type.value) q.type = type.value;
  if (categoryId.value) q.category = String(categoryId.value);
  router.replace({ query: q });
}

async function load(p = 1) {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listPosts(type.value || undefined, p, pageSize, undefined, categoryId.value);
  loading.value = false;
  if (err) { error.value = err; return; }
  posts.value = data?.posts || [];
  total.value = data?.total || 0;
  page.value = p;
}

// 文章列表数据预取（setup 顶层：SSG 静态化 + 输出 SEO head）
{
  const { data } = await listPostCategories();
  categories.value = data?.categories || [];
  await load(1);
  await applyListSeo();
}

/** 文章列表页 SEO：canonical 恒为 /posts（公告/博客筛选是同一列表变体，
 与静态壳 canonical 一致） */
async function applyListSeo() {
  const site = await fetchSiteSeo();
  const origin = typeof window !== "undefined" ? window.location.origin : site.url;
  applySeo({ title: `文章公告 - ${site.name}`, canonical: `${origin}/posts`, ogType: 'website' }, site);
}
</script>

<style scoped>
.posts-page { min-width:0; }
.posts-page .muted { color:#64748b; }
.post-empty { text-align:center; color:#475569; margin-top:24px; padding:32px 16px; }
.post-date { white-space:nowrap; }
.post-item .tag { flex-shrink:0; }
@media(max-width:640px) {
 .post-item { display:grid !important; grid-template-columns:auto minmax(0,1fr); gap:8px 12px; }
 .post-item .post-title { grid-column:1/-1; grid-row:2; font-size:16px; }
 .post-item .tag-category { justify-self:start; white-space:normal; overflow-wrap:anywhere; }
 .post-item .post-date { grid-column:1/-1; }
}

.post-item { color:#1f2937; text-decoration:none; min-width:0; cursor: pointer; display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
.post-title { min-width:0; overflow-wrap:anywhere; line-height:1.6; flex: 1; font-weight: 600; font-size: 15px; }

/* 栏目胶囊：横向滚动，选中蓝底白字（与首页分类导航同款交互） */
.cat-nav {
  display: flex; gap: 8px; align-items: center; overflow-x: auto;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 10px 14px;
  scrollbar-width: none;
}
.cat-nav::-webkit-scrollbar { display: none; }
.cat-chip {
  flex-shrink: 0; padding: 6px 16px; border-radius: 999px; font-size: 13px;
  color: #374151; background: #f3f4f6; border: 1px solid transparent;
  cursor: pointer; transition: all 0.15s; white-space: nowrap;
}
.cat-chip:hover { border-color: rgba(37, 99, 235, 0.5); color: #2563eb; }
.cat-chip.active { background: #2563eb; color: #fff; }

.tag-category { background: #f0f9ff; color: #0284c7; }
</style>
