<template>
  <div class="cart-page">
    <div v-if="!loaded" class="card" style="text-align: center;"><span class="muted">加载中…</span></div>

    <template v-else>
      <!-- 空购物车 -->
      <div v-if="!items.length" class="cart-empty">
        <div class="cart-empty-icon">🛒</div>
        <div class="cart-empty-text">购物车还是空的</div>
        <router-link class="btn btn-primary" to="/products">去逛逛</router-link>
      </div>

      <template v-else>
        <!-- 全选栏 -->
        <div class="cart-toolbar">
          <label class="cart-check">
            <input type="checkbox" :checked="allSelected" @change="toggleAll" />
            <span>全选</span>
            <span class="muted">（有效 {{ validItems.length }} 项）</span>
          </label>
          <span class="muted">失效/缺货商品不可结算</span>
        </div>

        <!-- 商品列表 -->
        <div v-for="it in items" :key="it.id" class="cart-item" :class="{ invalid: !it.valid || it.stock === 0 }">
          <input type="checkbox" :disabled="!it.valid || it.stock === 0" v-model="selected" :value="it.id" class="cart-item-check" />
          <router-link v-if="it.valid" :to="`/product/${it.product_id}`" class="cart-item-cover">
            <img v-if="it.product_cover" :src="it.product_cover" :alt="it.product_name" @error="onImgError" />
            <img v-else :src="NO_IMAGE" :alt="it.product_name" style="object-fit: contain;" />
          </router-link>
          <div v-else class="cart-item-cover">
            <img :src="NO_IMAGE" :alt="it.product_name" style="object-fit: contain;" />
          </div>
          <div class="cart-item-info">
            <router-link v-if="it.valid" :to="`/product/${it.product_id}`" class="cart-item-name">{{ it.product_name }}</router-link>
            <span v-else class="cart-item-name">{{ it.product_name }}</span>
            <div class="cart-item-badges">
              <span v-if="!it.valid" class="badge red">已失效</span>
              <span v-else-if="it.stock === 0" class="badge orange">缺货</span>
              <span v-if="it.points_only" class="tag">积分商品</span>
            </div>
            <div class="cart-item-sku muted" v-if="it.sku_id">SKU #{{ it.sku_id }}</div>
          </div>
          <div class="cart-item-price">
            <div class="cart-price">{{ formatMoney(it.price_cents) }}</div>
            <div class="muted">单价</div>
          </div>
          <div class="cart-item-qty">
            <button class="cart-qty-btn" :disabled="it.stock === 0" @click="changeQty(it, it.quantity - 1)">−</button>
            <input class="cart-qty-input" type="number" min="1" max="99" v-model.number="it.quantity"
                   :disabled="it.stock === 0" @change="changeQty(it, it.quantity)" />
            <button class="cart-qty-btn" :disabled="it.stock === 0" @click="changeQty(it, it.quantity + 1)">＋</button>
          </div>
          <div class="cart-item-subtotal">
            <div class="cart-subtotal">{{ formatMoney(it.price_cents * it.quantity) }}</div>
            <div class="muted">小计</div>
          </div>
          <button class="cart-item-del" @click="remove(it.id)" title="删除">✕</button>
        </div>

        <!-- 结算栏（sticky） -->
        <div class="cart-checkout">
          <div class="cart-total">
            <div class="muted">已选 <b>{{ selectedItems.length }}</b> 项</div>
            <div class="cart-total-row">
              <span class="muted">合计：</span>
              <span class="cart-total-price">{{ formatMoney(rawTotal) }}</span>
            </div>
            <div class="muted">券/会员折扣在下单时结算</div>
          </div>
          <div class="cart-checkout-fields">
            <input v-model="queryPwd" type="text" class="input" :placeholder="trade.queryPasswordRequired ? '查询密码 *（取货用，≥4 位）' : '查询密码（取货用，≥4 位）'" style="max-width: 170px;" />
            <input v-if="isGuestCart && trade.contactRequired !== 'none'" v-model="contact" type="text" class="input" :placeholder="`联系方式 *（${contactRequiredLabel(trade.contactRequired)}）`" style="max-width: 170px;" />
            <input v-model="couponCode" type="text" class="input" placeholder="优惠券码（选填）" style="max-width: 150px;" />
            <template v-if="isGuestCart && captchaCfg.order">
              <CaptchaInput ref="captchaRef" @update:code="captchaCode = $event" @update:captcha-id="captchaId = $event" />
            </template>
            <button class="btn btn-primary cart-checkout-btn" :disabled="!selectedItems.length || checkingOut" @click="checkout">
              {{ checkingOut ? '结算中…' : `去结算（${selectedItems.length}）` }}
            </button>
          </div>
        </div>

        <!-- 必填控件收集 -->
        <div v-if="controlsNeeded.length && showControls" class="cart-controls">
          <h3 class="cart-controls-title">补充信息（必填）</h3>
          <div v-for="g in controlsNeeded" :key="g.productId" class="cart-controls-group">
            <div class="cart-controls-name">{{ g.name }}</div>
            <div v-for="c in g.controls" :key="c.id" class="field">
              <label>{{ c.name }} *</label>
              <input v-if="c.type === 'text' || c.type === 'number' || c.type === 'password'" class="input"
                     :type="c.type" v-model="controlAnswers[String(c.id)]" />
              <select v-else-if="c.type === 'select'" class="input" v-model="controlAnswers[String(c.id)]">
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
          </div>
          <button class="btn btn-primary" style="margin-top: 8px;" :disabled="!controlsComplete || checkingOut" @click="doCheckout">
            {{ checkingOut ? '提交中…' : '确认并下单' }}
          </button>
        </div>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useRouter } from 'vue-router';
