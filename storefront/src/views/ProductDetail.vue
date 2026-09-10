<template>
  <div v-if="p" class="pd-page">
    <!-- 面包屑 -->
    <div class="pd-crumb">
      <router-link to="/">首页</router-link>
      <span class="pd-crumb-sep">/</span>
      <router-link to="/products">全部商品</router-link>
      <span class="pd-crumb-sep">/</span>
      <span class="pd-crumb-current">{{ p.name }}</span>
    </div>

    <div class="pd-main">
      <!-- 左栏：商品图 -->
      <div class="pd-gallery">
        <div class="pd-cover" :role="p.cover ? 'button' : undefined" :tabindex="p.cover ? 0 : undefined" aria-label="查看商品大图" @click="openCover" @keydown.enter="openCover" @keydown.space.prevent="openCover">
          <img v-if="p.cover" :src="p.cover" :alt="p.name" @error="onImgError" />
          <img v-else :src="NO_IMAGE" :alt="p.name" class="pd-noimg" />
          <span v-if="p.cover" class="pd-zoom-hint">🔍 点击查看大图</span>
        </div>
      </div>

      <!-- 右栏：购买区 -->
      <div class="pd-buy">
        <h1 class="pd-name">{{ p.name }}</h1>
        <div class="pd-sub">
          <span class="tag">{{ stockTypeLabel(p.stock_type) }}</span>
          <span v-if="p.points_required && p.points_required > 0" class="tag pd-points-tag">{{ p.points_required }} 积分</span>
        </div>

        <!-- 促销价格区 -->
        <div class="pd-price-card">
          <div class="pd-price-row">
            <span class="pd-price">{{ formatMoney(displayPrice) }}</span>
            <del v-if="selectedFlash && displayPrice < basePrice" class="muted">{{ formatMoney(basePrice) }}</del>
            <span v-if="p.points_required && p.points_required > 0" class="pd-price-points">或 {{ p.points_required }} 积分兑换</span>
          </div>
          <p v-if="selectedFlash" class="pd-flash-info">{{ flashSoldOut ? '本场秒杀已抢完' : '限时秒杀' }} · {{ new Date(selectedFlash.end_at * 1000).toLocaleString() }} 结束<span v-if="!flashSoldOut"> · 剩余 {{ selectedFlash.remaining }} 件<span v-if="selectedFlash.per_user_limit > 0"> · 每人限购 {{ selectedFlash.per_user_limit }} 件</span></span></p>
        </div>

        <!-- 汇总条：销量/库存（template.show_sales/show_stock 后台开关可关） -->
        <div v-if="showSales || showStock" class="pd-stats">
          <div v-if="showSales" class="pd-stat"><b>{{ p.sales_count || 0 }}</b><span>销量</span></div>
          <div v-if="showStock" class="pd-stat"><b>{{ stockDisplay }}</b><span>库存</span></div>
        </div>

        <!-- 服务保障 -->
        <div class="pd-assure">
          <span>⚡ 自动发货</span>
          <span>🛡️ 正品保障</span>
          <span>💬 售后无忧</span>
        </div>

        <!-- SKU 选择（卡片式） -->
        <div v-if="p.skus?.length" class="pd-field">
          <label class="pd-label">选择规格 <span class="pd-req">*</span></label>
          <div class="pd-skus">
            <button
              v-for="s in p.skus"
              :key="s.id"
              class="pd-sku"
              :class="{ active: selectedSku === Number(s.id) }"
              @click="selectedSku = Number(s.id)"
            >
              <span class="pd-sku-name">{{ s.name }}</span>
              <span class="pd-sku-price">{{ formatMoney(flash.price(s.price_cents, s.flash_sale)) }}</span>
            </button>
          </div>
        </div>

        <!-- 数量步进器 -->
        <div class="pd-field">
          <label class="pd-label">购买数量</label>
          <div class="pd-qty">
            <button class="pd-qty-btn" @click="quantity = Math.max(1, quantity - 1)">−</button>
            <input v-model.number="quantity" type="number" min="1" max="99" class="pd-qty-input" />
            <button class="pd-qty-btn" @click="quantity = Math.min(99, quantity + 1)">＋</button>
          </div>
        </div>

        <!-- 库存进度条 -->
        <div v-if="showStock && p.stock_visible && p.stock_type === 'card' && p.stock >= 0" class="pd-stock-bar">
          <div class="pd-stock-track"><div class="pd-stock-fill" :style="{ width: stockPct }"></div></div>
        </div>

        <!-- 评价入口（锚点；大厂电商：评分摘要卡 → 点击滚动到评价区） -->
        <button v-if="showReviews" class="pd-review-entry" @click="scrollToReviews">
          <span class="pd-review-entry-score">{{ p.reviews?.length ? `★ ${reviewAvg}` : '暂无评分' }}</span>
          <span class="pd-review-entry-count">{{ (p.reviews?.length || 0) }} 条评价</span>
          <span class="pd-review-entry-go">查看 ›</span>
        </button>

        <!-- 自定义控件（下单收集） -->
        <div v-for="c in p.controls" :key="c.id" class="pd-field">
          <label class="pd-label">{{ c.name }}{{ c.required ? ' <span class="pd-req">*</span>' : '' }}</label>
          <input
            v-if="c.type === 'text' || c.type === 'number'"
            v-model="controlAnswers[String(c.id)]"
            :type="c.type === 'number' ? 'number' : 'text'"
            class="pd-input"
          />
          <input
            v-else-if="c.type === 'password'"
            v-model="controlAnswers[String(c.id)]"
            type="password"
            class="pd-input"
          />
          <select v-else-if="c.type === 'select'" v-model="controlAnswers[String(c.id)]" class="pd-input">
            <option value="">请选择</option>
            <option v-for="o in c.options" :key="o" :value="o">{{ o }}</option>
          </select>
          <div v-else-if="c.type === 'radio'" class="pd-options">
            <label v-for="o in c.options" :key="o" class="pd-option">
              <input type="radio" :name="`ctrl-${c.id}`" :value="o" v-model="controlAnswers[String(c.id)]" /> {{ o }}
            </label>
          </div>
          <div v-else-if="c.type === 'checkbox'" class="pd-options">
            <label v-for="o in c.options" :key="o" class="pd-option">
              <input type="checkbox" :value="o" @change="toggleCheck(c.id, o)" /> {{ o }}
            </label>
          </div>
        </div>

        <div class="pd-field">
          <label class="pd-label">查询密码（取货用，至少 4 位）<span v-if="trade.queryPasswordRequired" class="pd-req">*</span></label>
          <input v-model="queryPassword" type="text" class="pd-input" placeholder="用于取货验证（忘记将无法取货）" />
        </div>
        <div v-if="isGuest || trade.contactScope === 'all'" class="pd-field">
          <label class="pd-label">联系方式 {{ trade.contactRequired !== 'none' ? ' *' : '（选填）' }}</label>
          <input v-model="contact" type="text" class="pd-input" :placeholder="`用于订单查询与售后（${contactRequiredLabel(trade.contactRequired)}）`" />
        </div>
        <div v-if="isGuest && captchaCfg.order" class="pd-field">
          <label class="pd-label">图形验证码 <span class="pd-req">*</span></label>
          <CaptchaInput ref="captchaRef" @update:code="captchaCode = $event" @update:captcha-id="captchaId = $event" />
        </div>
        <div class="pd-field">
          <label class="pd-label">优惠券码（选填）</label>
          <input v-model="couponCode" type="text" class="pd-input" placeholder="输入优惠券码" />
        </div>

        <div v-if="error" class="error" style="margin-bottom: 12px;">{{ error }}</div>

        <div v-if="stockUnknown" class="pd-stock-notice" role="status">
          <div>
            <strong>暂时无法确认库存</strong>
            <p>{{ stockRefreshMessage || '尚未获取到有效库存，这不代表已售罄。请稍后重新查询，或联系客服确认。' }}</p>
          </div>
          <button type="button" :disabled="stockRefreshing" @click="refreshStock">
            {{ stockRefreshing ? '查询中…' : '重新查询库存' }}
          </button>
        </div>

        <!-- 缺货和未知库存禁购；不限库存仍由后端按货源验证。 -->
        <div class="pd-actions">
          <button class="pd-btn-buy" :disabled="submitting || soldOut || stockUnknown || flashSoldOut" @click="buy">
            {{ submitting ? '提交中…' : stockUnknown ? '库存待确认' : soldOut ? '暂时缺货' : '立即购买' }}
          </button>
          <button
            v-if="canCart"
            class="pd-btn-cart"
            :class="{ 'is-in': inCartNow }"
            :disabled="submitting || cartBusy || soldOut || stockUnknown || flashSoldOut"
            :title="inCartNow ? '从购物车移除' : '加入购物车'"
            @click="inCartNow ? removeFromCart() : addToCart()"
          >
            <template v-if="cartBusy">{{ addingCart ? '加入中…' : '处理中…' }}</template>
            <template v-else-if="inCartNow"><ThemeIcon name="trash" />移除购物车</template>
            <template v-else><ThemeIcon name="cart" />加入购物车</template>
          </button>
          <button v-if="p.points_required && p.points_required > 0" class="pd-btn-points" :disabled="submitting || soldOut || stockUnknown || flashSoldOut" @click="exchangePoints">
            {{ stockUnknown ? '库存待确认' : soldOut ? '已兑完' : `积分兑换（${p.points_required} 分）` }}
          </button>
        </div>
      </div>
    </div>

    <!-- 描述区 -->
    <div v-if="p.description" class="pd-section pd-details">
      <h3 class="pd-section-title">商品详情</h3>
      <div ref="description" class="pd-desc" @click="openDescriptionImage" @keydown="descriptionKeydown" v-html="p.description"></div>
    </div>

    <!-- 评价区（template.show_reviews 开关控制；默认展示 3 条可展开） -->
    <div v-if="showReviews" id="pd-reviews" class="pd-section">
      <h3 class="pd-section-title">用户评价（{{ (p.reviews?.length || 0) }}）</h3>
      <p v-if="!p.reviews?.length" class="muted">暂无评价，欢迎购买后分享使用体验。</p>
      <p class="muted"><router-link to="/member?tab=orders">前往我的订单评价</router-link> · 发货完成后可提交，审核通过后展示。</p>
      <div v-for="r in visibleReviews" :key="`${r.is_virtual}-${r.id}`" class="pd-review">
        <span class="pd-avatar">{{ (r.nickname || '匿')[0] }}</span>
        <div class="pd-review-body">
          <div class="pd-review-head">
            <b>{{ r.nickname || '匿名用户' }}</b>
            <span class="pd-stars">{{ '★'.repeat(Math.min(5, r.rating)) }}</span>
          </div>
          <div class="pd-review-content">{{ r.content }}</div>
        </div>
      </div>
      <button v-if="(p.reviews?.length || 0) > reviewCollapsed" class="pd-review-more" @click="reviewsExpanded = !reviewsExpanded">
        {{ reviewsExpanded ? '收起评价 ↑' : `查看全部 ${(p.reviews?.length || 0)} 条评价 ↓` }}
      </button>
    </div>

    <ImageViewer v-if="previewImages.length" :images="previewImages" :initial-index="previewIndex" @close="previewImages = []" />
  </div>
  <div v-else-if="error" class="error">{{ error }}</div>
  <div v-else class="muted" style="text-align: center; padding: 40px;">加载中…</div>
