<script setup lang="ts">
import { computed, ref, reactive, watch, onMounted } from "vue";
import {
  NButton,
  NCard,
  NTag,
  NCheckbox,
  NInput,
  NInputNumber,
  NSelect,
  NPopconfirm,
  NAlert,
  NFormItem,
  NSpace,
} from "naive-ui";
import { request } from "@/service/request";
import { createRefund, fetchRefunds } from "@/service/api/order";
import { checkAuth } from "@/directives";
import { formatMoney, centsToYuan, yuanToFen } from "@/utils/money";
import {
  shippingStatus,
  shipments,
  countryOptions,
  regionOptions,
  type RegionData,
} from "../../../../../packages/shipping";
import { newRequestId } from "../../../../../packages/request-id";
const props = defineProps<{ order: any }>();
const emit = defineEmits<{ refresh: [] }>();
const busy = ref(false);
const selected = ref<number[]>([]);
const carrier = ref("");
const tracking = ref("");
let shipKey = "";
const packages = computed(() => shipments(props.order.shipments_json));
const editable = computed(() =>
  [
    "paid",
    "fulfilling",
    "partially_delivered",
    "delivered",
    "completed",
  ].includes(props.order.status),
);
const pending = computed(() =>
  (props.order.items || []).filter(
    (x: any) =>
      x.goods_type === "physical" &&
      Number(x.quantity) >
        Number(x.shipped_quantity || 0) + Number(x.canceled_quantity || 0),
  ),
);
const refundRows = ref<any[]>([]);
const showRefund = ref(false);
let refundKey = "";
const refundHistory = ref<any[]>([]);
const historyLoading = ref(false);
const historyError = ref("");
const historyMore = ref(false);
let historyRequest = 0;
async function loadRefundHistory(append = false) {
  const requestId = ++historyRequest;
  const no = props.order.order_no;
  historyLoading.value = true;
  historyError.value = "";
  const before = append
    ? Number(refundHistory.value.at(-1)?.id || 0)
    : undefined;
  const r = await fetchRefunds(undefined, no, before);
  if (requestId !== historyRequest || no !== props.order.order_no) return;
  historyLoading.value = false;
  if (r.error) {
    historyError.value = "退款记录读取失败，请重试";
    return;
  }
  const rows = (r.data as any)?.refunds || [];
  refundHistory.value = append ? [...refundHistory.value, ...rows] : rows;
  historyMore.value = rows.length === 50;
}
function allocations(row: any): any[] {
  try {
    const value = JSON.parse(row.item_allocations_json || "[]");
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}
function refundStatus(value: string) {
  return (
    (
      {
        created: "待处理",
        processing: "处理中",
        succeeded: "已退款",
        failed: "退款失败",
      } as Record<string, string>
    )[value] || value
  );
}
function refundItemName(id: any) {
  return (
    (props.order.items || []).find((x: any) => String(x.id) === String(id))
      ?.name || `商品项 #${id}`
  );
}
watch(
  () => props.order,
  () => {
    refundHistory.value = [];
    if (checkAuth("order:refund")) void loadRefundHistory();
  },
  { immediate: true },
);
const reason = ref("");
const channel = ref("wallet");
const externalReference = ref("");
const refundFee = ref(0);
const feeMax = computed(() =>
  Math.max(
    0,
    Number(props.order.paid_fee_cents || 0) -
      Number(props.order.refunded_fee_cents || 0),
  ),
);
const refundTotal = computed(() =>
  refundRows.value.reduce(
    (n, r) => n + yuanToFen(r.amount || 0) + yuanToFen(r.shipping || 0),
    0,
  ),
);
const regions = ref<RegionData>({});
const address = reactive<Record<string, string>>({});
const editAddress = ref(false);
const correctionReason = ref("");
watch(
  () => props.order,
  () => {
    selected.value = [];
    showRefund.value = false;
    editAddress.value = false;
    Object.assign(address, props.order.shipping_address || {});
  },
  { immediate: true },
);
onMounted(async () => {
  const r = await request<{ data_json: string }>({
    url: "/api/v1/storefront/shipping/regions",
  });
  if (r.data)
    try {
      regions.value = JSON.parse(r.data.data_json);
    } catch {}
});
async function act(path: string, body: any) {
  if (busy.value) return false;
  busy.value = true;
  try {
    const r = await request({
      url: `/api/v1/admin/fulfillment/${props.order.order_no}/${path}`,
      method: "post",
      data: body,
    });
    if (r.error) return false;
    window.$message?.success("已保存");
    emit("refresh");
    return true;
  } finally {
    busy.value = false;
  }
}
async function ship() {
  try {
    shipKey ||= newRequestId();
    if (
      await act("ship", {
        item_ids: selected.value,
        carrier: carrier.value,
        tracking_no: tracking.value,
        request_key: shipKey,
      })
    ) {
      shipKey = "";
      carrier.value = "";
      tracking.value = "";
    }
  } catch (error) { window.$message?.error(error instanceof Error ? error.message : "发货失败，请重试"); }
}
function startRefund() {
  refundKey = "";
  refundRows.value = (props.order.items || []).map((it: any) => ({
    ...it,
    amount: 0,
    shipping: 0,
    cancel: 0,
    maxAmount: Math.max(
      0,
      Number(it.paid_cents || 0) - Number(it.refunded_cents || 0),
    ),
    maxShipping: Math.max(
      0,
      Number(it.shipping_cents || 0) - Number(it.refunded_shipping_cents || 0),
    ),
    maxCancel:
      it.goods_type === "physical"
        ? Math.max(
            0,
            Number(it.quantity) -
              Number(it.canceled_quantity || 0) -
              Number(it.shipped_quantity || 0),
          )
        : it.fulfillment_status === "pending"
          ? Number(it.quantity) - Number(it.canceled_quantity || 0)
          : 0,
  }));
  channel.value = Number(props.order.user_id) > 0 ? "wallet" : "gateway";
  reason.value = "";
  externalReference.value = "";
  refundFee.value = 0;
  showRefund.value = true;
}
function fillRemaining() {
  for (const r of refundRows.value) {
    r.amount = centsToYuan(r.maxAmount);
    r.shipping = centsToYuan(r.maxShipping);
    r.cancel = r.maxCancel;
  }
}
async function refund() {
  if (busy.value) return;
  busy.value = true;
  try {
    refundKey ||= newRequestId();
    const r = await createRefund({
      request_key: refundKey,
      order_no: props.order.order_no,
      amount_cents: refundTotal.value,
      fee_cents: yuanToFen(refundFee.value || 0),
      expected_refunded_cents: Number(props.order.refunded_cents || 0),
      expected_refunded_fee_cents: Number(props.order.refunded_fee_cents || 0),
      channel: channel.value,
      reason: reason.value,
      external_confirmed: channel.value === "gateway",
      external_reference: externalReference.value,
      item_allocations_json: JSON.stringify(
        refundRows.value
          .filter((r) => r.amount || r.shipping || r.cancel)
          .map((r) => ({
            item_id: Number(r.id),
            amount_cents: yuanToFen(r.amount || 0),
            shipping_cents: yuanToFen(r.shipping || 0),
            cancel_quantity: r.cancel || 0,
          })),
      ),
    });
    if (!r.error) {
      showRefund.value = false;
      window.$message?.success("退款及取消记录已保存");
      emit("refresh");
    }
  } catch (error) {
    window.$message?.error(error instanceof Error ? error.message : "退款请求失败，请重试");
  } finally {
    busy.value = false;
  }
}
const correction = ref<any>();
function editPackage(p: any) {
  correction.value = { ...p };
  correctionReason.value = "";
}
const returnItem = ref<any>();
const returnQty = ref(1);
const returnReason = ref("");
let returnKey = "";
function startReturn(it: any) {
  returnItem.value = it;
  returnQty.value = 1;
  returnReason.value = "";
  returnKey = "";
}
async function restock() {
  try {
    returnKey ||= newRequestId();
    if (
      await act("restock", {
        item_id: returnItem.value.id,
        quantity: returnQty.value,
        reason: returnReason.value,
        request_key: returnKey,
      })
    )
      returnItem.value = null;
  } catch (error) { window.$message?.error(error instanceof Error ? error.message : "入库失败，请重试"); }
}
</script>
<template>
  <NCard title="实体配送" size="small" class="my-16px">
    <NSpace class="mb-12px"
      ><NTag type="info">{{ shippingStatus(order.shipping_status) }}</NTag
      ><span>运费 {{ formatMoney(order.shipping_cents || 0) }}</span></NSpace
    >
    <p class="break-all">
      {{ order.shipping_address?.name }} · {{ order.shipping_address?.phone }} ·
      {{ order.shipping_address?.country }}
      {{ order.shipping_address?.region }} {{ order.shipping_address?.city }}
      {{ order.shipping_address?.district }}
      {{ order.shipping_address?.address }} · 邮编
      {{ order.shipping_address?.postal_code || "无" }}
    </p>
    <NButton
      v-if="editable && !packages.length && checkAuth('order:deliver')"
      size="small"
      class="my-12px"
      @click="editAddress = !editAddress"
      >更正收货地址</NButton
    >
    <div v-if="editAddress" class="shipping-grid">
      <NFormItem label="国家或地区"
        ><NSelect
          v-model:value="address.country"
          disabled
          :options="countryOptions(regions)"
      /></NFormItem>
      <NFormItem label="省 / 州 / 地区"
        ><NSelect
          v-if="regionOptions(regions, address.country || '').length"
          v-model:value="address.region"
          :options="regionOptions(regions, address.country || '')" /><NInput
          v-else
          v-model:value="address.region"
      /></NFormItem>
      <NFormItem
        v-for="f in [
          { key: 'city', label: '城市' },
          { key: 'district', label: '区 / 县' },
          { key: 'address', label: '详细地址' },
          { key: 'name', label: '姓名' },
          { key: 'phone', label: '电话' },
          { key: 'postal_code', label: '邮编' },
        ]"
        :key="f.key"
        :label="f.label"
        ><NInput v-model:value="address[f.key]" :maxlength="300"
      /></NFormItem>
      <NFormItem label="更正原因"
        ><NInput v-model:value="correctionReason" :maxlength="120" /></NFormItem
      ><NButton
        :loading="busy"
        :disabled="!correctionReason.trim()"
        @click="act('shipping', { address, reason: correctionReason })"
        >保存地址</NButton
      >
    </div>
    <div v-for="p in packages" :key="p.id" class="package-row">
      <b
        >包裹 #{{ p.id }} ·
        {{ p.status === "received" ? "已收货" : "已寄出" }}</b
      >
      <p>{{ p.carrier }} · {{ p.tracking_no }}</p>
      <p v-for="(qty, id) in p.items" :key="id">
        {{
          (order.items || []).find((it: any) => String(it.id) === String(id))
            ?.name || `商品项 #${id}`
        }}
        × {{ qty }}
      </p>
      <NSpace v-if="checkAuth('order:deliver') && p.status === 'shipped'"
        ><NButton size="small" @click="editPackage(p)">更正快递信息</NButton
        ><NPopconfirm
          @positive-click="
            act('shipping', {
              shipment_id: p.id,
              received: true,
              reason: '管理员核实包裹已签收',
            })
          "
          ><template #trigger
            ><NButton size="small" :loading="busy"
              >确认已收货</NButton
            ></template
          >已向买家核实收到此包裹？</NPopconfirm
        ></NSpace
      >
    </div>
    <div v-if="correction" class="shipping-grid">
      <NFormItem label="快递公司"
        ><NInput
          v-model:value="correction.carrier"
          :maxlength="100" /></NFormItem
      ><NFormItem label="快递单号"
        ><NInput
          v-model:value="correction.tracking_no"
          :maxlength="100" /></NFormItem
      ><NFormItem label="更正原因"
        ><NInput v-model:value="correctionReason" :maxlength="120" /></NFormItem
      ><NButton
        :disabled="!correctionReason.trim()"
        :loading="busy"
        @click="
          async () => {
            if (
              await act('shipping', {
                shipment_id: correction.id,
                carrier: correction.carrier,
                tracking_no: correction.tracking_no,
                reason: correctionReason,
              })
            )
              correction = null;
          }
        "
        >保存快递信息</NButton
      >
    </div>
    <template v-if="editable && pending.length && checkAuth('order:deliver')">
      <h4 class="my-12px">选择本次寄出的商品（每项发出全部剩余数量）</h4>
      <div v-for="it in pending" :key="it.id" class="my-8px">
        <NCheckbox
          :checked="selected.includes(Number(it.id))"
          @update:checked="
            (v) =>
              (selected = v
                ? [...selected, Number(it.id)]
                : selected.filter((id) => id !== Number(it.id)))
          "
          >{{ it.name }} {{ it.sku_name }} · 待发
          {{
            Number(it.quantity) -
            Number(it.canceled_quantity || 0) -
            Number(it.shipped_quantity || 0)
          }}</NCheckbox
        >
      </div>
      <div class="shipping-grid">
        <NFormItem label="快递公司"
          ><NInput
            v-model:value="carrier"
            placeholder="填写承运公司"
            :maxlength="100" /></NFormItem
        ><NFormItem label="快递单号"
          ><NInput v-model:value="tracking" :maxlength="100"
        /></NFormItem>
      </div>
      <NPopconfirm @positive-click="ship"
        ><template #trigger
          ><NButton
            type="primary"
            :loading="busy"
            :disabled="!selected.length || !carrier.trim() || !tracking.trim()"
            >确认发货</NButton
          ></template
        >确认所选商品已交给快递公司？</NPopconfirm
      >
    </template>
    <div v-if="checkAuth('order:deliver')" class="my-12px">
      <template v-for="it in order.items || []" :key="it.id"
        ><NButton
          v-if="
            it.goods_type === 'physical' &&
            Number(it.shipped_quantity) > Number(it.returned_quantity || 0)
          "
          size="small"
          class="mr-8px"
          @click="startReturn(it)"
          >{{ it.name }}：退货验收入库</NButton
        ></template
      >
    </div>
    <div v-if="returnItem" class="shipping-grid">
      <NFormItem label="验收入库数量"
        ><NInputNumber
          v-model:value="returnQty"
          :min="1"
          :max="
            Number(returnItem.shipped_quantity) -
            Number(returnItem.returned_quantity || 0)
          "
          :precision="0" /></NFormItem
      ><NFormItem label="验收原因"
        ><NInput v-model:value="returnReason" :maxlength="120" /></NFormItem
      ><NPopconfirm @positive-click="restock"
        ><template #trigger
          ><NButton :loading="busy" :disabled="!returnReason.trim()"
            >确认商品已退回并可重新销售</NButton
          ></template
        >此操作会增加可售库存，不会自动退款。</NPopconfirm
      >
    </div>
    <section
      v-if="checkAuth('order:refund')"
      class="my-16px"
      aria-label="退款记录"
    >
      <h3>退款记录</h3>
      <p v-if="historyError" role="alert">
        {{ historyError }}
        <NButton size="small" @click="loadRefundHistory()">重试</NButton>
      </p>
      <p v-else-if="!refundHistory.length">
        {{ historyLoading ? "正在读取…" : "暂无退款记录" }}
      </p>
      <div v-for="row in refundHistory" :key="row.id" class="package-row">
        <b>退款 #{{ row.id }} · {{ refundStatus(row.status) }}</b>
        <p>
          商品
          {{
            formatMoney(
              Number(row.amount_cents || 0) - Number(row.shipping_cents || 0),
            )
          }}
          · 运费 {{ formatMoney(row.shipping_cents || 0) }} · 支付手续费
          {{ formatMoney(row.fee_cents || 0) }}
        </p>
        <p>
          {{ row.channel === "wallet" ? "退至会员余额" : "外部退款登记" }} ·
          {{ new Date(Number(row.created_at) * 1000).toLocaleString() }}
        </p>
        <p v-if="row.upstream_refund_id">凭证：{{ row.upstream_refund_id }}</p>
        <p v-for="a in allocations(row)" :key="a.item_id">
          {{ refundItemName(a.item_id) }}：商品
          {{ formatMoney(a.amount_cents || 0) }}，运费
          {{ formatMoney(a.shipping_cents || 0) }}，取消
          {{ a.cancel_quantity || 0 }} 件
        </p>
        <p v-if="row.reason">原因：{{ row.reason }}</p>
      </div>
      <NButton
        v-if="historyMore"
        :loading="historyLoading"
        @click="loadRefundHistory(true)"
        >加载更早记录</NButton
      >
    </section>
    <NButton
      v-if="
        (editable || order.status === 'refunded') && checkAuth('order:refund')
      "
      type="error"
      class="my-12px"
      @click="startRefund"
      >按商品退款 / 取消未发货数量</NButton
    >
    <div v-if="showRefund">
      <NAlert type="info" class="mb-12px"
        >退款金额、取消未发货数量、退货入库分别记录。已发货商品退款不会自动补回库存。</NAlert
      >
      <NButton size="small" class="mb-12px" @click="fillRemaining"
        >填入全部可退金额及可取消数量</NButton
      >
      <div v-for="r in refundRows" :key="r.id" class="package-row">
        <b>{{ r.name }} {{ r.sku_name }}</b>
        <div class="shipping-grid">
          <NFormItem :label="`退商品金额（最多 ${formatMoney(r.maxAmount)}）`"
            ><NInputNumber
              v-model:value="r.amount"
              :min="0"
              :max="centsToYuan(r.maxAmount)"
              :precision="2" /></NFormItem
          ><NFormItem :label="`退运费（最多 ${formatMoney(r.maxShipping)}）`"
            ><NInputNumber
              v-model:value="r.shipping"
              :min="0"
              :max="centsToYuan(r.maxShipping)"
              :precision="2" /></NFormItem
          ><NFormItem label="取消尚未发货数量"
            ><NInputNumber
              v-model:value="r.cancel"
              :min="0"
              :max="r.maxCancel"
              :precision="0"
          /></NFormItem>
        </div>
      </div>
      <NFormItem
        v-if="refundTotal + yuanToFen(refundFee || 0) > 0"
        label="退款渠道"
        ><NSelect
          v-model:value="channel"
          :options="[
            {
              label: '退至会员余额',
              value: 'wallet',
              disabled: !Number(order.user_id),
            },
            { label: '已在线下或渠道完成退款，登记凭证', value: 'gateway' },
          ]"
      /></NFormItem>
      <NFormItem
        v-if="
          channel === 'gateway' && refundTotal + yuanToFen(refundFee || 0) > 0
        "
        label="渠道退款凭证（须已实际退款成功）"
        ><NInput v-model:value="externalReference" :maxlength="64"
      /></NFormItem>
      <NFormItem label="退支付手续费"
        ><NInputNumber
          v-model:value="refundFee"
          :min="0"
          :max="centsToYuan(feeMax)"
          :precision="2"
      /></NFormItem>
      <NFormItem label="退款 / 取消原因"
        ><NInput v-model:value="reason" :maxlength="180"
      /></NFormItem>
      <p class="my-12px">
        本次退款合计 {{ formatMoney(refundTotal + yuanToFen(refundFee || 0)) }}
      </p>
      <NPopconfirm @positive-click="refund"
        ><template #trigger
          ><NButton
            type="error"
            :loading="busy"
            :disabled="
              !reason.trim() ||
              (channel === 'gateway' &&
                refundTotal + yuanToFen(refundFee || 0) > 0 &&
                !externalReference.trim())
            "
            >确认退款及取消内容</NButton
          ></template
        >{{
          refundTotal + yuanToFen(refundFee || 0) === 0
            ? "仅取消未发货数量，不产生资金退款。"
            : channel === "wallet"
              ? "将实际退入会员余额。"
              : "请确认外部渠道退款已经成功，此处只登记凭证。"
        }}确认退款分摊和取消数量无误？</NPopconfirm
      >
    </div>
  </NCard>
</template>
<style scoped>
.shipping-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 12px;
  align-items: start;
}
.package-row {
  padding: 12px 0;
  border-bottom: 1px solid #8883;
  overflow-wrap: anywhere;
}
.package-row p {
  margin: 6px 0;
}
@media (max-width: 640px) {
  .shipping-grid {
    grid-template-columns: 1fr;
  }
}
</style>
