<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
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

// 不预填账密（模板原默认 admin/admin123456——生产环境不得携带演示凭据）
const model: FormModel = reactive({
  userName: "",
  password: "",
});

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
  await validate();
  if (showCaptcha.value && !captchaCode.value.trim()) {
    window.$message?.warning("请输入图形验证码");
    return;
  }
  await authStore.login(
    model.userName,
    model.password,
    showCaptcha.value ? { captcha_id: captchaId.value, captcha_code: captchaCode.value.trim() } : undefined
  );
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
    <NFormItem path="userName">
      <NInput
        v-model:value="model.userName"
        :placeholder="$t('page.login.common.userNamePlaceholder')"
      />
    </NFormItem>
    <NFormItem path="password">
      <NInput
        v-model:value="model.password"
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
