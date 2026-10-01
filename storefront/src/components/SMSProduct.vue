<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { api, formatMoney, getToken } from "@/api/client";
import { getBalance, fetchPaymentChannels, createPayment, type Product } from "@/api";
import { authState } from "@/auth";
import { newRequestId } from "../../../packages/request-id";
const props = defineProps<{ product: Product }>();
type Option = { id: string; name: string };
type Offer = { offer_id: string; name: string; price_cents: number; stock: number };
type Quote = {
  quote_id: string;
  amount_cents: number;
  currency: string;
  expires_at: number;
  offer_name: string;
};
type Buy = {
  quote_id: string;
  request_id: string;
  amount_cents: number;
  order_no?: string;
  channel?: string;
};
type Snapshot = {
  state: string;
  phone_number?: string;
  otp_code?: string;
  otp_message?: string;
  can_cancel?: boolean;
  can_finish?: boolean;
  expires_at?: string;
  cancel_available_at?: string;
  settlement_state?: string;
  refunded_amount_cents?: number;
  version?: number;
  phase?: string;
  operation_status?: string;
  operation_action?: string;
  operation_request_id?: string;
  refund_status?: string;
  message?: string;
};
type Order = {
  supply_order_id: string;
  offer_name?: string;
  amount: number;
  status: string;
  fulfillment?: { sms?: Snapshot };
};
type Action = { action: "cancel" | "finish"; operation_id: string };
const countries = ref<Option[]>([]),
  platforms = ref<Option[]>([]),
  offers = ref<Offer[]>([]),
  orders = ref<Order[]>([]);
const country = ref(""),
  platform = ref(""),
  keyword = ref(""),
  selected = ref("");
const page = ref(1),
  historyPage = ref(1),
  hasMore = ref(false),
  balance = ref<number>();
const offersLoading = ref(false),
  optionsLoading = ref(false),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  historyError = ref(""),
  copied = ref("");
const loading = computed(() => offersLoading.value || optionsLoading.value);
const quote = ref<Quote>(),
  pending = ref<Buy>(),
  pendingActions = ref<Record<string, Action>>({});
const path = `/products/${encodeURIComponent(String(props.product.id))}/sms`;
const storageKey = computed(() => `zcard_sms_purchase:${authState.username}:${props.product.id}`);
const loggedIn = computed(() => authState.loggedIn || !!getToken());
const choice = computed(() => offers.value.find((o) => o.offer_id === selected.value));
const titles: Record<string, string> = {
  allocating: "获取号码中",
  waiting_sms: "等待短信",
  sms_received: "已收到短信",
  completed: "已结束",
  canceled: "已取消",
  expired: "已过期",
  rejected: "取号失败",
  review: "结果待核对",
};
let closed = false,
  generation = 0,
  optionsGeneration = 0,
  historyGeneration = 0,
  timer: ReturnType<typeof setTimeout> | undefined,
  searchTimer: ReturnType<typeof setTimeout> | undefined;
