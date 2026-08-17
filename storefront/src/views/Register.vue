<template>
  <div class="card" style="max-width: 420px; margin: 40px auto;">
    <h2 style="margin-bottom: 16px;">注册</h2>
    <div class="field">
      <label>用户名 *</label>
      <input class="input" v-model="username" type="text" placeholder="用户名" />
    </div>
    <div class="field">
      <label>密码 *</label>
      <input class="input" v-model="password" type="password" placeholder="密码" />
    </div>
    <div class="field">
      <label>邮箱</label>
      <input class="input" v-model="email" type="email" placeholder="选填" />
    </div>
    <div class="field">
      <label>邀请码</label>
      <input class="input" v-model="inviteCode" type="text" placeholder="选填（上级用户 ID）" />
    </div>
    <div v-if="error" class="error">{{ error }}</div>
    <button class="btn" style="width: 100%; margin-top: 8px;" :disabled="loading" @click="submit">
      {{ loading ? '注册中…' : '注册' }}
    </button>
    <div class="muted" style="margin-top: 12px; text-align: center;">
      已有账号？<router-link to="/login">登录</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { register } from '@/api';
import { setToken } from '@/api/client';

const route = useRoute();
const router = useRouter();
const username = ref('');
const password = ref('');
const email = ref('');
const inviteCode = ref('');
const error = ref('');
const loading = ref(false);

// 邀请链接 ?ref=<userId> 预填邀请码（P3-03 分销归因入口）
onMounted(() => {
  const ref = Array.isArray(route.query.ref) ? route.query.ref[0] : route.query.ref;
  if (typeof ref === 'string' && ref) inviteCode.value = ref;
});

async function submit() {
  if (!username.value || !password.value) {
    error.value = '用户名和密码必填';
    return;
  }
  loading.value = true;
  error.value = '';
  const { data, error: err } = await register({
    username: username.value,
    password: password.value,
    email: email.value || undefined,
    invite_code: inviteCode.value || undefined
  });
  loading.value = false;
  if (err || !data) {
    error.value = err || '注册失败';
    return;
  }
  setToken(data.token); // 注册即登录
  router.push(typeof route.query.redirect === 'string' ? route.query.redirect : '/member');
}
</script>
