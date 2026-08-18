<script setup lang="ts">
// 审计日志（audit:read）：操作审计（谁在何时动了什么）+ 安全审计（取货/登录/敏感操作）。
import { onMounted, ref } from "vue";
import { NDataTable, NInput, NInputNumber, NButton, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchOpLogs, fetchSecurityLogs } from "@/service/api";

defineOptions({ name: "AuditTab" });

const opLoading = ref(false);
const opLogs = ref<any[]>([]);
const opPage = ref(1);
const operatorFilter = ref<number | null>(null);

const secLoading = ref(false);
const secLogs = ref<any[]>([]);
const secPage = ref(1);
const actionFilter = ref("");

const opColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 70 },
  { title: "操作者", key: "operator_id", width: 76, render: (row) => `${row.operator_type === "admin" ? "员工" : "用户"}#${row.operator_id}` },
  { title: "权限点", key: "permission_point", width: 170, ellipsis: true },
  { title: "动作", key: "action", width: 70 },
  { title: "路由", key: "route", width: 220, ellipsis: true },
  {
    title: "时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
];

const secColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 70 },
  { title: "主体", key: "actor_id", width: 90, render: (row) => `${row.actor_type}#${row.actor_id}` },
  {
    title: "动作",
    key: "action",
    width: 170,
    render: (row) => row.action,
  },
  { title: "IP", key: "ip", width: 130 },
  { title: "元数据", key: "metadata_json", minWidth: 200, ellipsis: true },
  {
    title: "时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
];

async function loadOp() {
  opLoading.value = true;
  try {
    const { data, error } = await fetchOpLogs({ operator_id: operatorFilter.value || undefined, page: opPage.value, page_size: 20 });
    if (!error && data) opLogs.value = (data as any).logs || (data as any).op_logs || [];
  } finally {
    opLoading.value = false;
  }
}

async function loadSec() {
  secLoading.value = true;
  try {
    const { data, error } = await fetchSecurityLogs({ action: actionFilter.value || undefined, page: secPage.value, page_size: 20 });
    if (!error && data) secLogs.value = (data as any).logs || (data as any).security_logs || [];
  } finally {
    secLoading.value = false;
  }
}

onMounted(() => {
  loadOp();
  loadSec();
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <div>
      <div class="mb-8px flex items-center gap-8px">
        <span class="text-13px font-500">操作审计（变更类管理操作）</span>
        <NInputNumber v-model:value="operatorFilter" size="small" placeholder="操作者ID" class="w-110px" clearable @update:value="(opPage = 1), loadOp()" />
        <NButton size="tiny" @click="loadOp">刷新</NButton>
      </div>
      <NDataTable :columns="opColumns" :data="opLogs" :loading="opLoading" size="small" />
      <div class="mt-8px flex justify-end gap-8px">
        <NButton size="small" :disabled="opPage <= 1" @click="opPage--, loadOp()">上一页</NButton>
        <NButton size="small" :disabled="opLogs.length < 20" @click="opPage++, loadOp()">下一页</NButton>
      </div>
    </div>
    <div>
      <div class="mb-8px flex items-center gap-8px">
        <span class="text-13px font-500">安全审计（登录/取货/敏感操作）</span>
        <NInput v-model:value="actionFilter" size="small" placeholder="动作模糊搜索" class="w-160px" clearable @keyup.enter="(secPage = 1), loadSec()" />
        <NButton size="tiny" @click="loadSec">刷新</NButton>
      </div>
      <NDataTable :columns="secColumns" :data="secLogs" :loading="secLoading" size="small" />
      <div class="mt-8px flex justify-end gap-8px">
        <NButton size="small" :disabled="secPage <= 1" @click="secPage--, loadSec()">上一页</NButton>
        <NButton size="small" :disabled="secLogs.length < 20" @click="secPage++, loadSec()">下一页</NButton>
      </div>
    </div>
  </div>
</template>
