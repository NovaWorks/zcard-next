<template>
  <div class="captcha-input">
    <input
      class="input captcha-code-input"
      :value="code"
      type="text"
      inputmode="numeric"
      placeholder="4 位数字"
      maxlength="4"
      @input="onInput"
    />
    <img
      v-if="image"
      :src="image"
      class="captcha-img"
      alt="验证码"
      title="点击刷新"
      @click="refresh"
    />
    <div v-else class="captcha-loading">加载中…</div>
  </div>
</template>

<script setup lang="ts">
/**
 * 图形验证码输入（4 位数字）：图片自动加载 + 点击刷新 + 提交 payload 同步。
 * 验证失败后父组件调 refresh() 自动换图。
 */
import { ref, onMounted, onUnmounted } from 'vue';
import { fetchCaptcha } from '@/api';

const props = defineProps<{
  /** 初始是否自动加载（默认 true） */
  autoLoad?: boolean;
}>();
const emit = defineEmits<{
  (e: 'update:code', v: string): void;
  (e: 'update:captchaId', v: string): void;
}>();

const code = ref('');
const image = ref('');
const captchaId = ref('');
let loaded = false;
let fallbackTimer: ReturnType<typeof setTimeout> | null = null;

onMounted(() => {
  if (props.autoLoad !== false) refresh();
  // 兜底：500ms 后仍未加载（时序/瞬态网络）自动补拉一次
  fallbackTimer = setTimeout(() => { if (!loaded) refresh(); }, 500);
});
onUnmounted(() => { if (fallbackTimer) clearTimeout(fallbackTimer); });

async function refresh() {
  try {
    const { data } = await fetchCaptcha();
    if (data?.captcha_id) {
      loaded = true;
      captchaId.value = data.captcha_id;
      image.value = data.image_base64;
      code.value = '';
      emit('update:captchaId', data.captcha_id);
      emit('update:code', '');
    }
  } catch { /* 拉取失败保留占位——用户点击可重试 */ }
}

function onInput(e: Event) {
  const v = (e.target as HTMLInputElement).value.replace(/\D/g, '').slice(0, 4);
  code.value = v;
  (e.target as HTMLInputElement).value = v;
  emit('update:code', v);
}

/** 暴露：验证失败后刷新 */
defineExpose({ refresh });
</script>

<style scoped>
.captcha-input { display: flex; gap: 8px; align-items: center; }
.captcha-code-input { flex: 1; min-width: 0; letter-spacing: 4px; font-size: 15px; }
.captcha-img {
  height: 40px; border-radius: 6px; cursor: pointer;
  border: 1px solid #e5e7eb; flex-shrink: 0;
}
.captcha-img:hover { border-color: #2563eb; }
.captcha-loading {
  width: 100px; height: 40px; flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
  background: #f8fafc; border-radius: 6px; color: #9ca3af; font-size: 12px;
}
</style>
