<template>
  <div v-if="show" class="notice-mask" @click.self="close">
    <div class="notice-modal">
      <div class="notice-head">
        <span class="notice-horn">📢</span>
        <span class="notice-head-title">{{ post?.title || '系统公告' }}</span>
        <button class="notice-close" @click="close">✕</button>
      </div>
      <div class="notice-body">
        <div class="notice-date muted" v-if="post?.published_at">发布于 {{ formatDate(post.published_at) }}</div>
        <!-- 内容为后端 sanitize 后的富文本（与文章详情页同口径） -->
        <div class="notice-content" v-html="content"></div>
      </div>
      <div class="notice-foot">
        <router-link class="btn secondary" :to="'/posts?type=notice'" @click="close">查看全部公告</router-link>
        <button class="btn btn-primary" @click="close">知道了</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { StorePost } from '@/api';

defineProps<{
  show: boolean;
  post: StorePost | null;
  content: string;
}>();
const emit = defineEmits<{ (e: 'update:show', v: boolean): void }>();

function close() {
  emit('update:show', false);
}
function formatDate(unix?: number): string {
  if (!unix) return '';
  return new Date(unix * 1000).toLocaleDateString('zh-CN');
}
</script>

<style scoped>
.notice-mask {
  position: fixed; inset: 0; z-index: 999;
  background: rgba(0, 0, 0, 0.5);
  display: flex; align-items: center; justify-content: center; padding: 16px;
}
.notice-modal {
  width: 100%; max-width: 520px; max-height: 80vh;
  background: #fff; border-radius: 14px; overflow: hidden;
  display: flex; flex-direction: column;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.2);
}
.notice-head {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 18px; background: linear-gradient(90deg, #eff6ff, #dbeafe);
  border-bottom: 1px solid #e5e7eb;
}
.notice-horn { font-size: 18px; }
.notice-head-title { flex: 1; font-weight: 700; font-size: 15px; color: #1f2329; }
.notice-close {
  border: none; background: none; cursor: pointer; font-size: 14px; color: #9ca3af;
  width: 28px; height: 28px; border-radius: 999px;
}
.notice-close:hover { background: #e5e7eb; color: #111827; }
.notice-body { padding: 16px 18px; overflow-y: auto; flex: 1; }
.notice-date { margin-bottom: 10px; }
.notice-content { font-size: 14px; line-height: 1.7; color: #374151; word-break: break-word; }
.notice-foot {
  display: flex; justify-content: flex-end; gap: 10px;
  padding: 12px 18px; border-top: 1px solid #f3f4f6;
}
</style>
