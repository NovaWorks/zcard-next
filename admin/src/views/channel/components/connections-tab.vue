<script setup lang="ts">
// 货源渠道管理（supply:read / supply:write 超管专属）：
// 连接 CRUD（三类驱动凭据动态表单）/ 测试连接 / 手动同步（采集全量|增量 / 仅价格 /
// 仅状态）/ 定时计划对话框（三 scope 间隔+时间窗+请求节奏）/ 限流状态徽标与倒计时 /
// 同步任务抽屉（进度统计与取消）。
import { computed, h, onMounted, onUnmounted, reactive, ref } from "vue";
import {
  NButton, NCard, NDataTable, NDropdown, NInput, NInputNumber, NModal, NForm, NFormItem,
  NPopconfirm, NSelect, NSpace, NSwitch, NTag, NDrawer, NDescriptions, NDescriptionsItem, NAlert,
} from "naive-ui";
import type { DataTableColumns, DropdownOption } from "naive-ui";
import {
  fetchSupplyConnections, createSupplyConnection, updateSupplyConnection, deleteSupplyConnection,
  pingSupplyConnection, createSupplySyncTask, fetchSupplySyncTasks, cancelSupplySyncTask,
} from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney, yuanToFen } from "@/utils/money";
import FilterTabs from "@/components/common/filter-tabs.vue";
import TablePager from "@/components/common/table-pager.vue";
import ImportModal from "./import-modal.vue";

defineOptions({ name: "SupplyConnectionsTab" });

const loading = ref(false);
const connections = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
const statusFilter = ref<"" | "active" | "disabled">("");

const statusTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "已启用", value: "active", type: "success" as const },
  { label: "已禁用", value: "disabled", type: "error" as const },
];

const driverMeta: Record<string, { label: string; tag: "success" | "info" | "warning" }> = {
  zcard: { label: "ZCard", tag: "success" },
  dujiao_next: { label: "独角数卡", tag: "info" },
  acg_faka: { label: "异次元", tag: "warning" },
};

const canWrite = () => checkAuth("supply:write");

function fmtTime(ts?: number) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

// ── 限流状态（AIMD 节奏器）──
const nowTick = ref(Date.now());
let timer: ReturnType<typeof setInterval> | undefined;
onMounted(() => {
  timer = setInterval(() => (nowTick.value = Date.now()), 1000);
});
onUnmounted(() => timer && clearInterval(timer));

function rateLimitView(row: any): { label: string; type: "error" | "warning" | "success" } | null {
  if (!row.rate_limit_until) return null;
  const remain = row.rate_limit_until * 1000 - nowTick.value;
  if (remain <= 0) return { label: "冷却已过（半开探测中）", type: "warning" };
  const min = Math.floor(remain / 60000);
  const sec = Math.floor((remain % 60000) / 1000);
  return { label: `限流熔断 ${min}:${String(sec).padStart(2, "0")}`, type: "error" };
}

// scheduleSummary 定时计划概要（列表 ⏰ 标记与 title）。
function scheduleSummary(row: any): { on: boolean; tip: string } {
  try {
    const sch = JSON.parse(row.settings || "{}").schedule;
    if (!sch?.enabled) return { on: false, tip: "未启用定时同步" };
    const parts: string[] = [];
    if (sch.collect?.enabled) parts.push(`采集 ${sch.collect.interval}分钟`);
    if (sch.price?.enabled) parts.push(`价格 ${sch.price.interval}分钟`);
    if (sch.status?.enabled) parts.push(`状态 ${sch.status.interval}分钟`);
    return { on: true, tip: parts.length ? `定时：${parts.join(" / ")}` : "定时总开关开（未启用任何任务）" };
  } catch {
    return { on: false, tip: "" };
  }
}

function adaptiveDelayMs(row: any): number {
  try {
    const st = JSON.parse(row.rate_state || "{}");
    return Number(st.current_delay_ms) || 0;
  } catch {
    return 0;
  }
}

// ── 列表 ──
async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchSupplyConnections({ page: page.value, page_size: pageSize.value });
    if (!error && data) {
      const rows = (data as any).connections || [];
      connections.value =
        statusFilter.value === "" ? rows : rows.filter((r: any) => r.status === statusFilter.value);
      total.value = (data as any).total || rows.length;
    }
  } finally {
    loading.value = false;
  }
}

function onSearch() {
  page.value = 1;
  load();
}

