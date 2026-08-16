<template>
  <div class="card" style="max-width: 560px;">
    <h2 style="margin-bottom: 12px;">卡密取货</h2>
    <div class="field">
      <label>订单号</label>
      <input v-model="orderNo" type="text" class="input" placeholder="下单返回的订单号" />
    </div>
    <div class="field">
      <label>查询密码</label>
      <input v-model="queryPassword" type="text" class="input" placeholder="下单时设置的查询密码" />
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <button class="btn" :disabled="loading" @click="fetch">{{ loading ? '查询中…' : '取货' }}</button>

    <div v-if="result" style="margin-top: 16px;">
      <div class="success" style="margin-bottom: 8px;">订单状态：{{ result.status }} · 已取 {{ result.fetch_count }} 次</div>
      <div v-for="it in result.items" :key="it.item_id" class="card" style="margin-bottom: 8px; background: #f9fafb;">
        <div v-if="it.masked" class="muted">内容已掩码（仅首查可见）：{{ it.content }}</div>
        <div v-else style="font-family: monospace; word-break: break-all;">{{ it.content }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { fetchDelivery, type FetchDeliveryReply } from '@/api';

const orderNo = ref('');
const queryPassword = ref('');
const loading = ref(false);
const error = ref('');
const result = ref<FetchDeliveryReply | null>(null);

async function fetch() {
  if (!orderNo.value || !queryPassword.value) {
    error.value = '请填写订单号与查询密码';
    return;
  }
  loading.value = true;
  error.value = '';
  result.value = null;
  const { data, error: err } = await fetchDelivery(orderNo.value, queryPassword.value);
  loading.value = false;
  if (err) { error.value = err; return; }
  result.value = data;
}
</script>
