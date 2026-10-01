<script setup lang="ts">
// 实体快递发货进入订单配送面板；虚拟商品继续使用人工交付弹窗。
import { onMounted, ref, h, watch } from "vue";
import { NButton, NDataTable, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchPendingDeliveries } from "@/service/api";
import ManualDeliverDialog from "./manual-deliver-dialog.vue";
import { checkAuth } from "@/directives";

defineOptions({ name: "PendingDeliverTab" });
const props = defineProps<{ refreshKey: number }>();
const emit = defineEmits<{ openOrder: [orderNo: string] }>();

const loading = ref(false);
const page = ref(1);
const pageSize = 20;
const orders = ref<any[]>([]);
const hasMore = ref(false);

const showDeliver = ref(false);
const target = ref<any>(null);

const columns: DataTableColumns<any> = [
  {
    title: "订单号",
    key: "order_no",
    width: 210,
    render: (row) => h("span", { class: "text-13px" }, row.order_no),
  },
  { title: "商品", key: "product_name", minWidth: 160 },
  { title: "规格", key: "sku_name", width: 110 },
  { title: "交付方式", key: "goods_type", width: 100, render: (row) => row.goods_type === "physical" ? "快递配送" : "人工交付" },
  { title: "处理进度", key: "fulfillment_status", width: 110, render: (row) => row.goods_type === "physical" ? "待寄出" : row.fulfillment_status === "delivering" ? "处理中" : "待处理" },
  {
    title: "待发数量",
    key: "quantity",
    width: 64,
    render: (row) =>
      h(NTag, { size: "small", type: "warning" }, { default: () => `×${row.quantity}` }),
  },
  {
    title: "下单时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
  {
    title: "操作",
    key: "actions",
    width: 110,
    render: (row) =>
      row.goods_type === "physical" && checkAuth("order:deliver") && !checkAuth("order:read_detail")
        ? h(NTag, { size: "small", type: "warning" }, { default: () => "需详情权限" })
        : checkAuth("order:deliver")
        ? h(
            NButton,
            { size: "small", type: "primary", onClick: () => openDeliver(row) },
            { default: () => row.goods_type === "physical" ? "快递发货" : "手动发货" },
          )
        : null,
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchPendingDeliveries(page.value, pageSize);
    orders.value = !error && data ? (data as any).orders || [] : [];
    hasMore.value = !error && data ? Boolean((data as any).has_more) : false;
  } finally {
    loading.value = false;
  }
}

function openDeliver(row: any) {
  if (row.goods_type === "physical") {
    emit("openOrder", row.order_no);
    return;
  }
  target.value = row;
  showDeliver.value = true;
}

onMounted(load);
watch(() => props.refreshKey, load);
</script>

<template>
  <div>
    <NDataTable :columns="columns" :data="orders" :loading="loading" :scroll-x="1020" />
    <div class="mt-8px flex items-center justify-between">
      <span class="text-12px text-gray-400">第 {{ page }} 页 · 按订单分页</span>
      <div class="flex gap-8px">
        <NButton size="small" :disabled="loading || page <= 1" @click="page--, load()">上一页</NButton>
        <NButton size="small" :disabled="loading || !hasMore" @click="page++, load()">
          下一页
        </NButton>
      </div>
    </div>

    <ManualDeliverDialog v-model:show="showDeliver" :order-no="target?.order_no || ''" :default-item-id="Number(target?.order_item_id || 0)" @delivered="load" />
  </div>
</template>
