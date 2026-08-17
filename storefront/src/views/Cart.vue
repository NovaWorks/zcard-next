<template>
  <div>
    <div class="card" v-if="!loaded" style="text-align: center;"><span class="muted">加载中…</span></div>

    <template v-else>
      <div v-if="!items.length" class="card" style="text-align: center; padding: 40px;">
        <div style="margin-bottom: 12px;">购物车是空的</div>
        <router-link class="btn" to="/">去逛逛</router-link>
      </div>

      <template v-else>
        <div class="card" style="margin-bottom: 12px;">
          <label style="display: flex; align-items: center; gap: 8px;">
            <input type="checkbox" :checked="allSelected" @change="toggleAll" />
            <b>全选</b>
            <span class="muted">（有效 {{ validItems.length }} 项）</span>
          </label>
        </div>

        <div v-for="it in items" :key="it.id" class="card" style="margin-bottom: 8px; display: flex; gap: 12px; align-items: center; opacity: 0.55;">
          <input type="checkbox" :disabled="!it.valid || it.stock === 0" v-model="selected" :value="it.id" style="flex-shrink: 0;" />
          <div style="flex: 1; min-width: 0;">
            <div style="display: flex; gap: 8px; align-items: center; flex-wrap: wrap;">
              <router-link v-if="it.valid" :to="`/product/${it.product_id}`" style="font-weight: 600; color: inherit;">{{ it.product_name }}</router-link>
              <span v-else style="font-weight: 600;">{{ it.product_name }}</span>
              <span v-if="!it.valid" class="badge red">已失效</span>
              <span v-else-if="it.stock === 0" class="badge orange">缺货</span>
              <span v-if="it.points_only" class="tag">积分商品</span>
            </div>
            <div class="muted" style="margin-top: 4px;">
              单价 {{ formatMoney(it.price_cents) }}
              <template v-if="it.stock >= 0"> · 库存 {{ it.stock }}</template>
            </div>
          </div>
          <div style="display: flex; align-items: center; gap: 6px; flex-shrink: 0;">
            <button class="btn secondary" style="padding: 4px 10px;" @click="changeQty(it, it.quantity - 1)">−</button>
            <input class="input" type="number" min="1" max="99" v-model.number="it.quantity"
                   style="width: 64px; text-align: center;" @change="changeQty(it, it.quantity)" />
            <button class="btn secondary" style="padding: 4px 10px;" @click="changeQty(it, it.quantity + 1)">＋</button>
          </div>
          <div class="price" style="width: 90px; text-align: right; flex-shrink: 0;">{{ formatMoney(it.price_cents * it.quantity) }}</div>
          <button class="btn secondary" style="flex-shrink: 0;" @click="remove(it.id)">删除</button>
        </div>

        <!-- 结算栏 -->
        <div class="card" style="position: sticky; bottom: 12px; display: flex; gap: 16px; align-items: center; flex-wrap: wrap;">
          <div style="flex: 1; min-width: 200px;">
            <div>已选 <b>{{ selectedItems.length }}</b> 项 · 合计 <span class="price" style="font-size: 18px;">{{ formatMoney(rawTotal) }}</span></div>
            <div class="muted">券/会员折扣在下单时结算（多商品一单支付）</div>
          </div>
          <input v-model="queryPwd" type="text" class="input" placeholder="查询密码（取货用，≥4 位）" style="max-width: 170px;" />
          <input v-model="couponCode" type="text" class="input" placeholder="优惠券码（选填）" style="max-width: 150px;" />
          <button class="btn" :disabled="!selectedItems.length || checkingOut" @click="checkout">
            {{ checkingOut ? '结算中…' : '去结算' }}
          </button>
        </div>

        <!-- 必填控件收集（含必填控件的多商品：逐商品收答案） -->
        <div v-if="controlsNeeded.length && showControls" class="card" style="margin-top: 12px;">
          <h3 style="margin-bottom: 8px;">补充信息（必填）</h3>
          <div v-for="g in controlsNeeded" :key="g.productId" style="border-bottom: 1px solid #eee; padding: 8px 0;">
            <div style="font-weight: 600; margin-bottom: 6px;">{{ g.name }}</div>
            <div v-for="c in g.controls" :key="c.id" class="field">
              <label>{{ c.name }} *</label>
              <input v-if="c.type === 'text' || c.type === 'number' || c.type === 'password'" class="input"
                     :type="c.type" v-model="controlAnswers[String(c.id)]" />
              <select v-else-if="c.type === 'select'" class="input" v-model="controlAnswers[String(c.id)]">
                <option value="">请选择</option>
                <option v-for="o in c.options" :key="o" :value="o">{{ o }}</option>
              </select>
            </div>
          </div>
          <button class="btn" style="margin-top: 8px;" :disabled="!controlsComplete || checkingOut" @click="doCheckout">
            {{ checkingOut ? '提交中…' : '确认并下单' }}
          </button>
        </div>
      </template>
    </template>

    <!-- 游客提示 -->
    <div v-if="guestHint" class="card" style="text-align: center; padding: 40px;">
      <div style="margin-bottom: 12px;">购物车需要登录后使用</div>
      <router-link class="btn" :to="{ path: '/login', query: { redirect: '/cart' } }">去登录</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useRouter } from 'vue-router';
