<script setup lang="ts">
import { ref, reactive, computed, onMounted, h } from "vue";
import { NButton, NTag, NSpace, NPopconfirm } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { useRouter } from "vue-router";
import { checkAuth } from "@/directives";
import {
  fetchProducts,
  fetchProduct,
  fetchSkus,
  fetchUsers,
  listLotteryActivities,
  getLotteryActivity,
  saveLotteryActivity,
  setLotteryStatus,
  grantLotteryChances,
  listLotteryDraws,
  revealLotteryDraw,
  deliverLotteryDraw,
  listLotteryHistory,
  lotteryState,
  lotteryMode,
} from "@/service/api";
import type { LotteryActivity, LotteryPrize, LotteryDraw } from "@/service/api";
import { formatMoney } from "@/utils/money";
import TablePager from "@/components/common/table-pager.vue";
import MediaField from "@/components/common/media-picker/media-field.vue";
const router = useRouter();
const tab = ref("activities"),
  loading = ref(false),
  saving = ref(false),
  page = ref(1),
  pageSize = ref(20),
  total = ref(0),
  activities = ref<LotteryActivity[]>([]),
  keyword = ref("");
const showEdit = ref(false),
  step = ref(1),
  formError = ref("");
const fresh = (): LotteryActivity => ({
  name: "",
  description: "",
  image: "",
  status: "draft",
  start_at: Math.floor(Date.now() / 1000),
  end_at: Math.floor(Date.now() / 1000) + 7 * 86400,
  timezone: "Asia/Shanghai",
  chance_mode: "once",
  chance_count: 3,
  revision: 0,
  prizes: [],
});
const form = ref<LotteryActivity>(fresh());
const dateRange = ref<[number, number]>([
  Date.now(),
  Date.now() + 7 * 86400000,
]);
const modes = Object.entries(lotteryMode).map(([value, label]) => ({
  value,
  label,
}));
const chanceModes = [
  { label: "活动期间赠送一次", value: "once" },
  { label: "每日赠送（不累计）", value: "daily" },
  { label: "关闭自动赠送，仅后台补发", value: "manual" },
];
const timezones = [
  { label: "北京时间（UTC+8）", value: "Asia/Shanghai" },
  { label: "越南时间（UTC+7）", value: "Asia/Ho_Chi_Minh" },
  { label: "协调世界时（UTC）", value: "UTC" },
];
const probability = computed(() =>
  form.value.prizes.reduce((n, p) => n + p.probability, 0),
);
const time = (n?: number) => (n ? new Date(n * 1000).toLocaleString() : "—");
const stateTag = (v: string) =>
  h(
    NTag,
    {
      size: "small",
      type:
        v === "live" || v === "delivered"
          ? "success"
          : v === "pending" || v === "paused"
            ? "warning"
            : "default",
    },
    { default: () => lotteryState[v] || "未知状态" },
  );
async function load() {
  loading.value = true;
  try {
    const { data, error } = await listLotteryActivities({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value,
    });
    if (!error && data) {
      activities.value = data.items || [];
      total.value = Number(data.total || 0);
    }
  } finally {
    loading.value = false;
  }
}
function addPrize() {
  if (form.value.prizes.length < 12)
    form.value.prizes.push({
      name: "",
      image: "",
      mode: "manual",
      product_id: 0,
      sku_id: 0,
      probability: 0,
      quantity: 10,
      content: "",
    });
}
const productOptions = ref<{ label: string; value: number; cover?: string }[]>(
    [],
  ),
  productLoading = ref(false),
  skuOptions = reactive<Record<number, { label: string; value: number }[]>>({});