// ── 新增/编辑（驱动凭据动态表单）──
const showForm = ref(false);
const saving = ref(false);
const editingId = ref(0);
const form = reactive({
  name: "",
  driver: "zcard",
  base_url: "",
  credentials: "", // JSON 明文（编辑留空 = 不改）
  exchange_rate: 1,
  price_markup_percent: 10,
  markupAmountYuan: 0,
  failAction: "auto_refund",
  productUrlTemplate: "",
  price_rounding_mode: "none",
  auto_sync_price: true,
  stock_mode: "real",
});

const credFields = computed(() => {
  switch (form.driver) {
    case "dujiao_next":
      return [
        { key: "api_key", label: "Api Key" },
        { key: "api_secret", label: "Api Secret" },
      ];
    case "acg_faka":
      return [
        { key: "app_id", label: "商户ID（app_id）" },
        { key: "app_key", label: "对接密钥（app_key）" },
      ];
    default:
      return [
        { key: "api_key", label: "Api Key" },
        { key: "api_secret", label: "Api Secret" },
      ];
  }
});

const credDraft = reactive<Record<string, string>>({});

function openCreate() {
  editingId.value = 0;
  form.name = "";
  form.driver = "zcard";
  form.base_url = "";
  form.credentials = "";
  form.exchange_rate = 1;
  form.price_markup_percent = 10;
  form.markupAmountYuan = 0;
  form.failAction = "auto_refund";
  form.productUrlTemplate = "";
  form.price_rounding_mode = "none";
  form.auto_sync_price = true;
  form.stock_mode = "real";
  Object.keys(credDraft).forEach((k) => delete credDraft[k]);
  showForm.value = true;
}

function openEdit(row: any) {
  editingId.value = row.id;
  form.name = row.name;
  form.driver = row.driver;
  form.base_url = row.base_url;
  form.credentials = "";
  form.exchange_rate = row.exchange_rate || 1;
  form.price_markup_percent = row.price_markup_percent || 0;
  form.markupAmountYuan = (row.price_markup_amount || 0) / 100;
  try {
    const st = JSON.parse(row.settings || "{}");
    form.failAction = st.failure_action || "auto_refund";
    form.productUrlTemplate = st.product_url_template || "";
  } catch {
    form.failAction = "auto_refund";
  }
  form.price_rounding_mode = row.price_rounding_mode || "none";
  form.auto_sync_price = !!row.auto_sync_price;
  form.stock_mode = row.stock_mode || "real";
  Object.keys(credDraft).forEach((k) => delete credDraft[k]);
  showForm.value = true;
}

async function submitForm() {
  if (!form.name || !form.base_url) {
    window.$message?.warning("名称与上游地址必填");
    return;
  }
  let credentials = form.credentials;
  if (editingId.value === 0 || Object.values(credDraft).some((v) => v !== "")) {
    credentials = JSON.stringify(credDraft);
  }
  saving.value = true;
  try {
    const payload: Record<string, unknown> = {
      name: form.name,
      driver: form.driver,
      base_url: form.base_url,
      exchange_rate: form.exchange_rate,
      price_markup_percent: form.price_markup_percent,
      price_markup_amount: yuanToFen(form.markupAmountYuan || 0),
      price_rounding_mode: form.price_rounding_mode,
      auto_sync_price: form.auto_sync_price,
      stock_mode: form.stock_mode,
    };
    if (credentials) payload.credentials = credentials;
    // settings：编辑时保留原值（定时计划单独维护），叠加失败策略
    let baseSettings: Record<string, any> = {};
    if (editingId.value) {
      const row = connections.value.find((r) => r.id === editingId.value);
      try {
        baseSettings = JSON.parse(row?.settings || "{}");
      } catch {
        /* 空 */
      }
    }
    if (form.failAction !== "auto_refund") baseSettings.failure_action = form.failAction;
    else delete baseSettings.failure_action;
    if (form.productUrlTemplate.trim()) baseSettings.product_url_template = form.productUrlTemplate.trim();
    else delete baseSettings.product_url_template;
    if (Object.keys(baseSettings).length) payload.settings = JSON.stringify(baseSettings);
    if (editingId.value) {
      const { error } = await updateSupplyConnection(editingId.value, payload);
      if (!error) window.$message?.success("连接已更新");
    } else {
      const { error } = await createSupplyConnection(payload as any);
      if (!error) window.$message?.success("连接已创建");
    }
    showForm.value = false;
    load();
  } finally {
    saving.value = false;
  }
}