function restoreIntent() {
  pending.value = undefined;
  pendingActions.value = {};
  try {
    const v = JSON.parse(sessionStorage.getItem(storageKey.value) || "null");
    if (v?.quote_id && v?.request_id && Number(v.amount_cents) > 0) pending.value = v;
    const actions = JSON.parse(sessionStorage.getItem(storageKey.value + ":actions") || "{}");
    for (const [id, a] of Object.entries(actions) as [string, Action][]) {
      if (a?.operation_id && ["cancel", "finish"].includes(a.action)) pendingActions.value[id] = a;
    }
  } catch {
    /* Ignore invalid browser data. */
  }
}
function saveActions() {
  try {
    sessionStorage.setItem(storageKey.value + ":actions", JSON.stringify(pendingActions.value));
  } catch {}
}
type Session = { key: string; token: string | null };
function session(): Session {
  return { key: storageKey.value, token: getToken() };
}
function sameSession(value: Session) {
  return !closed && value.key === storageKey.value && value.token === getToken();
}
watch(storageKey, () => {
  generation++;
  optionsGeneration++;
  historyGeneration++;
  orders.value = [];
  offers.value = [];
  quote.value = undefined;
  balance.value = undefined;
  busy.value = false;
  optionsLoading.value = offersLoading.value = false;
  error.value = notice.value = historyError.value = "";
  if (timer) clearTimeout(timer);
  if (loggedIn.value) restoreIntent();
  else { pending.value = undefined; pendingActions.value = {}; }
  void loadOptions();
  void loadOffers();
  if (loggedIn.value) {
    void loadHistory();
    void loadBalance();
  }
});
watch(loggedIn, (value) => {
  if (!value) {
    orders.value = [];
    quote.value = undefined;
    pending.value = undefined;
    pendingActions.value = {};
    balance.value = undefined;
    busy.value = false;
    optionsLoading.value = offersLoading.value = false;
    error.value = notice.value = historyError.value = "";
    generation++;
    optionsGeneration++;
    historyGeneration++;
    if (timer) clearTimeout(timer);
    void loadOptions();
    void loadOffers();
  } else {
    restoreIntent();
    void loadHistory();
    void loadBalance();
  }
});
function savePending(value?: Buy) {
  pending.value = value;
  try {
    if (value) sessionStorage.setItem(storageKey.value, JSON.stringify(value));
    else sessionStorage.removeItem(storageKey.value);
  } catch {
    /* In-memory intent is retained when browser storage is unavailable. */
  }
}
async function loadBalance() {
  const member = session();
  const r = await getBalance();
  if (sameSession(member) && r.data) balance.value = r.data.available_cents;
}
async function loadOptions() {
  const version = ++optionsGeneration;
  optionsLoading.value = true;
  error.value = "";
  const r = await api.get<{ countries: Option[]; platforms: Option[] }>(`${path}/options`, {
    country_id: country.value || "0",
    keyword: keyword.value,
  });
  if (closed || version !== optionsGeneration) return;
  optionsLoading.value = false;
  if (r.error) {
    error.value = r.error;
    return;
  }
  countries.value = r.data?.countries || [];
  platforms.value = r.data?.platforms || [];
  if (platform.value && !platforms.value.some((p) => p.id === platform.value)) platform.value = "";
}
async function loadOffers() {
  const version = ++generation;
  offers.value = [];
  selected.value = "";
  quote.value = undefined;
  error.value = "";
  hasMore.value = false;
  if (!country.value || !platform.value) {
    offersLoading.value = false;
    return;
  }
  offersLoading.value = true;
  const r = await api.get<{ offers: Offer[]; has_more: boolean }>(`${path}/offers`, {
    country_id: country.value,
    platform_id: platform.value,
    page: page.value,
    page_size: 50,
  });
  if (closed || version !== generation) return;
  offersLoading.value = false;
  error.value = r.error || "";
  if (r.data) {
    offers.value = r.data.offers || [];
    hasMore.value = !!r.data.has_more;
  }
}
watch(country, () => {
  platform.value = "";
  page.value = 1;
  offers.value = [];
  selected.value = "";
  quote.value = undefined;
  void loadOptions();
});
watch(platform, () => {
  page.value = 1;
  void loadOffers();
});
watch(keyword, () => {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => void loadOptions(), 300);
});
async function confirmQuote() {
  if (!loggedIn.value || !choice.value || busy.value) return;
  const version = generation,
    offer = choice.value;
  const displayedPrice = Number(offer.price_cents);
  const member = session();
  busy.value = true;
  error.value = "";
  const r = await api.post<Quote>(`${path}/quotes`, {
    offer_id: offer.offer_id,
    country_id: country.value,
    platform_id: platform.value,
    page: page.value,
    page_size: 50,
  });
  if (!sameSession(member)) return;
  busy.value = false;
  if (version !== generation || offer.offer_id !== selected.value) return;
  if (r.error) {
    error.value = r.error;
    return;
  }
  if (r.data) {
    quote.value = r.data;
    offer.price_cents = Number(r.data.amount_cents);
    if (Number(r.data.amount_cents) !== displayedPrice)
      notice.value = "价格已更新，请确认最新金额后支付。";
  }
}
async function buy() {
  if (!loggedIn.value || busy.value) return;
  if (!pending.value) {
    if (!quote.value || quote.value.expires_at <= Date.now() / 1000) {
      quote.value = undefined;
      error.value = "报价已过期，请重新确认价格。";
      return;
    }
    try {
      savePending({
        quote_id: quote.value.quote_id,
        request_id: newRequestId(),
        amount_cents: Number(quote.value.amount_cents),
      });
    } catch (e) {
      error.value = e instanceof Error ? e.message : "无法生成购买标识";
      return;
    }
  }
  const member = session();
  busy.value = true;
  error.value = "";
  const intent = pending.value!;
  // Recover the original order before retrying payment. A lost payment response
  // must never cause another retail order or another supplier purchase.
  if (!intent.order_no) {
    const created = await api.post<{ order_no: string; total_cents: number }>(
      "/orders",
      {
        items: [{ product_id: String(props.product.id), quantity: 1 }],
        sms_quote_id: intent.quote_id,
      },
      { "Idempotency-Key": intent.request_id },
    );
    if (!sameSession(member)) return;
    if (created.error || !created.data) {
      busy.value = false;
      error.value = `${created.error || "建单失败"}。请确认原购买结果。`;
      if (created.status === 400 || created.status === 404) savePending();
      return;
    }
    if (Number(created.data.total_cents) !== intent.amount_cents) {
      busy.value = false;
      error.value = "订单金额与确认报价不一致，请联系客服核对。";
      return;
    }
    intent.order_no = created.data.order_no;
    savePending(intent);
  }
  const current = await api.get<{ status: string }>(
    `/orders/${encodeURIComponent(intent.order_no)}`,
  );
  if (!sameSession(member)) return;
  if (current.error || !current.data) {
    busy.value = false;
    error.value = `${current.error || "无法读取订单"}。请确认原购买结果。`;
    return;
  }
  if (!["pending_payment", "pending"].includes(current.data.status)) {
    if (["canceled", "expired"].includes(current.data.status)) {
      savePending();
      quote.value = undefined;
      busy.value = false;
      error.value = "原订单已取消或过期，请重新选价。";
      return;
    }
    await purchaseAccepted();
    return;
  }
  if (!intent.channel) {
    const channels = await fetchPaymentChannels();
    if (!sameSession(member)) return;
    const wallet = channels.data?.channels?.find(
      (c) => c.driver === "wallet" && !Number(c.fee || 0),
    );
    if (!wallet) {
      busy.value = false;
      error.value = channels.error || "本站尚未启用免手续费会员余额支付，请联系商户。";
      return;
    }
    intent.channel = wallet.code;
    savePending(intent);
  }
  const r = await createPayment(intent.order_no, intent.channel);
  if (!sameSession(member)) return;
  busy.value = false;
  if (r.error) {
    // Includes insufficient balance and unknown results: retry this same order.
    error.value = `${r.error}。可充值后确认原购买结果，或取消未付款订单后重选。`;
    return;
  }
  if (r.data) await purchaseAccepted();
}
async function purchaseAccepted() {
  savePending();
  quote.value = undefined;
  busy.value = false;
  notice.value = "余额付款已确认，号码和短信将在“我的号码”中显示。";
  historyPage.value = 1;
  await loadHistory();
  await loadBalance();
}
async function cancelUnpaid() {
  if (!pending.value?.order_no || busy.value) return;
  const member = session();
  busy.value = true;
  const r = await api.post(`/orders/${encodeURIComponent(pending.value.order_no)}/cancel`, {});
  if (!sameSession(member)) return;
  busy.value = false;
  if (r.error) {
    error.value = `${r.error}。请确认原购买结果。`;
    return;
  }
  savePending();
  quote.value = undefined;
  error.value = "";
  notice.value = "未付款订单已取消，可以重新选价。";
}