</template>

<script setup lang="ts">
import { useFlashOffers } from '@/composables/flash-offers';
import ThemeIcon from '@/components/ThemeIcon.vue';
import { ref, computed, onMounted, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getProduct, createOrder, rememberOrderPassword, fetchTradeConfig, contactRequiredLabel, contactValid, type Product, type TradeConfig } from '@/api';
import { formatMoney, getToken } from '@/api/client';
import { NO_IMAGE, onImgError } from '@/no-image';
import { authState } from '@/auth';
import { addToCart as addToCartStore, removeCartItem, cartItemOf, cartEnabled } from '@/cart';
import { getRefCode } from '@/ref';
import { fetchCaptchaConfig, type CaptchaConfig } from '@/api';
import { fetchSiteSeo, applySeo, stripHtml, truncate, type SiteSeoConfig } from '@/seo';
import ImageViewer from '@/components/ImageViewer.vue';
import CaptchaInput from '@/components/CaptchaInput.vue';

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
const addingCart = ref(false);
const removingCart = ref(false);
const description = ref<HTMLElement | null>(null);
const previewImages = ref<{ src: string; alt: string }[]>([]);
const previewIndex = ref(0);
function openCover() {
  if (!p.value?.cover) return;
  previewIndex.value = 0;
  previewImages.value = [{ src: p.value.cover, alt: p.value.name }];
}
function openDescriptionImage(event: Event) {
  if (!(event.target instanceof HTMLImageElement)) return;
  const images = [...(description.value?.querySelectorAll('img') || [])].filter(img => img.currentSrc || img.src);
  const index = images.indexOf(event.target);
  if (index < 0) return;
  event.preventDefault(); // 图片包在链接内时，预览优先，避免跳离教程。
  event.target.focus({ preventScroll: true });
  previewIndex.value = index;
  previewImages.value = images.map(img => ({ src: img.currentSrc || img.src, alt: img.alt || p.value?.name || '商品详情图片' }));
}
function descriptionKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ' ') openDescriptionImage(event);
}
watch([description, () => p.value?.description], () => {
  description.value?.querySelectorAll('img').forEach(img => {
    img.tabIndex = 0;
    img.setAttribute('role', 'button');
    img.setAttribute('aria-label', `放大查看${img.alt || '商品详情图片'}`);
  });
}, { flush: 'post' });
watch(() => route.params, () => { previewImages.value = []; });