let productSearchSeq = 0;
async function searchProducts(q = "") {
  const seq = ++productSearchSeq;
  productLoading.value = true;
  try {
    const { data, error } = await fetchProducts({
      keyword: q,
      status: 1,
      page_size: 50,
    });
    if (seq !== productSearchSeq) return;
    if (!error && data) {
      const rows = (data as any).products || [];
      const next = rows
        .filter(
          (p: any) =>
            p.stock_type === "card" &&
            !p.upstream_source_id &&
            p.delivery_mode !== "delete",
        )
        .map((p: any) => ({
          label: `${p.name}（#${p.id}）`,
          value: p.id,
          cover: p.cover,
        }));
      productOptions.value = [
        ...next,
        ...productOptions.value.filter(
          (p) => !next.some((n: any) => n.value === p.value),
        ),
      ].slice(0, 200);
    }
  } finally {
    if (seq === productSearchSeq) productLoading.value = false;
  }
}
async function loadSkus(id: number) {
  if (!id || skuOptions[id]) return;
  const { data, error } = await fetchSkus(id);
  if (!error && data)
    skuOptions[id] = ((data as any).skus || []).map((s: any) => ({
      label: s.name,
      value: s.id,
    }));
}
async function selectProduct(p: LotteryPrize, id: number | null) {
  p.product_id = id || 0;
  p.sku_id = 0;
  const o = productOptions.value.find((x) => x.value === id);
  if (!p.name && o) p.name = o.label.replace(/（#\d+）$/, "");
  if (!p.image && o?.cover) p.image = o.cover;
  if (id) await loadSkus(id);
}
async function edit(row?: LotteryActivity, copy = false) {
  formError.value = "";
  step.value = 1;
  if (row?.id) {
    const { data, error } = await getLotteryActivity(row.id);
    if (error || !data) return;
    form.value = {
      ...data,
      prizes: (data.prizes || []).map((p) => ({
        ...p,
        probability: p.probability || 0,
        issued: p.issued || 0,
        product_id: p.product_id || 0,
        sku_id: p.sku_id || 0,
        content: p.content || "",
      })),
    };
    if (copy) {
      form.value = {
        ...form.value,
        id: undefined,
        name: form.value.name + "（副本）",
        status: "draft",
        published: false,
        revision: 0,
        start_at: Math.floor(Date.now() / 1000),
        end_at: Math.floor(Date.now() / 1000) + 7 * 86400,
        prizes: form.value.prizes.map((p) => ({
          ...p,
          id: undefined,
          issued: 0,
        })),
      };
    }
    for (const p of form.value.prizes) {
      if (p.product_id) {
        const { data } = await fetchProduct(p.product_id);
        if (data && !productOptions.value.some((o) => o.value === p.product_id))
          productOptions.value.push({
            label: `${(data as any).name}（#${p.product_id}）`,
            value: p.product_id,
          });
        await loadSkus(p.product_id);
      }
    }
  } else {
    form.value = fresh();
    addPrize();
  }
  dateRange.value = [form.value.start_at * 1000, form.value.end_at * 1000];
  showEdit.value = true;
  void searchProducts();
}
function validate() {
  if (!form.value.name.trim()) return "请填写活动名称";
  if (!dateRange.value || dateRange.value[1] <= dateRange.value[0])
    return "请选择正确的活动时间";
  if (!form.value.prizes.length) return "请添加奖品";
  if (probability.value <= 0 || probability.value > 10000)
    return "奖品概率合计须大于 0 且不超过 100%";
  for (const p of form.value.prizes) {
    if (!p.name.trim()) return "请填写每种奖品的名称";
    if (p.mode === "card" && !p.product_id) return "自动发卡需要选择商品";
    if (
      p.mode === "card" &&
      (skuOptions[p.product_id]?.length || 0) > 0 &&
      !p.sku_id
    )
      return "请选择奖品具体规格";
    if (p.mode !== "card" && !p.content.trim())
      return "请填写发放内容或领取说明";
    if (!p.quantity || p.quantity < (p.issued || 0))
      return "奖品数量不能少于已发数量";
  }
  return "";
}
async function save() {
  formError.value = validate();
  if (formError.value) return;
  saving.value = true;
  try {
    const { error } = await saveLotteryActivity({
      ...form.value,
      start_at: Math.floor(dateRange.value[0] / 1000),
      end_at: Math.floor(dateRange.value[1] / 1000),
    });
    if (!error) {
      showEdit.value = false;
      window.$message?.success("活动配置已保存");
      await load();
    }
  } finally {
    saving.value = false;
  }
}
const changing = ref(false);
async function change(row: LotteryActivity, status: string) {
  if (!row.id || changing.value) return;
  changing.value = true;
  try {
    const { error } = await setLotteryStatus(row.id, status, row.revision);
    if (!error) {
      window.$message?.success("活动状态已更新");
      await load();
    }
  } finally {
    changing.value = false;
  }
}
const columns: DataTableColumns<LotteryActivity> = [
  {
    title: "活动",
    key: "name",
    minWidth: 180,
    render: (r) =>
      h("div", [
        h("div", r.name),
        h(
          "small",
          { class: "text-gray-500" },
          `#${r.id} · 规则版本 ${r.revision}`,
        ),
      ]),
  },
  {
    title: "活动时间",
    key: "time",
    minWidth: 190,
    render: (r) =>
      h("div", [h("div", time(r.start_at)), h("div", time(r.end_at))]),
  },
  {
    title: "次数规则",
    key: "chance",
    minWidth: 170,
    render: (r) =>
      `${chanceModes.find((m) => m.value === r.chance_mode)?.label || "—"}${r.chance_mode === "manual" ? "" : ` · ${r.chance_count} 次`}`,
  },
  {
    title: "状态",
    key: "status",
    width: 95,
    render: (r) => stateTag(r.status),
  },
  {
    title: "操作",
    key: "actions",
    minWidth: 370,
    render: (r) =>
      h(
        NSpace,
        { size: 6, wrap: true },
        {
          default: () => [
            ...(checkAuth("lottery:write")
              ? [
                  h(
                    NButton,
                    {
                      size: "tiny",
                      disabled: [
                        "live",
                        "scheduled",
                        "ended",
                        "archived",
                      ].includes(r.status),
                      onClick: () => edit(r),
                    },
                    { default: () => "编辑" },
                  ),
                  h(
                    NButton,
                    { size: "tiny", onClick: () => edit(r, true) },
                    { default: () => "复制" },
                  ),
                  ...(["draft", "paused"].includes(r.status)
                    ? [
                        h(
                          NPopconfirm,
                          { onPositiveClick: () => change(r, "live") },
                          {
                            trigger: () =>
                              h(
                                NButton,
                                {
                                  size: "tiny",
                                  type: "success",
                                  disabled: changing.value,
                                },
                                { default: () => "发布/恢复" },
                              ),
                            default: () =>
                              "发布后按规则发放次数和奖品，确认发布？",
                          },
                        ),
                      ]
                    : []),
                  ...(["live", "scheduled"].includes(r.status)
                    ? [
                        h(
                          NButton,
                          {
                            size: "tiny",
                            disabled: changing.value,
                            onClick: () => change(r, "paused"),
                          },
                          { default: () => "暂停" },
                        ),
                      ]
                    : []),
                  ...(["live", "scheduled", "paused"].includes(r.status)
                    ? [
                        h(
                          NPopconfirm,
                          { onPositiveClick: () => change(r, "ended") },
                          {
                            trigger: () =>
                              h(
                                NButton,
                                { size: "tiny", disabled: changing.value },
                                { default: () => "结束" },
                              ),
                            default: () => "结束后停止新增抽奖，保留历史奖品。",
                          },
                        ),
                      ]
                    : []),
                  ...(r.status !== "archived"
                    ? [
                        h(
                          NPopconfirm,
                          { onPositiveClick: () => change(r, "archived") },
                          {
                            trigger: () =>
                              h(
                                NButton,
                                { size: "tiny", disabled: changing.value },
                                { default: () => "归档" },
                              ),
                            default: () =>
                              "归档活动并隐藏入口，保留历史领奖记录？",
                          },
                        ),
                      ]
                    : []),
                  h(
                    NButton,
                    { size: "tiny", onClick: () => preview(r) },
                    { default: () => "预览" },
                  ),
                ]
              : []),
            ...(checkAuth("lottery:grant") &&
            r.published &&
            !["ended", "archived"].includes(r.status)
              ? [
                  h(
                    NButton,
                    { size: "tiny", onClick: () => openGrant(r) },
                    { default: () => "补次数" },
                  ),
                ]
              : []),
            h(
              NButton,
              { size: "tiny", onClick: () => history(r) },
              { default: () => "规则历史" },
            ),
            h(
              NButton,
              {
                size: "tiny",
                onClick: () => {
                  recordActivity.value = r.id || null;
                  tab.value = "records";
                  loadRecords();
                },
              },
              { default: () => "中奖记录" },
            ),
          ],
        },
      ),
  },
];
const showPreview = ref(false),
  previewData = ref<LotteryActivity | null>(null),
  demo = ref(false);
async function preview(r: LotteryActivity) {
  const { data, error } = await getLotteryActivity(r.id!);
  if (!error && data) {
    previewData.value = data;
    demo.value = false;
    showPreview.value = true;
  }
}
const showHistory = ref(false),
  historyItems = ref<any[]>([]);
async function history(r: LotteryActivity) {
  const { data, error } = await listLotteryHistory(r.id!);
  if (!error && data) {
    historyItems.value = (data.items || []).map((h) => ({
      ...h,
      config: JSON.parse(h.snapshot_json),
    }));
    showHistory.value = true;
  }
}
const showGrant = ref(false),
  grantActivity = ref<LotteryActivity | null>(null),
  grantUser = ref<number | null>(null),
  grantCount = ref(1),
  grantRemark = ref(""),
  grantKey = ref(""),
  userOptions = ref<{ label: string; value: number }[]>([]);
function newKey() {
  return crypto.randomUUID().replaceAll("-", "");
}
async function searchUsers(keyword = "") {
  const { data, error } = await fetchUsers({ keyword, page_size: 30 });
  if (!error && data)
    userOptions.value = ((data as any).users || []).map((u: any) => ({
      label: `${u.username}（#${u.id}）`,
      value: u.id,
    }));
}
function openGrant(r: LotteryActivity) {
  grantActivity.value = r;
  grantUser.value = null;
  grantCount.value = 1;
  grantRemark.value = "";
  grantKey.value = newKey();
  showGrant.value = true;
  void searchUsers();
}
async function grant() {
  if (!grantActivity.value?.id || !grantUser.value || !grantRemark.value.trim())
    return;
  saving.value = true;
  try {
    const { error } = await grantLotteryChances(grantActivity.value.id, {
      user_id: grantUser.value,
      count: grantCount.value,
      remark: grantRemark.value,
      request_key: grantKey.value,
    });
    if (!error) {
      showGrant.value = false;
      window.$message?.success("次数已补发");
    }
  } finally {
    saving.value = false;
  }
}
const records = ref<LotteryDraw[]>([]),
  recordTotal = ref(0),
  recordPage = ref(1),
  recordSize = ref(20),
  recordActivity = ref<number | null>(null),
  recordKeyword = ref(""),
  recordStatus = ref<string | null>(null),
  includeMissed = ref(false),
  recordRange = ref<[number, number] | null>(null),
  recordLoading = ref(false);
async function loadRecords() {
  recordLoading.value = true;
  try {
    const { data, error } = await listLotteryDraws({
      page: recordPage.value,
      page_size: recordSize.value,
      activity_id: recordActivity.value || undefined,
      keyword: recordKeyword.value,
      status: recordStatus.value || undefined,
      include_missed: includeMissed.value,
      start_at: recordRange.value
        ? Math.floor(recordRange.value[0] / 1000)
        : undefined,
      end_at: recordRange.value
        ? Math.floor(recordRange.value[1] / 1000)
        : undefined,
    });
    if (!error && data) {
      records.value = data.items || [];
      recordTotal.value = Number(data.total || 0);
    }
  } finally {
    recordLoading.value = false;
  }
}
const selectedDraw = ref<LotteryDraw | null>(null),
  drawContent = ref(""),
  deliveryRemark = ref("");
async function viewRecord(r: LotteryDraw) {
  selectedDraw.value = r;
  drawContent.value = "";
  deliveryRemark.value = "";
}
async function reveal() {
  if (!selectedDraw.value) return;
  const { data, error } = await revealLotteryDraw(selectedDraw.value.draw_no);
  if (!error && data) drawContent.value = data.content || "无发放内容";
}
async function deliver() {
  if (!selectedDraw.value) return;
  saving.value = true;
  try {
    const { error } = await deliverLotteryDraw(
      selectedDraw.value.draw_no,
      deliveryRemark.value,
    );
    if (!error) {
      selectedDraw.value = null;
      window.$message?.success("已确认发放");
      await loadRecords();
    }
  } finally {
    saving.value = false;
  }
}
const recordColumns: DataTableColumns<LotteryDraw> = [
  { title: "中奖编号", key: "draw_no", width: 280 },
  {
    title: "账号",
    key: "username",
    width: 150,
    render: (r) =>
      h(
        NButton,
        {
          text: true,
          type: "primary",
          onClick: () =>
            router.push({
              path: "/user",
              query: { keyword: r.username || String(r.user_id) },
            }),
        },
        { default: () => `${r.username || "账号"} #${r.user_id}` },
      ),
  },
  {
    title: "活动 / 奖品",
    key: "prize",
    minWidth: 180,
    render: (r) =>
      h("div", [
        h("div", r.activity_name),
        h("div", r.prize_name || "谢谢参与"),
      ]),
  },
  {
    title: "发放方式",
    key: "mode",
    width: 140,
    render: (r) => lotteryMode[r.mode] || "—",
  },
  {
    title: "状态",
    key: "status",
    width: 110,
    render: (r) => stateTag(r.status),
  },
  {
    title: "时间",
    key: "created_at",
    width: 170,
    render: (r) => time(r.created_at),
  },
  {
    title: "操作",
    key: "action",
    width: 90,
    render: (r) =>
      h(
        NButton,
        { size: "small", onClick: () => viewRecord(r) },
        { default: () => "详情" },
      ),
  },
];
onMounted(load);
</script>

<template>
  <div class="lottery-admin">
    <NAlert type="info" class="mb-16px"
      >免费抽奖按账号记录次数。系统发卡共用商品库存；人工奖品在中奖记录中确认发放。</NAlert
    >
    <NTabs
      v-model:value="tab"
      type="segment"
      @update:value="(v) => v === 'records' && loadRecords()"
    >
      <NTabPane name="activities" tab="活动管理">
        <div class="lottery-toolbar">
          <NButton
            v-if="checkAuth('lottery:write')"
            type="primary"
            @click="edit()"
            >新建抽奖活动</NButton
          ><NInput
            v-model:value="keyword"
            placeholder="搜索活动名称"
            clearable
            style="max-width: 280px"
            @keyup.enter="
              page = 1;
              load();
            "
          /><NButton
            @click="
              page = 1;
              load();
            "
            >搜索</NButton
          >
        </div>
        <NDataTable
          :columns="columns"
          :data="activities"
          :loading="loading"
          :scroll-x="1100"
          :row-key="(r) => r.id"
        />
        <TablePager
          v-model:page="page"
          v-model:page-size="pageSize"
          :total="total"
          @change="load"
        />
      </NTabPane>
      <NTabPane name="records" tab="中奖记录">
        <div class="lottery-toolbar">
          <NSelect
            v-model:value="recordActivity"
            :options="activities.map((a) => ({ label: a.name, value: a.id! }))"
            placeholder="全部活动"
            clearable
            filterable
            style="width: 210px"
          /><NInput
            v-model:value="recordKeyword"
            placeholder="用户名或完整中奖编号"
            style="max-width: 240px"
            clearable
          /><NSelect
            v-model:value="recordStatus"
            :options="
              ['pending', 'delivered', 'missed'].map((value) => ({
                value,
                label: lotteryState[value],
              }))
            "
            placeholder="全部状态"
            clearable
            style="width: 150px"
          /><NDatePicker
            v-model:value="recordRange"
            type="datetimerange"
            clearable
          /><NCheckbox v-model:checked="includeMissed">包括未中奖</NCheckbox
          ><NButton
            @click="
              recordPage = 1;
              loadRecords();
            "
            >查询</NButton
          >
        </div>
        <NDataTable
          :columns="recordColumns"
          :data="records"
          :loading="recordLoading"
          :scroll-x="1140"
          :row-key="(r) => r.draw_no"
        />
        <TablePager
          v-model:page="recordPage"
          v-model:page-size="recordSize"
          :total="recordTotal"
          @change="loadRecords"
        />
      </NTabPane>
    </NTabs>
    <NModal
      v-model:show="showEdit"
      preset="card"
      :title="form.id ? '编辑抽奖活动' : '新建抽奖活动'"
      class="lottery-modal"
      :mask-closable="!saving"
      :closable="!saving"
      :close-on-esc="!saving"
    >
      <NSteps :current="step" size="small" class="mb-24px"
        ><NStep title="基本信息" /><NStep title="奖品设置" /><NStep
          title="次数规则"
      /></NSteps>
      <NForm label-placement="top">
        <div v-show="step === 1">
          <NFormItem label="活动名称" required
            ><NInput
              v-model:value="form.name"
              :maxlength="100"
              placeholder="例如：会员幸运抽奖" /></NFormItem
          ><NFormItem label="活动时间" required
            ><NDatePicker
              v-model:value="dateRange"
              type="datetimerange"
              :is-date-disabled="() => false"
              class="w-full" /></NFormItem
          ><NFormItem label="活动时区"
            ><NSelect
              v-model:value="form.timezone"
              :options="timezones"
              :disabled="form.published" /></NFormItem
          ><NFormItem label="活动图片（可选）"
            ><MediaField v-model="form.image" /></NFormItem
          ><NFormItem label="活动说明"
            ><NInput
              v-model:value="form.description"
              type="textarea"
              :autosize="{ minRows: 4, maxRows: 10 }"
              :maxlength="5000"
              placeholder="参与条件、活动时间及领取说明"
          /></NFormItem>
        </div>
        <div v-show="step === 2">
          <NAlert
            :type="
              probability > 10000 || probability <= 0 ? 'warning' : 'success'
            "
            class="mb-16px"
            >奖品概率合计 {{ (probability / 100).toFixed(2) }}%；谢谢参与
            {{
              (Math.max(0, 10000 - probability) / 100).toFixed(2)
            }}%。任一有概率的奖品耗尽后暂停抽奖，补足后恢复。</NAlert
          >
          <div
            v-for="(p, i) in form.prizes"
            :key="i"
            class="lottery-prize-editor"
          >
            <div class="flex items-center justify-between mb-12px">
              <b
                >奖品 {{ i + 1
                }}<span v-if="p.issued"> · 已发 {{ p.issued }} 份</span></b
              ><NButton
                size="small"
                quaternary
                type="error"
                @click="form.prizes.splice(i, 1)"
                >移除奖项</NButton
              >
            </div>
            <div class="lottery-form-grid">
              <NFormItem label="奖品名称" required
                ><NInput
                  v-model:value="p.name"
                  :maxlength="100"
                  placeholder="中奖时显示的名称" /></NFormItem
              ><NFormItem label="发放方式" required
                ><NSelect
                  v-model:value="p.mode"
                  :options="modes"
                  :disabled="!!p.issued"
                  @update:value="
                    () => {
                      p.product_id = 0;
                      p.sku_id = 0;
                    }
                  "
              /></NFormItem>
            </div>
            <template v-if="p.mode === 'card'"
              ><NFormItem label="自动发卡商品" required
                ><NSelect
                  :value="p.product_id || null"
                  :options="productOptions"
                  :loading="productLoading"
                  remote
                  filterable
                  :disabled="!!p.issued"
                  placeholder="搜索普通自营卡密商品"
                  @search="searchProducts"
                  @update:value="(id) => selectProduct(p, id)" /></NFormItem
              ><NFormItem
                v-if="skuOptions[p.product_id]?.length"
                label="发奖规格"
                required
                ><NSelect
                  v-model:value="p.sku_id"
                  :options="skuOptions[p.product_id]"
                  :disabled="!!p.issued"
                  placeholder="请选择具体规格" /></NFormItem
              ><NAlert type="warning" class="mb-12px"
                >中奖会消耗该商品一张可用卡密，影响店铺可售库存。不支持上游代发、必填下单控件、靓号和发后即删商品。</NAlert
              ></template
            >
            <NFormItem
              v-else
              :label="
                p.mode === 'manual'
                  ? '中奖后的领取说明'
                  : '中奖后自动显示的文字或链接'
              "
              required
              ><NInput
                v-model:value="p.content"
                type="textarea"
                :autosize="{ minRows: 3, maxRows: 8 }"
                :maxlength="10000"
                :placeholder="
                  p.mode === 'manual'
                    ? '例如：请联系客服，提供中奖编号领取。'
                    : '每位中奖者获得同一份内容；不同卡密请选择系统发卡。'
                "
            /></NFormItem>
            <div class="lottery-form-grid">
              <NFormItem label="中奖概率（%）" required
                ><NInputNumber
                  :value="p.probability / 100"
                  :min="0"
                  :max="100"
                  :precision="2"
                  :show-button="false"
                  class="w-full"
                  @update:value="
                    (v) => (p.probability = Math.round((v || 0) * 100))
                  " /></NFormItem
              ><NFormItem label="活动可发总量（份）" required
                ><NInputNumber
                  v-model:value="p.quantity"
                  :min="Math.max(1, p.issued || 0)"
                  :max="1000000"
                  :precision="0"
                  :show-button="false"
                  class="w-full"
              /></NFormItem>
            </div>
            <NFormItem label="奖品图片（可选）"
              ><MediaField v-model="p.image"
            /></NFormItem>
          </div>
          <NButton
            dashed
            block
            :disabled="form.prizes.length >= 12"
            @click="addPrize"
            >添加奖品（{{ form.prizes.length }}/12）</NButton
          >
        </div>
        <div v-show="step === 3">
          <NAlert type="info" class="mb-16px"
            >游客可查看，登录后才可参与。赠送次数按账号去重，重复登录或换设备不会重复领取。</NAlert
          ><NFormItem label="自动赠送规则"
            ><NSelect
              v-model:value="form.chance_mode"
              :options="chanceModes"
              :disabled="form.published" /></NFormItem
          ><NFormItem
            v-if="form.chance_mode !== 'manual'"
            label="每次赠送次数"
            required
            ><NInputNumber
              v-model:value="form.chance_count"
              :min="1"
              :max="1000"
              :precision="0"
              :disabled="form.published"
          /></NFormItem>
          <p>
            {{
              form.chance_mode === "daily"
                ? "每日按活动时区重新领取，当日次数不累计到次日。"
                : form.chance_mode === "manual"
                  ? "关闭自动赠送后，需在活动列表中给指定账号补发次数。"
                  : "每个账号在整个活动期间只领取一次，暂停或重新登录不会重置。"
            }}
          </p>
          <p class="mt-12px text-gray-500">
            发布后次数规则固定，需要更改请复制新活动。每次独立抽奖，允许重复中奖。
          </p>
        </div>
      </NForm>
      <NAlert v-if="formError" type="error" class="mt-12px">{{
        formError
      }}</NAlert>
      <template #footer
        ><div class="flex flex-wrap justify-end gap-8px">
          <NButton :disabled="saving" @click="showEdit = false">取消</NButton
          ><NButton v-if="step > 1" :disabled="saving" @click="step--"
            >上一步</NButton
          ><NButton v-if="step < 3" type="primary" @click="step++"
            >下一步</NButton
          ><NButton v-else type="primary" :loading="saving" @click="save"
            >保存配置</NButton
          >
        </div></template
      >
    </NModal>
    <NModal
      v-model:show="showPreview"
      preset="card"
      title="活动预览"
      class="lottery-small-modal"
      ><NAlert type="info" class="mb-16px"
        >演示预览，不扣次数，不发真实奖品。</NAlert
      >
      <h3>{{ previewData?.name }}</h3>
      <p class="lottery-pre">{{ previewData?.description }}</p>
      <div class="lottery-preview-grid">
        <div
          v-for="p in previewData?.prizes"
          :key="p.id"
          class="lottery-preview-prize"
        >
          <img v-if="p.image" :src="p.image" alt="" /><b>{{ p.name }}</b
          ><span>概率 {{ (p.probability / 100).toFixed(2) }}%</span>
        </div>
      </div>
      <NButton type="primary" block class="mt-16px" @click="demo = true"
        >模拟抽奖</NButton
      ><NAlert v-if="demo" type="success" class="mt-12px"
        >演示结果：获得“{{
          previewData?.prizes[0]?.name
        }}”。这里仅展示效果，未真实发奖。</NAlert
      ></NModal
    >
    <NModal
      v-model:show="showGrant"
      preset="card"
      title="补发抽奖次数"
      class="lottery-small-modal"
      :mask-closable="!saving"
      ><p class="mb-12px">
        {{ grantActivity?.name }} ·
        {{
          grantActivity?.chance_mode === "daily"
            ? "补发次数当日有效"
            : "补发次数活动期间有效"
        }}
      </p>
      <NForm label-placement="top"
        ><NFormItem label="用户账号" required
          ><NSelect
            v-model:value="grantUser"
            :options="userOptions"
            filterable
            remote
            placeholder="搜索用户名"
            @search="searchUsers" /></NFormItem
        ><NFormItem label="补发次数" required
          ><NInputNumber
            v-model:value="grantCount"
            :min="1"
            :max="1000"
            :precision="0" /></NFormItem
        ><NFormItem label="补发原因" required
          ><NInput
            v-model:value="grantRemark"
            type="textarea"
            :maxlength="500" /></NFormItem></NForm
      ><template #footer
        ><div class="flex justify-end gap-8px">
          <NButton :disabled="saving" @click="showGrant = false">取消</NButton
          ><NButton
            type="primary"
            :loading="saving"
            :disabled="!grantUser || !grantRemark.trim()"
            @click="grant"
            >确认补发</NButton
          >
        </div></template
      ></NModal
    >
    <NModal
      v-model:show="showHistory"
      preset="card"
      title="活动规则历史"
      class="lottery-small-modal"
      ><div
        v-for="row in historyItems"
        :key="row.revision"
        class="lottery-prize-editor"
      >
        <b>版本 {{ row.revision }} · {{ lotteryState[row.config.status] }}</b>
        <p>{{ time(row.created_at) }} · 操作人 #{{ row.admin_id }}</p>
        <p>{{ row.config.name }} · {{ row.config.chance_count }} 次</p>
        <p v-for="p in row.config.prizes" :key="p.id">
          {{ p.name }}：{{ (p.probability / 100).toFixed(2) }}% /
          {{ p.quantity }} 份
        </p>
      </div>
      <NEmpty v-if="!historyItems.length" description="暂无规则历史"
    /></NModal>
    <NModal
      :show="!!selectedDraw"
      preset="card"
      title="中奖记录详情"
      class="lottery-small-modal"
      @update:show="(v) => !v && (selectedDraw = null)"
      ><template v-if="selectedDraw"
        ><NDescriptions :column="1" bordered label-placement="left"
          ><NDescriptionsItem label="中奖编号">{{
            selectedDraw.draw_no
          }}</NDescriptionsItem
          ><NDescriptionsItem label="用户"
            >{{ selectedDraw.username }} #{{
              selectedDraw.user_id
            }}</NDescriptionsItem
          ><NDescriptionsItem label="活动 / 奖品"
            >{{ selectedDraw.activity_name }} /
            {{ selectedDraw.prize_name || "谢谢参与" }}</NDescriptionsItem
          ><NDescriptionsItem label="状态">{{
            lotteryState[selectedDraw.status]
          }}</NDescriptionsItem
          ><NDescriptionsItem label="中奖时间">{{
            time(selectedDraw.created_at)
          }}</NDescriptionsItem
          ><NDescriptionsItem label="发放时间">{{
            time(selectedDraw.delivered_at)
          }}</NDescriptionsItem
          ><NDescriptionsItem label="奖品成本">{{
            formatMoney(selectedDraw.cost_cents || 0)
          }}</NDescriptionsItem
          ><NDescriptionsItem label="发放备注">{{
            selectedDraw.remark || "—"
          }}</NDescriptionsItem
          ><NDescriptionsItem label="发放操作人">{{
            selectedDraw.admin_id ? `#${selectedDraw.admin_id}` : "系统"
          }}</NDescriptionsItem></NDescriptions
        ><NButton
          v-if="selectedDraw.status !== 'missed' && checkAuth('lottery:reveal')"
          class="mt-16px"
          @click="reveal"
          >查看发放内容（记录审计）</NButton
        >
        <pre v-if="drawContent" class="lottery-pre mt-12px">{{
          drawContent
        }}</pre>
        <template
          v-if="
            selectedDraw.mode === 'manual' &&
            selectedDraw.status === 'pending' &&
            checkAuth('lottery:deliver')
          "
          ><NAlert type="warning" class="mt-16px mb-12px"
            >请实际交付奖品后再确认，此操作不会自动给用户发送商品。</NAlert
          ><NInput
            v-model:value="deliveryRemark"
            placeholder="发放备注（可选）"
            :maxlength="500"
          /><NPopconfirm @positive-click="deliver"
            ><template #trigger
              ><NButton type="primary" class="mt-12px" :loading="saving"
                >确认已发放</NButton
              ></template
            >确认已经把奖品交付给此用户？</NPopconfirm
          ></template
        ></template
      ></NModal
    >
  </div>
</template>
<style>
.lottery-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin: 8px 0 16px;
  align-items: center;
}
.lottery-modal {
  width: min(960px, calc(100vw - 32px));
  max-height: 90vh;
  overflow: auto;
}
.lottery-small-modal {
  width: min(620px, calc(100vw - 32px));
  max-height: 90vh;
  overflow: auto;
}
.lottery-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.lottery-prize-editor {
  padding: 18px;
  margin-bottom: 16px;
  border: 1px solid var(--n-border-color, #e5e7eb);
  border-radius: 10px;
}
.lottery-pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  padding: 12px;
  background: var(--n-color, #f5f6fa);
  border-radius: 8px;
}
.lottery-preview-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.lottery-preview-prize {
  display: flex;
  align-items: center;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  border: 1px solid #e5e7eb;
  border-radius: 10px;
}
.lottery-preview-prize img {
  width: 64px;
  height: 64px;
  object-fit: contain;
}
@media (max-width: 600px) {
  .lottery-form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
  .lottery-prize-editor {
    padding: 12px;
  }
  .lottery-toolbar > * {
    max-width: 100%;
  }
}
</style>
