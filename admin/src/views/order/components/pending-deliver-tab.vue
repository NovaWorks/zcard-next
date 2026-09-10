<script setup lang="ts">
// 待发货列表 + 手动发货（fulfillment 域：order:view_delivery / order:deliver 超管专属）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchPendingDeliveries, manualDeliver } from "@/service/api";
import ManualDeliverDialog from "./manual-deliver-dialog.vue";
import { checkAuth } from "@/directives";

defineOptions({ name: "PendingDeliverTab" });

const loading = ref(false);
const page = ref(1);
const pageSize = 20;
const orders = ref<any[]>([]);

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
  {
    title: "数量",
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
      checkAuth("order:deliver")
        ? h(
            NButton,
            { size: "small", type: "primary", onClick: () => openDeliver(row) },
            { default: () => "手动发货" },
          )
        : null,
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchPendingDeliveries(page.value, pageSize);
    if (!error && data) orders.value = (data as any).orders || [];
  } finally {
    loading.value = false;
  }
}

function openDeliver(row: any) {
  target.value = row;
  showDeliver.value = true;
}

onMounted(load);
</script>

<template>
  <div>
    <NDataTable :columns="columns" :data="orders" :loading="loading"  :scroll-x="700" />
    <div class="mt-8px flex items-center justify-between">
      <span class="text-12px text-gray-400">第 {{ page }} 页</span>
      <div class="flex gap-8px">
        <NButton size="small" :disabled="page <= 1" @click="page--, load()">上一页</NButton>
        <NButton size="small" :disabled="orders.length < pageSize" @click="page++, load()">
          下一页
        </NButton>
      </div>
    </div>

    <ManualDeliverDialog v-model:show="showDeliver" :order-no="target?.order_no || ''" @delivered="load" />
  </div>
</template>