// 购物车（淘宝式切换）：当前商品 + 所选 SKU 在购物车 → 按钮变灰「移除购物车」
const canCart = computed(() => cartEnabled.value && !(p.value?.points_required && p.value.points_required > 0));
const inCartNow = computed(() => {
  if (!p.value || !canCart.value) return false;
  return !!cartItemOf(p.value.id, selectedSku.value || 0);
});
const cartBusy = computed(() => addingCart.value || removingCart.value);

// 当前显示价（跟随所选 SKU）
const flash = useFlashOffers();
const selectedFlash = computed(() => flash.active((selectedSku.value ? p.value?.skus?.find(s => Number(s.id) === selectedSku.value)?.flash_sale : p.value?.flash_sale)));
const flashSoldOut = computed(() => !!selectedFlash.value && (selectedFlash.value.remaining || 0) <= 0);
const basePrice = computed(() => {
  if (!p.value) return 0;
  if (selectedSku.value) {
    const sku = p.value.skus?.find((s) => Number(s.id) === selectedSku.value);
    if (sku && sku.price_cents) return sku.price_cents;
  }
  return p.value.price_cents;
});

const displayPrice = computed(() => flash.price(basePrice.value, selectedFlash.value));

const stockDisplay = computed(() => {
  if (!p.value) return '-';
  const s = p.value.stock ?? 0;
  if (s < -1) return '待确认';
  if (s === -1) return '不限';
  return String(s);
});
// 自营/对接均使用各自库存；proto3 省略零值时仍按缺货处理。
const stockUnknown = computed(() => (p.value?.stock ?? 0) < -1);
const stockRefreshing = ref(false);
const stockRefreshMessage = ref('');
async function refreshStock() {
  if (!p.value || stockRefreshing.value) return;
  const productID = p.value.id;
  stockRefreshing.value = true;
  stockRefreshMessage.value = '';
  try {
    const fresh = await getProduct(productID);
    if (p.value?.id !== productID) return;
    if (fresh.data) {
      // 只更新库存，保留用户已填写的规格、数量和取货信息。
      p.value.stock = fresh.data.stock ?? 0;
    }
    if (fresh.error || stockUnknown.value) {
      stockRefreshMessage.value = '仍未获取到有效库存，请 15 秒后重试，或联系客服确认。';
    }
  } finally {
    stockRefreshing.value = false;
  }
}
const soldOut = computed(() => {
  if (!p.value) return false;
  return (p.value.stock ?? 0) === 0;
});

