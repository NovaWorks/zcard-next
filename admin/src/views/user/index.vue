<script setup lang="ts">
// 前台用户管理（storefront 注册用户）：列表 / 关键词搜索 / 状态筛选 / 封禁解封。
// 权限：identity:user_read（查看）、identity:user_status（封禁/解封，超管专属）。
import { ref, reactive, onMounted, h } from "vue";
import { NButton, NInput, NInputNumber, NTag, NPopconfirm, NDataTable, NCard, NSpace, NModal, NDescriptions, NDescriptionsItem, NForm, NFormItem } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchUsers, setUserStatus } from "@/service/api";
import { fetchWalletBalance, adjustWalletBalance } from "@/service/api/wallet";
import { checkAuth } from "@/directives";
import TablePager from "@/components/common/table-pager.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";
import { fenToYuan, yuanToFen } from "@/utils/money";

defineOptions({ name: "UserManagement" });

const loading = ref(false);
const users = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);

const query = reactive({
  keyword: "",
  status: "" as "" | "active" | "banned",
});

const showDetail = ref(false);
const detailUser = ref<any>(null);

// ── 调整余额（wallet:adjust 超管专属；正=入账 负=扣减）──
// 输入用元、提交经 yuanToFen 转分（铁律 15：表单禁止直接绑分）。
const showAdjust = ref(false);
const adjustUser = ref<any>(null);
const adjustLoading = ref(false);
const balance = ref<{ available_cents: number; locked_cents: number; total_cents: number } | null>(null);
const adjustAmountYuan = ref<number | null>(null);
const adjustForm = reactive({ reason: "" });

const canAdjust = () => checkAuth("wallet:adjust");

async function openAdjust(row: any) {
  adjustUser.value = row;
  adjustAmountYuan.value = null;
  adjustForm.reason = "";
  balance.value = null;
  showAdjust.value = true;
  await refreshBalance(row.id);
}

async function refreshBalance(userId: number) {
  const { data, error } = await fetchWalletBalance(userId);
  if (!error && data) balance.value = data as any;
}

async function submitAdjust() {
  if (adjustAmountYuan.value === null || adjustAmountYuan.value === 0) {
    window.$message?.warning("请填写调整金额（正=入账 负=扣减）");
    return;
  }
  if (!adjustForm.reason.trim()) {
    window.$message?.warning("请填写调账原因");
    return;
  }
  adjustLoading.value = true;
  try {
    const { error } = await adjustWalletBalance(adjustUser.value.id, {
      amount_cents: yuanToFen(adjustAmountYuan.value),
      reason: adjustForm.reason,
    });
    if (!error) {
      window.$message?.success("余额已调整");
      await refreshBalance(adjustUser.value.id);
    }
  } finally {
    adjustLoading.value = false;
  }
}

// 快捷筛选卡片（与状态列 NTag 同色系）
const statusTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "正常", value: "active", type: "success" as const },
  { label: "已封禁", value: "banned", type: "error" as const },
];

const canBan = () => checkAuth("identity:user_status");

