<template>
  <div>
    <div v-if="error" class="error card">{{ error }}</div>
    <template v-else-if="post">
      <div class="card post-head">
        <div class="tag">{{ typeLabel(post.type) }}</div>
        <h1 class="post-title">{{ post.title }}</h1>
        <div class="muted">{{ formatDate(post.published_at) }}</div>
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
import { getPost, type StorePost } from '@/api';

const route = useRoute();
const post = ref<StorePost | null>(null);
const content = ref('');
const error = ref('');

function typeLabel(t: string) {
  return ({ notice: '公告', blog: '博客' } as Record<string, string>)[t] || t;
}

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

onMounted(async () => {
  const { data, error: err } = await getPost(route.params.slug as string);
  if (err) { error.value = '文章不存在或未发布'; return; }
  post.value = data?.post || null;
  content.value = data?.content || '';
});
</script>

<style scoped>
.post-head { margin-bottom: 16px; }
.post-title { font-size: 22px; margin: 8px 0; }
.post-body { line-height: 1.8; font-size: 15px; }
.post-body :deep(p) { margin: 0 0 12px; }
.post-body :deep(img) { max-width: 100%; border-radius: 8px; }
</style>
