<script setup lang="ts">
import TablePager from "@/components/common/table-pager.vue";
import { NTabs, NTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import WithdrawTab from "./components/withdraw-tab.vue";
import GiftcardTab from "./components/giftcard-tab.vue";
import { ref, reactive, h } from "vue";
import { NTag, NSelect, NRadioGroup, NRadioButton, NRadio } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchWalletBalance, adjustWalletBalance, adjustWalletPoints, fetchWalletTransactions, fetchUsers, fetchCoupons, grantCoupon } from "@/service/api";
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

const adjustTab = ref<"balance" | "points" | "coupon">("balance");
const adjustDir = ref<"add" | "sub">("add"); // 增/扣 单选（免手输正负号）
const couponBatches = ref<any[]>([]);
const couponBatchId = ref<string | null>(null);
const couponCount = ref(1);
const couponLoading = ref(false);
const grantingCoupon = ref(false);
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

async function openAdjust() {
  adjustTab.value = "balance";
  adjustDir.value = "add";
  couponBatchId.value = null;
  couponCount.value = 1;
  showAdjust.value = true;
  if (!couponBatches.value.length) {
    couponLoading.value = true;
    try {
      const { data, error } = await fetchCoupons("unused", undefined, 1, 50);
      if (!error && data) {
        const seen = new Set<string>();
        const opts: any[] = [];
        for (const c of (data as any).coupons || []) {
          if (!seen.has(c.batch_id)) {
            seen.add(c.batch_id);
            opts.push({ label: `${c.name}（${c.batch_id}）`, value: c.batch_id });
          }
        }
        couponBatches.value = opts;
      }
    } finally {
      couponLoading.value = false;
    }
  }
}

async function handleAdjust() {
  if (!userId.value) return;
  if (adjustTab.value !== "coupon" && !adjustForm.reason.trim()) {
    window.$message?.warning("请填写调整原因（用于审计）");
    return;
  }
  if (adjustTab.value === "balance") {
    const amountYuan = adjustForm.amount_yuan;
    if (amountYuan === null || amountYuan <= 0) {
      window.$message?.warning("请填写金额");
      return;
    }
    // 增/扣单选 → 服务端正负语义（铁律 15：元→分防浮点）
    const cents = yuanToFen(amountYuan) * (adjustDir.value === "sub" ? -1 : 1);
    adjusting.value = true;
    try {
      const { data, error } = await adjustWalletBalance(userId.value, {
        amount_cents: cents,
        reason: adjustForm.reason,
      });
      if (!error) {
        window.$message?.success(adjustDir.value === "add" ? "已增加余额" : "已扣减余额");
        showAdjust.value = false;
        Object.assign(adjustForm, { amount_yuan: null, reason: "" });
        balance.value = data || balance.value;
        page.value = 1;
        loadTransactions();
      }
    } finally {
      adjusting.value = false;
    }
    return;
  }
  if (adjustTab.value === "points") {
    const pts = adjustForm.amount_yuan;
    if (pts === null || pts <= 0 || !Number.isInteger(pts)) {
      window.$message?.warning("请填写整数积分数量");
      return;
    }
    adjusting.value = true;
    try {
      const { error } = await adjustWalletPoints(userId.value, pts * (adjustDir.value === "sub" ? -1 : 1), adjustForm.reason);
      if (!error) {
        window.$message?.success(adjustDir.value === "add" ? "已增加积分" : "已扣减积分");
        showAdjust.value = false;
        Object.assign(adjustForm, { amount_yuan: null, reason: "" });
      }
    } finally {
      adjusting.value = false;
    }
    return;
  }
  // 优惠券赠送
  if (!couponBatchId.value) {
    window.$message?.warning("请选择优惠券批次");
    return;
  }
  grantingCoupon.value = true;
  try {
    const { data, error } = await grantCoupon(couponBatchId.value, userId.value, couponCount.value || 1);
    if (!error) {
      window.$message?.success(`已赠送 ${(data as any)?.granted ?? 0} 张券`);
      showAdjust.value = false;
    }
  } finally {
    grantingCoupon.value = false;
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
        <NButton v-auth="'wallet:adjust'" :disabled="!balance" @click="openAdjust">调整（余额/积分/券）</NButton>
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

    <!-- 调整弹窗：余额 / 积分 / 优惠券（增扣单选，免手输正负号） -->
    <NModal v-model:show="showAdjust" preset="dialog" title="账户调整" style="width: 520px">
      <NRadioGroup v-model:value="adjustTab" size="small" class="mb-16px">
        <NRadioButton value="balance">余额</NRadioButton>
        <NRadioButton value="points">积分</NRadioButton>
        <NRadioButton value="coupon">赠送优惠券</NRadioButton>
      </NRadioGroup>
      <NForm label-placement="left" label-width="90">
        <template v-if="adjustTab !== 'coupon'">
          <NFormItem :label="adjustTab === 'balance' ? '方向' : '方向'">
            <NRadioGroup v-model:value="adjustDir">
              <NRadio value="add">增加</NRadio>
              <NRadio value="sub">扣减</NRadio>
            </NRadioGroup>
          </NFormItem>
          <NFormItem :label="adjustTab === 'balance' ? '金额（元）' : '积分数量'" required>
            <NInputNumber
              v-model:value="adjustForm.amount_yuan"
              class="w-full"
              :precision="adjustTab === 'balance' ? 2 : 0"
              :step="adjustTab === 'balance' ? 0.01 : 1"
              :min="0"
              :placeholder="adjustTab === 'balance' ? `要${adjustDir === 'add' ? '增加' : '扣减'}的金额` : `要${adjustDir === 'add' ? '增加' : '扣减'}的积分`"
            />
          </NFormItem>
          <NFormItem label="原因" required>
            <NInput v-model:value="adjustForm.reason" type="textarea" :rows="3" placeholder="必填，用于审计" />
          </NFormItem>
        </template>
        <template v-else>
          <NFormItem label="优惠券批次" required>
            <NSelect
              v-model:value="couponBatchId"
              :options="couponBatches"
              :loading="couponLoading"
              filterable
              placeholder="选择批次（仅显示未使用券的批次）"
            />
            <div v-if="!couponLoading && !couponBatches.length" class="mt-4px text-12px text-gray-400">
              暂无可赠送的批次——先到 营销管理 → 优惠券 批量生成
            </div>
          </NFormItem>
          <NFormItem label="赠送数量" required>
            <NInputNumber v-model:value="couponCount" :min="1" :max="1000" class="w-full" />
          </NFormItem>
          <div class="text-12px text-gray-400">赠送后券立即出现在买家「我的优惠券」中</div>
        </template>
      </NForm>
      <template #action>
        <NButton @click="showAdjust = false">取消</NButton>
        <NButton
          v-auth="'wallet:adjust'"
          type="primary"
          :loading="adjustTab === 'coupon' ? grantingCoupon : adjusting"
          @click="handleAdjust"
        >
          {{ adjustTab === 'coupon' ? '赠送' : `确定${adjustDir === 'add' ? '增加' : '扣减'}` }}
        </NButton>
      </template>
    </NModal>
  </div>
</template>
