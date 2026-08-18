<script setup lang="ts">
// 优惠券管理：批次生成 / 列表 / 作废 / 赠送（coupon:read / write）。
import { computed, onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NInputNumber, NModal, NForm, NFormItem, NPopconfirm, NSelect, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchCoupons, createCouponBatch, disableCoupon, grantCoupon } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";

defineOptions({ name: "CouponsTab" });

const loading = ref(false);
const coupons = ref<any[]>([]);
const statusFilter = ref<string | null>(null);

const showBatch = ref(false);
const saving = ref(false);
const batchForm = ref({ name: "", type: "fixed", valueYuan: 10, count: 10 });

const showGrant = ref(false);
const granting = ref(false);
const grantForm = ref({ batch_id: "", user_id: null as number | null, count: 1 });

const canWrite = () => checkAuth("coupon:write");

const statusOptions = [
  { label: "全部", value: "" },
  { label: "未使用", value: "unused" },
  { label: "已使用", value: "used" },
  { label: "已作废", value: "disabled" },
];

const columns: DataTableColumns<any> = [
  { title: "批次", key: "batch_id", width: 200, ellipsis: true },
  { title: "名称", key: "name", width: 130 },
  {
    title: "类型",
    key: "type",
    width: 90,
    render: (row) => h(NTag, { size: "small", type: row.type === "fixed" ? "info" : "warning" }, { default: () => (row.type === "fixed" ? "满减" : "折扣") }),
  },
  {
    title: "面值",
    key: "value",
    width: 100,
    render: (row) => (row.type === "fixed" ? formatMoney(row.value) : `${(row.value / 100).toFixed(1)}折`),
  },
  { title: "券码", key: "code", width: 170 },
  {
    title: "状态",
    key: "status",
    width: 84,
    render: (row) => h(NTag, { size: "small", type: row.status === "unused" ? "success" : row.status === "used" ? "default" : "error" }, { default: () => ({ unused: "未使用", used: "已使用", disabled: "已作废" } as any)[row.status] || row.status }),
  },
  {
    title: "操作",
    key: "actions",
    width: 150,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        checkAuth("coupon:write")
          ? h(NButton, { size: "tiny", onClick: () => ((grantForm.value = { batch_id: row.batch_id, user_id: null, count: 1 }), (showGrant.value = true)) }, { default: () => "赠送" })
          : null,
        row.status === "unused" && canWrite()
          ? h(NPopconfirm, { onPositiveClick: () => handleDisable(row.batch_id) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "作废批次" }), default: () => "作废该批次全部未使用券？" })
          : null,
      ]),
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchCoupons(statusFilter.value || undefined);
    if (!error && data) coupons.value = (data as any).coupons || [];
  } finally {
    loading.value = false;
  }
}

async function handleBatch() {
  if (!batchForm.value.name || !batchForm.value.count) return;
  saving.value = true;
  try {
    const { data, error } = await createCouponBatch({
      name: batchForm.value.name,
      type: batchForm.value.type,
      value: batchForm.value.type === "fixed" ? Math.round(batchForm.value.valueYuan * 100) : batchForm.value.valueYuan,
      count: batchForm.value.count,
    });
    if (!error) {
      window.$message?.success(`已生成 ${(data as any)?.count ?? batchForm.value.count} 张券`);
      showBatch.value = false;
      load();
    }
  } finally {
    saving.value = false;
  }
}

async function handleDisable(batchId: string) {
  const { error } = await disableCoupon(batchId);
  if (!error) {
    window.$message?.success("批次已作废");
    load();
  }
}

async function handleGrant() {
  if (!grantForm.value.batch_id || !grantForm.value.user_id) return;
  granting.value = true;
  try {
    const { data, error } = await grantCoupon(grantForm.value.batch_id, grantForm.value.user_id, grantForm.value.count);
    if (!error) {
      window.$message?.success(`已赠送 ${(data as any)?.granted ?? 0} 张`);
      showGrant.value = false;
    }
  } finally {
    granting.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-8px flex items-center gap-8px">
      <NButton v-if="canWrite()" size="small" type="primary" @click="showBatch = true">批量生成</NButton>
      <NSelect v-model:value="statusFilter" :options="statusOptions" size="small" class="w-110px" @update:value="load" />
    </div>
    <NDataTable :columns="columns" :data="coupons" :loading="loading" size="small" />

    <!-- 批量生成 -->
    <NModal v-model:show="showBatch" preset="dialog" title="批量生成优惠券" style="width: 460px">
      <NForm :model="batchForm" label-placement="left" label-width="88">
        <NFormItem label="名称" required>
          <NInput v-model:value="batchForm.name" placeholder="如：新品满减券" />
        </NFormItem>
        <NFormItem label="类型">
          <NSelect v-model:value="batchForm.type" :options="[{ label: '满减（固定金额）', value: 'fixed' }, { label: '折扣（万分比）', value: 'percent' }]" />
        </NFormItem>
        <NFormItem :label="batchForm.type === 'fixed' ? '面值(元)' : '折扣(万分比)'" required>
          <NInputNumber v-model:value="batchForm.valueYuan" :min="1" class="w-full" />
        </NFormItem>
        <NFormItem label="数量" required>
          <NInputNumber v-model:value="batchForm.count" :min="1" :max="10000" class="w-full" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showBatch = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleBatch">生成</NButton>
      </template>
    </NModal>

    <!-- 赠送指定用户 -->
    <NModal v-model:show="showGrant" preset="dialog" title="赠送优惠券" style="width: 420px">
      <NForm :model="grantForm" label-placement="left" label-width="72">
        <NFormItem label="批次">
          <NInput :value="grantForm.batch_id" disabled />
        </NFormItem>
        <NFormItem label="用户ID" required>
          <NInputNumber v-model:value="grantForm.user_id" :min="1" class="w-full" placeholder="users 表 ID" />
        </NFormItem>
        <NFormItem label="数量" required>
          <NInputNumber v-model:value="grantForm.count" :min="1" class="w-full" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showGrant = false">取消</NButton>
        <NButton type="primary" :loading="granting" @click="handleGrant">赠送</NButton>
      </template>
    </NModal>
  </div>
</template>
