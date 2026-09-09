<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue';
import { useRoute } from 'vue-router';
import { api } from '@/api/client';
interface ReviewState { enabled: boolean; status: string; message: string; products?: { product_id: number; name: string }[]; rating?: number; content?: string; }
const props = defineProps<{ orderNo: string }>();
const route = useRoute();
const state = ref<ReviewState | null>(null);
const error = ref('');
const loading = ref(false);
const saving = ref(false);
const product = ref<number | string>('');
const rating = ref(5);
const content = ref('');
const section = ref<HTMLElement | null>(null);
const endpoint = `/orders/${encodeURIComponent(props.orderNo)}/review`;
async function load() {
  loading.value = true;
  const result = await api.get<ReviewState>(endpoint);
  state.value = result.data; error.value = result.error || '';
  product.value = result.data?.products?.[0]?.product_id || '';
  loading.value = false;
  if (route.hash === '#order-review') { await nextTick(); section.value?.scrollIntoView({ block: 'center' }); }
}
async function submit() {
  if (saving.value) return;
  saving.value = true; error.value = '';
  const result = await api.post<ReviewState>(endpoint, { product_id: product.value, rating: rating.value, content: content.value.trim() });
  saving.value = false;
  if (result.error) { error.value = result.error; return; }
  state.value = result.data;
}
onMounted(load);
</script>
<template>
  <section v-if="loading || error || state?.enabled" id="order-review" ref="section" class="card order-review">
    <h3>订单评价</h3>
    <p v-if="loading" class="muted">正在读取评价…</p>
    <p v-if="error" role="alert" class="error">{{ error }} <button v-if="!state" class="btn secondary" @click="load">重试</button></p>
    <template v-if="state?.enabled">
      <p class="muted" role="status">{{ state.message }}</p>
      <form v-if="state.status === 'available'" @submit.prevent="submit">
        <label for="review-product">评价商品</label>
        <select id="review-product" v-model="product" class="input" :disabled="saving" required><option v-for="item in state.products" :key="item.product_id" :value="item.product_id">{{ item.name }}</option></select>
        <fieldset :disabled="saving"><legend>评分</legend><label v-for="star in 5" :key="star" class="review-star"><input v-model="rating" type="radio" :value="star" name="review-rating" />{{ star }} 星</label></fieldset>
        <label for="review-content">使用体验</label>
        <textarea id="review-content" v-model="content" class="input" maxlength="1000" rows="4" required :disabled="saving" placeholder="分享商品的使用体验，勿填写卡密或个人信息" />
        <div class="review-footer"><span class="muted">{{ content.length }}/1000</span><button class="btn" :disabled="saving || !content.trim()">{{ saving ? '正在提交…' : '提交评价' }}</button></div>
      </form>
      <div v-else-if="state.content"><p>{{ state.rating }} 星</p><p class="review-content">{{ state.content }}</p></div>
    </template>
  </section>
</template>
<style scoped>
h3 { margin:0 0 12px; font-size:16px; } p { line-height:1.7; }
form { display:grid; gap:10px; } label, legend { font-size:13px; } select,textarea { width:100%; box-sizing:border-box; } textarea { resize:vertical; min-height:110px; }
fieldset { border:0; padding:0; margin:4px 0; display:flex; flex-wrap:wrap; gap:8px; }
.review-star { display:flex; align-items:center; gap:4px; min-height:44px; padding:0 8px; cursor:pointer; }
.review-footer { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.review-content { white-space:pre-wrap; overflow-wrap:anywhere; }
</style>
