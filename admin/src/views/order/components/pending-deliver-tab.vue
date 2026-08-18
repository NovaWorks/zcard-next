<script setup lang="ts">
// 待发货列表 + 手动发货（fulfillment 域：order:view_delivery / order:deliver 超管专属）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchPendingDeliveries, manualDeliver } from "@/service/api";
import { checkAuth } from "@/directives";

defineOptions({ name: "PendingDeliverTab" });

const loading = ref(false);
const page = ref(1);
const pageSize = 20;
const orders = ref<any[]>([]);

const showDeliver = ref(false);
const delivering = ref(false);
const target = ref<any>(null);
const form = ref({ content: "", logistics_no: "", remark: "" });

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
  form.value = { content: "", logistics_no: "", remark: "" };
  showDeliver.value = true;
}

async function handleDeliver() {
  if (!target.value) return;
  if (!form.value.content.trim() && !form.value.logistics_no.trim()) {
    window.$message?.warning("卡密内容与物流单号至少填一项");
    return;
  }
  delivering.value = true;
  try {
    const { error } = await manualDeliver(target.value.order_no, {
      content: form.value.content.trim() || undefined,
      logistics_no: form.value.logistics_no.trim() || undefined,
      remark: form.value.remark.trim() || undefined,
    });
    if (!error) {
      window.$message?.success(`订单 ${target.value.order_no} 已发货`);
      showDeliver.value = false;
      load();
    }
  } finally {
    delivering.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div>
    <NDataTable :columns="columns" :data="orders" :loading="loading" />
    <div class="mt-8px flex items-center justify-between">
      <span class="text-12px text-gray-400">第 {{ page }} 页</span>
      <div class="flex gap-8px">
        <NButton size="small" :disabled="page <= 1" @click="page--, load()">上一页</NButton>
        <NButton size="small" :disabled="orders.length < pageSize" @click="page++, load()">
          下一页
        </NButton>
      </div>
    </div>

    <!-- 手动发货（卡密内容多行=每行一条；或物流单号二选一） -->
    <NModal
      v-model:show="showDeliver"
      preset="dialog"
      :title="`手动发货：${target?.product_name || ''} ×${target?.quantity || 0}`"
      style="width: 520px"
    >
      <NForm label-placement="top">
        <NFormItem label="订单号">
          <NInput :value="target?.order_no" disabled />
        </NFormItem>
        <NFormItem label="卡密内容（多行，每行一条）">
          <NInput
            v-model:value="form.content"
            type="textarea"
            :rows="4"
            placeholder="与物流单号二选一；交付即走取货三重门（掩码默认，买家凭订单号+查询密码取货）"
          />
        </NFormItem>
        <NFormItem label="物流单号">
          <NInput v-model:value="form.logistics_no" placeholder="实体/卡板类填此处" />
        </NFormItem>
        <NFormItem label="备注">
          <NInput v-model:value="form.remark" placeholder="选填" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showDeliver = false">取消</NButton>
        <NButton type="primary" :loading="delivering" @click="handleDeliver">确认发货</NButton>
      </template>
    </NModal>
  </div>
</template>
