<script setup lang="ts">
// 货币管理（settings:currency_read / currency_write / currency_delete 超管专属）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NInputNumber, NModal, NForm, NFormItem, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { listCurrencies, createCurrency, updateCurrency, deleteCurrency } from "@/service/api";
import { checkAuth } from "@/directives";

defineOptions({ name: "CurrencyTab" });

const loading = ref(false);
const currencies = ref<any[]>([]);
const showCreate = ref(false);
const saving = ref(false);
const form = ref({ code: "", symbol: "¥", position: "prefix", precision: 2, rate_json: "1" });

const canWrite = () => checkAuth("settings:currency_write");

const columns: DataTableColumns<any> = [
  { title: "代码", key: "code", width: 80 },
  { title: "符号", key: "symbol", width: 70 },
  { title: "符号位置", key: "position", width: 84 },
  { title: "小数位", key: "precision", width: 70 },
  { title: "汇率(JSON)", key: "rate_json", width: 140, ellipsis: true },
  { title: "排序", key: "sort", width: 56 },
  {
    title: "状态",
    key: "enabled",
    width: 70,
    render: (row) => h(NTag, { size: "small", type: row.enabled ? "success" : "default" }, { default: () => (row.enabled ? "启用" : "停用") }),
  },
  {
    title: "操作",
    key: "actions",
    width: 150,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        canWrite()
          ? h(NButton, { size: "tiny", onClick: () => handleToggle(row) }, { default: () => (row.enabled ? "停用" : "启用") })
          : null,
        checkAuth("settings:currency_delete")
          ? h(NPopconfirm, { onPositiveClick: () => handleDelete(row.code) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }), default: () => "无引用时才可删除，确定？" })
          : null,
      ]),
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await listCurrencies();
    if (!error && data) currencies.value = (data as any).currencies || [];
  } finally {
    loading.value = false;
  }
}

async function handleToggle(row: any) {
  const { error } = await updateCurrency(row.code, { enabled: !row.enabled });
  if (!error) {
    window.$message?.success(!row.enabled ? "已启用" : "已停用");
    load();
  }
}

async function handleDelete(code: string) {
  const { error } = await deleteCurrency(code);
  if (!error) {
    window.$message?.success("已删除");
    load();
  } else {
    window.$message?.error("删除失败（可能仍被引用）");
  }
}

async function handleCreate() {
  if (!form.value.code || !form.value.symbol) return;
  saving.value = true;
  try {
    const { error } = await createCurrency({ ...form.value });
    if (!error) {
      window.$message?.success("货币已创建");
      showCreate.value = false;
      load();
    }
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-8px">
      <NButton v-if="canWrite()" size="small" type="primary" @click="showCreate = true">新增货币</NButton>
      <span class="ml-8px text-12px text-gray-400">基础货币 CNY 恒为 1；汇率 decimal 字符串，展示换算在下单时快照</span>
    </div>
    <NDataTable :columns="columns" :data="currencies" :loading="loading" size="small" />

    <NModal v-model:show="showCreate" preset="dialog" title="新增货币" style="width: 440px">
      <NForm :model="form" label-placement="left" label-width="72">
        <NFormItem label="代码" required>
          <NInput v-model:value="form.code" placeholder="如 USD" />
        </NFormItem>
        <NFormItem label="符号" required>
          <NInput v-model:value="form.symbol" placeholder="如 $" />
        </NFormItem>
        <NFormItem label="符号位置">
          <NSelect v-model:value="form.position" :options="[{ label: '前缀', value: 'prefix' }, { label: '后缀', value: 'suffix' }]" />
        </NFormItem>
        <NFormItem label="小数位">
          <NInputNumber v-model:value="form.precision" :min="0" :max="4" class="w-full" />
        </NFormItem>
        <NFormItem label="汇率" required>
          <NInput v-model:value="form.rate_json" placeholder='如 "0.14"（1 CNY = 0.14 USD）' />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCreate = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleCreate">创建</NButton>
      </template>
    </NModal>
  </div>
</template>
