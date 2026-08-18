<script setup lang="ts">
// 会员等级管理：等级 CRUD + 折扣/阈值/积分规则（memberlevel:read / write / delete）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NInputNumber, NModal, NForm, NFormItem, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchMemberLevels, createMemberLevel, updateMemberLevel, deleteMemberLevel } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";

defineOptions({ name: "LevelsTab" });

const loading = ref(false);
const levels = ref<any[]>([]);
const showForm = ref(false);
const editing = ref<any>(null);
const saving = ref(false);
const form = ref({ name: "", threshold_type: "consume", threshold_recharge: 0, threshold_consume: 0, discount: 10000, sort: 0, enabled: true, points_rule_json: "" });

const canWrite = () => checkAuth("memberlevel:write");

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  { title: "名称", key: "name", width: 110 },
  {
    title: "升级条件",
    key: "threshold",
    width: 200,
    render: (row) => {
      const t: Record<string, string> = { recharge: "累计充值", consume: "累计消费", both_and: "充值且消费", both_or: "充值或消费" };
      const parts: string[] = [t[row.threshold_type] || row.threshold_type];
      if (row.threshold_recharge) parts.push(formatMoney(row.threshold_recharge));
      if (row.threshold_consume) parts.push(formatMoney(row.threshold_consume));
      return parts.join(" ≥ ");
    },
  },
  {
    title: "折扣",
    key: "discount",
    width: 90,
    render: (row) => (row.discount >= 10000 ? "无折扣" : `${(row.discount / 100).toFixed(1)}折`),
  },
  { title: "积分规则", key: "points_rule_json", width: 180, ellipsis: true, render: (row) => row.points_rule_json || "-" },
  { title: "排序", key: "sort", width: 60 },
  {
    title: "状态",
    key: "enabled",
    width: 70,
    render: (row) => h(NTag, { type: row.enabled ? "success" : "default", size: "small" }, { default: () => (row.enabled ? "启用" : "停用") }),
  },
  {
    title: "操作",
    key: "actions",
    width: 150,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        canWrite()
          ? h(NButton, { size: "tiny", onClick: () => openEdit(row) }, { default: () => "编辑" })
          : null,
        canWrite()
          ? h(NButton, { size: "tiny", type: row.enabled ? "warning" : "success", quaternary: true, onClick: () => toggleEnabled(row) }, { default: () => (row.enabled ? "停用" : "启用") })
          : null,
        checkAuth("memberlevel:delete")
          ? h(NPopconfirm, { onPositiveClick: () => handleDelete(row.id) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }), default: () => "确定删除该等级？" })
          : null,
      ]),
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchMemberLevels();
    if (!error && data) levels.value = (data as any).levels || [];
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  editing.value = null;
  form.value = { name: "", threshold_type: "consume", threshold_recharge: 0, threshold_consume: 0, discount: 10000, sort: 0, enabled: true, points_rule_json: "" };
  showForm.value = true;
}

function openEdit(row: any) {
  editing.value = row;
  form.value = {
    name: row.name,
    threshold_type: row.threshold_type,
    threshold_recharge: row.threshold_recharge || 0,
    threshold_consume: row.threshold_consume || 0,
    discount: row.discount,
    sort: row.sort,
    enabled: row.enabled,
    points_rule_json: row.points_rule_json || "",
  };
  showForm.value = true;
}

async function handleSave() {
  if (!form.value.name) return;
  saving.value = true;
  try {
    const { error } = editing.value
      ? await updateMemberLevel(editing.value.id, { name: form.value.name, discount: form.value.discount, sort: form.value.sort, enabled: form.value.enabled, points_rule_json: form.value.points_rule_json || undefined })
      : await createMemberLevel({
          ...form.value,
          threshold_recharge: Math.round(form.value.threshold_recharge * 100),
          threshold_consume: Math.round(form.value.threshold_consume * 100),
          points_rule_json: form.value.points_rule_json || undefined,
        });
    if (!error) {
      window.$message?.success(editing.value ? "已保存" : "等级已创建");
      showForm.value = false;
      load();
    }
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(row: any) {
  const { error } = await updateMemberLevel(row.id, { enabled: !row.enabled });
  if (!error) {
    window.$message?.success(!row.enabled ? "已启用" : "已停用");
    load();
  }
}

async function handleDelete(id: number) {
  const { error } = await deleteMemberLevel(id);
  if (!error) {
    window.$message?.success("已删除");
    load();
  }
}

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-8px">
      <NButton v-if="canWrite()" size="small" type="primary" @click="openCreate">新增等级</NButton>
      <span class="ml-8px text-12px text-gray-400">折扣为万分比（10000=无折扣，9500=95 折）；阈值在创建时设定</span>
    </div>
    <NDataTable :columns="columns" :data="levels" :loading="loading" size="small" />

    <NModal v-model:show="showForm" preset="dialog" :title="editing ? `编辑等级：${editing.name}` : '新增等级'" style="width: 480px">
      <NForm :model="form" label-placement="left" label-width="88">
        <NFormItem label="名称" required>
          <NInput v-model:value="form.name" />
        </NFormItem>
        <NFormItem v-if="!editing" label="升级条件">
          <NSelect
            v-model:value="form.threshold_type"
            :options="[
              { label: '累计消费', value: 'consume' },
              { label: '累计充值', value: 'recharge' },
              { label: '充值且消费', value: 'both_and' },
              { label: '充值或消费', value: 'both_or' },
            ]"
          />
        </NFormItem>
        <NFormItem v-if="!editing" label="充值阈值(元)">
          <NInputNumber v-model:value="form.threshold_recharge" :min="0" class="w-full" />
        </NFormItem>
        <NFormItem v-if="!editing" label="消费阈值(元)">
          <NInputNumber v-model:value="form.threshold_consume" :min="0" class="w-full" />
        </NFormItem>
        <NFormItem label="折扣(万分比)">
          <NInputNumber v-model:value="form.discount" :min="1" :max="10000" class="w-full" />
        </NFormItem>
        <NFormItem label="积分规则">
          <NInput v-model:value="form.points_rule_json" placeholder='如 {"spend_cents":1000,"points":10}（消费10元得10分）' />
        </NFormItem>
        <NFormItem label="排序">
          <NInputNumber v-model:value="form.sort" class="w-full" />
        </NFormItem>
        <NFormItem v-if="editing" label="启用">
          <NSwitch v-model:value="form.enabled" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showForm = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleSave">保存</NButton>
      </template>
    </NModal>
  </div>
</template>
