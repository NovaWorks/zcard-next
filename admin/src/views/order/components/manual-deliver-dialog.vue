<script setup lang="ts">
import { ref, watch, computed } from "vue";
import { NModal, NForm, NFormItem, NInput, NSelect, NButton, NAlert, NPopconfirm } from "naive-ui";
import OrderAnswers from '@/components/common/order-answers.vue';
import { fetchOrder, manualDeliver,startService, fetchProcurements, markProcurementManual, retryProcurement } from "@/service/api";

import { checkAuth } from "@/directives";

const props = defineProps<{ show: boolean; orderNo: string; defaultItemId?: number }>();
const emit = defineEmits<{ 'update:show': [boolean]; delivered: [] }>();
let requestSeq = 0;
const loading = ref(false);
const saving = ref(false);
const error = ref('');
const items = ref<any[]>([]);
const itemId = ref<number | null>(null);
const content = ref('');
const serviceContent=ref(''),deliveryKind=ref('service');
const selectedItem=computed(()=>items.value.find(it=>Number(it.id)===Number(itemId.value)));
const needsProcurementCheck = computed(() => selectedItem.value?.fulfillment_type === "upstream");
const procurements = ref<any[]>([]);
const procurementLoading = ref(false);
const procurementError = ref('');
const selectedProcurement = computed(() => procurements.value.find(p => String(p.order_item_id) === String(itemId.value)));
const procurementBlocked = computed(() => !!selectedProcurement.value && !['manual','rejected','refunding'].includes(selectedProcurement.value.status));
const canManageProcurement = computed(() => checkAuth('procurement:write'));
const canTransfer = computed(() => selectedProcurement.value && (['pending','submitted','polling'].includes(selectedProcurement.value.status) || (selectedProcurement.value.status === 'fulfilled' && selectedProcurement.value.delivery_incomplete)));
const canRecover = computed(() => canTransfer.value && Number(selectedProcurement.value?.received_cards) > 0);
const messageOf = (err: any, fallback: string) => err?.response?.data?.message || err?.message || fallback;
async function loadProcurements(seq = requestSeq) {
  if (!checkAuth('procurement:read')) return;
  procurementLoading.value = true; procurementError.value = '';
  const collected: any[] = [];
  try {
    for (let page = 1; ; page++) {
      const { data, error: err } = await fetchProcurements({order_no: props.orderNo, page, page_size: 100});
      if (seq !== requestSeq) return;
      if (err || !data) { procurementError.value = '读取采购状态失败，请重试后补发'; return; }
      const rows = (data as any).procurements || [];
      collected.push(...rows);
      if (!rows.length || collected.length >= Number((data as any).total || 0)) break;
    }
    procurements.value = collected;
  } finally { if (seq === requestSeq) procurementLoading.value = false; }
}
async function transferManual() {
  if (!selectedProcurement.value || saving.value) return;
  saving.value = true; error.value = '';
  try {
    const {error: err} = await markProcurementManual(selectedProcurement.value.id, '管理员在订单详情核实上游结果后转人工补发');
    if (err) { error.value = messageOf(err, '转人工失败，请刷新核对'); return; }
    await loadProcurements();
    emit('delivered');
  } finally { saving.value = false; }
}
async function recoverDelivery() {
  if (!selectedProcurement.value || saving.value) return;
  saving.value = true; error.value = '';
  try {
    const {error: err} = await retryProcurement(selectedProcurement.value.id);
    if (err) { error.value = messageOf(err, '重试交付失败，请核实后转人工'); return; }
    emit('update:show', false); emit('delivered');
    window.$message?.success('已处理交付恢复，请核对订单状态');
  } finally { saving.value = false; }
}
const isManual=computed(()=>selectedItem.value?.fulfillment_type==='manual');
async function start(){if(saving.value||!itemId.value)return;saving.value=true;try{const {error:err}=await startService(props.orderNo,itemId.value);if(!err){const {data}=await fetchOrder(props.orderNo);items.value=(data as any)?.items || [];emit('delivered');}}finally{saving.value=false}}
const logistics = ref('');
const remark = ref('');
const options = computed(() => items.value.map(it => ({
  label: `${it.name || '#' + it.product_id}${it.sku_name ? ' / ' + it.sku_name : ''} · 购买 ${it.quantity} 件${it.fulfillment_status === 'delivered' ? '（已发货）' : ''}`,
  value: Number(it.id), disabled: it.fulfillment_status === 'delivered' || it.fulfillment_status === 'refunded',
})));
watch(() => [props.show, props.orderNo, props.defaultItemId], async () => {
  const seq = ++requestSeq;
  if (!props.show || !props.orderNo) return;
  loading.value = true;
  error.value = ''; items.value = []; itemId.value = null; procurements.value = []; procurementError.value = ''; procurementLoading.value = false;
  content.value = ''; logistics.value = ''; remark.value = '';serviceContent.value='';deliveryKind.value='service';
  try {
    const { data, error: err } = await fetchOrder(props.orderNo);
    if (seq !== requestSeq) return;
    if (err || !data) { error.value = '读取订单失败，请关闭后重试'; return; }
    items.value = (data as any).items || [];
    itemId.value = props.defaultItemId
      ? options.value.find(it => it.value === Number(props.defaultItemId) && !it.disabled)?.value ?? null
      : options.value.find(it => !it.disabled)?.value ?? null;
    if (!itemId.value) error.value = "该采购商品已发货或不可补发，请刷新采购单核对状态";
    if (items.value.some(it => it.fulfillment_type === "upstream")) await loadProcurements(seq);
  } finally { if (seq === requestSeq) loading.value = false; }
});
async function submit() {
  if (saving.value || !itemId.value || (needsProcurementCheck.value && (procurementLoading.value || procurementError.value)) || procurementBlocked.value) return;
  if (!(isManual.value && deliveryKind.value==='service') && !content.value.trim() === !logistics.value.trim()) { error.value = '卡密内容与物流单号必须且只能填写一项'; return; }
  saving.value = true; error.value = '';
  try {
    const { error: err } = await manualDeliver(props.orderNo, { order_item_id: itemId.value,service_content:isManual.value&&deliveryKind.value==='service'?serviceContent.value.trim():undefined, content: (!isManual.value||deliveryKind.value==='card')?content.value.trim() || undefined:undefined, logistics_no:!isManual.value?logistics.value.trim() || undefined:undefined, remark: remark.value.trim() || undefined });
    if (err) { error.value = messageOf(err, '补发未成功，请检查后重试'); return; }
    window.$message?.success(isManual.value ? '服务已完成，买家可在原订单查看交付结果' : '补发成功，买家可凭原订单查询');
    emit('update:show', false); emit('delivered');
  } finally { saving.value = false; }
}
</script>

