<template>
  <div v-if="show" class="notice-mask" @click.self="close">
    <div class="notice-modal">
      <div class="notice-head">
        <span class="notice-horn">📢</span>
        <span class="notice-head-title">{{ headTitle }}</span>
        <button class="notice-close" @click="close">✕</button>
      </div>
      <div class="notice-body">
        <!-- 设置公告：文本 -->
        <div v-if="announcement && announcement.type === 'text'" class="notice-content">
          {{ announcement.text }}
        </div>
        <!-- 设置公告：单图 / 轮播（指示点 + 自动播放，hover 暂停） -->
        <div v-else-if="announcement && announcement.images.length" class="ann-slider" @mouseenter="stopAuto" @mouseleave="startAuto">
          <div class="ann-track" :style="{ transform: `translateX(-${idx * 100}%)` }">
            <div v-for="(img, i) in announcement.images" :key="i" class="ann-slide">
              <img :src="img" alt="公告图片" />
            </div>
          </div>
          <div v-if="announcement.images.length > 1" class="ann-dots">
            <span
              v-for="(img, i) in announcement.images"
              :key="i"
              class="ann-dot"
              :class="{ active: i === idx }"
              @click="idx = i"
            ></span>
          </div>
        </div>
        <!-- 文章公告（内容为后端 sanitize 后的富文本，与文章详情页同口径） -->
        <template v-else>
          <div class="notice-date muted" v-if="post?.published_at">发布于 {{ formatDate(post.published_at) }}</div>
          <div class="notice-content" v-html="content"></div>
        </template>
      </div>
      <div class="notice-foot">
        <router-link class="btn secondary" :to="'/posts?type=notice'" @click="close">查看全部公告</router-link>
        <button class="btn btn-primary" @click="close">知道了</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import type { StorePost, AnnouncementConfig } from '@/api';

const props = defineProps<{
  show: boolean;
  post: StorePost | null;
  content: string;
  announcement?: AnnouncementConfig | null;
}>();
const emit = defineEmits<{ (e: 'update:show', v: boolean): void }>();

const headTitle = computed(() => (props.announcement ? '系统公告' : props.post?.title || '系统公告'));

// 设置公告图片轮播：自动播放 4s，指示点切换，hover 暂停
const idx = ref(0);
let timer: ReturnType<typeof setInterval> | null = null;
function startAuto() {
  stopAuto();
  const n = props.announcement?.images.length || 0;
  if (n > 1) {
    timer = setInterval(() => {
      idx.value = (idx.value + 1) % n;
    }, 4000);
  }
}
function stopAuto() {
  if (timer) { clearInterval(timer); timer = null; }
}

watch(() => props.show, (v) => {
  if (v) { idx.value = 0; startAuto(); } else stopAuto();
});
watch(() => props.announcement?.images.length, () => { idx.value = 0; });
onUnmounted(stopAuto);

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
.notice-content { font-size: 14px; line-height: 1.7; color: #374151; word-break: break-word; white-space: pre-wrap; }
.notice-content :deep(p) { margin: 0 0 10px; }
.notice-content :deep(img) { max-width: 100%; border-radius: 8px; }
.notice-content :deep(ul), .notice-content :deep(ol) { padding-left: 22px; margin: 0 0 10px; }
.notice-content :deep(table) { width: 100%; border-collapse: collapse; margin: 10px 0; font-size: 13px; }
.notice-content :deep(th), .notice-content :deep(td) { border: 1px solid #e5e7eb; padding: 6px 10px; text-align: left; }
.notice-content :deep(th) { background: #f8fafc; }
.notice-content :deep(pre) { background: #0f172a; color: #e2e8f0; padding: 12px; border-radius: 8px; overflow-x: auto; font-size: 12px; margin: 10px 0; }
.notice-content :deep(blockquote) { margin: 10px 0; padding: 8px 12px; border-left: 4px solid #2563eb; background: #f5f9ff; border-radius: 0 8px 8px 0; }

/* 设置公告图片轮播 */
.ann-slider { position: relative; border-radius: 10px; overflow: hidden; }
.ann-track { display: flex; transition: transform 0.4s ease; }
.ann-slide { flex: 0 0 100%; }
.ann-slide img { width: 100%; max-height: 300px; object-fit: contain; display: block; }
.ann-dots {
  position: absolute; bottom: 8px; right: 12px; display: flex; gap: 6px;
}
.ann-dot {
  width: 8px; height: 8px; border-radius: 999px; background: rgba(255, 255, 255, 0.55);
  cursor: pointer; transition: all 0.2s;
}
.ann-dot.active { background: #fff; width: 18px; }

.notice-foot {
  display: flex; justify-content: flex-end; gap: 10px;
  padding: 12px 18px; border-top: 1px solid #f3f4f6;
}
</style>