import { createOrder, getProduct, updateCart, rememberOrderPassword, fetchTradeConfig, contactRequiredLabel, contactValid, type CartItem, type ProductControl, type TradeConfig } from '@/api';
import { formatMoney } from '@/api/client';
import { loadCart, updateGuestQty, removeCartItem, clearPurchased } from '@/cart';
import { NO_IMAGE, onImgError } from '@/no-image';
import { getRefCode } from '@/ref';
import { fetchCaptchaConfig, type CaptchaConfig } from '@/api';
import CaptchaInput from '@/components/CaptchaInput.vue';

const router = useRouter();
const items = ref<CartItem[]>([]);
const selected = ref<number[]>([]);
const loaded = ref(false);
const isGuestCart = ref(false);
const couponCode = ref('');
const contact = ref('');
const trade = ref<TradeConfig>({ queryPasswordRequired: true, contactRequired: 'any' });
const captchaCfg = ref<CaptchaConfig>({ login: false, register: true, order: false, reset: true });
const captchaId = ref('');
const captchaCode = ref('');
const captchaRef = ref<InstanceType<typeof CaptchaInput> | null>(null);
const checkingOut = ref(false);
const showControls = ref(false);
const controlAnswers = ref<Record<string, string>>({});
const controlsNeeded = ref<{ productId: number; name: string; controls: ProductControl[] }[]>([]);

// 有效项：登录购物车按库存过滤；游客本地购物车（库存未知）仅按 valid 判定，缺货由后端下单时校验
const validItems = computed(() =>
  isGuestCart.value
    ? items.value.filter((i) => i.valid)
    : items.value.filter((i) => i.valid && i.stock !== 0),
);
const allSelected = computed(() => validItems.value.length > 0 && selected.value.length === validItems.value.length);
const selectedItems = computed(() => items.value.filter((i) => selected.value.includes(i.id)));
const rawTotal = computed(() => selectedItems.value.reduce((s, i) => s + i.price_cents * i.quantity, 0));
const controlsComplete = computed(() =>
  controlsNeeded.value.every((g) => g.controls.every((c) => (controlAnswers.value[String(c.id)] || '').trim() !== ''))
);

