<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { testEmail } from "@/service/api";

const props = defineProps<{ unsaved: boolean }>();
const show = ref(false);
const sending = ref(false);
const form = reactive({ recipient: "", subject: "SMTP 邮件测试", content: "这是一封测试邮件，用于确认本站邮件发送是否正常。" });
type TestResult = { success: boolean; message: string; metadata?: Record<string, string> };
const result = ref<TestResult | null>(null);
const connectionModes: Record<string, string> = { auto: "自动", plain: "普通 SMTP", tls: "SSL/TLS", starttls: "STARTTLS" };
const authModes: Record<string, string> = { auto: "自动", plain: "PLAIN", login: "LOGIN", cram_md5: "CRAM-MD5", none: "无需认证" };
const valid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.recipient.trim()) && !!form.subject.trim() && !!form.content.trim());

async function send() {
  if (props.unsaved || sending.value || !valid.value) return;
  sending.value = true;
  result.value = null;
  try {
    const { error } = await testEmail({ recipient: form.recipient.trim(), subject: form.subject.trim(), content: form.content });
    if (error) {
      const detail = error.response?.data as { message?: string; metadata?: Record<string, string> } | undefined;
      result.value = { success: false, message: detail?.message || error.message || "测试发送失败，请检查 SMTP 配置后重试。", metadata: detail?.metadata };
    } else {
      result.value = { success: true, message: "SMTP 服务器已接受测试邮件，请检查收件箱或垃圾邮件。若未收到，请查看邮件服务投递日志及 SPF / DKIM / DMARC 配置。" };
    }
  } catch (error: any) {
    result.value = { success: false, message: error?.message || "测试发送失败，请重试。" };
  } finally { sending.value = false; }
}
</script>

<template>
  <div v-auth="'settings:update'" class="mt-16px max-w-760px">
    <NButton :disabled="unsaved" @click="show = true; result = null">测试邮件发送</NButton>
    <p class="mt-6px text-13px text-gray-500">{{ unsaved ? '请先保存上方配置，再发送测试邮件。' : '使用已保存的配置发送测试邮件；失败时显示具体阶段、服务器错误和排查建议。' }}</p>
    <NModal v-model:show="show" preset="card" title="测试邮件发送"
      :style="{ width: 'min(600px, calc(100vw - 32px))' }"
      :closable="!sending" :mask-closable="!sending" :close-on-esc="!sending">
      <NForm label-placement="top" :disabled="sending" :aria-busy="sending">
        <NFormItem label="收件邮箱" required>
          <NInput v-model:value="form.recipient" placeholder="输入接收测试邮件的邮箱" :maxlength="254" :input-props="{ type: 'email', 'aria-label': '收件邮箱' }" @update:value="result = null" />
        </NFormItem>
        <NFormItem label="邮件标题" required>
          <NInput v-model:value="form.subject" :input-props="{ 'aria-label': '邮件标题' }" placeholder="输入邮件标题" @update:value="result = null" />
        </NFormItem>
        <NFormItem label="邮件内容" required>
          <NInput v-model:value="form.content" :input-props="{ 'aria-label': '邮件内容' }" type="textarea" :rows="5" placeholder="输入邮件内容（纯文本）" @update:value="result = null" />
        </NFormItem>
      </NForm>
      <NAlert v-if="result" :type="result.success ? 'success' : 'error'" :title="result.success ? '提交成功' : '发送失败'" role="status" class="mb-16px">
        <div class="break-words" style="overflow-wrap: anywhere">
          <p v-if="result.metadata?.stage_label" class="mb-8px font-600">失败阶段：{{ result.metadata.stage_label }}</p>
          <p v-if="result.metadata?.host" class="mb-8px text-13px">
            {{ result.metadata.host }}:{{ result.metadata.port }} · {{ connectionModes[result.metadata.security] || result.metadata.security }}
            · 认证：{{ authModes[result.metadata.authentication] || result.metadata.authentication }}
          </p>
          <p v-if="result.metadata?.smtp_code && result.metadata.smtp_code !== '0'" class="mb-8px">SMTP 状态码：{{ result.metadata.smtp_code }}</p>
          <p>{{ result.message }}</p>
          <p v-if="result.metadata?.hint" class="mt-12px"><b>排查建议：</b>{{ result.metadata.hint }}</p>
        </div>
      </NAlert>
      <div class="flex justify-end gap-8px">
        <NButton :disabled="sending" @click="show = false">关闭</NButton>
        <NButton type="primary" :loading="sending" :disabled="unsaved || !valid" @click="send">发送测试邮件</NButton>
      </div>
    </NModal>
  </div>
</template>
