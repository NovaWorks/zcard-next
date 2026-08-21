<template>
  <div class="card" style="max-width: 420px; margin: 40px auto;">
    <h2 style="margin-bottom: 16px;">注册</h2>

    <!-- 注册关闭 -->
    <template v-if="cfg && !cfg.enabled">
      <div class="muted" style="text-align: center; padding: 24px 0;">站点暂未开放注册</div>
      <div class="muted" style="text-align: center;">
        已有账号？<router-link to="/login">登录</router-link>
      </div>
    </template>

    <template v-else>
      <!-- 注册方式切换（多选时显示；单选自动隐藏） -->
      <div v-if="methods.length > 1" class="method-tabs">
        <button
          v-for="m in methods"
          :key="m"
          class="method-tab"
          :class="{ active: activeMethod === m }"
          @click="activeMethod = m"
        >
          {{ methodLabel(m) }}
        </button>
      </div>

      <div class="field">
        <label>用户名 *</label>
        <input class="input" v-model="username" type="text" placeholder="用户名" />
      </div>
      <div class="field">
        <label>密码 *</label>
        <input class="input" v-model="password" type="password" placeholder="至少 6 位" />
      </div>

      <!-- 邮箱验证模式：邮箱必填 + 验证码 -->
      <div v-if="activeMethod === 'email'" class="field">
        <label>邮箱 *</label>
        <div style="display: flex; gap: 8px;">
          <input class="input" v-model="email" type="email" placeholder="用于登录和找回密码" style="flex: 1;" />
          <button class="btn secondary" :disabled="sending || cooldown > 0" @click="doSendCode('email')">
            {{ cooldown > 0 ? `${cooldown}s` : sending ? '发送中' : '发验证码' }}
          </button>
        </div>
      </div>
      <div v-else class="field">
        <label>邮箱</label>
        <input class="input" v-model="email" type="email" placeholder="选填" />
      </div>

      <!-- 手机验证模式：手机号必填 + 短信验证码 -->
      <div v-if="activeMethod === 'phone'" class="field">
        <label>手机号 *</label>
        <div style="display: flex; gap: 8px;">
          <input class="input" v-model="phone" type="tel" placeholder="11 位手机号" style="flex: 1;" />
          <button class="btn secondary" :disabled="sending || cooldown > 0" @click="doSendCode('phone')">
            {{ cooldown > 0 ? `${cooldown}s` : sending ? '发送中' : '发验证码' }}
          </button>
        </div>
      </div>

      <!-- 验证码输入（email/phone 模式） -->
      <div v-if="activeMethod === 'email' || activeMethod === 'phone'" class="field">
        <label>验证码 *</label>
        <input class="input" v-model="code" type="text" placeholder="6 位数字验证码" maxlength="6" />
      </div>

      <!-- 图形验证码（captcha_register 开启时） -->
      <div v-if="captchaCfg.register" class="field">
        <label>图形验证码 *</label>
        <CaptchaInput ref="captchaRef" @update:code="captchaCode = $event" @update:captcha-id="captchaId = $event" />
      </div>
      <div class="field">
        <label>推广码</label>
        <input class="input" v-model="inviteCode" type="text" placeholder="选填（好友的推广码，如 ABC12345）" />
        <div v-if="inviteCode" class="muted" style="margin-top: 4px;">已通过推广链接自动填写</div>
      </div>
      <div v-if="error" class="error">{{ error }}</div>
      <button class="btn" style="width: 100%; margin-top: 8px;" :disabled="loading" @click="submit">
        {{ loading ? '注册中…' : '注册' }}
      </button>
      <div class="muted" style="margin-top: 12px; text-align: center;">
        已有账号？<router-link to="/login">登录</router-link>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { register, sendRegisterCode, fetchRegisterConfig, fetchCaptchaConfig, type RegisterConfig, type CaptchaConfig } from '@/api';
import CaptchaInput from '@/components/CaptchaInput.vue';
import { setToken } from '@/api/client';
import { refreshAuth } from '@/auth';
import { getRefCode } from '@/ref';

const route = useRoute();
const router = useRouter();
const username = ref('');
const password = ref('');
const email = ref('');
const phone = ref('');
const code = ref('');
const inviteCode = ref('');
const error = ref('');
const loading = ref(false);
const cfg = ref<RegisterConfig | null>(null);
const captchaCfg = ref<CaptchaConfig>({ login: false, register: true, order: false, reset: true });
const captchaId = ref('');
const captchaCode = ref('');
const captchaRef = ref<InstanceType<typeof CaptchaInput> | null>(null);

