<script setup lang="ts">
import { ref } from "vue";
import { recoveryCodes } from "./state";
const saved = ref(false);
function download() {
  const url = URL.createObjectURL(
    new Blob(
      [
        "ZCard 后台两步验证恢复码\n每个恢复码只能使用一次，请离线保管。\n\n" +
          recoveryCodes.value.join("\n"),
      ],
      { type: "text/plain;charset=utf-8" },
    ),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = "zcard-recovery-codes.txt";
  a.click();
  URL.revokeObjectURL(url);
}
function close() {
  recoveryCodes.value = [];
  saved.value = false;
}
</script>
<template>
  <NModal
    :show="recoveryCodes.length > 0"
    :mask-closable="false"
    :close-on-esc="false"
    preset="card"
    title="保存恢复码"
    :closable="false"
    style="width: 520px; max-width: calc(100vw - 24px)"
  >
    <NSpace vertical :size="16">
      <NAlert type="success">验证器已绑定，旧后台会话已失效。请保存恢复码后重新登录。</NAlert>
      <p>恢复码仅展示这一次。手机丢失时，可用密码和任意一个未使用的恢复码登录。不要分享给他人。</p>
      <pre
        class="overflow-auto rounded p-12px"
        style="background: var(--n-color-modal); user-select: text"
        >{{ recoveryCodes.join("\n") }}</pre
      >
      <NButton @click="download">下载恢复码</NButton>
      <NCheckbox v-model:checked="saved">我已安全保存恢复码</NCheckbox>
      <NButton type="primary" :disabled="!saved" @click="close">完成，返回登录</NButton>
    </NSpace>
  </NModal>
</template>