async function loadHistory() {
  if (closed || !loggedIn.value) return;
  const version = ++historyGeneration;
  if (timer) clearTimeout(timer);
  const r = await api.get<{
    orders: { order_no: string; offer_name: string; amount_cents: number; sms: Snapshot }[];
  }>(`${path}/sessions`, { page: historyPage.value });
  if (closed || version !== historyGeneration) return;
  historyError.value = r.error || "";
  if (r.data) {
    orders.value = (r.data.orders || []).map((o) => ({
      supply_order_id: o.order_no,
      offer_name: o.offer_name,
      amount: Number(o.amount_cents),
      status: o.sms?.state || "allocating",
      fulfillment: {
        sms: {
          ...o.sms,
          state: o.sms?.phase === "review" ? "review" : o.sms?.state || "allocating",
          settlement_state: o.sms?.refund_status === "succeeded" ? "refunded" : "paid",
          refunded_amount_cents: o.sms?.refund_status === "succeeded" ? Number(o.amount_cents) : 0,
        },
      },
    }));
    for (const o of orders.value) {
      const snap = o.fulfillment?.sms;
      const action = pendingActions.value[o.supply_order_id];
      if (
        (action &&
          snap?.operation_request_id === action.operation_id &&
          snap.operation_status &&
          snap.operation_status !== "pending") ||
        snap?.refund_status === "succeeded"
      )
        delete pendingActions.value[o.supply_order_id];
    }
    saveActions();
  }
  timer = setTimeout(() => void loadHistory(), document.hidden ? 30000 : r.error ? 10000 : 4000);
}
function changeHistory(n: number) {
  historyPage.value = n;
  orders.value = [];
  void loadHistory();
}
function changePage(n: number) {
  page.value = n;
  void loadOffers();
}
async function act(order: Order, action: "cancel" | "finish") {
  if (!loggedIn.value || busy.value) return;
  const existing = pendingActions.value[order.supply_order_id];
  if (existing && existing.action !== action) return;
  if (
    !existing &&
    !window.confirm(
      action === "cancel"
        ? "申请取消？上游确认退款后退还本次实付，受理不代表退款完成。"
        : "结束本次接码？结束后停止接收短信，已有短信仍可查看。",
    )
  )
    return;
  if (!existing) {
    try {
      pendingActions.value[order.supply_order_id] = { action, operation_id: newRequestId() };
    } catch (e) {
      error.value = e instanceof Error ? e.message : "无法生成操作标识";
      return;
    }
  }
  saveActions();
  const member = session();
  busy.value = true;
  const r = await api.post<Snapshot>(
    `/orders/${encodeURIComponent(order.supply_order_id)}/sms/${action}`,
    { request_id: pendingActions.value[order.supply_order_id].operation_id },
  );
  if (!sameSession(member)) return;
  busy.value = false;
  if (r.error) {
    error.value = r.error;
    if (r.status === 400 || r.status === 404) delete pendingActions.value[order.supply_order_id];
  } else if (r.data) {
    notice.value =
      r.data.operation_status === "pending"
        ? "操作已受理，正在等待服务确认。"
        : r.data.operation_status === "rejected"
          ? "服务未接受操作，请查看号码当前状态。"
          : r.data.operation_status === "review"
            ? "操作结果待核对，请凭订单号联系客服。"
            : "操作已完成";
    if (
      r.data.operation_request_id === pendingActions.value[order.supply_order_id]?.operation_id &&
      r.data.operation_status !== "pending"
    )
      delete pendingActions.value[order.supply_order_id];
  }
  saveActions();
  await loadHistory();
  await loadBalance();
}
async function copy(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    copied.value = "已复制";
  } catch {
    copied.value = "请手动选择复制";
  }
}
function visible() {
  if (!document.hidden && loggedIn.value) {
    void loadHistory();
    void loadBalance();
  }
}
onMounted(() => {
  void loadOptions();
  if (loggedIn.value) {
    restoreIntent();
    void loadHistory();
    void loadBalance();
  }
  document.addEventListener("visibilitychange", visible);
});
onBeforeUnmount(() => {
  closed = true;
  generation++;
  optionsGeneration++;
  historyGeneration++;
  if (timer) clearTimeout(timer);
  if (searchTimer) clearTimeout(searchTimer);
  document.removeEventListener("visibilitychange", visible);
  orders.value = [];
});
</script>