function fmtTime(ts?: number) {
  if (!ts) return "-";
  const d = new Date(ts * 1000);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 70 },
  { title: "用户名", key: "username", width: 160, ellipsis: true },
  { title: "邮箱", key: "email", ellipsis: true, render: (row) => row.email || "-" },
  {
    title: "状态",
    key: "status",
    width: 90,
    render: (row) =>
      h(
        NTag,
        { size: "small", type: row.status === "active" ? "success" : "error", bordered: false },
        { default: () => (row.status === "active" ? "正常" : row.status === "banned" ? "已封禁" : "已删除") },
      ),
  },
  { title: "注册时间", key: "created_at", width: 150, render: (row) => fmtTime(row.created_at) },
  { title: "最近登录", key: "last_login_at", width: 150, render: (row) => fmtTime(row.last_login_at) },
  {
    title: "操作",
    key: "actions",
    width: 150,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        h(NButton, { size: "tiny", onClick: () => openDetail(row) }, { default: () => "详情" }),
        canAdjust()
          ? h(NButton, { size: "tiny", type: "warning", quaternary: true, onClick: () => openAdjust(row) }, { default: () => "调整余额" })
          : null,
        canBan() && row.status === "active"
          ? h(
              NPopconfirm,
              { onPositiveClick: () => handleBan(row) },
              {
                trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "封禁" }),
                default: () => "封禁后该用户将无法登录，确定？",
              },
            )
          : null,
        canBan() && row.status === "banned"
          ? h(NButton, { size: "tiny", type: "primary", quaternary: true, onClick: () => handleUnban(row) }, { default: () => "解封" })
          : null,
      ]),
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchUsers({
      keyword: query.keyword || undefined,
      status: query.status || undefined,
      page: page.value,
      page_size: pageSize.value,
    });
    if (!error && data) {
      users.value = (data as any).users || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

function onSearch() {
  page.value = 1;
  load();
}

function openDetail(row: any) {
  detailUser.value = row;
  showDetail.value = true;
}

async function handleBan(row: any) {
  const { error } = await setUserStatus(row.id, "banned");
  if (!error) {
    window.$message?.success(`已封禁 ${row.username}`);
    load();
  }
}

async function handleUnban(row: any) {
  const { error } = await setUserStatus(row.id, "active");
  if (!error) {
    window.$message?.success(`已解封 ${row.username}`);
    load();
  }
}

onMounted(load);
</script>

<template>
  <div class="min-h-500px">
    <NCard title="用户管理">
      <NSpace class="mb-12px" align="center">
        <NInput
          v-model:value="query.keyword"
          placeholder="搜索用户名 / 邮箱"
          clearable
          class="w-240px"
          @keyup.enter="onSearch"
        />
        <NButton type="primary" size="small" @click="onSearch">搜索</NButton>
      </NSpace>

      <FilterTabs v-model:value="query.status" :options="statusTabs" class="mb-12px" @change="onSearch" />

      <NDataTable :columns="columns" :data="users" :loading="loading" size="small" :row-key="(row: any) => row.id" :max-height="540" />

      <div class="mt-12px flex justify-end">
        <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
      </div>
    </NCard>

    <NModal v-model:show="showDetail" preset="card" title="用户详情" style="width: 480px">
      <NDescriptions v-if="detailUser" :column="1" bordered size="small">
        <NDescriptionsItem label="ID">{{ detailUser.id }}</NDescriptionsItem>
        <NDescriptionsItem label="用户名">{{ detailUser.username }}</NDescriptionsItem>
        <NDescriptionsItem label="邮箱">{{ detailUser.email || "-" }}</NDescriptionsItem>
        <NDescriptionsItem label="状态">
          <NTag size="small" :type="detailUser.status === 'active' ? 'success' : 'error'" :bordered="false">
            {{ detailUser.status === "active" ? "正常" : detailUser.status === "banned" ? "已封禁" : "已删除" }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="注册时间">{{ fmtTime(detailUser.created_at) }}</NDescriptionsItem>
        <NDescriptionsItem label="最近登录">{{ fmtTime(detailUser.last_login_at) }}</NDescriptionsItem>
      </NDescriptions>
      <template #footer>
        <NButton size="small" @click="showDetail = false">关闭</NButton>
      </template>
    </NModal>

    <!-- 调整余额（超管：wallet:adjust） -->
    <NModal v-model:show="showAdjust" preset="card" title="调整余额" style="width: 460px">
      <template v-if="adjustUser && balance">
        <div class="mb-16px rounded-6px bg-gray-50 p-12px text-13px dark:bg-gray-800">
          <div class="mb-4px">用户：<b>{{ adjustUser.username }}</b></div>
          <div class="flex gap-24px text-gray-600 dark:text-gray-300">
            <span>可用：<b class="text-primary">{{ fenToYuan(balance.available_cents) }}</b></span>
            <span>锁定：<b>{{ fenToYuan(balance.locked_cents) }}</b></span>
            <span>总额：<b>{{ fenToYuan(balance.total_cents) }}</b></span>
          </div>
        </div>
        <NForm label-placement="left" label-width="90">
          <NFormItem label="调整金额" required>
            <NInputNumber
              v-model:value="adjustAmountYuan"
              class="flex-1"
              :min="-9999999"
              :max="9999999"
              :precision="2"
              placeholder="单位：元（正=入账 负=扣减）"
            />
          </NFormItem>
          <NFormItem label="调整原因" required>
            <NInput
              v-model:value="adjustForm.reason"
              type="textarea"
              :rows="2"
              placeholder="必填；将写入审计日志"
            />
          </NFormItem>
        </NForm>
      </template>
      <template #footer>
        <NButton size="small" type="primary" :loading="adjustLoading" class="ml-auto" @click="submitAdjust">确认调整</NButton>
      </template>
    </NModal>
  </div>
</template>