// checkbox 多选：逗号拼接存储（与详情页一致，后端按逗号解析）
function toggleCheck(id: number, val: string) {
  const key = String(id);
  const cur = (controlAnswers.value[key] || '').split(',').filter(Boolean);
  const idx = cur.indexOf(val);
  if (idx >= 0) cur.splice(idx, 1);
  else cur.push(val);
  controlAnswers.value[key] = cur.join(',');
}

onMounted(async () => {
  await load();
  const [tc, cc] = await Promise.all([fetchTradeConfig(), fetchCaptchaConfig()]);
  trade.value = tc;
  captchaCfg.value = cc;
});

async function load() {
  const { items: list, error, isGuest } = await loadCart();
  items.value = list || [];
  isGuestCart.value = isGuest;
  selected.value = validItems.value.map((i) => i.id);
  loaded.value = true;
}

function toggleAll() {
  selected.value = allSelected.value ? [] : validItems.value.map((i) => i.id);
}

async function changeQty(it: CartItem, qty: number) {
  const q = Math.max(1, Math.min(99, qty || 1));
  if (isGuestCart.value) {
    // 游客本地购物车
    updateGuestQty(it.id, q);
    it.quantity = q;
    return;
  }
  const { error } = await updateCart(it.id, q);
  if (!error) it.quantity = q;
}

async function remove(id: number) {
  const { error } = await removeCartItem(id);
  if (!error) {
    items.value = items.value.filter((i) => i.id !== id);
    selected.value = selected.value.filter((s) => s !== id);
  }
}

// 结算：先收必填控件（有则展开表单），后下单
async function checkout() {
  // 交易设置校验（与后端同口径：查询密码强制 + 游客联系方式）
  if (trade.value.queryPasswordRequired && queryPwd.value.trim().length < 4) {
    alert('请设置查询密码（取货用，至少 4 位）');
    return;
  }
  if (isGuestCart.value && trade.value.contactRequired !== 'none') {
    if (!contact.value.trim()) {
      alert(`请填写联系方式（${contactRequiredLabel(trade.value.contactRequired)}），用于订单查询与售后`);
      return;
    }
    if (!contactValid(contact.value, trade.value.contactRequired)) {
      alert(`联系方式格式不符（需要${contactRequiredLabel(trade.value.contactRequired)}）`);
      return;
    }
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
    ref_code: getRefCode() || undefined,
    captcha_id: (isGuestCart.value && captchaCfg.value.order) ? captchaId.value : undefined,
    captcha_code: (isGuestCart.value && captchaCfg.value.order) ? captchaCode.value : undefined,
    contact: (isGuestCart.value && contact.value.trim()) || undefined,
    control_answers: Object.keys(controlAnswers.value).length ? controlAnswers.value : undefined
  });
  checkingOut.value = false;
  if (error || !data) {
    alert(error || '下单失败');
    return;
  }
  // 下单成功后移除已结算项（游客清本地 / 登录删后端）
  await clearPurchased(selectedItems.value.map((i) => i.id));
  rememberOrderPassword(data.order_no, queryPwd.value); // 支付成功自动取货用
  router.push(`/payment/${data.order_no}`);
}

// 查询密码（多商品合并单共用一个取货密码；结算栏输入）
const queryPwd = ref('');
</script>

<style scoped>
.cart-page { max-width: 860px; margin: 0 auto; display: flex; flex-direction: column; gap: 12px; }