// ── 测试连接 ──
const pinging = reactive<Record<number, boolean>>({});
async function handlePing(row: any) {
  pinging[row.id] = true;
  try {
    const { data, error } = await pingSupplyConnection(row.id);
    if (!error && data) {
      const d = data as any;
      window.$message?.success(`连接成功：${d.site_name || "上游"}，余额 ${formatMoney(d.balance_cents ?? d.balance ?? 0)}`);
      load();
    }
  } finally {
    pinging[row.id] = false;
  }
}

// ── 同步任务 ──
function handleSync(row: any, scope: string, mode: string) {
  createSupplySyncTask({ connection_id: row.id, scope, mode }).then(({ error }) => {
    if (!error) {
      window.$message?.success("同步任务已创建");
      openTasks(row.id);
      load();
    }
  });
}

const showTasks = ref(false);
const tasksConn = ref(0);
const tasks = ref<any[]>([]);
const tasksLoading = ref(false);
function openTasks(connectionId: number) {
  tasksConn.value = connectionId;
  showTasks.value = true;
  loadTasks();
}
async function loadTasks() {
  tasksLoading.value = true;
  try {
    const { data, error } = await fetchSupplySyncTasks({ connection_id: tasksConn.value || undefined, page: 1, page_size: 20 });
    if (!error && data) tasks.value = (data as any).tasks || [];
  } finally {
    tasksLoading.value = false;
  }
}
function handleCancelTask(id: number) {
  cancelSupplySyncTask(id).then(() => loadTasks());
}

// 手动重跑（failed/canceled/done 均可按原参数重建任务）
async function handleRerunTask(row: any) {
  const { error } = await createSupplySyncTask({
    connection_id: row.connection_id,
    scope: row.scope || "collect",
    mode: row.mode || "full",
  });
  if (!error) {
    window.$message?.success("已重新创建任务");
    loadTasks();
  }
}

const taskColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "范围", key: "scope", width: 70, render: (r) => ({ collect: "采集", price: "价格", status: "状态" } as any)[r.scope || "collect"] || r.scope },
  { title: "模式", key: "mode", width: 76 },
  {
    title: "状态",
    key: "status",
    width: 92,
    render: (r) =>
      h(
        NTag,
        { size: "small", type: r.status === "done" ? "success" : r.status === "failed" ? "error" : r.status === "processing" ? "info" : r.status === "canceled" ? "default" : "warning", bordered: false },
        { default: () => ({ done: "完成", failed: "失败", processing: "执行中", canceled: "已取消", pending: "排队中" } as any)[r.status] || r.status },
      ),
  },
  {
    title: "进度",
    key: "processed",
    width: 150,
    render: (r) =>
      h("div", { class: "flex flex-col" }, [
        h("span", `${r.processed || 0}/${r.total || 0} 件`),
        r.current_stage
          ? h("span", { class: "text-11px text-gray-400" }, `${stageText(r.current_stage)}${r.page ? ` · 第 ${r.page} 页` : ""}`)
          : null,
      ]),
  },
  {
    title: "统计",
    key: "stats",
    width: 210,
    render: (r) =>
      h(
        "div",
        {
          class: "text-12px",
          title: `新增 ${r.created || 0} · 更新 ${r.updated || 0} · 价格变更 ${r.price_updated || 0} · 改价保护跳过 ${r.manual_skipped || 0} · 隐藏 ${r.hidden || 0} · 对账下架 ${r.deleted || 0}`,
        },
        [
          h("span", { class: "text-success" }, `新${r.created || 0}`),
          " ",
          h("span", { class: "text-info" }, `更${r.updated || 0}`),
          " ",
          h("span", { class: "text-primary" }, `价${r.price_updated || 0}`),
          " ",
          h("span", { class: "text-warning" }, `护${r.manual_skipped || 0}`),
          " ",
          h("span", { class: "text-gray-400" }, `藏${r.hidden || 0}`),
          " ",
          h("span", { class: "text-error" }, `删${r.deleted || 0}`),
        ],
      ),
  },
  {
    title: "上游调用",
    key: "error_code",
    minWidth: 170,
    ellipsis: { tooltip: true },
    render: (r) =>
      r.error_code
        ? h("span", { title: r.error_context || "", class: "text-error" }, `${r.error_code}`)
        : h("span", { class: "text-gray-400" }, r.status === "done" ? "正常" : "-"),
  },
  {
    title: "操作",
    key: "actions",
    width: 110,
    render: (r) =>
      h("div", { class: "flex gap-4px" }, [
        r.status === "processing" || r.status === "pending"
          ? h(NButton, { size: "tiny", quaternary: true, onClick: () => handleCancelTask(r.id) }, { default: () => "取消" })
          : null,
        ["failed", "canceled", "done"].includes(r.status) && canWrite()
          ? h(NButton, { size: "tiny", type: "primary", quaternary: true, onClick: () => handleRerunTask(r) }, { default: () => "重跑" })
          : null,
      ]),
  },
];

