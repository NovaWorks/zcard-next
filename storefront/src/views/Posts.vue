<template>
  <div>
    <div class="card" style="display: flex; align-items: center; gap: 12px; margin-bottom: 16px;">
      <div style="display: flex; gap: 8px;">
        <button :class="['btn', type !== 'notice' ? '' : 'secondary']" @click="switchType('')">全部</button>
        <button :class="['btn', type === 'notice' ? '' : 'secondary']" @click="switchType('notice')">公告</button>
        <button :class="['btn', type === 'blog' ? '' : 'secondary']" @click="switchType('blog')">博客</button>
      </div>
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <div class="post-list">
      <div v-for="p in posts" :key="p.id" class="card post-item" @click="$router.push(`/posts/${p.slug}`)">
        <div class="tag">{{ typeLabel(p.type) }}</div>
        <div class="post-title">{{ p.title }}</div>
        <div class="muted">{{ formatDate(p.published_at) }}</div>
      </div>
    </div>
    <div v-if="posts.length === 0 && !loading" class="muted" style="text-align: center; margin-top: 24px;">暂无内容</div>
    <div v-if="total > pageSize" style="display: flex; gap: 8px; justify-content: center; margin-top: 16px;">
      <button class="btn secondary" :disabled="page <= 1" @click="load(page - 1)">上一页</button>
      <span class="muted" style="align-self: center;">{{ page }} / {{ Math.ceil(total / pageSize) }}</span>
      <button class="btn secondary" :disabled="page * pageSize >= total" @click="load(page + 1)">下一页</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { listPosts, type StorePost } from '@/api';

const route = useRoute();
const posts = ref<StorePost[]>([]);
const type = ref<string>((route.query.type as string) || '');
const page = ref(1);
const pageSize = 20;
const total = ref(0);
const loading = ref(false);
const error = ref('');

function typeLabel(t: string) {
  return ({ notice: '公告', blog: '博客' } as Record<string, string>)[t] || t;
}

function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

function switchType(t: string) {
  type.value = t;
  load(1);
}

async function load(p = 1) {
  loading.value = true;
  error.value = '';
  const { data, error: err } = await listPosts(type.value || undefined, p, pageSize);
  loading.value = false;
  if (err) { error.value = err; return; }
  posts.value = data?.posts || [];
  total.value = data?.total || 0;
  page.value = p;
}

onMounted(() => load(1));
</script>

<style scoped>
.post-item { cursor: pointer; display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
.post-title { flex: 1; font-weight: 600; font-size: 15px; }
</style>
