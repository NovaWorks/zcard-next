<script setup lang="ts">
import { uiText, t as $t, localeTag } from '@/i18n';

import { computed, nextTick, reactive, ref, watch } from "vue";
import { api, formatMoney } from "@/api/client";
import { createOrder, quoteOrder, type CreateOrderInput, type CreateOrderReply } from "@/api";
import {
  countryOptions,
  regionOptions,
  emptyAddress,
  addressFields,
  addressRequired,
  validateAddress,
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
const countries = computed(() => countryOptions(regions.value, allowed.value, localeTag.value));
const states = computed(() => regionOptions(regions.value, address.country, localeTag.value));
const locationFields = computed(() => addressFields(regions.value, address.country));
const fieldErrors = ref<Record<string, string>>({});
const touched = new Set<string>();
let attempted = false;
const fieldNames = { city: "郊区或城市", district: "区 / 县", region: "州 / 省 / 地区", postal_code: "邮政编码" };
const fieldAutocomplete = { city: "address-level2", district: "address-level3", region: "address-level1", postal_code: "postal-code" };
function checkField(field: string) {
  touched.add(field);
  const errors = validateAddress(address, regions.value);
  if (errors[field]) fieldErrors.value[field] = errors[field];
  else delete fieldErrors.value[field];
}
let finish: ((value: CreateOrderReply | null) => void) | undefined;
watch(address, () => {
  quote.value = undefined;
  error.value = "";
  const errors = validateAddress(address, regions.value);
  if (attempted) fieldErrors.value = errors;
  else for (const field of touched) {
    if (errors[field]) fieldErrors.value[field] = errors[field];
    else delete fieldErrors.value[field];
  }
});
function changeCountry() {
  if (!locationFields.value.includes("region") || (states.value.length && !states.value.some(state => state.value === address.region))) address.region = "";
  for (const field of ["city", "district", "postal_code"] as const) if (!locationFields.value.includes(field)) address[field] = "";
}
async function open(
  input: CreateOrderInput,
  deliveryCountries: string[],
): Promise<CreateOrderReply | null> {
  body.value = input;
  allowed.value = deliveryCountries;
  error.value = "";
  quote.value = undefined;
  attempted = false;
  touched.clear();
  fieldErrors.value = {};
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
  attempted = true;
  fieldErrors.value = validateAddress(address, regions.value);
  if (Object.keys(fieldErrors.value).length) {
    await nextTick();
    dialog.value?.querySelector<HTMLElement>(`#shipping-${Object.keys(fieldErrors.value)[0]}`)?.focus();
    return;
  }
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
    <form novalidate @submit.prevent="submit">
      <div class="shipping-head">
        <h2 id="shipping-title">{{ $t('填写收货地址') }}</h2>
        <button type="button" class="btn secondary" :disabled="busy" @click="close()">{{ $t('关闭') }}</button>
      </div>
      <p class="muted">{{ $t('实体商品将寄送至以下地址。确认金额后再创建订单。') }}</p>
      <fieldset :disabled="busy" class="shipping-fields shipping-recipient">
        <legend>{{ $t('收货人') }}</legend>
        <label for="shipping-name">{{ $t('收货人姓名') }}
          <input id="shipping-name" v-model="address.name" required maxlength="100" class="input" autocomplete="shipping name" :aria-invalid="!!fieldErrors.name" :aria-describedby="fieldErrors.name ? 'shipping-name-error' : undefined" @blur="checkField('name')" />
          <span v-if="fieldErrors.name" id="shipping-name-error" class="field-error">{{ $t(fieldErrors.name) }}</span>
        </label>
        <label for="shipping-phone">{{ $t('电话号码') }}
          <input id="shipping-phone" v-model="address.phone" type="tel" required maxlength="30" class="input" autocomplete="shipping tel" :placeholder="$t('如 +61 412 345 678')" :aria-invalid="!!fieldErrors.phone" :aria-describedby="fieldErrors.phone ? 'shipping-phone-error' : undefined" @blur="checkField('phone')" />
          <span v-if="fieldErrors.phone" id="shipping-phone-error" class="field-error">{{ $t(fieldErrors.phone) }}</span>
        </label>
      </fieldset>
      <fieldset :disabled="busy" class="shipping-fields shipping-address">
        <legend>{{ $t('配送地址') }}</legend>
        <label for="shipping-country">{{ $t('国家或地区') }}
          <select id="shipping-country" v-model="address.country" required class="input" autocomplete="shipping country" :aria-invalid="!!fieldErrors.country" :aria-describedby="fieldErrors.country ? 'shipping-country-error' : undefined" @change="changeCountry" @blur="checkField('country')">
            <option value="" disabled>{{ $t('请选择') }}</option>
            <option v-for="c in countries" :key="c.value" :value="c.value">{{ c.label }}</option>
          </select>
          <span v-if="fieldErrors.country" id="shipping-country-error" class="field-error">{{ $t(fieldErrors.country) }}</span>
        </label>
        <label for="shipping-address">{{ $t('地址第一行') }}
          <input id="shipping-address" v-model="address.address" required maxlength="300" class="input" autocomplete="shipping address-line1" :placeholder="$t('街道名称和门牌号')" :aria-invalid="!!fieldErrors.address" :aria-describedby="fieldErrors.address ? 'shipping-address-error' : undefined" @blur="checkField('address')" />
          <span v-if="fieldErrors.address" id="shipping-address-error" class="field-error">{{ $t(fieldErrors.address) }}</span>
        </label>
        <label for="shipping-address_line2">{{ $t('地址第二行（选填）') }}
          <input id="shipping-address_line2" v-model="address.address_line2" maxlength="300" class="input" autocomplete="shipping address-line2" :placeholder="$t('公寓、套房、单元号等（选填）')" :aria-invalid="!!fieldErrors.address_line2" :aria-describedby="fieldErrors.address_line2 ? 'shipping-address_line2-error' : undefined" @blur="checkField('address_line2')" />
          <span v-if="fieldErrors.address_line2" id="shipping-address_line2-error" class="field-error">{{ $t(fieldErrors.address_line2) }}</span>
        </label>
        <label v-for="field in locationFields" :key="field" :for="`shipping-${field}`">
          {{ $t(fieldNames[field]) }}{{ addressRequired(regions, address.country, field) ? '' : $t('（选填）') }}
          <select v-if="field === 'region' && states.length" :id="`shipping-${field}`" v-model="address.region" :required="addressRequired(regions, address.country, field)" class="input" :autocomplete="`shipping ${fieldAutocomplete[field]}`" :aria-invalid="!!fieldErrors[field]" :aria-describedby="fieldErrors[field] ? `shipping-${field}-error` : undefined" @blur="checkField(field)">
            <option value="" :disabled="addressRequired(regions, address.country, field)">{{ $t('请选择地区') }}</option>
            <option v-for="state in states" :key="state.value" :value="state.value">{{ state.label }}</option>
          </select>
          <input v-else :id="`shipping-${field}`" v-model="address[field]" :required="addressRequired(regions, address.country, field)" :maxlength="field === 'postal_code' ? 30 : 100" class="input" :autocomplete="`shipping ${fieldAutocomplete[field]}`" :aria-invalid="!!fieldErrors[field]" :aria-describedby="fieldErrors[field] ? `shipping-${field}-error` : undefined" @blur="checkField(field)" />
          <span v-if="fieldErrors[field]" :id="`shipping-${field}-error`" class="field-error">{{ $t(fieldErrors[field]) }}</span>
        </label>
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
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 16px;
  min-width: 0;
}
.shipping-fields legend {
  padding: 0 0 12px;
  font-size: 16px;
  font-weight: 600;
}
.shipping-address { grid-template-columns: minmax(0, 1fr); }
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
.shipping-fields .input[aria-invalid="true"] {
  border-color: #b42318;
}
.field-error {
  color: #b42318;
  font-size: 14px;
  line-height: 1.4;
  overflow-wrap: anywhere;
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
