<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { NAlert, NButton, NInput, NModal, NSpace, NSpin } from "naive-ui";
import { deleteProduct, previewDeleteProduct, type ProductDeletePreview } from "@/service/api/catalog";

const props = defineProps<{ show: boolean; product: { id: number; name: string } | null }>();
const emit = defineEmits<{ (e: "update:show", value: boolean): void; (e: "deleted"): void }>();
const preview = ref<ProductDeletePreview | null>(null);
const loading = ref(false);
const deleting = ref(false);
const loadError = ref("");
const withOrders = ref(false);
const confirmName = ref("");
const orderCount = computed(() => Number(preview.value?.order_count || 0));
let generation = 0;
async function load() {
  const current = ++generation;
  preview.value = null;
  loadError.value = "";
  withOrders.value = false;
  confirmName.value = "";
  if (!props.show || !props.product) { loading.value = false; return; }
  loading.value = true;
  try {
    const { data, error } = await previewDeleteProduct(props.product.id);
    if (current !== generation) return;
    if (error || !data) loadError.value = "无法读取删除影响，请重新加载后再操作。";
    else preview.value = data;
  } finally {
    if (current === generation) loading.value = false;
  }
}
watch(() => [props.show, props.product?.id], load, { immediate: true });
function close() { if (!deleting.value) emit("update:show", false); }
async function submit(deleteOrders: boolean) {
  if (deleting.value || loading.value || !props.product || !preview.value) return;
  if (preview.value.delete_block_reason || (deleteOrders && (preview.value.delete_orders_block_reason || confirmName.value !== preview.value.name))) return;
  deleting.value = true;
  try {
    const { error } = await deleteProduct(props.product.id, {
      delete_orders: deleteOrders,
      confirm_name: deleteOrders ? confirmName.value : "",
      expected_order_count: orderCount.value,
    });
    if (!error) {
      window.$message?.success(deleteOrders ? `商品及 ${orderCount.value} 条关联订单已删除` : "商品已删除，历史订单已保留");
      emit("deleted");
      emit("update:show", false);
    } else await load(); // 重新读取状态和数量，不能用过期预览再次提交。
  } finally { deleting.value = false; }
}
</script>

<template>
  <NModal :show="show" preset="card" :title="withOrders ? '确认删除关联订单' : '删除商品'" style="width: 640px; max-width: 94vw"
    :closable="!deleting" :mask-closable="false" :close-on-esc="!deleting" @update:show="!$event && close()">
    <NSpin :show="loading">
      <div class="delete-content">
        <p class="product-name">{{ preview?.name || product?.name }}</p>
        <NAlert v-if="loadError" type="error" :bordered="false">{{ loadError }} <NButton size="small" @click="load">重新加载</NButton></NAlert>
        <template v-if="preview">
          <p>关联订单：<strong>{{ orderCount }}</strong> 条</p>
          <NAlert v-if="preview.delete_block_reason" type="warning" :bordered="false">{{ preview.delete_block_reason }}</NAlert>
          <template v-if="!withOrders">
            <p><strong>仅删除商品：</strong>从商城和商品管理移除，商品详情链接失效；保留历史订单、已交付内容和取货记录，方便查询与售后。</p>
            <p><strong>删除商品及关联订单：</strong>同时删除关联订单、取货、退款及采购明细，客户将无法查询或取货，无法撤销。</p>
            <p class="text-12px text-gray-500">支付幂等记录及钱包、积分、佣金等账务流水保留，账户余额不变。更换供货商通常选择“仅删除商品”即可。</p>
            <NAlert v-if="!preview.delete_block_reason && preview.delete_orders_block_reason" type="info" :bordered="false">{{ preview.delete_orders_block_reason }}</NAlert>
          </template>
          <template v-else>
            <NAlert type="error" :bordered="false">将永久删除此商品的 {{ orderCount }} 条关联订单及取货记录，客户将无法查询或取货。此操作无法撤销，请确认已完成所需备份。</NAlert>
            <label for="delete-product-confirm">请输入完整商品名称以确认删除</label>
            <NInput v-model:value="confirmName" :input-props="{ id: 'delete-product-confirm' }" :disabled="deleting" placeholder="输入上方完整商品名称" />
          </template>
        </template>
      </div>
    </NSpin>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="deleting" @click="withOrders ? (withOrders = false) : close()">{{ withOrders ? '返回' : '取消' }}</NButton>
        <template v-if="!withOrders">
          <NButton type="error" secondary :disabled="loading || deleting || !preview || !!preview.delete_orders_block_reason || !orderCount" @click="withOrders = true">删除商品及关联订单</NButton>
          <NButton type="primary" :loading="deleting" :disabled="loading || !preview || !!preview.delete_block_reason" @click="submit(false)">仅删除商品（保留订单）</NButton>
        </template>
        <NButton v-else type="error" :loading="deleting" :disabled="loading || !preview || confirmName !== preview.name || !!preview.delete_orders_block_reason" @click="submit(true)">确认永久删除 {{ orderCount }} 条订单</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.delete-content { display: flex; flex-direction: column; gap: 12px; max-height: 65dvh; overflow: auto; line-height: 1.7; }
.product-name { font-weight: 600; overflow-wrap: anywhere; }
</style>
