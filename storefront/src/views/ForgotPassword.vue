<template>
  <div class="card" style="max-width: 420px; margin: 40px auto;">
    <h2 style="margin-bottom: 16px;">找回密码</h2>

    <!-- 步骤一：输入邮箱发码 -->
    <template v-if="step === 1">
      <div class="field">
        <label>注册邮箱 *</label>
        <input class="input" v-model="email" type="email" placeholder="you@example.com" @keyup.enter="sendCode" />
        <div class="muted">若该邮箱已注册，验证码将发送至邮箱（15 分钟内有效）</div>
      </div>
      <div v-if="captchaCfg.reset" class="field">
        <label>图形验证码 *</label>
        <CaptchaInput ref="captchaRef" @update:code="captchaCode = $event" @update:captcha-id="captchaId = $event" />
      </div>
      <div v-if="error" class="error" style="margin-bottom: 8px;">{{ error }}</div>
      <button class="btn" style="width: 100%; margin-top: 8px;" :disabled="sending" @click="sendCode">
        {{ sending ? '发送中…' : '发送验证码' }}
      </button>
    </template>

    <!-- 步骤二：验证码 + 新密码 -->
    <template v-else>
      <div class="muted" style="margin-bottom: 12px;">
        验证码已发送至 <b>{{ email }}</b>
        <button class="btn secondary" style="margin-left: 8px; padding: 2px 10px;" :disabled="cooldown > 0 || sending" @click="sendCode">
          {{ cooldown > 0 ? `${cooldown}s 后可重发` : '重新发送' }}
        </button>
      </div>
      <div class="field">
        <label>验证码 *</label>
        <input class="input" v-model="code" type="text" maxlength="6" placeholder="6 位数字" />
      </div>
      <div class="field">
        <label>新密码 *</label>
        <input class="input" v-model="newPassword" type="password" placeholder="至少 6 位" />
      </div>
      <div class="field">
        <label>确认新密码 *</label>
        <input class="input" v-model="confirmPassword" type="password" placeholder="再输入一次" />
      </div>
      <div v-if="error" class="error" style="margin-bottom: 8px;">{{ error }}</div>
      <button class="btn" style="width: 100%; margin-top: 8px;" :disabled="resetting" @click="doReset">
        {{ resetting ? '重置中…' : '重置密码' }}
      </button>
    </template>

    <div class="muted" style="margin-top: 12px; text-align: center;">
      想起密码了？<router-link to="/login">返回登录</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import CaptchaInput from '@/components/CaptchaInput.vue';
import { forgotPassword, resetPassword, fetchCaptchaConfig, type CaptchaConfig } from '@/api';
import { setToken } from '@/api/client';
import { refreshAuth } from '@/auth';

const router = useRouter();
const step = ref<1 | 2>(1);
const email = ref('');
const code = ref('');
const newPassword = ref('');
const confirmPassword = ref('');
const error = ref('');
const sending = ref(false);
const captchaCfg = ref<CaptchaConfig>({ login: false, register: true, order: false, reset: true });
const captchaId = ref('');
const captchaCode = ref('');
const captchaRef = ref<InstanceType<typeof CaptchaInput> | null>(null);

onMounted(async () => {
  captchaCfg.value = await fetchCaptchaConfig();
});
const resetting = ref(false);
const cooldown = ref(0);
let timer: ReturnType<typeof setInterval> | null = null;

onUnmounted(() => { if (timer) clearInterval(timer); });

function startCooldown() {
  cooldown.value = 60;
  timer = setInterval(() => {
    cooldown.value--;
    if (cooldown.value <= 0 && timer) { clearInterval(timer); timer = null; }
  }, 1000);
}

async function sendCode() {
  if (!email.value.includes('@')) {
    error.value = '请输入有效邮箱';
    return;
  }
  sending.value = true;
  error.value = '';
  const { error: err } = await forgotPassword(email.value.trim(), captchaCfg.value.reset ? { captcha_id: captchaId.value, captcha_code: captchaCode.value } : undefined);
  sending.value = false;
  // 防枚举：任何输入都成功——不区分"邮箱不存在"
  if (err) {
    error.value = err; // 仅冷却等业务错误到达这里
    return;
  }
  step.value = 2;
  if (cooldown.value <= 0) startCooldown();
}

async function doReset() {
  if (code.value.length !== 6) {
    error.value = '请输入 6 位验证码';
    return;
  }
  if (newPassword.value.length < 6) {
    error.value = '新密码至少 6 位';
    return;
  }
  if (newPassword.value !== confirmPassword.value) {
    error.value = '两次输入的密码不一致';
    return;
  }
  resetting.value = true;
  error.value = '';
  const { data, error: err } = await resetPassword({
    email: email.value.trim(),
    code: code.value.trim(),
    new_password: newPassword.value
  });
  resetting.value = false;
  if (err || !data) {
    error.value = err || '重置失败（验证码错误或已过期）';
    return;
  }
  setToken(data.token); // 重置即登录
  await refreshAuth();
  router.push('/member');
}
</script>
