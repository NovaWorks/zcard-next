<script setup lang="ts">
// 账单流水（全站钱包流水：user_id=0 拉全部用户；归属用户列 + 类型筛选 + 金额汇总）。
import { onMounted, ref, h } from "vue";
import { NDataTable, NInput, NSelect, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchWalletTransactions } from "@/service/api";
import TablePager from "@/components/common/table-pager.vue";
import { formatMoney, formatSignedMoney } from "@/utils/money";

defineOptions({ name: "WalletBillsTab" });

const loading = ref(false);
const bills = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
// 类型筛选（客户端过滤当前页；服务端 type 筛选参数暂未开放）
const typeFilter = ref<string | null>(null);

const TYPE_TEXT: Record<string, string> = {
  adjust: "调账",
  recharge: "充值",
  order_pay: "订单支付",
  order_refund: "订单退款",
  payment: "支付",
  refund: "退款",
  commission: "分销佣金",
  withdraw: "提现",
  freeze: "冻结",
  unfreeze: "解冻",
  giftcard: "礼品卡兑换",
};

function typeText(t?: string) {
  return (t && TYPE_TEXT[t]) || t || "-";
}

function directionText(d?: string) {
  return d === "in" ? "入账" : d === "out" ? "出账" : d || "-";
}

const typeOptions = Object.entries(TYPE_TEXT).map(([value, label]) => ({ label, value }));

const filtered = () =>
  typeFilter.value ? bills.value.filter((b) => b.type === typeFilter.value) : bills.value;

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchWalletTransactions(0, {
      page: page.value,
      page_size: pageSize.value,
    });
    if (!error && data) {
      bills.value = (data as any).transactions || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

// 当页收支汇总（随筛选联动，给运营一眼对账）
function sums() {
  let inCents = 0;
  let outCents = 0;
  for (const b of filtered()) {
    if (b.direction === "in") inCents += b.amount_cents || 0;
    else if (b.direction === "out") outCents += b.amount_cents || 0;
  }
  return { inCents, outCents };
}

function formatTime(ts?: number) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 64 },
  {
    title: "用户",
    key: "username",
    width: 140,
    render: (row) =>
      h("span", { title: `用户 ID ${row.user_id}` }, row.username ? `${row.username}` : `#${row.user_id}`),
  },
  {
    title: "方向",
    key: "direction",
    width: 76,
    render: (row) =>
      h(
        NTag,
        { type: row.direction === "in" ? "success" : "error", size: "small", bordered: false },
        { default: () => directionText(row.direction) },
      ),
  },
  { title: "类型", key: "type", width: 100, render: (row) => typeText(row.type) },
  {
    title: "金额",
    key: "amount_cents",
    width: 110,
    render: (row) => formatSignedMoney(row.amount_cents),
  },
  {
    title: "变动后余额",
    key: "balance_after_cents",
    width: 110,
    render: (row) => formatMoney(row.balance_after_cents),
  },
  { title: "关联单号", key: "reference", minWidth: 150, ellipsis: { tooltip: true } },
  { title: "备注", key: "remark", minWidth: 120, ellipsis: { tooltip: true } },
  { title: "时间", key: "created_at", width: 160, render: (row) => formatTime(row.created_at) },
];

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-12px flex flex-wrap items-center gap-12px">
      <NSelect
        v-model:value="typeFilter"
        :options="typeOptions"
        clearable
        placeholder="全部类型"
        class="w-140px"
      />
      <NInput readonly disabled class="w-0px hidden" />
      <span class="text-12px text-gray-400">
        当页收入 <b class="text-success">{{ formatMoney(sums().inCents) }}</b> · 支出
        <b class="text-error">{{ formatMoney(sums().outCents) }}</b>
        · 共 {{ total }} 笔（充值/消费/退款/佣金/提现/调账全量流水）
      </span>
    </div>
    <NDataTable :columns="columns" :data="filtered()" :loading="loading" :max-height="540" />
    <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
  </div>
</template>