// ── 评价（template.show_reviews 后台开关；入口锚点 + 折叠展开）──
const showReviews = ref(true);
const reviewsExpanded = ref(false);
// 销量/库存显示（template.show_sales / show_stock；缺省显示，与商品卡片同源）
const showSales = ref(true);
const showStock = ref(true);
const reviewCollapsed = 3;
const visibleReviews = computed(() =>
  reviewsExpanded.value ? (p.value?.reviews || []) : (p.value?.reviews || []).slice(0, reviewCollapsed),
);
const reviewAvg = computed(() => {
  const list = p.value?.reviews || [];
  if (!list.length) return '—';
  return (list.reduce((s, r) => s + (r.rating || 5), 0) / list.length).toFixed(1);
});
function scrollToReviews() {
  document.getElementById('pd-reviews')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
}
const stockPct = computed(() => {
  const s = p.value?.stock;
  if (s == null || s <= 0) return '4%';
  return `${Math.min(100, Math.round((s / 600) * 100))}%`;
});

// 购物车：积分商品不可加购（积分单走兑换按钮）；游客加购进本地购物车（老项目同款）

async function addToCart() {
  if (!p.value || soldOut.value || stockUnknown.value) return;
  addingCart.value = true;
  error.value = '';
  const { error: err } = await addToCartStore({ ...p.value, price_cents: basePrice.value, flash_sale: selectedFlash.value }, quantity.value, selectedSku.value || 0);
  addingCart.value = false;
  if (err) {
    error.value = err;
    const fresh = await getProduct(p.value.id);
    if (fresh.data) p.value = fresh.data;
    return;
  }
  // 成功后按钮经 cartState 响应式切换为「移除购物车」（无临时态）
}

async function removeFromCart() {
  if (!p.value) return;
  const item = cartItemOf(p.value.id, selectedSku.value || 0);
  if (!item) return;
  removingCart.value = true;
  const { error: err } = await removeCartItem(item.id);
  removingCart.value = false;
  if (err) { error.value = err; return; }
}

function stockTypeLabel(t: string) {
  return ({ card: '卡密', url: '链接', code: '兑换码' } as Record<string, string>)[t] || t;
}

// 手机端吸底操作栏点击后，表单校验失败要能看到错误提示：滚动到购买卡内的 error
function scrollToFormError() {
  requestAnimationFrame(() => {
    document.querySelector('.pd-buy .error')?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  });
}

