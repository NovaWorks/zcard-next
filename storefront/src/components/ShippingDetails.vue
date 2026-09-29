<script setup lang="ts">
import { computed, ref } from "vue";
import { api, formatMoney } from "@/api/client";
import { shippingStatus, shipments } from "../../../packages/shipping";
const props = defineProps<{ order: any; password: string }>();
const emit = defineEmits<{ refresh: [] }>();
const busy = ref(false);
const error = ref("");
const packages = computed(() => shipments(props.order.shipments_json));
async function receive(id: number) {
  if (busy.value || !confirm("确认已收到此包裹中的商品？")) return;
  busy.value = true;
  const r = await api.post(`/orders/${props.order.order_no}/receive`, {
    shipment_id: id,
    query_password: props.password,
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
  <section class="card shipping-details" aria-label="收货及配送信息">
    <h3>实体配送 · {{ shippingStatus(order.shipping_status) }}</h3>
    <p>{{ order.shipping_address?.name }} · {{ order.shipping_address?.phone }}</p>
    <p>
      {{ order.shipping_address?.country }} {{ order.shipping_address?.region }}
      {{ order.shipping_address?.city }} {{ order.shipping_address?.district }}
      {{ order.shipping_address?.address }}
    </p>
    <p>
      邮编：{{ order.shipping_address?.postal_code || "无需填写" }} · 运费
      {{ formatMoney(order.shipping_cents || 0) }}
    </p>
    <p v-if="!packages.length" class="muted">
      {{
        order.status === "pending_payment"
          ? "付款后安排发货"
          : order.shipping_status === "canceled"
            ? "实体商品配送已取消"
            : "等待商家寄出，发货后将在此显示快递信息。"
      }}
    </p>
    <div v-for="p in packages" :key="p.id" class="shipment">
      <b>包裹 #{{ p.id }} · {{ p.status === "received" ? "已收货" : "已寄出" }}</b>
      <p>
        {{ p.carrier }} · <span class="tracking">{{ p.tracking_no }}</span>
      </p>
      <p v-for="(qty, id) in p.items" :key="id">
        {{
          (order.items || []).find((i: any) => String(i.id) === String(id))?.product_name ||
          `商品项 #${id}`
        }}
        × {{ qty }}
      </p>
      <button
        v-if="p.status === 'shipped'"
        class="btn secondary"
        :disabled="busy"
        @click="receive(p.id)"
      >
        确认收到此包裹
      </button>
    </div>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
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
