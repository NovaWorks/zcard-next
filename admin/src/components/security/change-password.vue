<script setup lang="ts">
import { reactive, ref } from "vue";
import { request } from "@/service/request";
import { useAuthStore } from "@/store/modules/auth";

const emit = defineEmits<{ completed: []; cancel: [] }>();
const auth = useAuthStore();
const busy = ref(false);
const form = reactive({ current_password: "", new_password: "", confirm_password: "" });
const errors = reactive({ current_password: "", new_password: "", confirm_password: "" });
const errorText = ref("");
async function submit() {
  if (busy.value) return;
  errors.current_password = form.current_password ? "" : "请输入当前密码";
  errors.new_password = [...form.new_password].length < 6 ? "新密码至少需要 6 个字符" : new TextEncoder().encode(form.new_password).length > 72 ? "新密码过长，请缩短后再试" : form.new_password === form.current_password ? "新密码不能与当前密码相同" : "";
  errors.confirm_password = !form.confirm_password ? "请再次输入新密码" : form.confirm_password !== form.new_password ? "两次输入的新密码不一致" : "";
  errorText.value = "";
  if (Object.values(errors).some(Boolean)) return;
  busy.value = true;
  try {
    const { error } = await request({ url: "/api/v1/admin/auth/password", method: "post", data: { ...form } });
    if (error) {
      errorText.value = (error as any)?.response?.data?.message || "修改失败，请检查当前密码或稍后重试";
      return;
    }
    form.current_password = form.new_password = form.confirm_password = "";
    emit("completed");
    window.$message?.success("密码已修改，请使用新密码重新登录");
    // The server has already invalidated all old access and refresh tokens.
    await auth.resetStore();
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <NModal :show="true" preset="card" title="修改密码" style="width: 460px; max-width: calc(100vw - 24px)" :mask-closable="!busy" :close-on-esc="!busy" :closable="!busy" @update:show="!$event && emit('cancel')">
    <p class="mb-16px text-gray-500">修改成功后将自动退出，请使用新密码重新登录。</p>
    <NAlert v-if="errorText" type="error" class="mb-16px" role="alert">{{ errorText }}</NAlert>
    <NForm :disabled="busy" @submit.prevent="submit">
      <NFormItem label="当前密码" :validation-status="errors.current_password ? 'error' : undefined" :feedback="errors.current_password">
        <NInput v-model:value="form.current_password" type="password" show-password-on="click" autocomplete="current-password" :input-props="{ 'aria-label': '当前密码' }" />
      </NFormItem>
      <NFormItem label="新密码" :validation-status="errors.new_password ? 'error' : undefined" :feedback="errors.new_password">
        <NInput v-model:value="form.new_password" type="password" show-password-on="click" autocomplete="new-password" placeholder="至少 6 个字符" :input-props="{ 'aria-label': '新密码' }" />
      </NFormItem>
      <NFormItem label="确认新密码" :validation-status="errors.confirm_password ? 'error' : undefined" :feedback="errors.confirm_password">
        <NInput v-model:value="form.confirm_password" type="password" show-password-on="click" autocomplete="new-password" :input-props="{ 'aria-label': '确认新密码' }" />
      </NFormItem>
      <NSpace justify="end">
        <NButton :disabled="busy" @click="emit('cancel')">取消</NButton>
        <NButton type="primary" attr-type="submit" :loading="busy" :disabled="busy">确认修改</NButton>
      </NSpace>
    </NForm>
  </NModal>
</template>
