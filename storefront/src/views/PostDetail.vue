<template>
  <div>
    <div v-if="error" class="error card">{{ error }}</div>
    <template v-else-if="post">
      <div class="card post-head">
        <div class="tag">{{ typeLabel(post.type) }}</div>
        <h1 class="post-title">{{ post.title }}</h1>
        <div class="muted">
          <template v-if="categoryName(post.category_id)">栏目：{{ categoryName(post.category_id) }} · </template>{{ formatDate(post.published_at) }}
        </div>
      </div>
      <div class="card post-body" v-html="content"></div>
    </template>
    <div v-else class="muted" style="text-align: center; margin-top: 24px;">加载中…</div>
    <div style="margin-top: 16px;">
      <router-link class="btn secondary" to="/posts">← 返回列表</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { getPost, listPostCategories, type StorePost, type PostCategory } from '@/api';
import { fetchSiteSeo, applySeo, stripHtml, truncate } from '@/seo';

const route = useRoute();
const post = ref<StorePost | null>(null);
const content = ref('');
const error = ref('');
const categories = ref<PostCategory[]>([]);

function typeLabel(t: string) {
  return ({ notice: '公告', blog: '博客' } as Record<string, string>)[t] || t;
}

function categoryName(id?: number): string {
  if (!id) return '';
  return categories.value.find((c) => c.id === id)?.name || '';
}

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

// 文章数据预取（setup 顶层：SSG 静态化文章页内容 + 输出 SEO head）
const { data: catData } = await listPostCategories();
categories.value = catData?.categories || [];
const { data: detail, error: postErr } = await getPost(route.params.slug as string);
if (postErr) {
  error.value = '文章不存在或未发布';
} else {
  post.value = detail?.post || null;
  content.value = detail?.content || '';
  applyPostSeo(detail);
}

/** 文章页 SEO：title/description/canonical + Article/Breadcrumb JSON-LD */
async function applyPostSeo(detail: { post: StorePost; content: string } | null) {
  const site = await fetchSiteSeo();
  const base = site.url || (typeof window !== "undefined" ? window.location.origin : "");
  const origin = typeof window !== "undefined" ? window.location.origin : site.url;
  const slug = route.params.slug as string;
  const postTitle = detail?.post?.title || '';
  const desc = detail?.post?.summary ? truncate(stripHtml(detail.post.summary)) : truncate(stripHtml(detail?.content || ''));
  const date = detail?.post?.published_at ? new Date(detail.post.published_at * 1000).toISOString() : undefined;
  applySeo(
    {
      title: postTitle ? `${postTitle} - ${site.name}` : `${site.name} - 文章`,
      description: desc || undefined,
      keywords: [postTitle, site.name].filter(Boolean).join(','),
      canonical: `${origin}/posts/${slug}`,
      ogType: 'article',
      jsonLd: [
        {
          '@context': 'https://schema.org',
          '@type': 'Article',
          headline: postTitle,
          ...(date ? { datePublished: date, dateModified: date } : {}),
          author: { '@type': 'Organization', name: site.name },
          publisher: { '@type': 'Organization', name: site.name },
        },
        {
          '@context': 'https://schema.org',
          '@type': 'BreadcrumbList',
          itemListElement: [
            { '@type': 'ListItem', position: 1, name: site.name, item: base + '/' },
            { '@type': 'ListItem', position: 2, name: '文章', item: base + '/posts' },
            { '@type': 'ListItem', position: 3, name: postTitle, item: `${base}/posts/${slug}` },
          ],
        },
      ],
    },
    site,
  );
}
</script>

<style scoped>
.post-head { margin-bottom: 16px; }
.post-title { font-size: 22px; margin: 8px 0; }
.post-body { line-height: 1.8; font-size: 15px; word-break: break-word; }
.post-body :deep(h1), .post-body :deep(h2), .post-body :deep(h3), .post-body :deep(h4) {
  margin: 20px 0 10px; font-weight: 700; color: #111827; line-height: 1.4;
}
.post-body :deep(h1) { font-size: 24px; }
.post-body :deep(h2) { font-size: 20px; padding-bottom: 8px; border-bottom: 1px solid #eef0f2; }
.post-body :deep(h3) { font-size: 17px; }
.post-body :deep(h4) { font-size: 15px; }
.post-body :deep(p) { margin: 0 0 12px; }
.post-body :deep(ul), .post-body :deep(ol) { margin: 0 0 12px; padding-left: 24px; }
.post-body :deep(li) { margin-bottom: 4px; }
.post-body :deep(a) { color: #2563eb; text-decoration: underline; word-break: break-all; }
.post-body :deep(img) { max-width: 100%; border-radius: 8px; margin: 6px 0; }
.post-body :deep(blockquote) {
  margin: 12px 0; padding: 10px 14px; border-left: 4px solid #2563eb;
  background: #f5f9ff; color: #4b5563; border-radius: 0 8px 8px 0;
}
.post-body :deep(pre) {
  background: #0f172a; color: #e2e8f0; padding: 14px 16px; border-radius: 10px;
  overflow-x: auto; font-size: 13px; line-height: 1.6; margin: 12px 0;
}
.post-body :deep(code) { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.post-body :deep(:not(pre) > code) {
  background: #f1f5f9; color: #be185d; padding: 2px 6px; border-radius: 4px; font-size: 13px;
}
.post-body :deep(table) {
  width: 100%; border-collapse: collapse; margin: 12px 0; font-size: 14px;
  overflow: hidden; border-radius: 10px;
}
.post-body :deep(th), .post-body :deep(td) {
  border: 1px solid #e5e7eb; padding: 8px 12px; text-align: left;
}
.post-body :deep(th) { background: #f8fafc; font-weight: 600; color: #111827; }
.post-body :deep(tr:nth-child(even) td) { background: #fafbfc; }
.post-body :deep(hr) { border: none; border-top: 1px solid #e5e7eb; margin: 20px 0; }
</style>