function toggleCheck(id: number, val: string) {
  const key = String(id);
  const cur = (controlAnswers.value[key] || '').split(',').filter(Boolean);
  const idx = cur.indexOf(val);
  if (idx >= 0) cur.splice(idx, 1);
  else cur.push(val);
  controlAnswers.value[key] = cur.join(',');
}

// 交易设置（查询密码强制 / 游客联系方式）；游客判定（authState 响应式——
// 登出/登录后自动切换字段显隐）
const trade = ref<TradeConfig>({ queryPasswordRequired: true, contactRequired: 'any' });
const captchaCfg = ref<CaptchaConfig>({ login: false, register: true, order: false, reset: true });
const captchaId = ref('');
const captchaCode = ref('');
const captchaRef = ref<InstanceType<typeof CaptchaInput> | null>(null);
const isGuest = computed(() => !authState.loggedIn);

// 商品数据预取（setup 顶层：SSG 静态化商品页内容 + 输出 SEO head）
const productResp = await getProduct(Number(route.params.id));
if (productResp.error) {
  error.value = productResp.error;
} else {
  p.value = productResp.data;
  selectedSku.value = Number(productResp.data?.skus?.[0]?.id || 0);
  if (productResp.data) applyProductSeo(productResp.data);
}

onMounted(async () => {
  // 交易配置/验证码（客户端交互能力；SSG 不需要）+ 评价/销量/库存显示开关
  const [cfg, capCfg, tplResp] = await Promise.all([
    fetchTradeConfig(),
    fetchCaptchaConfig(),
    fetch('/api/v1/storefront/config').then((r) => r.json()).catch(() => null),
  ]);
  captchaCfg.value = capCfg;
  trade.value = cfg;
  const pick = (key: string) => tplResp?.entries?.find((e: any) => e.key === key)?.value_json;
  const parseFlag = (raw: string | undefined) => {
    if (raw === undefined) return true;
    try { return JSON.parse(raw) !== false; } catch { return true; }
  };
  showReviews.value = parseFlag(pick('template.show_reviews'));
  showSales.value = parseFlag(pick('template.show_sales'));
  showStock.value = parseFlag(pick('template.show_stock'));
});

/** 商品页 SEO：title/description/keywords/canonical/og + Product/Breadcrumb JSON-LD */
async function applyProductSeo(product: Product) {
  const site = await fetchSiteSeo();
  const base = site.url || (typeof window !== "undefined" ? window.location.origin : "");
  const desc = truncate(stripHtml(product.description || ''));
  const origin = typeof window !== "undefined" ? window.location.origin : site.url;
  applySeo(
    {
      title: `${product.name} - ${site.name}`,
      description: desc || undefined,
      keywords: [product.name, site.name].filter(Boolean).join(','),
      canonical: `${origin}/product/${product.id}`,
      ogType: 'product',
      ogImage: product.cover || undefined,
      jsonLd: [
        {
          '@context': 'https://schema.org',
          '@type': 'Product',
          name: product.name,
          image: product.cover || undefined,
          description: desc || undefined,
          ...(product.price_cents > 0
            ? {
                offers: {
                  '@type': 'Offer',
                  price: (product.price_cents / 100).toFixed(2),
                  priceCurrency: 'CNY',
                  availability: 'https://schema.org/InStock',
                },
              }
            : {}),
        },
        {
          '@context': 'https://schema.org',
          '@type': 'BreadcrumbList',
          itemListElement: [
            { '@type': 'ListItem', position: 1, name: site.name, item: base + '/' },
            { '@type': 'ListItem', position: 2, name: '全部商品', item: base + '/products' },
            { '@type': 'ListItem', position: 3, name: product.name, item: `${base}/product/${product.id}` },
          ],
        },
      ],
    },
    site,
  );
}

/** 下单前校验（与后端 validateTradeRequirements 同口径） */
function validateTradeFields(requireContact = true): boolean {
  if (trade.value.queryPasswordRequired && queryPassword.value.trim().length < 4) {
    error.value = '请设置查询密码（至少 4 位，取货时使用）';
    return false;
  }
  if (requireContact && (isGuest.value || trade.value.contactScope === 'all') && trade.value.contactRequired !== 'none') {
    if (!contact.value.trim()) {
      error.value = `请填写联系方式（${contactRequiredLabel(trade.value.contactRequired)}），用于订单查询与售后`;
      return false;
    }
    if (!contactValid(contact.value, trade.value.contactRequired)) {
      error.value = `联系方式格式不符（需要${contactRequiredLabel(trade.value.contactRequired)}）`;
      return false;
    }
  }
  return true;
}