<template>
  <div class="sms-product">
    <nav class="sms-crumb">
      <router-link to="/">首页</router-link><span>/</span><span>{{ product.name }}</span>
    </nav>
    <header>
      <h1>{{ product.name }}</h1>
      <p class="muted">选择国家与服务，确认价格后获取号码。购买、收码与取消都在这里完成。</p>
    </header>
    <div class="sms-layout">
      <section class="sms-panel" aria-labelledby="sms-select-title">
        <h2 id="sms-select-title">选择号码</h2>
        <p v-if="!loggedIn" class="sms-empty">
          可直接筛选国家、服务并查看价格，购买时需要登录。
        </p>
          <p v-if="loggedIn" class="sms-wallet">
            可用余额：<strong>{{ balance === undefined ? "加载中" : formatMoney(balance) }}</strong
            ><router-link to="/member">充值</router-link>
          </p>
          <p v-if="!product.sms_sales_enabled" class="sms-message">
            暂未开放新购，已购买号码仍可收码和查看。
          </p>
          <fieldset :disabled="busy || !!pending">
            <label for="sms-country">国家 / 地区</label>
            <select id="sms-country" v-model="country">
              <option value="">请选择国家或地区</option>
              <option v-for="c in countries" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
            <label for="sms-search">搜索平台 / 服务</label
            ><input
              id="sms-search"
              v-model="keyword"
              type="search"
              placeholder="平台名称或服务 ID"
            />
            <label for="sms-platform">平台 / 服务</label
            ><select id="sms-platform" v-model="platform" :disabled="!country">
              <option value="">请选择平台 / 服务</option>
              <option v-for="p in platforms" :key="p.id" :value="p.id">{{ p.name }}</option>
            </select>
            <p v-if="loading" class="muted" role="status">正在加载可用选项…</p>
            <button
              v-if="error && !loading"
              type="button"
              class="btn"
              @click="country && platform ? loadOffers() : loadOptions()"
            >
              重试加载
            </button>
            <div
              v-if="offers.length"
              class="sms-offers"
              role="radiogroup"
              aria-label="可用号码报价"
            >
              <label
                v-for="o in offers"
                :key="o.offer_id"
                class="sms-offer"
                :class="{ selected: selected === o.offer_id }"
                ><input
                  v-model="selected"
                  type="radio"
                  name="sms-offer"
                  :value="o.offer_id"
                  :disabled="o.stock === 0"
                  @change="quote = undefined"
                /><span
                  >{{ o.name
                  }}<small>{{
                    o.stock === -1 ? "可用数量不限" : `可用 ${o.stock} 个`
                  }}</small></span
                ><strong>{{ formatMoney(o.price_cents) }}</strong></label
              >
            </div>
            <p v-else-if="country && platform && !loading && !error" class="sms-empty">
              当前筛选暂无可用号码，可更换国家或服务。
            </p>
            <div v-if="country && platform" class="sms-pagination">
              <button
                type="button"
                class="btn"
                :disabled="page <= 1 || loading"
                @click="changePage(page - 1)"
              >
                上一页</button
              ><span>第 {{ page }} 页</span
              ><button
                type="button"
                class="btn"
                :disabled="!hasMore || loading"
                @click="changePage(page + 1)"
              >
                下一页
              </button>
            </div>
            <p class="muted">每次购买 1 个号码。可用时长及取消规则以购买后的服务说明为准。</p>
          </fieldset>
          <div v-if="quote || pending" class="sms-total">
            <span>本次实付</span
            ><strong>{{ formatMoney(pending?.amount_cents ?? quote?.amount_cents ?? 0) }}</strong>
          </div>
          <p v-if="quote && !pending" class="muted">
            报价有效至 {{ new Date(Number(quote.expires_at) * 1000).toLocaleTimeString() }}
          </p>
          <router-link
            v-if="!loggedIn"
            class="btn btn-primary sms-submit"
            :to="{ path: '/login', query: { redirect: `/product/${product.id}` } }"
          >登录后购买</router-link>
          <button
            v-else-if="pending"
            type="button"
            class="btn btn-primary sms-submit"
            :disabled="busy"
            @click="buy"
          >
            {{ busy ? "确认中…" : "确认原购买结果" }}
          </button>
          <button
            v-else-if="quote"
            type="button"
            class="btn btn-primary sms-submit"
            :disabled="busy || !product.sms_sales_enabled"
            @click="buy"
          >
            {{ busy ? "购买中…" : `余额支付 ${formatMoney(quote.amount_cents)}` }}
          </button>
          <button
            v-else
            type="button"
            class="btn btn-primary sms-submit"
            :disabled="busy || loading || !choice || !product.sms_sales_enabled"
            @click="confirmQuote"
          >
            {{ busy ? "确认价格中…" : "确认价格" }}
          </button>
        <p v-if="error" role="alert" class="sms-error">{{ error }}</p>
        <button
          v-if="pending?.order_no"
          class="btn secondary"
          :disabled="busy"
          @click="cancelUnpaid"
        >
          取消未付款订单后重选
        </button>
        <p v-if="notice" role="status" class="sms-message">{{ notice }}</p>
      </section>
      <section class="sms-panel" aria-labelledby="sms-receive-title">
        <div class="sms-section-heading">
          <h2 id="sms-receive-title">接收短信 / 我的号码</h2>
          <button v-if="loggedIn" type="button" class="btn" @click="loadHistory">刷新</button>
        </div>
        <p v-if="historyError" role="alert" class="sms-error">{{ historyError }}</p>
        <div v-if="!orders.length" class="sms-empty sms-receive-empty">
          <span class="sms-message-icon" aria-hidden="true">✉</span>
          <p>购买成功后，号码与短信将显示在这里。</p>
          <p>已购买的号码可在这里继续查看。</p>
        </div>
        <article v-for="o in orders" :key="o.supply_order_id" class="sms-session">
          <div class="sms-section-heading">
            <strong>{{ o.offer_name || `订单 ${o.supply_order_id}` }}</strong
            ><span class="sms-state">{{
              titles[o.fulfillment?.sms?.state || ""] || "确认结果中"
            }}</span>
          </div>
          <p class="muted">实付 {{ formatMoney(o.amount) }} · 订单 {{ o.supply_order_id }}</p>
          <template v-if="o.fulfillment?.sms">
            <div v-if="o.fulfillment.sms.phone_number" class="sms-copy">
              <strong>{{ o.fulfillment.sms.phone_number }}</strong
              ><button type="button" class="btn" @click="copy(o.fulfillment.sms.phone_number)">
                复制号码
              </button>
            </div>
            <div v-if="o.fulfillment.sms.otp_code" class="sms-copy">
              <strong class="sms-code">{{ o.fulfillment.sms.otp_code }}</strong
              ><button type="button" class="btn" @click="copy(o.fulfillment.sms.otp_code)">
                复制验证码
              </button>
            </div>
            <p v-if="o.fulfillment.sms.otp_message" class="sms-text">
              {{ o.fulfillment.sms.otp_message }}
            </p>
            <p v-if="o.fulfillment.sms.expires_at" class="muted">
              有效至 {{ new Date(o.fulfillment.sms.expires_at).toLocaleString() }}
            </p>
            <p v-if="o.fulfillment.sms.message" class="muted">{{ o.fulfillment.sms.message }}</p>
            <p v-if="o.fulfillment.sms.refund_status === 'pending'" class="muted">
              正在退还本次实付，请等待余额确认。
            </p>
            <p v-if="o.fulfillment.sms.settlement_state === 'refunded'" class="sms-refund">
              已退还 {{ formatMoney(o.fulfillment.sms.refunded_amount_cents || 0) }}
            </p>
            <p v-else-if="o.fulfillment.sms.settlement_state !== 'paid'" class="muted">
              结算状态：{{ o.fulfillment.sms.settlement_state }}
            </p>
            <div class="sms-actions">
              <button
                v-if="pendingActions[o.supply_order_id]"
                type="button"
                class="btn"
                :disabled="busy"
                @click="act(o, pendingActions[o.supply_order_id].action)"
              >
                确认{{
                  pendingActions[o.supply_order_id].action === "cancel" ? "取消" : "结束"
                }}结果
              </button>
              <template v-else
                ><button
                  v-if="o.fulfillment.sms.can_cancel"
                  type="button"
                  class="btn"
                  :disabled="busy"
                  @click="act(o, 'cancel')"
                >
                  取消接码</button
                ><button
                  v-if="o.fulfillment.sms.can_finish"
                  type="button"
                  class="btn"
                  :disabled="busy"
                  @click="act(o, 'finish')"
                >
                  结束接码
                </button></template
              >
            </div>
          </template>
        </article>
        <div v-if="loggedIn && (orders.length || historyPage > 1)" class="sms-pagination">
          <button
            type="button"
            class="btn"
            :disabled="historyPage <= 1"
            @click="changeHistory(historyPage - 1)"
          >
            较新号码</button
          ><span>第 {{ historyPage }} 页</span
          ><button
            type="button"
            class="btn"
            :disabled="orders.length < 20"
            @click="changeHistory(historyPage + 1)"
          >
            较早号码
          </button>
        </div>
        <p v-if="copied" role="status" class="muted">{{ copied }}</p>
      </section>
    </div>
    <section v-if="product.description" class="sms-panel sms-description">
      <h2>服务说明</h2>
      <div class="rich-content" v-html="product.description"></div>
    </section>
  </div>
