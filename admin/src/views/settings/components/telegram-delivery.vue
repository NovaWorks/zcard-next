<script setup lang="ts">
import { ref } from 'vue';
import { NAlert, NButton, NCard, NEmpty, NSpace, NTag, NPagination } from 'naive-ui';
import { request } from '@/service/request';
import { checkAuth } from '@/directives';

defineProps<{ unsaved: boolean }>();
interface DeliveryLog { id: number; event_type: string; recipient: string; subject: string; status: string; error_message?: string; attempts?: number; next_attempt_at?: number; message_id?: string; retryable?: boolean }
const logs = ref<DeliveryLog[]>([]);
const loading = ref(false);
const testing = ref(false);
const retrying = ref<number | null>(null);
const total = ref(0);
const page = ref(1);
const loaded = ref(false);
async function load() {
  loading.value = true;
  try {
    const { data, error } = await request<{logs?: DeliveryLog[]; total?: number}>({ url: '/api/v1/admin/notify/logs', params: { page: page.value, page_size: 10, channel: 'telegram' } });
    if (!error && data) { logs.value = data.logs || []; total.value = Number(data.total || 0); loaded.value = true; }
  } finally { loading.value = false; }
}
async function test() {
  testing.value = true;
  try {
    const { error } = await request({ url: '/api/v1/admin/notify/telegram/test', method: 'post', data: {} });
    if (!error) { window.$message?.success('测试消息已排队，请稍后刷新发送记录'); if (checkAuth('notify:read')) await load(); }
  } finally { testing.value = false; }
}
async function retry(id: number) {
  retrying.value = id;
  try {
    const { error } = await request({ url: `/api/v1/admin/notify/logs/${id}/resend`, method: 'post', data: {} });
    if (!error) { window.$message?.success('已加入重试队列'); await load(); }
  } finally { retrying.value = null; }
}
const labels: Record<string,string> = { pending:'待发送 / 重试中', sent:'已发送', failed:'发送失败', skipped:'已停止' };
</script>
<template>
  <NCard title="Telegram 订单通知" class="mt-24px" size="small">
    <NAlert type="info" :show-icon="false">
      通知发送给商家个人或管理群，仅包含主站订单。机器人需已加入群，或由接收人先向机器人发送 /start。
      默认只通知付款成功；人工服务订单会提醒处理。先保存上方配置，再测试发送。
    </NAlert>
    <div class="telegram-preview">
      <b>消息示例 · 付款成功</b><br />订单号：示例订单<br />支付金额：CNY 10.00<br />支付渠道：余额支付<br />商品 / 规格 × 数量<br />人工服务：请在后台查看并处理<br />查看后台订单（需要登录）
    </div>
    <NSpace class="mt-12px">
      <NButton v-if="checkAuth('notify:write')" :loading="testing" :disabled="unsaved" @click="test">发送测试消息</NButton>
      <NButton v-if="checkAuth('notify:read')" :loading="loading" @click="load">刷新发送记录</NButton>
      <span v-if="unsaved">请先保存更改</span>
    </NSpace>
    <NEmpty v-if="loaded && !logs.length" class="mt-16px" description="暂无 Telegram 发送记录" />
    <article v-for="log in logs" :key="log.id" class="telegram-log">
      <NSpace align="center"><b>{{ log.subject }}</b><NTag size="small" :type="log.status === 'sent' ? 'success' : log.status === 'failed' ? 'error' : 'default'">{{ labels[log.status] || log.status }}</NTag></NSpace>
      <div>接收人：{{ log.recipient }} · 尝试 {{ log.attempts || 0 }} 次<span v-if="log.message_id"> · 消息 ID {{ log.message_id }}</span></div>
      <div v-if="log.next_attempt_at">下次尝试：{{ new Date(Number(log.next_attempt_at) * 1000).toLocaleString() }}</div>
      <div v-if="log.error_message" role="status">{{ log.error_message }}</div>
      <NButton v-if="log.retryable && checkAuth('notify:write')" size="small" :loading="retrying === log.id" :disabled="retrying !== null" @click="retry(log.id)">重试此接收人</NButton>
    </article>
    <NPagination v-if="total > 10" v-model:page="page" :item-count="total" :page-size="10" @update:page="load" />
  </NCard>
</template>
<style scoped>
.telegram-preview { margin-top: 12px; padding: 12px; border: 1px solid var(--n-border-color); border-radius: 6px; line-height: 1.8; }
.telegram-log { padding: 16px 0; border-bottom: 1px solid var(--n-border-color); overflow-wrap: anywhere; line-height: 1.8; }
</style>
