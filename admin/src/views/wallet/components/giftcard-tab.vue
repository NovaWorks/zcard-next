<script setup lang="ts">
// 礼品卡批次（giftcard:read / giftcard:write 超管专属；明文码仅创建时一次性返回）。
import { onMounted, ref } from "vue";
import { NButton, NDataTable, NInput, NModal, NForm, NFormItem, NInputNumber } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchGiftcardBatches, createGiftcardBatch } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";

defineOptions({ name: "GiftcardTab" });

const loading = ref(false);
const batches = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const showCreate = ref(false);
const saving = ref(false);
const form = ref({ batch_no: "", name: "", amountYuan: 50, quantity: 10 });

// 明文码一次性展示（铁律 11：此后任何端点不可再取）
const showCodes = ref(false);
const codesBatch = ref("");
const plainCodes = ref<string[]>([]);

const columns: DataTableColumns<any> = [
  { title: "批次号", key: "batch_no", width: 170 },
  { title: "名称", key: "name", width: 140, ellipsis: { tooltip: true } },
  { title: "面值", key: "amount_cents", width: 90, render: (row) => formatMoney(row.amount_cents) },
  { title: "数量", key: "quantity", width: 70 },
  { title: "已兑换", key: "redeemed", width: 70, render: (row) => row.redeemed ?? row.redeemed_count ?? "-" },
  {
    title: "创建时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchGiftcardBatches(page.value);
    if (!error && data) {
      batches.value = (data as any).batches || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

async function handleCreate() {
  if (!form.value.batch_no || !form.value.name || !form.value.quantity) return;
  saving.value = true;
  try {
    const { data, error } = await createGiftcardBatch({
      batch_no: form.value.batch_no,
      name: form.value.name,
      amount_cents: Math.round(form.value.amountYuan * 100),
      quantity: form.value.quantity,
    });
    if (!error && data) {
      codesBatch.value = form.value.batch_no;
      plainCodes.value = (data as any).codes || [];
      showCodes.value = true;
      showCreate.value = false;
      load();
    }
  } finally {
    saving.value = false;
  }
}

function copyCodes() {
  navigator.clipboard?.writeText(plainCodes.value.join("\n"));
  window.$message?.success("已复制到剪贴板");
}

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-8px">
      <NButton v-if="checkAuth('giftcard:write')" size="small" type="primary" @click="showCreate = true">新建批次</NButton>
      <span class="ml-8px text-12px text-gray-400">兑换码明文仅创建时一次性展示，库内无明文（安全铁律）</span>
    </div>
    <NDataTable :columns="columns" :data="batches" :loading="loading" size="small"  :max-height="540" />
    <div class="mt-8px flex items-center justify-end gap-8px">
      <NButton size="small" :disabled="page <= 1" @click="page--, load()">上一页</NButton>
      <NButton size="small" :disabled="batches.length < 20" @click="page++, load()">下一页</NButton>
    </div>

    <NModal v-model:show="showCreate" preset="dialog" title="新建礼品卡批次" style="width: 440px">
      <NForm :model="form" label-placement="left" label-width="72">
        <NFormItem label="批次号" required>
          <NInput v-model:value="form.batch_no" placeholder="如 GC-2026-001" />
        </NFormItem>
        <NFormItem label="名称" required>
          <NInput v-model:value="form.name" placeholder="如 50 元礼品卡" />
        </NFormItem>
        <NFormItem label="面值(元)" required>
          <NInputNumber v-model:value="form.amountYuan" :min="0.01" :precision="2" class="w-full" />
        </NFormItem>
        <NFormItem label="数量" required>
          <NInputNumber v-model:value="form.quantity" :min="1" :max="10000" class="w-full" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCreate = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleCreate">创建</NButton>
      </template>
    </NModal>

    <!-- 明文码一次性展示 -->
    <NModal :show="showCodes" preset="dialog" :title="`批次 ${codesBatch} 兑换码（仅此一次展示）`" style="width: 480px" :mask-closable="false">
      <NInput :value="plainCodes.join('\n')" type="textarea" :rows="8" readonly />
      <template #action>
        <NButton @click="copyCodes">复制全部</NButton>
        <NButton type="primary" @click="showCodes = false">我已妥善保存</NButton>
      </template>
    </NModal>
  </div>
</template>
