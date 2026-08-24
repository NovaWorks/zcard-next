<script setup lang="ts">
import TablePager from "@/components/common/table-pager.vue";
import { NTabs, NTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import WithdrawTab from "./components/withdraw-tab.vue";
import GiftcardTab from "./components/giftcard-tab.vue";
import { ref, reactive, h } from "vue";
import { NTag, NSelect } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchWalletBalance, adjustWalletBalance, fetchWalletTransactions, fetchUsers } from "@/service/api";
import { formatMoney, formatSignedMoney, yuanToFen } from "@/utils/money";

defineOptions({ name: "WalletManagement" });

const userId = ref<number | null>(null);
const selectedUser = ref<any>(null);
const userOptions = ref<{ label: string; value: number }[]>([]);
const userLoading = ref(false);
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

/** 远程搜索用户（用户名/注册邮箱模糊匹配），选中后以用户 ID 查余额/流水/调账 */
async function onUserSearch(keyword: string) {
  if (!keyword.trim()) {
    userOptions.value = [];
    return;
  }
  userLoading.value = true;
  try {
    const { data, error } = await fetchUsers({ keyword: keyword.trim(), page: 1, page_size: 10 });
    if (!error && data) {
      userOptions.value = (data.users || []).map((u: any) => ({
        label: u.email ? `${u.username}（${u.email}）` : u.username,
        value: u.id,
        user: u,
      }));
    }
  } finally {
    userLoading.value = false;
  }
}

function onUserChange(_value: number | null, option: any) {
  selectedUser.value = option?.user || null;
}

async function loadBalance() {
  if (!userId.value) {
    window.$message?.warning("请搜索并选择用户（用户名或邮箱）");
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
      <NTabs type="line">
      <NTabPane name="balance" tab="余额/流水">
      <!-- 查询 -->
      <div class="mb-16px flex items-center gap-12px">
        <NSelect
          v-model:value="userId"
          :options="userOptions"
          :loading="userLoading"
          filterable
          remote
          clearable
          placeholder="搜索用户名或注册邮箱"
          class="w-300px"
          @search="onUserSearch"
          @update:value="onUserChange"
        />
        <NButton type="primary" :loading="balanceLoading" @click="loadBalance">查询余额</NButton>
        <NButton v-auth="'wallet:adjust'" :disabled="!balance" @click="showAdjust = true">调账</NButton>
        <span v-if="selectedUser" class="text-13px text-gray-500">
          当前用户：#{{ selectedUser.id }} {{ selectedUser.username }}<template v-if="selectedUser.email">（{{ selectedUser.email }}）</template>
        </span>
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
      <NDataTable :columns="columns" :data="transactions" :loading="loading"  :max-height="540" />

      <TablePager
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="total"
        @change="loadTransactions"
      />
          </NTabPane>
      <NTabPane v-if="checkAuth('wallet:withdraw')" name="withdraw" tab="提现审核">
        <WithdrawTab />
      </NTabPane>
      <NTabPane v-if="checkAuth('giftcard:read')" name="giftcard" tab="礼品卡">
        <GiftcardTab />
      </NTabPane>
      </NTabs>
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
        <NButton v-auth="'wallet:adjust'" type="primary" :loading="adjusting" @click="handleAdjust">确定</NButton>
      </template>
    </NModal>
  </div>
</template>