</template>
<style scoped>
.sms-product {
  max-width: 1160px;
  margin: 0 auto;
  padding: 24px 0 48px;
  color: #1e293b;
}
.sms-crumb,
.sms-section-heading,
.sms-copy,
.sms-total,
.sms-pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.sms-crumb {
  justify-content: flex-start;
  font-size: 13px;
  color: #64748b;
  margin-bottom: 20px;
}
.sms-product h1 {
  font-size: 26px;
  font-weight: 650;
  margin: 0 0 10px;
}
.sms-product h2 {
  font-size: 18px;
  margin: 0 0 20px;
}
.sms-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr);
  gap: 20px;
  margin-top: 24px;
  align-items: start;
}
.sms-panel {
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  padding: 24px;
  min-width: 0;
}
.sms-wallet {
  display: flex;
  gap: 8px;
  align-items: center;
  margin: 0 0 20px;
}
.sms-wallet a {
  margin-left: auto;
}
.sms-product fieldset {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.sms-product fieldset > label {
  display: block;
  font-size: 13px;
  margin: 18px 0 8px;
}
.sms-product select,
.sms-product input[type="search"] {
  width: 100%;
  min-height: 44px;
  border: 1px solid #cbd5e1;
  border-radius: 7px;
  background: #fff;
  color: #1e293b;
  padding: 10px 12px;
  font-size: 14px;
}
.sms-product input:focus-visible,
.sms-product select:focus-visible,
.sms-product button:focus-visible {
  outline: 2px solid var(--zc-primary, #2563eb);
  outline-offset: 2px;
}
.sms-product .btn {
  min-height: 44px;
  display: inline-flex;
  justify-content: center;
  align-items: center;
  padding: 8px 14px;
}
.sms-offers {
  display: grid;
  gap: 8px;
  margin: 20px 0;
}
.sms-offer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  cursor: pointer;
}
.sms-offer.selected {
  border-color: var(--zc-primary, #2563eb);
  background: var(--zc-primary-soft, #eff6ff);
}
.sms-offer span {
  flex: 1;
  overflow-wrap: anywhere;
}
.sms-offer small {
  display: block;
  color: #64748b;
  margin-top: 4px;
}
.sms-offer strong {
  white-space: nowrap;
}
.sms-submit {
  width: 100%;
  margin-top: 16px;
}
.sms-total {
  border-top: 1px solid #e2e8f0;
  margin-top: 20px;
  padding-top: 18px;
}
.sms-total strong {
  font-size: 22px;
}
.sms-empty {
  color: #64748b;
  font-size: 14px;
  line-height: 1.8;
}
.sms-receive-empty {
  text-align: center;
  padding: 48px 8px;
}
.sms-message-icon {
  display: inline-grid;
  place-items: center;
  width: 60px;
  height: 60px;
  border-radius: 50%;
  background: var(--zc-primary-soft, #eff6ff);
  color: var(--zc-primary, #2563eb);
  font-size: 28px;
  margin-bottom: 16px;
}
.sms-section-heading h2 {
  margin-bottom: 0;
}
.sms-section-heading {
  margin-bottom: 16px;
}
.sms-error {
  color: #b91c1c;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.sms-message {
  padding: 12px;
  background: #eff6ff;
  border-radius: 8px;
  font-size: 14px;
  line-height: 1.6;
}
.sms-session {
  padding: 18px 0;
  border-top: 1px solid #e2e8f0;
  overflow-wrap: anywhere;
}
.sms-state {
  font-size: 12px;
  color: #64748b;
  flex-shrink: 0;
}
.sms-copy {
  margin: 14px 0;
}
.sms-code {
  font-size: 24px;
  letter-spacing: 2px;
}
.sms-text {
  white-space: pre-wrap;
  background: #f8fafc;
  padding: 12px;
  border-radius: 8px;
}
.sms-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.sms-pagination {
  margin-top: 16px;
  font-size: 13px;
}
.sms-refund {
  color: #15803d;
}
.sms-description {
  margin-top: 20px;
}
.sms-product .muted {
  font-size: 13px;
  color: #64748b;
  line-height: 1.7;
}
button:disabled {
  opacity: 0.55;
  cursor: default;
}
@media (max-width: 760px) {
  .sms-product {
    padding: 16px 0 32px;
  }
  .sms-layout {
    grid-template-columns: 1fr;
    gap: 16px;
  }
  .sms-panel {
    padding: 18px;
  }
  .sms-product h1 {
    font-size: 22px;
  }
  .sms-section-heading {
    flex-wrap: wrap;
  }
  .sms-offer {
    align-items: flex-start;
  }
  .sms-copy strong {
    min-width: 0;
    overflow-wrap: anywhere;
  }
}
</style>
