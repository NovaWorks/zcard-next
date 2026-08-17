<template>
  <div class="card" style="max-width: 420px; margin: 40px auto;">
    <h2 style="margin-bottom: 16px;">登录</h2>
    <div class="field">
      <label>用户名</label>
      <input class="input" v-model="username" type="text" placeholder="用户名" @keyup.enter="submit" />
    </div>
    <div class="field">
      <label>密码</label>
      <input class="input" v-model="password" type="password" placeholder="密码" @keyup.enter="submit" />
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
import { ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { login } from '@/api';
import { setToken } from '@/api/client';
import { refreshAuth } from '@/auth';

const route = useRoute();
const router = useRouter();
const username = ref('');
const password = ref('');
const error = ref('');
const loading = ref(false);

async function submit() {
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码';
    return;
  }
  loading.value = true;
  error.value = '';
  const { data, error: err } = await login({ username: username.value, password: password.value });
  loading.value = false;
  if (err || !data) {
    error.value = err || '登录失败';
    return;
  }
  setToken(data.access_token);
  await refreshAuth(); // 顶栏登录态即时更新（SPA 内不整页刷新）
  router.push(typeof route.query.redirect === 'string' ? route.query.redirect : '/member');
}
</script>
