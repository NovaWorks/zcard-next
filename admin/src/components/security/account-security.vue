<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { NQrCode } from "naive-ui";
import { request } from "@/service/request";
import { fetchGetUserInfo } from "@/service/api";
import { useAuthStore } from "@/store/modules/auth";
import { recoveryCodes, recoveryTicket } from "./state";
const props = defineProps<{ resetTarget?: { id: number; username: string } }>();
const emit = defineEmits<{ completed: [] }>();
const auth = useAuthStore();
const enabled = ref(false);
const boundAt = ref(0);
const loaded = ref(false);
const loadFailed = ref(false);
const busy = ref(false);
const mode = ref<"status" | "setup" | "disable">("status");
const pending = ref<{ secret: string; otpauth_url: string }>();
const form = reactive({ password: "", code: "", confirm: "", reason: "" });
const errorText = ref("");
async function loadStatus() {
  loadFailed.value = false;
  const { data, error } = await fetchGetUserInfo();
  if (!error) {
    enabled.value = Boolean(data?.admin?.totp_enabled);
    boundAt.value = Number(data?.admin?.totp_bound_at || 0);
    loaded.value = true;
  } else {
    loadFailed.value = true;
  }
}
onMounted(loadStatus);
async function run(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  errorText.value = "";
  try {
    await fn();
  } finally {
    busy.value = false;
  }
}
function start(value: "setup" | "disable") {
  mode.value = value;
  form.password = "";
  form.code = "";
}
async function setup() {
  if (!form.password || (enabled.value && !form.code && !recoveryTicket.value)) {
    errorText.value = "请输入当前密码和动态码或恢复码";
    return;
  }
  await run(async () => {
    const { data, error } = await request<{ secret: string; otpauth_url: string }>({
      url: "/api/v1/admin/auth/totp/enable",
      method: "post",
      data: { password: form.password, code: form.code, recovery_ticket: recoveryTicket.value },
    });
    if (!error) {
      pending.value = data;
      recoveryTicket.value = "";
      form.password = "";
      form.code = "";
      form.confirm = "";
    }
  });
}
async function confirm() {
  if (!/^\d{6}$/.test(form.confirm)) {
    errorText.value = "请输入 6 位动态验证码";
    return;
  }
  await run(async () => {
    const { data, error } = await request<{ recovery_codes: string[] }>({
      url: "/api/v1/admin/auth/totp/confirm",
      method: "post",
      data: { code: form.confirm },
    });
    if (!error) {
      recoveryCodes.value = data.recovery_codes;
      pending.value = undefined;
      form.confirm = "";
      await auth.resetStore();
    }
  });
}
async function disable() {
  if (!form.password || !form.code) {
    errorText.value = "请输入当前密码和动态码或恢复码";
    return;
  }
  await run(async () => {
    const { error } = await request({
      url: "/api/v1/admin/auth/totp/disable",
      method: "post",
      data: { password: form.password, code: form.code },
    });
    if (!error) {
      window.$message?.success("已解绑，请重新登录");
      await auth.resetStore();
    }
  });
}
async function resetOther() {
  if (
    !props.resetTarget ||
    !form.password ||
    !form.reason.trim() ||
    (enabled.value && !form.code)
  ) {
    errorText.value = "请填写当前密码、重置原因，以及你自己的动态码（已绑定时）";
    return;
  }
  await run(async () => {
    const { error } = await request({
      url: "/api/v1/admin/auth/totp/reset",
      method: "post",
      data: {
        admin_id: props.resetTarget!.id,
        password: form.password,
        code: form.code,
        reason: form.reason,
      },
    });
    if (!error) {
      form.password = "";
      form.code = "";
      window.$message?.success("已重置该员工的两步验证，旧会话已失效");
      emit("completed");
    }
  });
}
async function cancel() {
  if (pending.value) {
    await run(async () => {
      const { error } = await request({ url: "/api/v1/admin/auth/totp/cancel", method: "post" });
      if (!error) {
        pending.value = undefined;
        mode.value = "status";
      }
    });
  } else {
    mode.value = "status";
  }
  form.password = "";
  form.code = "";
  form.confirm = "";
}
</script>
<template>
  <NCard
    :title="resetTarget ? `重置 ${resetTarget.username} 的两步验证` : '当前账号两步验证'"
    class="my-16px"
  >
    <NSpin :show="(!loaded && !loadFailed) || busy">
      <NSpace :key="resetTarget ? 'reset' : pending ? 'pending' : mode" vertical :size="16">
        <NAlert v-if="loadFailed" type="error"
          >读取账号安全状态失败。<NButton text @click="loadStatus">重试</NButton></NAlert
        >
        <NAlert v-if="errorText" type="error" role="alert">{{ errorText }}</NAlert>
        <template v-if="resetTarget">
          <NAlert type="warning"
            >该员工的验证器、恢复码及全部后台会话将失效。请验证你自己的身份后重置。</NAlert
          >
          <NFormItem label="你的当前密码"
            ><NInput v-model:value="form.password" type="password" autocomplete="current-password"
          /></NFormItem>
          <NFormItem v-if="enabled" label="你的动态码或恢复码"
            ><NInput v-model:value="form.code" autocomplete="one-time-code"
          /></NFormItem>
          <NFormItem label="重置原因"
            ><NInput v-model:value="form.reason" :maxlength="255"
          /></NFormItem>
          <NButton type="error" :loading="busy" :disabled="!loaded" @click="resetOther"
            >确认强制重置</NButton
          >
        </template>
        <template v-else-if="pending">
          <NAlert type="info"
            >请在 10 分钟内用 Google Authenticator
            扫码，并输入动态码完成绑定。确认前原有登录方式继续有效。</NAlert
          >
          <NQrCode :value="pending.otpauth_url" :size="200" aria-label="谷歌验证器绑定二维码" />
          <NFormItem label="无法扫码时手动输入密钥"
            ><NInput :value="pending.secret" readonly
          /></NFormItem>
          <NFormItem label="新验证器的 6 位动态码"
            ><NInput
              v-model:value="form.confirm"
              :maxlength="6"
              autocomplete="one-time-code"
              :input-props="{ inputmode: 'numeric' }"
              @keyup.enter="confirm"
          /></NFormItem>
          <NSpace
            ><NButton type="primary" :loading="busy" @click="confirm">确认绑定</NButton
            ><NButton :disabled="busy" @click="cancel">取消绑定</NButton></NSpace
          >
        </template>
        <template v-else-if="mode !== 'status'">
          <NAlert :type="mode === 'disable' ? 'warning' : 'info'">{{
            mode === "disable"
              ? "解绑后登录后台不再要求动态码，全部旧会话会失效。"
              : "绑定仅对当前账号生效。更换验证器确认成功前，旧验证器继续有效。"
          }}</NAlert>
          <NFormItem label="当前密码"
            ><NInput v-model:value="form.password" type="password" autocomplete="current-password"
          /></NFormItem>
          <NAlert v-if="enabled && recoveryTicket && mode === 'setup'" type="info"
            >已通过恢复码验证身份，可以重新绑定验证器。</NAlert
          >
          <NFormItem v-else-if="enabled" label="当前动态码或未使用的恢复码"
            ><NInput v-model:value="form.code" autocomplete="one-time-code"
          /></NFormItem>
          <NSpace
            ><NButton
              :type="mode === 'disable' ? 'error' : 'primary'"
              :loading="busy"
              @click="mode === 'disable' ? disable() : setup()"
              >{{ mode === "disable" ? "确认解绑" : "生成绑定二维码" }}</NButton
            ><NButton :disabled="busy" @click="cancel">取消</NButton></NSpace
          >
        </template>
        <template v-else>
          <NTag :type="enabled ? 'success' : 'default'">{{
            enabled ? "已绑定 Google Authenticator" : "尚未绑定"
          }}</NTag>
          <p v-if="enabled && boundAt">绑定时间：{{ new Date(boundAt * 1000).toLocaleString() }}</p>
          <p>
            绑定后，每次登录后台都需要密码和 6
            位动态验证码。手机丢失时可使用恢复码；恢复码也丢失时，可在服务器通过命令行恢复。
          </p>
          <NAlert v-if="recoveryTicket" type="warning"
            >你刚通过恢复码登录，建议现在重新绑定验证器。</NAlert
          >
          <NSpace
            ><NButton type="primary" :disabled="!loaded" @click="start('setup')">{{
              enabled ? "更换验证器 / 更新恢复码" : "绑定谷歌验证器"
            }}</NButton
            ><NButton v-if="enabled" @click="start('disable')">解绑验证器</NButton></NSpace
          >
        </template>
      </NSpace>
    </NSpin>
  </NCard>
</template>
