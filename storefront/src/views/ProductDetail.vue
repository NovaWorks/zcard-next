<template>
  <div v-if="p" class="card">
    <h2 style="margin-bottom: 8px;">{{ p.name }}</h2>
    <div class="price" style="font-size: 22px; margin-bottom: 12px;">{{ formatMoney(p.price_cents) }}</div>
    <div style="margin-bottom: 12px;">类型：{{ stockTypeLabel(p.stock_type) }} · 销量 {{ p.sales_count }}</div>
    <div style="margin-bottom: 16px; white-space: pre-wrap;">{{ p.description || '暂无描述' }}</div>

    <div class="field">
      <label>购买数量</label>
      <input v-model.number="quantity" type="number" min="1" class="input" style="max-width: 120px;" />
    </div>

    <!-- 多规格 SKU（可选；下单按 SKU 价） -->
    <div v-if="p.skus?.length" class="field">
      <label>规格</label>
      <select v-model.number="selectedSku" class="input" style="max-width: 320px;">
        <option :value="0">默认规格</option>
        <option v-for="s in p.skus" :key="s.id" :value="s.id">
          {{ s.name }}（{{ formatMoney(s.price_cents) }}）
        </option>
      </select>
    </div>

    <!-- 自定义控件（下单收集） -->
    <div v-for="c in p.controls" :key="c.id" class="field">
      <label>{{ c.name }}{{ c.required ? ' *' : '' }}</label>
      <input
        v-if="c.type === 'text' || c.type === 'number'"
        v-model="controlAnswers[String(c.id)]"
        :type="c.type === 'number' ? 'number' : 'text'"
        class="input"
      />
      <input
        v-else-if="c.type === 'password'"
        v-model="controlAnswers[String(c.id)]"
        type="password"
        class="input"
      />
      <select v-else-if="c.type === 'select'" v-model="controlAnswers[String(c.id)]" class="input">
        <option value="">请选择</option>
        <option v-for="o in c.options" :key="o" :value="o">{{ o }}</option>
      </select>
      <div v-else-if="c.type === 'radio'" style="display: flex; gap: 12px; flex-wrap: wrap;">
        <label v-for="o in c.options" :key="o" style="display: flex; gap: 4px; align-items: center;">
          <input type="radio" :name="`ctrl-${c.id}`" :value="o" v-model="controlAnswers[String(c.id)]" /> {{ o }}
        </label>
      </div>
      <div v-else-if="c.type === 'checkbox'" style="display: flex; gap: 12px; flex-wrap: wrap;">
        <label v-for="o in c.options" :key="o" style="display: flex; gap: 4px; align-items: center;">
          <input type="checkbox" :value="o" @change="toggleCheck(c.id, o)" /> {{ o }}
        </label>
      </div>
    </div>

    <div class="field">
      <label>查询密码（取货用，至少 4 位）</label>
      <input v-model="queryPassword" type="text" class="input" placeholder="用于取货验证" style="max-width: 320px;" />
    </div>
    <div class="field">
      <label>联系方式（选填）</label>
      <input v-model="contact" type="text" class="input" style="max-width: 320px;" />
    </div>
    <div class="field">
      <label>优惠券码（选填）</label>
      <input v-model="couponCode" type="text" class="input" style="max-width: 320px;" />
    </div>
    <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>
    <button class="btn" :disabled="submitting" @click="buy">{{ submitting ? '提交中…' : '立即购买' }}</button>

    <!-- 评价（真实 approved + 虚拟合并） -->
    <div v-if="p.reviews?.length" style="margin-top: 24px;">
      <h3 style="margin-bottom: 8px;">用户评价（{{ p.reviews.length }}）</h3>
      <div v-for="r in p.reviews" :key="`${r.is_virtual}-${r.id}`" class="field" style="border-bottom: 1px solid #eee; padding-bottom: 8px;">
        <div style="display: flex; gap: 8px; align-items: center;">
          <strong>{{ r.nickname || '匿名用户' }}</strong>
          <span class="muted">{{ '★'.repeat(Math.min(5, r.rating)) }}</span>
          <span v-if="r.is_virtual" class="muted">（虚拟）</span>
        </div>
        <div style="white-space: pre-wrap;">{{ r.content }}</div>
      </div>
    </div>
  </div>
  <div v-else-if="error" class="error">{{ error }}</div>
  <div v-else class="muted">加载中…</div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getProduct, createOrder, type Product } from '@/api';
import { formatMoney } from '@/api/client';

const route = useRoute();
const router = useRouter();
const p = ref<Product | null>(null);
const quantity = ref(1);
const selectedSku = ref(0);
const queryPassword = ref('');
const contact = ref('');
const couponCode = ref('');
const controlAnswers = ref<Record<string, string>>({});
const submitting = ref(false);
const error = ref('');

function stockTypeLabel(t: string) {
  return ({ card: '卡密', url: '链接', code: '兑换码' } as Record<string, string>)[t] || t;
}

function toggleCheck(id: number, val: string) {
  const key = String(id);
  const cur = (controlAnswers.value[key] || '').split(',').filter(Boolean);
  const idx = cur.indexOf(val);
  if (idx >= 0) cur.splice(idx, 1);
  else cur.push(val);
  controlAnswers.value[key] = cur.join(',');
}

onMounted(async () => {
  const id = Number(route.params.id);
  const { data, error: err } = await getProduct(id);
  if (err) { error.value = err; return; }
  p.value = data;
});

async function buy() {
  if (!p.value) return;
  // 必填控件校验
  for (const c of p.value.controls) {
    if (c.required && !controlAnswers.value[String(c.id)]?.trim()) {
      error.value = `请填写「${c.name}」`;
      return;
    }
  }
  submitting.value = true;
  error.value = '';
  const { data, error: err } = await createOrder({
    items: [{ product_id: p.value.id, sku_id: selectedSku.value || undefined, quantity: quantity.value }],
    query_password: queryPassword.value || undefined,
    contact: contact.value || undefined,
    coupon_code: couponCode.value || undefined,
    control_answers: Object.keys(controlAnswers.value).length ? controlAnswers.value : undefined
  });
  submitting.value = false;
  if (err) { error.value = err; return; }
  router.push(`/payment/${data!.order_no}`);
}
</script>
