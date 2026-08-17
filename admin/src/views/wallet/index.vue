<script setup lang="ts">
import TablePager from "@/components/common/table-pager.vue";
import { ref, reactive, h } from "vue";
import { NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchWalletBalance, adjustWalletBalance, fetchWalletTransactions } from "@/service/api";
import { formatMoney, formatSignedMoney, yuanToFen } from "@/utils/money";

defineOptions({ name: "WalletManagement" });

const userId = ref<number | null>(null);
const balance = ref<any>(null);
const balanceLoading = ref(false);
const loading = ref(false);
const adjusting = ref(false);
const showAdjust = ref(false);

const transactions = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);

const adjustForm = reactive<{ amount_yuan: number | null; reason: string }>({
  amount_yuan: null,
  reason: "",
});

function directionText(d?: string) {
  if (!d) return "-";
  const map: Record<string, string> = {
    in: "入账",
    out: "出账",
    credit: "入账",
    debit: "出账",
    income: "入账",
    expense: "出账",
    add: "入账",
    sub: "扣减",
    adjust_in: "入账",
    adjust_out: "扣减",
  };
  return map[d] || d;
}

function directionType(d?: string): "success" | "error" | "default" {
  if (["in", "credit", "income", "add", "adjust_in"].includes(d || "")) return "success";
  if (["out", "debit", "expense", "sub", "adjust_out"].includes(d || "")) return "error";
  return "default";
}

function typeText(t?: string) {
  if (!t) return "-";
  const map: Record<string, string> = {
    adjust: "调账",
    recharge: "充值",
    payment: "支付",
    refund: "退款",
    freeze: "冻结",
    unfreeze: "解冻",
  };
  return map[t] || t;
}

function formatTime(ts?: number) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

const columns: DataTableColumns<any> = [
  {
    title: "方向",
    key: "direction",
    width: 80,
    render: (row) =>
      h(
        NTag,
        { type: directionType(row.direction), size: "small" },
        { default: () => directionText(row.direction) },
      ),
  },
  { title: "类型", key: "type", width: 100, render: (row) => typeText(row.type) },
  {
    title: "金额",
    key: "amount_cents",
    width: 120,
    render: (row) => formatSignedMoney(row.amount_cents),
  },
  {
    title: "余额前",
    key: "balance_before_cents",
    width: 120,
    render: (row) => formatMoney(row.balance_before_cents),
  },
  {
    title: "余额后",
    key: "balance_after_cents",
    width: 120,
    render: (row) => formatMoney(row.balance_after_cents),
  },
  { title: "备注", key: "remark", minWidth: 140, ellipsis: { tooltip: true } },
  { title: "时间", key: "created_at", width: 160, render: (row) => formatTime(row.created_at) },
];

async function loadBalance() {
  if (!userId.value) {
    window.$message?.warning("请输入用户 ID");
    return;
  }
  balanceLoading.value = true;
  try {
    const { data, error } = await fetchWalletBalance(userId.value);
    if (!error && data) {
      balance.value = data;
      page.value = 1;
      loadTransactions();
    } else {
      balance.value = null;
      transactions.value = [];
      total.value = 0;
    }
  } finally {
    balanceLoading.value = false;
  }
}

async function loadTransactions() {
  if (!userId.value) return;
  loading.value = true;
  try {
    const { data, error } = await fetchWalletTransactions(userId.value, {
      page: page.value,
      page_size: pageSize.value,
    });
    if (!error && data) {
      transactions.value = (data as any).transactions || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

async function handleAdjust() {
  const amountYuan = adjustForm.amount_yuan;
  if (!userId.value || amountYuan === null || amountYuan === 0 || !adjustForm.reason.trim()) {
    window.$message?.warning("请填写金额和原因");
    return;
  }
  adjusting.value = true;
  try {
    // 元 → 分（铁律 15：提交统一 *100，经 utils/money 防浮点）
    const { data, error } = await adjustWalletBalance(userId.value, {
      amount_cents: yuanToFen(amountYuan),
      reason: adjustForm.reason,
    });
    if (!error) {
      window.$message?.success("调账成功");
      showAdjust.value = false;
      Object.assign(adjustForm, { amount_yuan: null, reason: "" });
      balance.value = data || balance.value;
      page.value = 1;
      loadTransactions();
    }
  } finally {
    adjusting.value = false;
  }
}
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="钱包管理" class="flex-1">
      <!-- 查询 -->
      <div class="mb-16px flex items-center gap-12px">
        <NInputNumber v-model:value="userId" :min="1" placeholder="用户 ID" class="w-200px" />
        <NButton type="primary" :loading="balanceLoading" @click="loadBalance">查询余额</NButton>
        <NButton :disabled="!balance" @click="showAdjust = true">调账</NButton>
      </div>

      <!-- 余额卡片 -->
      <NGrid
        v-if="balance"
        :x-gap="16"
        :y-gap="16"
        cols="s:1 m:3"
        responsive="screen"
        class="mb-16px"
      >
        <NGi>
          <NCard size="small" :bordered="false">
            <div class="text-14px">可用余额</div>
            <div class="mt-8px text-24px font-bold">{{ formatMoney(balance.available_cents) }}</div>
          </NCard>
        </NGi>
        <NGi>
          <NCard size="small" :bordered="false">
            <div class="text-14px">冻结余额</div>
            <div class="mt-8px text-24px font-bold">{{ formatMoney(balance.locked_cents) }}</div>
          </NCard>
        </NGi>
        <NGi>
          <NCard size="small" :bordered="false">
            <div class="text-14px">总余额</div>
            <div class="mt-8px text-24px font-bold">{{ formatMoney(balance.total_cents) }}</div>
          </NCard>
        </NGi>
      </NGrid>

      <!-- 流水 -->
      <NDataTable :columns="columns" :data="transactions" :loading="loading" />

      <TablePager
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="total"
        @change="loadTransactions"
      />
    </NCard>

    <!-- 调账弹窗 -->
    <NModal v-model:show="showAdjust" preset="dialog" title="手动调账" style="width: 480px">
      <NForm label-placement="left" label-width="90">
        <NFormItem label="金额（元）" required>
          <NInputNumber
            v-model:value="adjustForm.amount_yuan"
            class="w-full"
            placeholder="正数入账，负数扣减"
            :precision="2"
            :step="0.01"
          />
        </NFormItem>
        <NFormItem label="原因" required>
          <NInput
            v-model:value="adjustForm.reason"
            type="textarea"
            :rows="3"
            placeholder="必填，用于审计"
          />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showAdjust = false">取消</NButton>
        <NButton type="primary" :loading="adjusting" @click="handleAdjust">确定</NButton>
      </template>
    </NModal>
  </div>
</template>
