<script setup lang="ts">
import { computed, reactive } from "vue";
import { useAuthStore } from "@/store/modules/auth";
import { useFormRules, useNaiveForm } from "@/hooks/common/form";
import { $t } from "@/locales";

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

async function handleSubmit() {
  await validate();
  await authStore.login(model.userName, model.password);
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
