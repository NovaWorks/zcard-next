<script setup lang="ts">
// 货币管理（settings:currency_read / currency_write / currency_delete 超管专属）。
import { onMounted, ref, h, computed, watch } from "vue";
import { NButton, NDataTable, NInput, NInputNumber, NModal, NForm, NFormItem, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { listCurrencies, createCurrency, updateCurrency, deleteCurrency, fetchSettings } from "@/service/api";
import { checkAuth } from "@/directives";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "CurrencyTab" });

const loading = ref(false);
const currencies = ref<any[]>([]);
const showModal = ref(false);
const saving = ref(false);
// editing 非空 = 编辑已有货币（code 不可改）；空 = 新增。
const editing = ref<string | null>(null);
const form = ref({ code: "", symbol: "", position: "prefix", precision: 2, rate_json: "1" });

// 启用状态快捷筛选（客户端过滤——货币全量加载，带实时计数）
const enabledFilter = ref<"" | "on" | "off">("");
const enabledTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "已启用", value: "on", type: "success" as const },
  { label: "已停用", value: "off", type: "default" as const },
];
const enabledCounts = computed(() => ({
  "": currencies.value.length,
  on: currencies.value.filter((c) => c.enabled).length,
  off: currencies.value.filter((c) => !c.enabled).length,
}));
const filteredCurrencies = computed(() =>
  enabledFilter.value === "" ? currencies.value : currencies.value.filter((c) => (enabledFilter.value === "on" ? c.enabled : !c.enabled)),
);

// 当前基础货币（i18n.base_currency；读取失败回落默认 CNY）。
const baseCurrency = ref("CNY");
const isBaseCurrency = computed(() => form.value.code !== "" && form.value.code === baseCurrency.value);
const rateHint = computed(() =>
  isBaseCurrency.value
    ? form.value.code === "CNY"
      ? "全站统一按人民币（CNY）结算，基础货币汇率固定为 1"
      : `全站统一按基础货币 ${form.value.code} 结算，汇率固定为 1`
    : "汇率 = 1 基础货币可兑换的本币数量，如 1 CNY = 0.14 USD",
);

const canWrite = () => checkAuth("settings:currency_write");

// 输入基础货币代码时汇率自动固定为 1（与后端 settings.CURRENCY_BASE_RATE 约束一致）。
watch(
  () => form.value.code,
  (code) => {
    if (code && code === baseCurrency.value) form.value.rate_json = "1";
  },
);

async function loadBaseCurrency() {
  try {
    const { data, error } = await fetchSettings("i18n");
    if (!error && (data as any)?.items) {
      const it = ((data as any).items as any[]).find((x: any) => x.key === "base_currency");
      if (it) {
        try {
          baseCurrency.value = JSON.parse(it.value_json);
        } catch {
          // 保持默认 CNY
        }
      }
    }
  } catch {
    // 无权限/接口异常：保持默认 CNY
  }
}

function openCreate() {
  editing.value = null;
  form.value = { code: "", symbol: "", position: "prefix", precision: 2, rate_json: "1" };
  showModal.value = true;
}

function openEdit(row: any) {
  editing.value = row.code;
  form.value = {
    code: row.code,
    symbol: row.symbol ?? "",
    position: row.position ?? "prefix",
    precision: row.precision ?? 2,
    rate_json: row.rate_json ?? "1",
  };
  showModal.value = true;
}

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
    width: 190,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        canWrite()
          ? h(NButton, { size: "tiny", onClick: () => openEdit(row) }, { default: () => "编辑" })
          : null,
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

async function handleSave() {
  if (!form.value.code || !form.value.symbol) return;
  saving.value = true;
  try {
    if (editing.value) {
      const { error } = await updateCurrency(editing.value, { ...form.value });
      if (!error) {
        window.$message?.success("货币已更新");
        showModal.value = false;
        load();
      }
    } else {
      const { error } = await createCurrency({ ...form.value });
      if (!error) {
        window.$message?.success("货币已创建");
        showModal.value = false;
        load();
      }
    }
  } finally {
    saving.value = false;
  }
}

onMounted(() => {
  load();
  loadBaseCurrency();
});
</script>

<template>
  <div>
    <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
      <FilterTabs v-model:value="enabledFilter" :options="enabledTabs" :counts="enabledCounts" size="small" />
      <div>
        <NButton v-if="canWrite()" size="small" type="primary" @click="openCreate">新增货币</NButton>
        <span class="ml-8px text-12px text-gray-400">基础货币汇率恒为 1；汇率 decimal 字符串，展示换算在下单时快照</span>
      </div>
    </div>
    <NDataTable :columns="columns" :data="filteredCurrencies" :loading="loading" size="small"  :max-height="540" />

    <NModal v-model:show="showModal" preset="dialog" :title="editing ? `编辑货币 ${editing}` : '新增货币'" style="width: 440px">
      <NForm :model="form" label-placement="left" label-width="72">
        <NFormItem label="代码" required>
          <NInput v-model:value="form.code" :disabled="!!editing" placeholder="如 USD" />
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
          <NInput v-model:value="form.rate_json" :disabled="isBaseCurrency" placeholder='如 "0.14"' />
          <div class="text-12px text-gray-400 mt-4px">{{ rateHint }}</div>
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showModal = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleSave">{{ editing ? "保存" : "创建" }}</NButton>
      </template>
    </NModal>
  </div>
</template>