/* ── 空态 ── */
.cart-empty {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 60px 20px; text-align: center;
}
.cart-empty-icon { font-size: 48px; opacity: 0.4; margin-bottom: 12px; }
.cart-empty-text { color: #6b7280; font-size: 15px; margin-bottom: 16px; }

/* ── 工具栏 ── */
.cart-toolbar {
  display: flex; align-items: center; justify-content: space-between;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 12px 16px;
}
.cart-check { display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; }

/* ── 商品行 ── */
.cart-item {
  display: flex; align-items: center; gap: 14px;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 14px 16px;
}
.cart-item.invalid { opacity: 0.55; }
.cart-item-check { width: 16px; height: 16px; flex-shrink: 0; }
.cart-item-cover {
  width: 56px; height: 56px; border-radius: 10px; overflow: hidden; flex-shrink: 0;
  background: linear-gradient(135deg, #eff6ff, #dbeafe);
  display: flex; align-items: center; justify-content: center;
  text-decoration: none;
}
.cart-item-cover img { width: 100%; height: 100%; object-fit: cover; display: block; }
.cart-item-ph { font-size: 20px; font-weight: 700; color: #93c5fd; }
.cart-item-info { flex: 1; min-width: 0; }
.cart-item-name {
  font-size: 14px; font-weight: 600; color: #111827; text-decoration: none;
  display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.cart-item-name:hover { color: #2563eb; }
.cart-item-badges { display: flex; gap: 6px; margin-top: 4px; }
.cart-item-sku { margin-top: 2px; }
.cart-item-price, .cart-item-subtotal { text-align: center; width: 76px; flex-shrink: 0; }
.cart-price { color: #ff5722; font-weight: 700; font-size: 14px; }
.cart-subtotal { font-weight: 700; font-size: 14px; color: #111827; }

.cart-item-qty { display: inline-flex; border: 1px solid #d1d5db; border-radius: 8px; overflow: hidden; flex-shrink: 0; }
.cart-qty-btn {
  width: 30px; height: 32px; border: none; background: #f8fafc; cursor: pointer;
  font-size: 14px; color: #374151;
}
.cart-qty-btn:hover:not(:disabled) { background: #eff6ff; color: #2563eb; }
.cart-qty-input {
  width: 48px; height: 32px; border: none; border-left: 1px solid #e5e7eb; border-right: 1px solid #e5e7eb;
  text-align: center; font-size: 13px; outline: none;
}

.cart-item-del {
  width: 28px; height: 28px; border: none; background: none; cursor: pointer; flex-shrink: 0;
  border-radius: 999px; color: #9ca3af; font-size: 13px; transition: all 0.15s;
}
.cart-item-del:hover { background: #fee2e2; color: #ef4444; }

/* ── 结算栏 ── */
.cart-checkout {
  display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  padding: 14px 18px;
  position: sticky; bottom: 12px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08);
}
.cart-total-row { display: flex; align-items: baseline; gap: 6px; margin-top: 4px; }
.cart-total-price { color: #ff5722; font-size: 22px; font-weight: 800; }
.cart-checkout-fields { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.cart-checkout-btn { padding: 10px 22px; font-weight: 700; }

/* ── 控件收集 ── */
.cart-controls {
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 18px;
}
.cart-controls-title { font-size: 15px; font-weight: 700; margin-bottom: 12px; }
.cart-controls-group { border-bottom: 1px solid #f3f4f6; padding: 10px 0; }
.cart-controls-group:last-child { border-bottom: none; }
.cart-controls-name { font-weight: 600; font-size: 13px; margin-bottom: 6px; }

/* ── 移动端：商品行两行化（第一行 勾选+封面+名称，第二行 单价+数量+小计+删除）── */
@media (max-width: 768px) {
  .cart-item { flex-wrap: wrap; row-gap: 10px; gap: 10px; padding: 12px; }
  /* 第一行占满剩余宽，把价格组挤到第二行 */
  .cart-item-info { flex: 1 1 calc(100% - 16px - 56px - 40px); }
  .cart-item-price { order: 1; width: auto; text-align: left; }
  .cart-item-qty { order: 2; margin-left: auto; }
  .cart-item-subtotal { order: 3; width: auto; text-align: right; }
  .cart-item-del { order: 4; }
  /* 结算栏：合计一行、输入与按钮整行铺满（sticky 条不再挤压） */
  .cart-checkout { padding: 12px; }
  .cart-checkout-fields { width: 100%; }
  .cart-checkout-fields .input { max-width: none !important; flex: 1 1 150px; min-width: 0; }
  .cart-checkout-btn { flex: 1 1 100%; }
  .cart-controls { padding: 14px; }
}
</style>
