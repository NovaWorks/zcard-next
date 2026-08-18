<script setup lang="ts">
// 提现审核/打款（wallet:withdraw 列表 / wallet:withdraw_review 超管专属）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NModal, NForm, NFormItem, NPopconfirm, NSelect, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchWithdrawals, reviewWithdrawal, payWithdrawal } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";

defineOptions({ name: "WithdrawTab" });

const loading = ref(false);
const withdrawals = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const statusFilter = ref<string | null>(null);

const showReject = ref(false);
const rejecting = ref(false);
const rejectTarget = ref<any>(null);
const rejectReason = ref("");

const canReview = () => checkAuth("wallet:withdraw_review");

const statusOptions = [
  { label: "全部", value: "" },
  { label: "待审核", value: "pending" },
  { label: "已通过", value: "approved" },
  { label: "已打款", value: "paid" },
  { label: "已驳回", value: "rejected" },
];

function statusTag(s: string) {
  const type = s === "paid" ? "success" : s === "pending" ? "warning" : s === "rejected" ? "error" : "info";
  const text = ({ pending: "待审核", approved: "已通过", paid: "已打款", rejected: "已驳回" } as any)[s] || s;
  return h(NTag, { type, size: "small" }, { default: () => text });
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "用户ID", key: "user_id", width: 70 },
  {
    title: "金额",
    key: "amount_cents",
    width: 90,
    render: (row) => formatMoney(row.amount_cents),
  },
  { title: "手续费", key: "fee_cents", width: 84, render: (row) => formatMoney(row.fee_cents) },
  { title: "方式", key: "method_type", width: 80 },
  { title: "账户", key: "account", width: 160, ellipsis: true },
  { title: "状态", key: "status", width: 84, render: (row) => statusTag(row.status) },
  { title: "驳回原因", key: "reject_reason", width: 120, ellipsis: true },
  {
    title: "操作",
    key: "actions",
    width: 170,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        row.status === "pending" && canReview()
          ? h(NPopconfirm, { onPositiveClick: () => handleReview(row, true) }, { trigger: () => h(NButton, { size: "tiny", type: "success", secondary: true }, { default: () => "通过" }), default: () => "通过该提现申请？" })
          : null,
        row.status === "pending" && canReview()
          ? h(NButton, { size: "tiny", type: "warning", secondary: true, onClick: () => openReject(row) }, { default: () => "驳回" })
          : null,
        row.status === "approved" && canReview()
          ? h(NPopconfirm, { onPositiveClick: () => handlePay(row) }, { trigger: () => h(NButton, { size: "tiny", type: "primary", secondary: true }, { default: () => "确认打款" }), default: () => "确认已完成线下打款？将解锁冻结金额并结算。" })
          : null,
      ]),
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchWithdrawals({ status: statusFilter.value || undefined, page: page.value, page_size: 20 });
    if (!error && data) {
      withdrawals.value = (data as any).withdrawals || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

async function handleReview(row: any, approve: boolean) {
  const { error } = await reviewWithdrawal(row.id, approve);
  if (!error) {
    window.$message?.success(approve ? "已通过（金额保持冻结待打款）" : "已驳回");
    load();
  }
}

async function handlePay(row: any) {
  const { error } = await payWithdrawal(row.id);
  if (!error) {
    window.$message?.success("已确认打款（冻结金额已扣减）");
    load();
  }
}

function openReject(row: any) {
  rejectTarget.value = row;
  rejectReason.value = "";
  showReject.value = true;
}

async function handleReject() {
  if (!rejectTarget.value || !rejectReason.value.trim()) return;
  rejecting.value = true;
  try {
    const { error } = await reviewWithdrawal(rejectTarget.value.id, false, rejectReason.value.trim());
    if (!error) {
      window.$message?.success("已驳回（冻结金额解除）");
      showReject.value = false;
      load();
    }
  } finally {
    rejecting.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-8px flex items-center gap-8px">
      <NSelect v-model:value="statusFilter" :options="statusOptions" size="small" class="w-110px" @update:value="(page = 1), load()" />
      <span class="text-12px text-gray-400">共 {{ total }} 单</span>
    </div>
    <NDataTable :columns="columns" :data="withdrawals" :loading="loading" size="small" />
    <div class="mt-8px flex items-center justify-end gap-8px">
      <NButton size="small" :disabled="page <= 1" @click="page--, load()">上一页</NButton>
      <NButton size="small" :disabled="withdrawals.length < 20" @click="page++, load()">下一页</NButton>
    </div>

    <NModal v-model:show="showReject" preset="dialog" :title="`驳回提现 #${rejectTarget?.id || ''}`" style="width: 420px">
      <NForm label-placement="top">
        <NFormItem label="驳回原因（必填，用户可见）" required>
          <NInput v-model:value="rejectReason" type="textarea" :rows="2" placeholder="如：账户信息有误" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showReject = false">取消</NButton>
        <NButton type="warning" :loading="rejecting" :disabled="!rejectReason.trim()" @click="handleReject">驳回</NButton>
      </template>
    </NModal>
  </div>
</template>