// 注册方式（多选数组；单选时无切换器）
type RegisterMethod = 'username' | 'email' | 'phone';
const methods = computed<RegisterMethod[]>(() =>
  (cfg.value?.methods?.length ? cfg.value.methods : ['username']) as RegisterMethod[]
);
const activeMethod = ref<RegisterMethod>('username');

watch(methods, (list) => {
  if (!list.includes(activeMethod.value)) {
    activeMethod.value = list.includes('username') ? 'username' : list[0];
  }
});

function methodLabel(m: string) {
  return m === 'email' ? '邮箱验证' : m === 'phone' ? '手机验证' : '用户名';
}

// 验证码发送
const sending = ref(false);
const cooldown = ref(0);
let cdTimer: ReturnType<typeof setInterval> | null = null;

function startCooldown() {
  cooldown.value = 60;
  cdTimer = setInterval(() => {
    cooldown.value -= 1;
    if (cooldown.value <= 0 && cdTimer) {
      clearInterval(cdTimer);
      cdTimer = null;
    }
  }, 1000);
}

async function doSendCode(channel: 'email' | 'phone') {
  const target = channel === 'email' ? email.value.trim() : phone.value.trim();
  if (!target) {
    error.value = channel === 'email' ? '请先填写邮箱' : '请先填写手机号';
    return;
  }
  if (captchaCfg.value.register && !captchaCode.value) {
    error.value = '请先输入图形验证码';
    return;
  }
  sending.value = true;
  error.value = '';
  const { error: err } = await sendRegisterCode(target, channel, captchaCfg.value.register ? { captcha_id: captchaId.value, captcha_code: captchaCode.value } : undefined);
  sending.value = false;
  if (err) {
    error.value = err;
    captchaRef.value?.refresh();
    return;
  }
  window.alert('验证码已发送，请查收');
  startCooldown();
}

// 推广归因预填：URL ?ref= 优先；否则读 localStorage（推广链接进站捕获的 30 天归因）
onMounted(async () => {
  const ref = Array.isArray(route.query.ref) ? route.query.ref[0] : route.query.ref;
  if (typeof ref === 'string' && ref) {
    inviteCode.value = ref;
  } else {
    const stored = getRefCode();
    if (stored) inviteCode.value = stored;
  }
  cfg.value = await fetchRegisterConfig();
  captchaCfg.value = await fetchCaptchaConfig();
});
onUnmounted(() => { if (cdTimer) clearInterval(cdTimer); });

async function submit() {
  if (!username.value || !password.value) {
    error.value = '用户名和密码必填';
    return;
  }
  if (activeMethod.value === 'email' && !email.value.trim()) {
    error.value = '请填写邮箱';
    return;
  }
  if (activeMethod.value === 'phone' && !phone.value.trim()) {
    error.value = '请填写手机号';
    return;
  }
  if ((activeMethod.value === 'email' || activeMethod.value === 'phone') && !code.value.trim()) {
    error.value = '请填写验证码';
    return;
  }
  if (captchaCfg.value.register && !captchaCode.value) {
    error.value = '请输入图形验证码';
    return;
  }
  loading.value = true;
  error.value = '';
  const { data, error: err } = await register({
    username: username.value,
    password: password.value,
    email: email.value.trim() || undefined,
    phone: phone.value.trim() || undefined,
    code: code.value.trim() || undefined,
    invite_code: inviteCode.value || undefined,
    captcha_id: captchaId.value || undefined,
    captcha_code: captchaCode.value || undefined,
  });
  loading.value = false;
  if (err || !data) {
    error.value = err || '注册失败';
    captchaRef.value?.refresh();
    return;
  }
  setToken(data.token);
  await refreshAuth();
  router.push(typeof route.query.redirect === 'string' ? route.query.redirect : '/member');
}
</script>

<style scoped>
.method-tabs {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
  padding: 4px;
  background: #f3f4f6;
  border-radius: 8px;
}
.method-tab {
  flex: 1;
  padding: 7px 0;
  font-size: 13px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #6b7280;
  cursor: pointer;
  transition: all 0.15s;
}
.method-tab.active {
  background: #fff;
  color: #2563eb;
  font-weight: 600;
  box-shadow: 0 1px 4px rgba(15, 23, 42, 0.08);
}
</style>