import { listCart, updateCart, removeCart, createOrder, getProduct, type CartItem, type ProductControl } from '@/api';
import { formatMoney } from '@/api/client';
import { getToken } from '@/api/client';


const router = useRouter();
const items = ref<CartItem[]>([]);
const selected = ref<number[]>([]);
const loaded = ref(false);
const guestHint = ref(false);
const couponCode = ref('');
const checkingOut = ref(false);
const showControls = ref(false);
const controlAnswers = ref<Record<string, string>>({});
const controlsNeeded = ref<{ productId: number; name: string; controls: ProductControl[] }[]>([]);

const validItems = computed(() => items.value.filter((i) => i.valid && i.stock !== 0));
const allSelected = computed(() => validItems.value.length > 0 && selected.value.length === validItems.value.length);
const selectedItems = computed(() => items.value.filter((i) => selected.value.includes(i.id)));
const rawTotal = computed(() => selectedItems.value.reduce((s, i) => s + i.price_cents * i.quantity, 0));
const controlsComplete = computed(() =>
  controlsNeeded.value.every((g) => g.controls.every((c) => (controlAnswers.value[String(c.id)] || '').trim() !== ''))
);

onMounted(async () => {
  if (!getToken()) {
    guestHint.value = true;
    loaded.value = true;
    return;
  }
  await load();
});

async function load() {
  const { data, error } = await listCart();
  if (error === 'HTTP 401') { guestHint.value = true; loaded.value = true; return; }
  items.value = data?.items || [];
  selected.value = items.value.filter((i) => i.valid && i.stock !== 0).map((i) => i.id);
  loaded.value = true;
}

function toggleAll() {
  selected.value = allSelected.value ? [] : validItems.value.map((i) => i.id);
}

async function changeQty(it: CartItem, qty: number) {
  const q = Math.max(1, Math.min(99, qty || 1));
  const { error } = await updateCart(it.id, q);
  if (!error) it.quantity = q;
}

async function remove(id: number) {
  const { error } = await removeCart(id);
  if (!error) {
    items.value = items.value.filter((i) => i.id !== id);
    selected.value = selected.value.filter((s) => s !== id);
  }
}

// 结算：先收必填控件（有则展开表单），后下单
async function checkout() {
  if (!queryPwd.value || queryPwd.value.length < 4) {
    alert('请先设置查询密码（取货用，至少 4 位）');
    return;
  }
  showControls.value = false;
  controlsNeeded.value = [];
  controlAnswers.value = {};
  const needed: typeof controlsNeeded.value = [];
  for (const it of selectedItems.value) {
    const { data: p } = await getProduct(it.product_id);
    const req = (p?.controls || []).filter((c) => c.required);
    if (req.length) needed.push({ productId: it.product_id, name: p?.name || `商品 ${it.product_id}`, controls: req });
  }
  if (needed.length) {
    controlsNeeded.value = needed;
    showControls.value = true;
    return;
  }
  await doCheckout();
}

async function doCheckout() {
  checkingOut.value = true;
  const { data, error } = await createOrder({
    items: selectedItems.value.map((i) => ({ product_id: i.product_id, sku_id: i.sku_id || undefined, quantity: i.quantity })),
    coupon_code: couponCode.value || undefined,
    query_password: queryPwd.value,
    control_answers: Object.keys(controlAnswers.value).length ? controlAnswers.value : undefined
  });
  checkingOut.value = false;
  if (error || !data) {
    alert(error || '下单失败');
    return;
  }
  // 下单成功后移除已结算项（勾选的全部）
  for (const it of selectedItems.value) await removeCart(it.id);
  router.push(`/payment/${data.order_no}`);
}

// 查询密码（多商品合并单共用一个取货密码；结算栏输入）
const queryPwd = ref('');
</script>