<template>
  <NModal :show="show" @update:show="v => !saving && emit('update:show', v)" preset="card" :title="isManual ? '处理人工服务' : '人工补发'" style="width: min(640px, calc(100vw - 24px))">
    <NAlert v-if="!isManual" type="info" class="mb-16px">补发内容归属所选商品，每行一条卡密，数量须与待发数量一致。买家使用原订单取货，无需重新下单或付款。</NAlert>
    <NAlert v-if="error" type="error" class="mb-12px">{{ error }}</NAlert>
    <NForm label-placement="top" :disabled="loading || saving">
      <NFormItem label="订单号"><NInput :value="orderNo" disabled /></NFormItem>
      <NFormItem label="补发商品"><NSelect v-model:value="itemId" :options="options" :disabled="!!defaultItemId" :loading="loading" placeholder="请选择商品" /></NFormItem>
      <NAlert v-if="needsProcurementCheck && procurementError" type="error" class="mb-12px">{{ procurementError }} <NButton text @click="loadProcurements()">重试</NButton></NAlert>
      <NAlert v-if="needsProcurementCheck && checkAuth('procurement:read') && !loading && !procurementLoading && !procurementError && !selectedProcurement" type="info" class="mb-12px">未找到该商品的采购单。请先核实上游是否扣款或出货，再填写补发内容。</NAlert>
      <NAlert v-if="procurementBlocked" type="warning" class="mb-12px">
        该商品关联采购单 #{{ selectedProcurement.id }}。采购仍在处理或已返回结果，请先核对上游，避免重复发货。
        <div v-if="canManageProcurement" class="mt-8px flex gap-8px flex-wrap">
          <NButton v-if="canRecover" :loading="saving" @click="recoverDelivery">重试已有卡密交付</NButton>
          <NPopconfirm v-if="canTransfer" @positive-click="transferManual"><template #trigger><NButton :disabled="saving">核实后转人工</NButton></template>确认已核实上游扣款及出货结果，需要停止自动交付并人工补发？</NPopconfirm>
        </div>
        <p v-else>请让有采购管理权限的管理员核实并转人工。</p>
      </NAlert>
      <div v-if="selectedItem"><b>买家填写资料</b><OrderAnswers :value="selectedItem.form_answers_json" /></div>
      <NAlert v-if="isManual" type="info" class="my-12px">先核对资料并开始处理，完成实际服务后再提交交付结果。内部备注不会显示给买家。</NAlert>
      <NButton v-if="isManual && selectedItem?.fulfillment_status === 'pending'" class="mb-12px" :loading="saving" @click="start">开始处理</NButton>
      <NFormItem v-if="isManual" label="交付内容类型"><NSelect v-model:value="deliveryKind" :options="[{label:'服务完成说明',value:'service'},{label:'卡密',value:'card'}]" /></NFormItem>
      <NFormItem v-if="isManual && deliveryKind === 'service'" label="交付说明（买家可见）"><NInput v-model:value="serviceContent" type="textarea" :rows="5" :maxlength="4000" placeholder="如：已完成开通；能量交付可附交易编号" /></NFormItem>
      <NFormItem v-if="!isManual || deliveryKind === 'card'" label="卡密内容（每行一条）"><NInput v-model:value="content" type="textarea" :rows="6" placeholder="粘贴需要补发的卡密" /></NFormItem>
      <NFormItem v-if="!isManual" label="物流单号（与卡密内容二选一）"><NInput v-model:value="logistics" /></NFormItem>
      <NFormItem label="内部处理备注"><NInput v-model:value="remark" placeholder="例如：上游余额不足，人工补卡" /></NFormItem>
    </NForm>
    <template #footer><div class="flex justify-end gap-8px"><NButton :disabled="saving" @click="emit('update:show', false)">取消</NButton><NButton type="primary" :loading="saving" :disabled="loading || (needsProcurementCheck && (procurementLoading || !!procurementError)) || procurementBlocked || !itemId || (isManual && (selectedItem?.fulfillment_status !== 'delivering' || (deliveryKind === 'service' && !serviceContent.trim())))" @click="submit">{{isManual ? '确认完成' : '确认补发'}}</NButton></div></template>
  </NModal>
</template>
