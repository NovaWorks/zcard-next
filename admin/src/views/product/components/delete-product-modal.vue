<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { NAlert, NButton, NModal, NSpace, NSpin } from "naive-ui";
import { deleteProduct, previewDeleteProduct, type ProductDeletePreview } from "@/service/api/catalog";

const props = defineProps<{ show: boolean; product: { id: number; name: string } | null }>();
const emit = defineEmits<{ (e: "update:show", value: boolean): void; (e: "deleted"): void }>();
const preview = ref<ProductDeletePreview | null>(null);
const loading = ref(false);
const deleting = ref(false);
const loadError = ref("");
const orderCount = computed(() => Number(preview.value?.order_count || 0));
let generation = 0;
async function load() {
  const current = ++generation;
  preview.value = null;
  loadError.value = "";
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
async function submit() {
  if (deleting.value || loading.value || !props.product || !preview.value) return;
  deleting.value = true;
  try {
    const { error } = await deleteProduct(props.product.id);
    if (!error) {
      window.$message?.success("商品已删除，订单快照及交付记录已保留");
      emit("deleted");
      emit("update:show", false);
    } else await load();
  } finally { deleting.value = false; }
}
</script>

<template>
  <NModal :show="show" preset="card" title="删除商品" style="width: 560px; max-width: 94vw"
    :closable="!deleting" :mask-closable="false" :close-on-esc="!deleting" @update:show="!$event && close()">
    <NSpin :show="loading">
      <div class="delete-content">
        <p class="product-name">{{ preview?.name || product?.name }}</p>
        <NAlert v-if="loadError" type="error" :bordered="false">{{ loadError }} <NButton size="small" @click="load">重新加载</NButton></NAlert>
        <template v-if="preview">
          <p>保留关联订单：<strong>{{ orderCount }}</strong> 条</p>
          <NAlert type="warning" :bordered="false">确认后商品立即从商城、商品管理和供货目录移除，详情链接失效。商品锁定、待付款或发货中的订单不会阻止删除。</NAlert>
          <p>保留订单快照、交付内容、取货及退款记录，已有订单继续处理；账户余额不变。删除后无法通过上下架或自动同步恢复商品。</p>
        </template>
      </div>
    </NSpin>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="deleting" @click="close">取消</NButton>
        <NButton type="error" :loading="deleting" :disabled="loading || !preview" @click="submit">确认删除（保留订单）</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.delete-content { display: flex; flex-direction: column; gap: 12px; max-height: 65dvh; overflow: auto; line-height: 1.7; }
.product-name { font-weight: 600; overflow-wrap: anywhere; }
</style>
