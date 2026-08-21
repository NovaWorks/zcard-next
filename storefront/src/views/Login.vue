<template>
  <div class="card" style="max-width: 420px; margin: 40px auto;">
    <h2 style="margin-bottom: 16px;">登录</h2>
    <div class="field">
      <label>账号</label>
      <input class="input" v-model="username" type="text" placeholder="用户名 / 邮箱 / 手机号" @keyup.enter="submit" />
    </div>
    <div class="field">
      <label>密码</label>
      <input class="input" v-model="password" type="password" placeholder="密码" @keyup.enter="submit" />
    </div>
    <div v-if="captchaCfg.login" class="field">
      <label>图形验证码 *</label>
      <CaptchaInput ref="captchaRef" @update:code="captchaCode = $event" @update:captcha-id="captchaId = $event" />
    </div>
    <div v-if="error" class="error">{{ error }}</div>
    <button class="btn" style="width: 100%; margin-top: 8px;" :disabled="loading" @click="submit">
      {{ loading ? '登录中…' : '登录' }}
    </button>
    <div class="muted" style="margin-top: 12px; text-align: center;">
      没有账号？<router-link :to="{ path: '/register', query: $route.query.redirect ? { redirect: String($route.query.redirect) } : {} }">注册</router-link>
      ｜ <router-link to="/forgot-password">忘记密码？</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { login, fetchCaptchaConfig, type CaptchaConfig } from '@/api';
import CaptchaInput from '@/components/CaptchaInput.vue';
import { setToken } from '@/api/client';
import { refreshAuth } from '@/auth';

const route = useRoute();
const router = useRouter();
const username = ref('');
const password = ref('');
const error = ref('');
const loading = ref(false);
const captchaCfg = ref<CaptchaConfig>({ login: false, register: true, order: false, reset: true });
const captchaId = ref('');
const captchaCode = ref('');
const captchaRef = ref<InstanceType<typeof CaptchaInput> | null>(null);

onMounted(async () => {
  captchaCfg.value = await fetchCaptchaConfig();
});

async function submit() {
  if (!username.value || !password.value) {
    error.value = '请输入账号和密码';
    return;
  }
  loading.value = true;
  error.value = '';
  if (captchaCfg.value.login && !captchaCode.value) {
    error.value = '请输入图形验证码';
    return;
  }
  const { data, error: err } = await login({
    username: username.value,
    password: password.value,
    captcha_id: captchaId.value || undefined,
    captcha_code: captchaCode.value || undefined,
  });
  loading.value = false;
  if (err || !data) {
    error.value = err || '登录失败';
    captchaRef.value?.refresh(); // 失败自动换图
    return;
  }
  setToken(data.access_token);
  await refreshAuth(); // 顶栏登录态即时更新（SPA 内不整页刷新）
  router.push(typeof route.query.redirect === 'string' ? route.query.redirect : '/member');
}
</script>