function stageText(stage: string) {
  return (
    ({
      fetching_products: "拉取商品",
      fetching_stock: "补查库存",
      saving_products: "写入本地",
      reconciling: "删除对账",
      finalizing: "收尾",
    } as Record<string, string>)[stage] || stage
  );
}

function syncActions(row: any): DropdownOption[] {
  return [
    { label: "采集（增量）", key: "collect:incremental", disabled: !canWrite() },
    { label: "采集（全量 + 删除对账）", key: "collect:full", disabled: !canWrite() },
    { label: "仅同步价格", key: "price:incremental", disabled: !canWrite() },
    { label: "仅同步上下架/库存", key: "status:incremental", disabled: !canWrite() },
  ];
}
function onSyncAction(row: any, key: string) {
  const [scope, mode] = key.split(":");
  handleSync(row, scope, mode);
}

// ── 交互式导入（P2-10 D）──
const showImport = ref(false);
const importConn = ref<any>(null);
function openImport(row: any) {
  importConn.value = row;
  showImport.value = true;
}

// ── 定时计划对话框（settings.schedule；S2/S3 参数）──
const showSchedule = ref(false);
const scheduleConn = ref<any>(null);
const schedule = reactive({
  enabled: false,
  request_delay: 1,
  stock_concurrency: 3,
  stock_request_delay_ms: 200,
  collect: { enabled: false, interval: 360, mode: "incremental", windows: "" },
  price: { enabled: false, interval: 30, windows: "" },
  status: { enabled: false, interval: 60, windows: "" },
});

// scheduleStatus 执行状态面板数据（上次执行 + 下次预计）。
const scheduleStatus = computed(() => {
  const conn = scheduleConn.value;
  if (!conn) return [];
  const fmt = (ts?: number) => (ts ? new Date(ts * 1000).toLocaleString() : "从未执行");
  const next = (ts: number | undefined, intervalMin: number) => {
    if (!ts) return "即将（首轮）";
    const nextAt = new Date((ts + intervalMin * 60) * 1000);
    return nextAt <= new Date() ? "到期（下轮扫描派发）" : nextAt.toLocaleString();
  };
  return [
    {
      label: "采集商品",
      on: schedule.collect.enabled,
      last: fmt(conn.last_collect_at || conn.last_synced_at),
      next: next(conn.last_collect_at || conn.last_synced_at, schedule.collect.interval),
    },
    {
      label: "同步价格",
      on: schedule.price.enabled,
      last: fmt(conn.last_price_sync_at),
      next: next(conn.last_price_sync_at, schedule.price.interval),
    },
    {
      label: "上下架/库存",
      on: schedule.status.enabled,
      last: fmt(conn.last_status_sync_at),
      next: next(conn.last_status_sync_at, schedule.status.interval),
    },
  ];
});

function openSchedule(row: any) {
  scheduleConn.value = row;
  const s = JSON.parse(row.settings || "{}").schedule || {};
  schedule.enabled = !!s.enabled;
  schedule.request_delay = Number(s.request_delay ?? 1);
  schedule.stock_concurrency = Number(s.stock_concurrency ?? 3);
  schedule.stock_request_delay_ms = Number(s.stock_request_delay_ms ?? 200);
  const fill = (key: "collect" | "price" | "status") => {
    const src = s[key] || {};
    schedule[key].enabled = !!src.enabled;
    schedule[key].interval = Number(src.interval ?? { collect: 360, price: 30, status: 60 }[key]);
    if (key === "collect") schedule[key].mode = src.mode || "incremental";
    schedule[key].windows = Array.isArray(src.windows)
      ? src.windows.map((w: any) => `${w.start}-${w.end}`).join(", ")
      : "";
  };
  fill("collect");
  fill("price")
  fill("status");
  showSchedule.value = true;
}

