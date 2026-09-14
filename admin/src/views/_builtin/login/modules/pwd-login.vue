<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { NCheckbox } from "naive-ui";
import { useAuthStore } from "@/store/modules/auth";
import { useFormRules, useNaiveForm } from "@/hooks/common/form";
import { $t } from "@/locales";
import { fetchAdminCaptchaConfig, fetchAdminCaptchaImage } from "@/service/api";

defineOptions({
  name: "PwdLogin",
});

const authStore = useAuthStore();
const { formRef, validate } = useNaiveForm();

interface FormModel {
  userName: string;
  password: string;
}

const model: FormModel = reactive({
  userName: "",
  password: "",
});

// 仅保存账号；进入登录页时清除旧版本曾保存的密码。
const REMEMBER_KEY = "zcard.admin.remember";
const rememberAccount = ref(false);
const challenge = ref("");
const otpCode = ref("");
const useRecovery = ref(false);
const challengeUntil = ref(0);
function loadRemembered() {
  try {
    const r = JSON.parse(localStorage.getItem(REMEMBER_KEY) || "{}");
    if (r.userName) model.userName = r.userName;
    rememberAccount.value = Boolean(r.userName);
    localStorage.setItem(REMEMBER_KEY, JSON.stringify({ userName: r.userName || "" }));
  } catch {
    /* 损坏忽略 */
  }
}
function saveRemembered() {
  if (rememberAccount.value)
    localStorage.setItem(REMEMBER_KEY, JSON.stringify({ userName: model.userName }));
  else localStorage.removeItem(REMEMBER_KEY);
}
async function restartLogin() {
  challenge.value = "";
  otpCode.value = "";
  model.password = "";
  useRecovery.value = false;
  if (showCaptcha.value) await loadCaptchaImage();
}

const rules = computed<Record<keyof FormModel, App.Global.FormRule[]>>(() => {
  // inside computed to make locale reactive, if not apply i18n, you can define it without computed
  const { formRules } = useFormRules();

  return {
    userName: formRules.userName,
    password: formRules.pwd,
  };
});

// ── 后台登录图形验证码（security.captcha_admin_login 开启时显示）──
const showCaptcha = ref(false);
const captchaId = ref("");
const captchaImage = ref("");
const captchaCode = ref("");

onMounted(async () => {
  loadRemembered();
  try {
    const { data } = await fetchAdminCaptchaConfig();
    showCaptcha.value = data?.enabled === true;
    if (showCaptcha.value) await loadCaptchaImage();
  } catch {
    // 开关接口失败：按未开启处理（后端开启时会拒绝无验证码登录）
  }
});

/** 拉取验证码图片（点击图片可刷新） */
async function loadCaptchaImage() {
  try {
    const { data } = await fetchAdminCaptchaImage();
    if (data?.captcha_id) {
      captchaId.value = data.captcha_id;
      captchaImage.value = data.image_base64;
      captchaCode.value = "";
    }
  } catch {
    // 拉取失败保留原图——用户点击可重试
  }
}

async function handleSubmit() {
  if (authStore.loginLoading) return;
  if (challenge.value) {
    if (Date.now() >= challengeUntil.value) {
      window.$message?.warning("验证已过期，请重新登录");
      await restartLogin();
      return;
    }
    if (
      useRecovery.value
        ? !/^[a-fA-F0-9]{20}$/.test(otpCode.value.trim())
        : !/^\d{6}$/.test(otpCode.value.trim())
    ) {
      window.$message?.warning(useRecovery.value ? "请输入 20 位恢复码" : "请输入 6 位动态码");
      return;
    }
    const result = await authStore.login("", "", {
      challenge: challenge.value,
      totp_code: otpCode.value.trim(),
    });
    otpCode.value = "";
    if (result?.reason === "identity.MFA_EXPIRED") await restartLogin();
    return;
  }
  await validate();
  if (showCaptcha.value && !captchaCode.value.trim()) {
    window.$message?.warning("请输入图形验证码");
    return;
  }
  saveRemembered();
  const result = await authStore.login(
    model.userName,
    model.password,
    showCaptcha.value
      ? { captcha_id: captchaId.value, captcha_code: captchaCode.value.trim() }
      : undefined,
  );
  if (result?.challenge) {
    challenge.value = result.challenge;
    challengeUntil.value = Date.now() + 5 * 60_000;
    model.password = "";
    return;
  }
  // 登录失败（错误消息已由 store 处理）或成功跳转后刷新验证码，下次登录用新图
  if (showCaptcha.value) await loadCaptchaImage();
}
</script>

<template>
  <NForm
    ref="formRef"
    :model="model"
    :rules="rules"
    size="large"
    :show-label="false"
    @keyup.enter="handleSubmit"
  >
    <template v-if="!challenge">
      <NFormItem path="userName">
        <NInput
          v-model:value="model.userName"
          autocomplete="username"
          :placeholder="$t('page.login.common.userNamePlaceholder')"
        />
      </NFormItem>
      <NFormItem path="password">
        <NInput
          v-model:value="model.password"
          autocomplete="current-password"
          type="password"
          show-password-on="click"
          :placeholder="$t('page.login.common.passwordPlaceholder')"
        />
      </NFormItem>
      <!-- 后台登录验证码（安全 → 后台登录验证码 开启时显示） -->
      <NFormItem v-if="showCaptcha" path="captchaCode">
        <div class="flex w-full items-center gap-8px">
          <NInput
            v-model:value="captchaCode"
            class="flex-1"
            :maxlength="4"
            placeholder="图形验证码（4 位数字）"
          />
          <img
            v-if="captchaImage"
            :src="captchaImage"
            alt="验证码"
            title="点击刷新"
            class="h-40px cursor-pointer rounded-6px border border-#e5e7eb"
            @click="loadCaptchaImage"
          />
          <NButton v-else quaternary size="small" @click="loadCaptchaImage">获取验证码</NButton>
        </div>
      </NFormItem>
      <div class="mb-8px">
        <NCheckbox v-model:checked="rememberAccount" size="small">记住账号</NCheckbox>
      </div>
    </template>
    <template v-else>
      <NAlert type="info" class="mb-16px">{{
        useRecovery
          ? "输入一个未使用的恢复码。每个恢复码只能使用一次。"
          : "请输入 Google Authenticator 中的 6 位动态验证码。"
      }}</NAlert>
      <NFormItem :label="useRecovery ? '恢复码' : '动态验证码'" :show-label="true">
        <NInput
          v-model:value="otpCode"
          :placeholder="useRecovery ? '20 位恢复码' : '6 位动态验证码'"
          :maxlength="useRecovery ? 20 : 6"
          autocomplete="one-time-code"
          :input-props="{ inputmode: useRecovery ? 'text' : 'numeric' }"
        />
      </NFormItem>
      <NSpace class="mb-16px"
        ><NButton
          text
          @click="
            useRecovery = !useRecovery;
            otpCode = '';
          "
          >{{ useRecovery ? "使用动态验证码" : "手机丢失？使用恢复码" }}</NButton
        ><NButton text @click="restartLogin">返回账号登录</NButton></NSpace
      >
    </template>
    <NSpace vertical :size="24">
      <!-- admin 面仅密码登录：验证码登录/注册/找回密码均为 storefront 能力，
           后端无对应端点，模板入口已移除（勿恢复——会切到无实现的登录模块） -->
      <NButton
        type="primary"
        size="large"
        round
        block
        :loading="authStore.loginLoading"
        @click="handleSubmit"
      >
        {{ $t("common.confirm") }}
      </NButton>
    </NSpace>
  </NForm>
</template>

<style scoped></style>