async function buy() {
  if (!p.value || soldOut.value || stockUnknown.value) return;
  // 交易设置校验（查询密码 / 游客联系方式）
  if (!validateTradeFields()) { scrollToFormError(); return; }
  // 必填控件校验（proto3 空数组省略 → undefined 兜底空数组）
  for (const c of p.value.controls || []) {
    if (c.required && !controlAnswers.value[String(c.id)]?.trim()) {
      error.value = `请填写「${c.name}」`;
      scrollToFormError();
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
    ref_code: getRefCode() || undefined,
    captcha_id: (isGuest.value && captchaCfg.value.order) ? captchaId.value : undefined,
    captcha_code: (isGuest.value && captchaCfg.value.order) ? captchaCode.value : undefined,
    control_answers: Object.keys(controlAnswers.value).length ? controlAnswers.value : undefined
  });
  submitting.value = false;
  if (err) { error.value = err; return; }
  rememberOrderPassword(data!.order_no, queryPassword.value); // 支付成功自动取货用
  router.push(`/payment/${data!.order_no}`);
}

// 积分兑换（：use_points → 服务端同事务扣积分 → 订单直落 paid → 取货页交付）
async function exchangePoints() {
  if (!p.value || soldOut.value || stockUnknown.value) return;
  if (!getToken()) {
    router.push({ path: '/login', query: { redirect: route.fullPath } });
    return;
  }
  if (!validateTradeFields(false)) return;
  if (!queryPassword.value || queryPassword.value.length < 4) {
    error.value = '积分兑换同样需要设置查询密码（取货用，至少 4 位）';
    return;
  }
  if (!confirm(`确认用 ${p.value.points_required} 积分兑换本商品？`)) return;
  submitting.value = true;
  error.value = '';
  const { data, error: err } = await createOrder({
    items: [{ product_id: p.value.id, quantity: 1 }],
    query_password: queryPassword.value,
    use_points: true
  });
  submitting.value = false;
  if (err) { error.value = err; return; }
  alert('兑换成功，订单已支付，请前往取货页领取');
  router.push('/fetch');
}
</script>

