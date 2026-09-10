<script setup lang="ts">
import { ref, watch, computed } from "vue";
import { NModal, NForm, NFormItem, NInput, NSelect, NButton, NAlert } from "naive-ui";
import { fetchOrder, manualDeliver } from "@/service/api";

const props = defineProps<{ show: boolean; orderNo: string }>();
const emit = defineEmits<{ 'update:show': [boolean]; delivered: [] }>();
const loading = ref(false);
const saving = ref(false);
const error = ref('');
const items = ref<any[]>([]);
const itemId = ref<number | null>(null);
const content = ref('');
const logistics = ref('');
const remark = ref('');
const options = computed(() => items.value.map(it => ({
  label: `${it.name || '#' + it.product_id}${it.sku_name ? ' / ' + it.sku_name : ''} · 购买 ${it.quantity} 件${it.fulfillment_status === 'delivered' ? '（已发货）' : ''}`,
  value: Number(it.id), disabled: it.fulfillment_status === 'delivered' || it.fulfillment_status === 'refunded',
})));
watch(() => [props.show, props.orderNo], async () => {
  if (!props.show || !props.orderNo) return;
  loading.value = true;
  error.value = ''; items.value = []; itemId.value = null;
  content.value = ''; logistics.value = ''; remark.value = '';
  try {
    const { data, error: err } = await fetchOrder(props.orderNo);
    if (err || !data) { error.value = '读取订单失败，请关闭后重试'; return; }
    items.value = (data as any).items || [];
    itemId.value = options.value.find(it => !it.disabled)?.value ?? null;
  } finally { loading.value = false; }
});
async function submit() {
  if (saving.value || !itemId.value) return;
  if (!content.value.trim() === !logistics.value.trim()) { error.value = '卡密内容与物流单号必须且只能填写一项'; return; }
  saving.value = true; error.value = '';
  try {
    const { error: err } = await manualDeliver(props.orderNo, { order_item_id: itemId.value, content: content.value.trim() || undefined, logistics_no: logistics.value.trim() || undefined, remark: remark.value.trim() || undefined });
    if (err) { error.value = '补发未成功，请按错误提示检查后重试'; return; }
    window.$message?.success('补发成功，买家可凭原订单号和查询密码取货');
    emit('update:show', false); emit('delivered');
  } finally { saving.value = false; }
}
</script>

<template>
  <NModal :show="show" @update:show="v => !saving && emit('update:show', v)" preset="card" title="人工补发" style="width: min(640px, calc(100vw - 24px))">
    <NAlert type="info" class="mb-16px">补发内容归属所选商品，每行一条卡密，数量须与待发数量一致。买家使用原订单取货，无需重新下单或付款。</NAlert>
    <NAlert v-if="error" type="error" class="mb-12px">{{ error }}</NAlert>
    <NForm label-placement="top" :disabled="loading || saving">
      <NFormItem label="订单号"><NInput :value="orderNo" disabled /></NFormItem>
      <NFormItem label="补发商品"><NSelect v-model:value="itemId" :options="options" :loading="loading" placeholder="请选择商品" /></NFormItem>
      <NFormItem label="卡密内容（每行一条）"><NInput v-model:value="content" type="textarea" :rows="6" placeholder="粘贴需要补发的卡密" /></NFormItem>
      <NFormItem label="物流单号（与卡密内容二选一）"><NInput v-model:value="logistics" /></NFormItem>
      <NFormItem label="处理备注"><NInput v-model:value="remark" placeholder="例如：上游余额不足，人工补卡" /></NFormItem>
    </NForm>
    <template #footer><div class="flex justify-end gap-8px"><NButton :disabled="saving" @click="emit('update:show', false)">取消</NButton><NButton type="primary" :loading="saving" :disabled="loading || !itemId" @click="submit">确认补发</NButton></div></template>
  </NModal>
</template>