async function submitSchedule() {
  const parseWindows = (s: string) =>
    s.split(/[,，]/)
      .map((seg) => seg.trim())
      .filter(Boolean)
      .map((seg) => {
        const [start, end] = seg.split("-").map((x) => x.trim());
        return { start: start || "00:00", end: end || "23:59" };
      });
  const settings = JSON.parse(scheduleConn.value.settings || "{}");
  settings.schedule = {
    enabled: schedule.enabled,
    request_delay: schedule.request_delay,
    stock_concurrency: schedule.stock_concurrency,
    stock_request_delay_ms: schedule.stock_request_delay_ms,
    collect: {
      enabled: schedule.collect.enabled,
      interval: schedule.collect.interval,
      mode: schedule.collect.mode,
      windows: parseWindows(schedule.collect.windows),
    },
    price: { enabled: schedule.price.enabled, interval: schedule.price.interval, windows: parseWindows(schedule.price.windows) },
    status: { enabled: schedule.status.enabled, interval: schedule.status.interval, windows: parseWindows(schedule.status.windows) },
  };
  const { error } = await updateSupplyConnection(scheduleConn.value.id, { settings: JSON.stringify(settings) });
  if (!error) {
    window.$message?.success("定时计划已保存");
    showSchedule.value = false;
    load();
  }
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 56 },
  {
    title: "名称",
    key: "name",
    width: 150,
    ellipsis: true,
    render: (row) => {
      const sch = scheduleSummary(row);
      return h("div", { class: "flex items-center gap-4px" }, [
        h("span", { title: sch.tip }, row.name),
        sch.on
          ? h("span", { title: sch.tip, class: "cursor-help" }, "⏰")
          : null,
      ]);
    },
  },
  {
    title: "类型",
    key: "driver",
    width: 92,
    render: (row) => h(NTag, { size: "small", type: driverMeta[row.driver]?.tag || "default", bordered: false }, { default: () => driverMeta[row.driver]?.label || row.driver }),
  },
  { title: "上游地址", key: "base_url", minWidth: 200, ellipsis: true },
  {
    title: "余额",
    key: "balance_cache",
    width: 100,
    render: (row) => formatMoney(row.balance_cache),
  },
  {
    title: "最近采集",
    key: "last_collect_at",
    width: 150,
    render: (row) => fmtTime(row.last_collect_at || row.last_synced_at),
  },
  {
    title: "限流状态",
    key: "rate",
    width: 170,
    render: (row) => {
      const rl = rateLimitView(row);
      const delay = adaptiveDelayMs(row);
      return h("div", { class: "flex flex-col gap-2px" }, [
        rl
          ? h(NTag, { size: "tiny", type: rl.type, bordered: false }, { default: () => rl.label })
          : h(NTag, { size: "tiny", type: "success", bordered: false }, { default: () => "正常" }),
        delay > 0
          ? h("span", { class: "text-11px text-gray-400" }, `自适应间隔 ${(delay / 1000).toFixed(0)}s`)
          : null,
      ]);
    },
  },
  {
    title: "操作",
    key: "actions",
    width: 290,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        h(NButton, { size: "tiny", loading: pinging[row.id], onClick: () => handlePing(row) }, { default: () => "测试" }),
        canWrite()
          ? h(
              NDropdown,
              {
                options: syncActions(row),
                onSelect: (key: string) => onSyncAction(row, key),
                trigger: "click",
              },
              { default: () => h(NButton, { size: "tiny", type: "primary", quaternary: true }, { default: () => "同步 ▾" }) },
            )
          : null,
        h(NButton, { size: "tiny", quaternary: true, onClick: () => openTasks(row.id) }, { default: () => "任务" }),
        h(NButton, { size: "tiny", type: "warning", secondary: true, onClick: () => openSchedule(row) }, { default: () => "⏰ 定时" }),
        canWrite()
          ? h(NButton, { size: "tiny", type: "primary", secondary: true, onClick: () => openImport(row) }, { default: () => "导入" })
          : null,
        canWrite() ? h(NButton, { size: "tiny", quaternary: true, onClick: () => openEdit(row) }, { default: () => "编辑" }) : null,
        canWrite()
          ? h(
              NPopconfirm,
              { onPositiveClick: () => handleDelete(row.id) },
              {
                trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }),
                default: () => "存在商品映射时不可删除，确定？",
              },
            )
          : null,
      ]),
  },
];

