<script setup lang="ts">
// 会员等级管理：等级 CRUD + 折扣/阈值/积分规则（memberlevel:read / write / delete）。
import { computed, onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NInputNumber, NModal, NForm, NFormItem, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchMemberLevels, createMemberLevel, updateMemberLevel, deleteMemberLevel } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "LevelsTab" });

const loading = ref(false);
const levels = ref<any[]>([]);
const showForm = ref(false);
const editing = ref<any>(null);
const saving = ref(false);
// discount_percent：UI 层百分比（100=无折扣，95=9.5 折）；提交转万分比（×100）。
// 积分规则 UI 层为「每消费 X 元得 Y 积分」，提交序列化为 {"spend_cents","points"}。
const form = ref({
  name: "",
  threshold_type: "consume",
  threshold_recharge: 0,
  threshold_consume: 0,
  discount_percent: 100,
  sort: 0,
  enabled: true,
  points_enabled: false,
  points_spend_yuan: 10,
  points_earn: 10,
});

/** 百分比 → 万分比（≥100 一律归一为无折扣 10000） */
function discountToBp(pct: number) {
  return pct >= 100 ? 10000 : Math.round(pct * 100);
}

/** 万分比 → 百分比（10000 = 无折扣） */
function bpToPercent(bp: number) {
  return bp >= 10000 ? 100 : bp / 100;
}

/** 积分规则 JSON → 表单字段（解析失败/未配置返回关闭态） */
function parsePointsRule(json?: string) {
  const empty = { points_enabled: false, points_spend_yuan: 10, points_earn: 10 };
  if (!json) return empty;
  try {
    const rule = JSON.parse(json);
    const spendYuan = Number(rule.spend_cents) / 100;
    const points = Number(rule.points);
    if (!spendYuan || !points) return empty;
    return { points_enabled: true, points_spend_yuan: spendYuan, points_earn: points };
  } catch {
    return empty;
  }
}

/** 表单字段 → 积分规则 JSON（未启用返回 undefined 不提交） */
function buildPointsRuleJson(): string | undefined {
  if (!form.value.points_enabled) return undefined;
  const spendYuan = form.value.points_spend_yuan;
  const points = form.value.points_earn;
  if (!spendYuan || !points) return undefined;
  return JSON.stringify({ spend_cents: Math.round(spendYuan * 100), points });
}

/** 积分规则 JSON → 列表友好文本 */
function pointsRuleText(json?: string) {
  if (!json) return "-";
  try {
    const rule = JSON.parse(json);
    const spendYuan = Number(rule.spend_cents) / 100;
    const points = Number(rule.points);
    if (!spendYuan || !points) return "-";
    return `消费${spendYuan}元得${points}积分`;
  } catch {
    return json;
  }
}

// 启用状态快捷筛选（客户端过滤——等级全量加载，带实时计数）
const enabledFilter = ref<"" | "on" | "off">("");
const enabledTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "已启用", value: "on", type: "success" as const },
  { label: "已停用", value: "off", type: "default" as const },
];
const enabledCounts = computed(() => ({
  "": levels.value.length,
  on: levels.value.filter((l) => l.enabled).length,
  off: levels.value.filter((l) => !l.enabled).length,
}));
const filteredLevels = computed(() =>
  enabledFilter.value === "" ? levels.value : levels.value.filter((l) => (enabledFilter.value === "on" ? l.enabled : !l.enabled)),
);

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
    width: 110,
    render: (row) => {
      if (row.discount >= 10000) return "无折扣";
      const pct = row.discount / 100;
      return `${pct}%（${(pct / 10).toFixed(1)}折）`;
    },
  },
  { title: "积分规则", key: "points_rule_json", width: 160, ellipsis: true, render: (row) => pointsRuleText(row.points_rule_json) },
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
  form.value = { name: "", threshold_type: "consume", threshold_recharge: 0, threshold_consume: 0, discount_percent: 100, sort: 0, enabled: true, points_enabled: false, points_spend_yuan: 10, points_earn: 10 };
  showForm.value = true;
}

function openEdit(row: any) {
  editing.value = row;
  form.value = {
    name: row.name,
    threshold_type: row.threshold_type,
    threshold_recharge: row.threshold_recharge || 0,
    threshold_consume: row.threshold_consume || 0,
    discount_percent: bpToPercent(row.discount || 0),
    sort: row.sort,
    enabled: row.enabled,
    ...parsePointsRule(row.points_rule_json),
  };
  showForm.value = true;
}

async function handleSave() {
  if (!form.value.name) return;
  saving.value = true;
  try {
    const payload = {
      name: form.value.name,
      discount: discountToBp(form.value.discount_percent),
      sort: form.value.sort,
      enabled: form.value.enabled,
      points_rule_json: buildPointsRuleJson(),
    };
    const { error } = editing.value
      ? await updateMemberLevel(editing.value.id, payload)
      : await createMemberLevel({
          ...payload,
          threshold_type: form.value.threshold_type,
          threshold_recharge: Math.round(form.value.threshold_recharge * 100),
          threshold_consume: Math.round(form.value.threshold_consume * 100),
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
    <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
      <FilterTabs v-model:value="enabledFilter" :options="enabledTabs" :counts="enabledCounts" size="small" />
      <div>
        <NButton v-if="canWrite()" size="small" type="primary" @click="openCreate">新增等级</NButton>
        <span class="ml-8px text-12px text-gray-400">折扣按支付比例百分比填写（100=无折扣，95=9.5 折）；阈值在创建时设定</span>
      </div>
    </div>
    <NDataTable :columns="columns" :data="filteredLevels" :loading="loading" size="small" />

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
        <NFormItem label="折扣(%)" required>
          <NInputNumber v-model:value="form.discount_percent" :min="0.1" :max="100" :precision="1" :step="1" class="w-full" />
          <span class="w-full text-12px text-gray-400">支付比例：100 = 无折扣，95 = 打 9.5 折，10 = 打 1 折</span>
        </NFormItem>
        <NFormItem label="积分规则">
          <div class="w-full">
            <div class="flex items-center gap-8px">
              <NSwitch v-model:value="form.points_enabled" />
              <span class="text-13px">启用积分规则</span>
              <span v-if="!form.points_enabled" class="text-12px text-gray-400">未配置则消费不产生积分</span>
            </div>
            <template v-if="form.points_enabled">
              <div class="mt-8px flex items-center gap-8px">
                <span class="w-64px text-right text-13px">每消费</span>
                <NInputNumber v-model:value="form.points_spend_yuan" :min="0.01" :precision="2" class="w-140px" />
                <span class="text-13px">元</span>
              </div>
              <div class="mt-8px flex items-center gap-8px">
                <span class="w-64px text-right text-13px">可得</span>
                <NInputNumber v-model:value="form.points_earn" :min="1" class="w-140px" />
                <span class="text-13px">积分</span>
              </div>
            </template>
          </div>
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