<style scoped>
.pd-flash-info { margin:8px 0 0; color:#b91c1c; font-size:13px; line-height:1.7; overflow-wrap:anywhere; }
.pd-page { max-width: 1024px; margin: 0 auto; padding: 0 24px; display: flex; flex-direction: column; gap: 20px; }
.pd-crumb { font-size: 13px; color: #9ca3af; display: flex; gap: 6px; align-items: center; }
.pd-crumb a { color: #6b7280; text-decoration: none; }
.pd-crumb a:hover { color: #2563eb; }
.pd-crumb-current { color: #374151; }

.pd-main { display: grid; grid-template-columns: 1fr; gap: 20px; align-items: start; }
@media (min-width: 768px) { .pd-main { grid-template-columns: 1.05fr 1fr; } }

/* ── 左栏图 ── */
.pd-cover {
  position: relative; aspect-ratio: 1/1; border-radius: 14px; overflow: hidden;
  background: #f1f5f9; cursor: zoom-in; border: 1px solid #e5e7eb;
}
.pd-cover img { width: 100%; height: 100%; object-fit: cover; }
/* 无图占位（SVG data URI）：contain 完整显示，不提示放大 */
.pd-noimg { object-fit: contain !important; cursor: default; }
.pd-cover-placeholder {
  width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
  font-size: 56px; font-weight: 700; color: #bfdbfe;
  background: linear-gradient(135deg, #eff6ff, #dbeafe);
}
.pd-zoom-hint {
  position: absolute; right: 10px; bottom: 10px;
  padding: 4px 10px; border-radius: 999px; font-size: 11px;
  background: rgba(255,255,255,0.85); color: #6b7280;
}

/* ── 右栏购买区（独立白卡：固定白底，不受全站背景图影响）── */
.pd-buy {
  background: #fff;
  border: 1px solid #e5e7eb;
  border-radius: 14px;
  padding: 18px 20px;
}
.pd-name { font-size: 20px; font-weight: 700; color: #111827; line-height: 1.4; word-break: break-word; overflow-wrap: anywhere; }
.pd-sub { display: flex; gap: 8px; margin-top: 8px; }
.pd-points-tag { background: #eef2ff; color: #4338ca; }

.pd-price-card {
  margin-top: 14px; padding: 14px 16px; border-radius: 12px;
  background: linear-gradient(135deg, #fff3ed, #fff);
  border: 1px solid rgba(255, 87, 34, 0.25);
}
.pd-price-row { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
.pd-price { color: #ff5722; font-size: 28px; font-weight: 800; }
.pd-price-points { color: #4338ca; font-size: 13px; font-weight: 600; }

.pd-stats {
  display: flex; border-top: 1px solid #f3f4f6; border-bottom: 1px solid #f3f4f6;
  margin-top: 14px; padding: 10px 0;
}
.pd-stat { flex: 1; text-align: center; border-right: 1px solid #f3f4f6; }
.pd-stat:last-child { border-right: none; }
.pd-stat b { display: block; font-size: 15px; color: #111827; }
.pd-stat span { font-size: 11px; color: #9ca3af; }

.pd-assure {
  display: flex; gap: 14px; flex-wrap: wrap;
  font-size: 11px; color: #6b7280; padding: 10px 0;
}

.pd-field { margin-top: 12px; }
.pd-label { display: block; font-size: 13px; font-weight: 600; color: #4b5563; margin-bottom: 6px; }
.pd-req { color: #ef4444; }

.pd-input {
  width: 100%; padding: 9px 12px; border: 1px solid #d1d5db; border-radius: 8px;
  font-size: 14px; outline: none; transition: all 0.15s;
}
.pd-input:focus { border-color: #2563eb; box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.15); }

.pd-skus { display: flex; gap: 8px; flex-wrap: wrap; }
.pd-sku {
  border: 2px solid #e5e7eb; border-radius: 10px; padding: 8px 14px;
  background: #fff; cursor: pointer; text-align: center; transition: all 0.15s;
  min-width: 90px;
  max-width: 100%; /* 超长规格名不撑破容器（配合 name 断词） */
}
.pd-sku:hover { border-color: rgba(37, 99, 235, 0.4); }
.pd-sku.active { border-color: #2563eb; background: #eff6ff; }
.pd-sku-name { display: block; font-size: 13px; font-weight: 600; color: #111827; word-break: break-word; overflow-wrap: anywhere; }
.pd-sku-price { display: block; font-size: 12px; color: #ff5722; margin-top: 2px; }

.pd-qty { display: inline-flex; border: 1px solid #d1d5db; border-radius: 8px; overflow: hidden; }
.pd-qty-btn {
  width: 36px; height: 36px; border: none; background: #f8fafc; cursor: pointer;
  font-size: 16px; color: #374151; transition: all 0.15s;
}
.pd-qty-btn:hover { background: #eff6ff; color: #2563eb; }
.pd-qty-input {
  width: 56px; height: 36px; border: none; border-left: 1px solid #e5e7eb; border-right: 1px solid #e5e7eb;
  text-align: center; font-size: 14px; outline: none;
}

.pd-stock-track { height: 6px; background: #f1f5f9; border-radius: 999px; overflow: hidden; }
.pd-stock-fill { height: 100%; background: #16a34a; border-radius: 999px; transition: width 0.3s; }

.pd-options { display: flex; gap: 14px; flex-wrap: wrap; }
.pd-option { display: flex; align-items: center; gap: 4px; font-size: 14px; }

.pd-stock-notice { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; margin-top: 16px; padding: 14px; border: 1px solid #f4d58d; border-radius: 10px; background: #fff9eb; color: #775015; }
.pd-stock-notice > div { flex: 1; min-width: 180px; }
.pd-stock-notice p { margin: 5px 0 0; font-size: 13px; line-height: 1.6; }
.pd-stock-notice button { min-height: 44px; padding: 8px 12px; border: 1px solid #b98d39; border-radius: 8px; background: #fff; color: inherit; cursor: pointer; font: inherit; white-space: nowrap; }
.pd-stock-notice button:disabled { opacity: 0.6; cursor: wait; }
.pd-actions { display: flex; gap: 10px; margin-top: 18px; flex-wrap: wrap; }
.pd-btn-buy {
  flex: 1; min-width: 140px; padding: 12px 0; border: none; cursor: pointer;
  border-radius: 10px; font-size: 15px; font-weight: 700; color: #fff;
  background: linear-gradient(90deg, #2563eb, #1d4ed8);
  box-shadow: 0 4px 12px rgba(37, 99, 235, 0.3); transition: all 0.15s;
}
.pd-btn-buy:hover:not(:disabled) { box-shadow: 0 6px 18px rgba(37, 99, 235, 0.4); transform: translateY(-1px); }
.pd-btn-cart {
  display: inline-flex; align-items: center; justify-content: center; gap: 8px;
  flex: 1; min-width: 140px; padding: 12px 0; cursor: pointer;
  border-radius: 10px; font-size: 15px; font-weight: 700;
  background: #fff; color: #2563eb; border: 2px solid #2563eb; transition: all 0.15s;
}
.pd-btn-cart:hover:not(:disabled) { background: #eff6ff; }
/* 已在购物车：灰色「移除」形态（淘宝式切换） */
.pd-btn-cart.is-in {
  background: #f3f4f6; color: #6b7280; border-color: #d1d5db;
}
.pd-btn-cart.is-in:hover:not(:disabled) {
  background: #e5e7eb; color: #374151; border-color: #9ca3af;
}
.pd-btn-points {
  flex: 1; min-width: 140px; padding: 12px 0; cursor: pointer;
  border-radius: 10px; font-size: 15px; font-weight: 700;
  background: #f3f4f6; color: #4338ca; border: none; transition: all 0.15s;
}
.pd-btn-points:hover:not(:disabled) { background: #eef2ff; }
.pd-actions button:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── 描述/评价 ── */
.pd-section { background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 20px; }
.pd-section-title {
  font-size: 15px; font-weight: 700; color: #111827;
  border-left: 3px solid #2563eb; padding-left: 10px; margin-bottom: 14px;
}
.pd-desc { font-size: 14px; line-height: 1.8; color: #374151; word-break: break-word; overflow-wrap: anywhere; }
.pd-desc :deep(img) { max-width: 100%; border-radius: 8px; }
.pd-desc :deep(p) { margin: 8px 0; }
.pd-desc :deep(ol), .pd-desc :deep(ul) { margin: 12px 0; padding-inline-start: 1.75em; list-style-position: outside; }
.pd-desc :deep(li) { padding-inline-start: 0.25em; }
.pd-desc :deep(li + li) { margin-top: 8px; }
.pd-desc :deep(li > ol), .pd-desc :deep(li > ul) { margin: 8px 0; }
.pd-desc > :deep(:first-child) { margin-top: 0; }
.pd-desc > :deep(:last-child) { margin-bottom: 0; }

.pd-review { display: flex; gap: 12px; padding: 12px 0; border-bottom: 1px solid #f3f4f6; }
.pd-review:last-child { border-bottom: none; }
/* 评价入口（评分摘要锚点卡） */
.pd-review-entry {
  display: flex; align-items: center; gap: 10px; width: 100%; margin-top: 12px;
  padding: 10px 14px; border: 1px solid #e5e7eb; border-radius: 10px;
  background: #fafbfc; cursor: pointer; font-family: inherit; font-size: 13px;
  transition: border-color 0.15s, background 0.15s;
}
.pd-review-entry:hover { border-color: rgba(37, 99, 235, 0.45); background: #f0f6ff; }
.pd-review-entry-score { font-weight: 700; color: #b45309; }
.pd-review-entry-count { color: #374151; }
.pd-review-entry-go { margin-left: auto; color: #2563eb; font-weight: 600; }
.pd-review-more {
  display: block; width: 100%; margin-top: 10px; padding: 9px 0;
  border: 1px dashed #d1d5db; border-radius: 8px; background: none;
  color: #2563eb; font-size: 13px; font-weight: 600; cursor: pointer; font-family: inherit;
}
.pd-review-more:hover { border-color: #2563eb; background: #f0f6ff; }
.pd-avatar {
  width: 36px; height: 36px; border-radius: 999px; flex-shrink: 0;
  background: #eff6ff; color: #2563eb; font-size: 14px; font-weight: 700;
  display: flex; align-items: center; justify-content: center;
}
.pd-review-head { display: flex; gap: 8px; align-items: center; margin-bottom: 4px; }
.pd-stars { color: #f59e0b; font-size: 12px; }
.pd-review-content { font-size: 13px; color: #4b5563; }

.pd-desc :deep(img) { cursor: zoom-in; }
.pd-desc :deep(img:focus-visible), .pd-cover:focus-visible { outline: 2px solid #2563eb; outline-offset: 3px; }
/* ── 手机端（大厂商品页范式：无面包屑、紧凑留白、底部吸底操作栏）── */
@media (max-width: 768px) {
  .pd-page { padding: 0 12px calc(76px + env(safe-area-inset-bottom)); gap: 12px; }
  .pd-crumb { display: none; }
  .pd-buy { padding: 14px; border-radius: 12px; }
  .pd-name { font-size: 17px; }
  .pd-price { font-size: 24px; }
  .pd-price-card { margin-top: 12px; padding: 12px 14px; }
  .pd-section { padding: 14px; border-radius: 12px; }
  .pd-details { padding: 20px; }
  .pd-actions {
    position: fixed; left: 0; right: 0; bottom: 0; z-index: 100;
    margin: 0; padding: 10px 12px calc(10px + env(safe-area-inset-bottom));
    background: #fff; border-top: 1px solid #e5e7eb;
    box-shadow: 0 -4px 16px rgba(15, 23, 42, 0.06);
  }
  .pd-actions button { min-width: 0; flex: 1; }
}
</style>
