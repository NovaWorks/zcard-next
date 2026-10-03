<script setup lang="ts">
import { uiText, t as $t } from '@/i18n';

import { computed, ref } from "vue";
import { api, formatMoney } from "@/api/client";
import { getOrderAccessToken } from "@/api";
import { shippingStatus, shipments, addressLines } from "../../../packages/shipping";
const props = defineProps<{ order: any; password: string }>();
const emit = defineEmits<{ refresh: [] }>();
const busy = ref(false);
const error = ref("");
const packages = computed(() => shipments(props.order.shipments_json));
async function receive(id: number) {
  if (busy.value || !confirm($t("确认已收到此包裹中的商品？"))) return;
  busy.value = true;
  const r = await api.post(`/orders/${props.order.order_no}/receive`, {
    shipment_id: id,
    query_password: props.password,
    order_access_token: getOrderAccessToken(props.order.order_no),
  });
  busy.value = false;
  if (r.error) {
    error.value = r.error;
    return;
  }
  error.value = "";
  emit("refresh");
}
</script>
<template>
  <section class="card shipping-details" :aria-label="$t('收货及配送信息')">
    <h3>{{ $t('实体配送 ·') }} {{ $t(shippingStatus(order.shipping_status) || '') }}</h3>
    <p>{{ order.shipping_address?.name }} · {{ order.shipping_address?.phone }}</p>
    <p v-for="(line, index) in addressLines(order.shipping_address || {})" :key="index">{{ line }}</p>
    <p>{{ $t('运费') }} {{ formatMoney(order.shipping_cents || 0) }}</p>
    <p v-for="it in (order.items || []).filter((i: any) => i.goods_type === 'physical')" :key="it.id" class="muted">
      {{ it.product_name }}{{ $t('：购买') }} {{ it.quantity }} {{ $t('件 · 已发') }} {{ it.shipped_quantity || 0 }} {{ $t('件 · 已收') }} {{ it.received_quantity || 0 }} {{ $t('件') }}<span v-if="it.canceled_quantity"> {{ $t('· 已取消') }} {{ it.canceled_quantity }} {{ $t('件') }}</span><span v-if="it.returned_quantity"> {{ $t('· 已退货') }} {{ it.returned_quantity }} {{ $t('件') }}</span>
    </p>
    <p v-if="!packages.length" class="muted">
      {{
        order.status === "pending_payment"
          ? $t('付款后安排发货')
          : order.shipping_status === "canceled"
            ? $t('实体商品配送已取消')
            : $t('等待商家寄出，发货后将在此显示快递信息。')
      }}
    </p>
    <div v-for="p in packages" :key="p.id" class="shipment">
      <b>{{ $t('包裹 #') }}{{ p.id }} · {{ p.status === "received" ? $t('已收货') : $t('已寄出') }}</b>
      <p>
        {{ p.carrier }} · <span class="tracking">{{ p.tracking_no }}</span>
      </p>
      <p v-for="(qty, id) in p.items" :key="id">
        {{
          (order.items || []).find((i: any) => String(i.id) === String(id))?.product_name ||
          $t('商品项 #{0}', [id])
        }}
        × {{ qty }}
      </p>
      <button
        v-if="p.status === 'shipped'"
        class="btn secondary"
        :disabled="busy"
        @click="receive(p.id)"
      > {{ $t('确认收到此包裹') }} </button>
    </div>
    <p v-if="error" class="error" role="alert">{{ uiText(error) }}</p>
  </section>
</template>
<style scoped>
.shipping-details p {
  margin: 8px 0;
  overflow-wrap: anywhere;
}
.shipping-details h3 {
  margin: 0 0 16px;
}
.shipment {
  border-top: 1px solid #e5e7eb;
  padding: 16px 0;
}
.tracking {
  user-select: all;
  font-family: monospace;
}
</style>