async function handleDelete(id: number) {
  const { error } = await deleteSupplyConnection(id);
  if (!error) {
    window.$message?.success("已删除");
    load();
  }
}

onMounted(load);
</script>

<template>
  <div>
    <div class="mb-12px flex flex-wrap items-center justify-between gap-8px">
      <FilterTabs v-model:value="statusFilter" :options="statusTabs" @change="onSearch" />
      <NButton v-if="canWrite()" size="small" type="primary" @click="openCreate">新增渠道</NButton>
    </div>

    <NDataTable
      :columns="columns"
      :data="connections"
      :loading="loading"
      size="small"
      :row-key="(r: any) => r.id"
      :max-height="540"
      :scroll-x="1400"
    />
    <div class="mt-12px flex justify-end">
      <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
    </div>

    <!-- 新增/编辑 -->
    <NModal v-model:show="showForm" preset="card" :title="editingId ? '编辑渠道' : '新增渠道'" style="width: 560px">
      <NForm label-placement="left" label-width="110">
        <NFormItem label="名称" required>
          <NInput v-model:value="form.name" placeholder="如：主站独角" />
        </NFormItem>
        <NFormItem label="渠道类型" required>
          <NSelect
            v-model:value="form.driver"
            :disabled="!!editingId"
            :options="[
              { label: 'ZCard（自有协议）', value: 'zcard' },
              { label: '独角数卡 dujiao-next', value: 'dujiao_next' },
              { label: '异次元 acg-faka', value: 'acg_faka' },
            ]"
          />
        </NFormItem>
        <NFormItem label="上游地址" required>
          <NInput v-model:value="form.base_url" placeholder="https://up.example.com" />
        </NFormItem>
        <NFormItem v-for="f in credFields" :key="f.key" :label="f.label" :required="editingId === 0">
          <NInput v-model:value="credDraft[f.key]" :placeholder="editingId ? '留空 = 不修改' : ''" />
        </NFormItem>
        <NFormItem label="汇率">
          <NInputNumber v-model:value="form.exchange_rate" :min="0.00000001" class="w-full" />
        </NFormItem>
        <NFormItem label="加价规则">
          <div class="w-full">
            <div class="flex items-center gap-8px">
              <span class="w-64px shrink-0 text-13px">比例上浮</span>
              <NInputNumber v-model:value="form.price_markup_percent" :min="0" size="small" class="w-110px" placeholder="0">
                <template #suffix>%</template>
              </NInputNumber>
              <span class="text-12px text-gray-400">＋</span>
              <span class="w-64px shrink-0 text-13px">固定加价</span>
              <NInputNumber v-model:value="form.markupAmountYuan" :min="0" :precision="2" size="small" class="w-120px" placeholder="0.00">
                <template #suffix>元</template>
              </NInputNumber>
            </div>
            <div class="mt-4px text-11px text-gray-400">
              本店售价 = 上游价 × 汇率 ×（1 + 比例上浮%）＋ 固定加价；两项可只填其一（0 = 不启用）
            </div>
          </div>
        </NFormItem>
        <NFormItem label="商品链接模板">
          <NInput v-model:value="form.productUrlTemplate" placeholder="如 {base}/product/{code}（商品列表跳上游）" />
        </NFormItem>
        <NFormItem label="采购失败策略">
          <NSelect
            v-model:value="form.failAction"
            :options="[
              { label: '自动退款（默认）', value: 'auto_refund' },
              { label: '转人工处理', value: 'manual' },
            ]"
          />
        </NFormItem>
        <NFormItem label="取整模式">
          <NSelect
            v-model:value="form.price_rounding_mode"
            :options="[
              { label: '不取整（两位小数）', value: 'none' },
              { label: '向上取整到元', value: 'ceil_int' },
              { label: '向上取整到角', value: 'ceil_tenth' },
            ]"
          />
        </NFormItem>
        <NFormItem label="同步自动改价">
          <NSpace align="center">
            <NSwitch v-model:value="form.auto_sync_price" />
            <span class="text-12px text-gray-400">关闭后同步永不覆盖本地价（运营手工定价域）</span>
          </NSpace>
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton size="small" class="mr-8px" @click="showForm = false">取消</NButton>
        <NButton size="small" type="primary" :loading="saving" @click="submitForm">
          {{ editingId ? "保存" : "创建" }}
        </NButton>
      </template>
    </NModal>

    <!-- 定时计划 -->
    <NModal v-model:show="showSchedule" preset="card" title="定时同步计划" style="width: 620px">
      <!-- 执行状态（锚点来自连接行数据；下次 = 上次 + 间隔，窗口内生效） -->
      <div v-if="scheduleConn" class="mb-12px rounded-6px bg-gray-50 p-10px dark:bg-gray-800">
        <div class="mb-6px text-13px font-500">自动任务执行状态</div>
        <div class="flex flex-col gap-3px text-12px text-gray-600 dark:text-gray-300">
          <div v-for="s in scheduleStatus" :key="s.label" class="flex items-center gap-8px">
            <span class="w-70px shrink-0">{{ s.label }}</span>
            <NTag size="tiny" :type="s.on ? 'success' : 'default'" :bordered="false">{{ s.on ? "已启用" : "未启用" }}</NTag>
            <span>上次：{{ s.last }}</span>
            <span v-if="s.on" class="text-primary">下次：{{ s.next }}</span>
          </div>
        </div>
      </div>
      <NAlert type="info" :bordered="false" class="mb-12px">
        采集过快可能被上游封锁 IP：遇 429/WAF 拦截会自动降速并熔断冷却（界面列表可见倒计时），
        请求间隔为自适应下限。三类任务各自独立间隔，均在时间窗口内执行。保存后由系统每分钟检查到期自动派发。
      </NAlert>
      <NForm label-placement="left" label-width="130">
        <NFormItem label="启用定时同步">
          <NSwitch v-model:value="schedule.enabled" />
        </NFormItem>
        <NFormItem label="分页请求间隔(秒)">
          <NInputNumber v-model:value="schedule.request_delay" :min="0" :max="60" class="w-full" />
        </NFormItem>
        <NFormItem label="库存补查并发">
          <NInputNumber v-model:value="schedule.stock_concurrency" :min="1" :max="10" class="w-full" />
        </NFormItem>
        <NFormItem label="库存批次间隔(ms)">
          <NInputNumber v-model:value="schedule.stock_request_delay_ms" :min="0" :max="10000" class="w-full" />
        </NFormItem>
        <NFormItem label="采集商品">
          <NSpace align="center" class="w-full">
            <NSwitch v-model:value="schedule.collect.enabled" size="small" />
            <NInputNumber v-model:value="schedule.collect.interval" :min="5" size="small" class="w-100px" />
            <span class="text-12px">分钟</span>
            <NSelect
              v-model:value="schedule.collect.mode"
              size="small"
              class="w-110px"
              :options="[
                { label: '增量', value: 'incremental' },
                { label: '全量', value: 'full' },
              ]"
            />
          </NSpace>
        </NFormItem>
        <NFormItem label="同步价格">
          <NSpace align="center">
            <NSwitch v-model:value="schedule.price.enabled" size="small" />
            <NInputNumber v-model:value="schedule.price.interval" :min="5" size="small" class="w-100px" />
            <span class="text-12px">分钟</span>
          </NSpace>
        </NFormItem>
        <NFormItem label="同步上下架/库存">
          <NSpace align="center">
            <NSwitch v-model:value="schedule.status.enabled" size="small" />
            <NInputNumber v-model:value="schedule.status.interval" :min="5" size="small" class="w-100px" />
            <span class="text-12px">分钟</span>
          </NSpace>
        </NFormItem>
        <NFormItem label="执行时间窗">
          <NInput v-model:value="schedule.collect.windows" placeholder="如 01:00-05:00, 22:00-06:00（空=全天）" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton size="small" class="mr-8px" @click="showSchedule = false">取消</NButton>
        <NButton size="small" type="primary" @click="submitSchedule">保存计划</NButton>
      </template>
    </NModal>

    <!-- 交互式导入 -->
    <ImportModal v-model:show="showImport" :connection="importConn" @imported="load" />

    <!-- 同步任务抽屉 -->
    <NDrawer v-model:show="showTasks" :width="720">
      <NDrawerContent title="同步任务" closable>
        <NDataTable
        :max-height="540"
          size="small"
          :data="tasks"
          :loading="tasksLoading"
          :columns="taskColumns"
        />
      </NDrawerContent>
    </NDrawer>
  </div>
</template>
