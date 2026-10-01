<script setup lang="ts">
import { uiText, t as $t } from '@/i18n';

import { computed, nextTick, reactive, ref, watch } from "vue";
import { api, formatMoney } from "@/api/client";
import { createOrder, quoteOrder, type CreateOrderInput, type CreateOrderReply } from "@/api";
import {
  countryOptions,
  regionOptions,
  emptyAddress,
  type RegionData,
} from "../../../packages/shipping";
import { newRequestId } from "../../../packages/request-id";
const dialog = ref<HTMLDialogElement>();
const address = reactive(emptyAddress());
const regions = ref<RegionData>({});
const allowed = ref<string[]>([]);
const body = ref<CreateOrderInput>();
const busy = ref(false);
const error = ref("");
const quote = ref<CreateOrderReply>();
const countries = computed(() => countryOptions(regions.value, allowed.value));
const states = computed(() => regionOptions(regions.value, address.country));
const required = computed(() => regions.value[address.country]?.require || "AC");
let finish: ((value: CreateOrderReply | null) => void) | undefined;
watch(address, () => {
  quote.value = undefined;
  error.value = "";
});
function changeCountry() {
  address.region = "";
  address.city = "";
  address.district = "";
  address.postal_code = "";
}
async function open(
  input: CreateOrderInput,
  deliveryCountries: string[],
): Promise<CreateOrderReply | null> {
  body.value = input;
  allowed.value = deliveryCountries;
  error.value = "";
  quote.value = undefined;
  if (!allowed.value.includes(address.country)) {
    address.country = allowed.value.includes("CN") ? "CN" : allowed.value[0] || "";
    changeCountry();
  }
  await nextTick();
  dialog.value?.showModal();
  const result = new Promise<CreateOrderReply | null>((resolve) => {
    finish = resolve;
  });
  if (!Object.keys(regions.value).length) {
    busy.value = true;
    const r = await api.get<{ data_json: string }>("/shipping/regions");
    busy.value = false;
    if (r.error) error.value = r.error;
    else
      try {
        regions.value = JSON.parse(r.data?.data_json || "{}");
      } catch {
        error.value = $t("地址数据加载失败，请关闭后重试");
      }
  }
  if (!allowed.value.length) error.value = $t("所选商品没有共同的可配送国家，请分开下单");
  return result;
}
function close(result: CreateOrderReply | null = null) {
  if (busy.value) return;
  dialog.value?.close();
  finish?.(result);
  finish = undefined;
}
async function submit() {
  if (!body.value || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    const input = { ...body.value, shipping_address: { ...address } };
    if (!quote.value) {
      const r = await quoteOrder(input);
      busy.value = false;
      if (r.error) {
        error.value = r.error;
        return;
      }
      quote.value = r.data!;
      return;
    }
    const signature = JSON.stringify(input);
    let storageKey = "";
    let key = "";
    try {
      const bytes = await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(signature),
      );
      storageKey =
        "shipping-checkout:" +
        Array.from(new Uint8Array(bytes))
          .map((x) => x.toString(16).padStart(2, "0"))
          .join("");
      key = sessionStorage.getItem(storageKey) || newRequestId();
      sessionStorage.setItem(storageKey, key);
    } catch {
      key = retrySignature === signature && retryKey.value ? retryKey.value : newRequestId();
    }
    retrySignature = signature;
    retryKey.value = key;
    const r = await createOrder({ ...input, quote_key: quote.value.quote_key }, key);
    busy.value = false;
    if (r.error) {
      error.value = r.error;
      return;
    }
    if (storageKey)
      try {
        sessionStorage.removeItem(storageKey);
      } catch {}
    retryKey.value = "";
    close(r.data);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : $t("提交失败，请重试");
  } finally {
    busy.value = false;
  }
}
const retryKey = ref("");
let retrySignature = "";
defineExpose({ open });
</script>
<template>
  <dialog
    ref="dialog"
    class="shipping-dialog"
    aria-labelledby="shipping-title"
    @cancel.prevent="close()"
  >
    <form @submit.prevent="submit">
      <div class="shipping-head">
        <h2 id="shipping-title">{{ $t('填写收货地址') }}</h2>
        <button type="button" class="btn secondary" :disabled="busy" @click="close()">{{ $t('关闭') }}</button>
      </div>
      <p class="muted">{{ $t('实体商品将寄送至以下地址。确认金额后再创建订单。') }}</p>
      <fieldset :disabled="busy" class="shipping-fields">
        <label
          >{{ $t('国家或地区') }} <select
            v-model="address.country"
            required
            class="input"
            autocomplete="country"
            @change="changeCountry"
          >
            <option value="" disabled>{{ $t('请选择') }}</option>
            <option v-for="c in countries" :key="c.value" :value="c.value">{{ c.label }}</option>
          </select></label
        >
        <label
          >{{ $t('省 / 州 / 地区') }} <select
            v-if="states.length"
            v-model="address.region"
            required
            class="input"
            autocomplete="address-level1"
          >
            <option value="" disabled>{{ $t('请选择地区') }}</option>
            <option v-for="s in states" :key="s.value" :value="s.value">
              {{ s.label }}
            </option></select
          ><input
            v-else
            v-model="address.region"
            class="input"
            :required="required.includes('S')"
            maxlength="100"
            autocomplete="address-level1"
        /></label>
        <label
          >{{ $t('城市') }} <input
            v-model="address.city"
            class="input"
            :required="required.includes('C')"
            maxlength="100"
            autocomplete="address-level2"
        /></label>
        <label
          >{{ $t('区 / 县（选填）') }}<input
            v-model="address.district"
            class="input"
            maxlength="100"
            autocomplete="address-level3"
        /></label>
        <label class="shipping-wide"
          >{{ $t('详细地址') }} <input
            v-model="address.address"
            required
            maxlength="300"
            class="input"
            autocomplete="street-address"
            :placeholder="$t('街道、门牌号、楼层及房号')"
        /></label>
        <label
          >{{ $t('收货人姓名') }} <input v-model="address.name" required maxlength="100" class="input" autocomplete="name"
        /></label>
        <label
          >{{ $t('电话号码') }} <input
            v-model="address.phone"
            type="tel"
            required
            maxlength="30"
            class="input"
            autocomplete="tel"
            :placeholder="$t('如 +86 13800138000')"
        /></label>
        <label
          >{{ $t('邮编') }}{{ required.includes("Z") ? "" : $t('（选填）') }}
          <input
            v-model="address.postal_code"
            :required="required.includes('Z')"
            maxlength="30"
            class="input"
            autocomplete="postal-code"
        /></label>
      </fieldset>
      <p v-if="error" role="alert" class="error">{{ uiText(error) }}</p>
      <div v-if="quote" class="shipping-quote" aria-live="polite">
        <span
          >{{ $t('优惠后商品金额') }} {{
            formatMoney(Number(quote.total_cents || 0) - Number(quote.shipping_cents || 0))
          }}</span
        ><span>{{ $t('运费') }} {{ formatMoney(quote.shipping_cents || 0) }}</span
        ><strong>{{ $t('合计') }} {{ formatMoney(quote.total_cents || 0) }}</strong
        ><small class="muted">{{ $t('支付渠道手续费将在支付前单独显示。') }}</small>
      </div>
      <button
        v-if="quote"
        type="button"
        class="btn secondary"
        :disabled="busy"
        @click="quote = undefined"
      > {{ $t('重新核算金额') }} </button>
      <button class="btn btn-primary shipping-submit" :disabled="busy || !countries.length">
        {{ busy ? $t('正在处理…') : quote ? $t('确认地址及金额，创建订单') : $t('核算商品金额及运费') }}
      </button>
    </form>
  </dialog>
</template>
<style scoped>
.shipping-dialog {
  margin: auto;
  border: 1px solid #e5e7eb;
  border-radius: 16px;
  padding: 24px;
  width: min(640px, calc(100vw - 24px));
  max-height: 90dvh;
  box-sizing: border-box;
  color: #1f2937;
  background: #fff;
}
.shipping-dialog::backdrop {
  background: #0008;
}
.shipping-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.shipping-head h2 {
  font-size: 20px;
  margin: 0;
}
.shipping-fields {
  border: 0;
  padding: 0;
  margin: 20px 0;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  min-width: 0;
}
.shipping-fields label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 14px;
  min-width: 0;
}
.shipping-fields .input {
  width: 100%;
  box-sizing: border-box;
  min-height: 44px;
}
.shipping-wide {
  grid-column: 1/-1;
}
.shipping-quote {
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: #f8fafc;
  padding: 16px;
  margin: 16px 0;
  border-radius: 10px;
}
.shipping-submit {
  width: 100%;
  min-height: 44px;
}
.error {
  margin: 16px 0;
  color: #b91c1c;
}
@media (max-width: 480px) {
  .shipping-fields {
    grid-template-columns: 1fr;
  }
  .shipping-dialog {
    padding: 18px;
  }
}
</style>
