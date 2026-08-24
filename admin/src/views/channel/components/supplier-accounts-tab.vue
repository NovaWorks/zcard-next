<script setup lang="ts">
// 供货账号管理（supplier:read / supplier:write）：下游客户账户 + 对接申请审核工作台。
// protocol 三选一：ZCard 自有协议 / dujiao-next 兼容 / acg-faka 兼容（P2-10 B/C）——
// 兼容账号让对应平台不改代码即可对接本站；创建后展示「对接配置说明」。
// 前台个人中心提交的申请（owner_user_id>0）在「待审核」筛选下集中处理：
// 审核弹窗展示完整申请资料，「通过并开通」即生效（用户前台可查凭据），驳回须填意见。
import { h, computed, onMounted, reactive, ref } from "vue";
import { useRoute } from "vue-router";
import {
  NButton, NCard, NDataTable, NInput, NInputNumber, NModal, NForm, NFormItem,
  NPopconfirm, NSelect, NSpace, NTag, NAlert, NDescriptions, NDescriptionsItem,
} from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import {
  fetchSupplierAccounts, createSupplierAccount, reviewSupplierAccount, toggleSupplierAccount,
  resetSupplierSecret, rechargeSupplierAccount, fetchSupplierLedger,
  fetchSupplierCallbacks, resendSupplierCallback, upsertSupplierPrice,
  fetchSupplierPrices, deleteSupplierPrice, setSupplierIPWhitelist,
} from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney, yuanToFen, fenToYuan } from "@/utils/money";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "SupplierAccountsTab" });

const route = useRoute();
const loading = ref(false);
const allAccounts = ref<any[]>([]);
const statusFilter = ref<"" | "applying" | "approved" | "rejected" | "disabled">(
  route.query.status === "applying" ? "applying" : "",
);

// 客户端过滤（全量 ≤200 拉取，内存筛选）
const accounts = computed(() =>
  statusFilter.value === "" ? allAccounts.value : allAccounts.value.filter((r: any) => r.status === statusFilter.value),
);
// 待审核数（横幅提醒——有待审申请时置顶提示，一键切到审核视图）
const applyingCount = computed(() => allAccounts.value.filter((r: any) => r.status === "applying").length);

const statusTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "待审核", value: "applying", type: "warning" as const },
  { label: "已通过", value: "approved", type: "success" as const },
  { label: "已驳回", value: "rejected", type: "error" as const },
  { label: "已禁用", value: "disabled", type: "default" as const },
];

const protocolMeta: Record<string, { label: string; tag: "success" | "info" | "warning"; hint: string }> = {
  zcard: { label: "ZCard", tag: "success", hint: "对接另一套 ZCard：填本站地址 + api_key/api_secret（X-Supply-* 四头签名）" },
  dujiao_next: { label: "dujiao 兼容", tag: "info", hint: "对方 dujiao-next 后台「站点连接」填本站地址 + 下发的是 api_key/api_secret（Dujiao-Next-* 三头）" },
  acg_faka: { label: "acg 兼容", tag: "warning", hint: "对方 acg-faka 后台「共享店铺(异次元)」填本站地址 + 商户ID=api_key、密钥=api_secret" },
};

const canWrite = () => checkAuth("supplier:write");

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchSupplierAccounts({ page: 1, page_size: 200 });
    if (!error && data) {
      allAccounts.value = (data as any).accounts || [];
    }
  } finally {
    loading.value = false;
  }
}

// ── 新增 ──
const showForm = ref(false);
const saving = ref(false);
const form = reactive({
  name: "",
  protocol: "zcard",
  api_key: "",
  api_secret: "",
  display_name: "",
  contact: "",
});

function openCreate() {
  form.name = "";
  form.protocol = "zcard";
  form.api_key = "";
  form.api_secret = "";
  form.display_name = "";
  form.contact = "";
  showForm.value = true;
}

const secretOnce = ref(""); // 创建/重置时一次性回显
async function submitForm() {
  if (!form.name || !form.api_key || !form.api_secret) {
    window.$message?.warning("名称、api_key、api_secret 必填");
    return;
  }
  saving.value = true;
  try {
    const { data, error } = await createSupplierAccount({ ...form });
    if (!error && data) {
      secretOnce.value = (data as any).api_secret || "";
      window.$message?.success("账号已创建（待审核后生效）");
      showForm.value = false;
      load();
    }
  } finally {
    saving.value = false;
  }
}

// ── 审核工作台（统一弹窗：申请资料 + 意见 + 通过并开通/驳回）──
const reviewModal = ref(false);
const reviewTarget = ref<any>(null);
const reviewNote = ref("");
const reviewSubmitting = ref(false);

function openReview(row: any) {
  reviewTarget.value = row;
  reviewNote.value = "";
  reviewModal.value = true;
}

function fmtTime(ts: number) {
  return ts ? new Date(ts * 1000).toLocaleString() : "-";
}

async function submitReview(approve: boolean) {
  if (!approve && !reviewNote.value.trim()) {
    window.$message?.warning("驳回时请填写审核意见（将展示给申请人）");
    return;
  }
  reviewSubmitting.value = true;
  try {
    const { error } = await reviewSupplierAccount(reviewTarget.value.id, approve, reviewNote.value.trim() || undefined);
    if (!error) {
      window.$message?.success(approve ? "已通过并开通（申请人可在前台查看凭据）" : "已驳回（申请人可查看意见并重新申请）");
      reviewModal.value = false;
      load();
    }
  } finally {
    reviewSubmitting.value = false;
  }
}

async function handleToggle(row: any) {
  const { error } = await toggleSupplierAccount(row.id, row.status === "disabled");
  if (!error) {
    window.$message?.success(row.status === "disabled" ? "已启用" : "已禁用");
    load();
  }
}

// ── 账户详情（对接信息查看 + IP 白名单管理；api_key 仅在此展示）──
const detailModal = ref(false);
const detailTarget = ref<any>(null);
const detailWhitelist = ref<string[]>([]);
const detailWhitelistInput = ref("");
const detailWhitelistSaving = ref(false);

function openDetail(row: any) {
  detailTarget.value = row;
  detailWhitelist.value = [...(row.ip_whitelist || [])];
  detailWhitelistInput.value = "";
  detailModal.value = true;
}

function addDetailWhitelistIP() {
  const ip = detailWhitelistInput.value.trim();
  if (!ip) return;
  if (detailWhitelist.value.includes(ip)) {
    window.$message?.warning("该 IP 已在白名单中");
    return;
  }
  if (detailWhitelist.value.length >= 20) {
    window.$message?.warning("白名单最多 20 条");
    return;
  }
  detailWhitelist.value.push(ip);
  detailWhitelistInput.value = "";
}

async function saveDetailWhitelist() {
  detailWhitelistSaving.value = true;
  try {
    const { data, error } = await setSupplierIPWhitelist(detailTarget.value.id, detailWhitelist.value);
    if (!error) {
      detailWhitelist.value = [...((data as any)?.ip_whitelist || detailWhitelist.value)];
      window.$message?.success(detailWhitelist.value.length ? `白名单已更新（${detailWhitelist.value.length} 条）` : "白名单已清空（所有 IP 放行）");
      load();
    }
  } finally {
    detailWhitelistSaving.value = false;
  }
}

async function handleResetSecret(row: any) {
  const { data, error } = await resetSupplierSecret(row.id);
  if (!error && data) {
    secretOnce.value = (data as any).api_secret || "";
    window.$message?.success("密钥已重置（旧密钥立即失效）");
  }
}

// ── 充值 ──
const showRecharge = ref(false);
const rechargeTarget = ref<any>(null);
const rechargeYuan = ref<number | null>(null);
const rechargeRemark = ref("");
const recharging = ref(false);
function openRecharge(row: any) {
  rechargeTarget.value = row;
  rechargeYuan.value = null;
  rechargeRemark.value = "";
  showRecharge.value = true;
}
async function submitRecharge() {
  if (rechargeYuan.value === null || rechargeYuan.value === 0) {
    window.$message?.warning("请填写充值金额（元）");
    return;
  }
  recharging.value = true;
  try {
    const { error } = await rechargeSupplierAccount(
      rechargeTarget.value.id,
      yuanToFen(rechargeYuan.value),
      `recharge:admin:${Date.now()}`,
      rechargeRemark.value || "管理员充值",
    );
    if (!error) {
      window.$message?.success("已充值");
      showRecharge.value = false;
      load();
    }
  } finally {
    recharging.value = false;
  }
}

// ── 账本抽屉 ──
const showLedger = ref(false);
const ledgerRows = ref<any[]>([]);
const ledgerTarget = ref<any>(null);
const ledgerLoading = ref(false);
async function openLedger(row: any) {
  ledgerTarget.value = row;
  showLedger.value = true;
  ledgerLoading.value = true;
  try {
    const { data, error } = await fetchSupplierLedger({ account_id: row.id, page: 1, page_size: 50 });
    if (!error && data) ledgerRows.value = (data as any).entries || [];
  } finally {
    ledgerLoading.value = false;
  }
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 56 },
  { title: "名称", key: "name", width: 120, ellipsis: true },
  {
    title: "协议",
    key: "protocol",
    width: 96,
    render: (row) => h(NTag, { size: "small", type: protocolMeta[row.protocol]?.tag || "default", bordered: false }, { default: () => protocolMeta[row.protocol]?.label || row.protocol }),
  },
  { title: "余额", key: "balance_cache", width: 90, render: (row) => formatMoney(row.balance_cache) },
  {
    title: "状态",
    key: "status",
    width: 80,
    render: (row) =>
      h(NTag, { size: "small", type: row.status === "approved" ? "success" : row.status === "applying" ? "warning" : row.status === "rejected" ? "error" : "default", bordered: false }, { default: () => ({ approved: "已通过", applying: "待审核", rejected: "已驳回", disabled: "已禁用" } as any)[row.status] || row.status }),
  },
  {
    title: "申请/审核信息",
    key: "apply_info",
    minWidth: 180,
    ellipsis: true,
    render: (row) => {
      const parts: string[] = [];
      if (row.owner_user_id) parts.push(`申请人 #${row.owner_user_id}`);
      if (row.apply_reason) parts.push(`理由：${row.apply_reason}`);
      if (row.review_note) parts.push(`意见：${row.review_note}`);
      return parts.length ? parts.join(" ｜ ") : "-";
    },
  },
  {
    title: "操作",
    key: "actions",
    width: 360,
    render: (row) =>
      h("div", { class: "flex flex-wrap gap-4px" }, [
        h(NButton, { size: "tiny", quaternary: true, onClick: () => openDetail(row) }, { default: () => "📄 详情" }),
        row.status === "applying" && canWrite()
          ? h(NButton, { size: "tiny", type: "warning", secondary: true, onClick: () => openReview(row) }, { default: () => "🔍 审核" })
          : null,
        row.status !== "applying" && canWrite()
          ? h(NButton, { size: "tiny", quaternary: true, onClick: () => handleToggle(row) }, { default: () => (row.status === "disabled" ? "启用" : "禁用") })
          : null,
        canWrite()
          ? h(NPopconfirm, { onPositiveClick: () => handleResetSecret(row) }, { trigger: () => h(NButton, { size: "tiny", type: "warning", quaternary: true }, { default: () => "重置密钥" }), default: () => "重置后旧密钥立即失效，确定？" })
          : null,
        canWrite()
          ? h(NButton, { size: "tiny", type: "primary", quaternary: true, onClick: () => openRecharge(row) }, { default: () => "充值" })
          : null,
        h(NButton, { size: "tiny", quaternary: true, onClick: () => openLedger(row) }, { default: () => "账本" }),
        canWrite()
          ? h(NButton, { size: "tiny", quaternary: true, onClick: () => openPrice(row) }, { default: () => "专属价" })
          : null,
      ]),
  },
];

// ── 回调记录（死信重发）──
const showCallbacks = ref(false);
const callbacks = ref<any[]>([]);
const callbacksLoading = ref(false);
async function openCallbacks() {
  showCallbacks.value = true;
  callbacksLoading.value = true;
  try {
    const { data, error } = await fetchSupplierCallbacks({ page: 1, page_size: 50 });
    if (!error && data) callbacks.value = (data as any).callbacks || [];
  } finally {
    callbacksLoading.value = false;
  }
}
async function handleResend(row: any) {
  const { error } = await resendSupplierCallback(row.id);
  if (!error) {
    window.$message?.success("已重发");
    openCallbacks();
  }
}

// ── 专属价（账号 × 商品覆盖价；空 SKU = 商品级默认）──
const showPrice = ref(false);
const priceTarget = ref<any>(null);
const priceForm = reactive({ product_id: null as number | null, sku_id: null as number | null, priceYuan: null as number | null });
const priceSaving = ref(false);
const priceRows = ref<any[]>([]);
const priceLoading = ref(false);
function openPrice(row: any) {
  priceTarget.value = row;
  priceForm.product_id = null;
  priceForm.sku_id = null;
  priceForm.priceYuan = null;
  showPrice.value = true;
  loadPrices(row.id);
}
async function loadPrices(accountId: number) {
  priceLoading.value = true;
  try {
    const { data, error } = await fetchSupplierPrices(accountId);
    if (!error && data) priceRows.value = (data as any).prices || [];
  } finally {
    priceLoading.value = false;
  }
}
async function handleDeletePrice(row: any) {
  const { error } = await deleteSupplierPrice(row.id);
  if (!error) {
    window.$message?.success("已删除（恢复基础供货价）");
    loadPrices(priceTarget.value.id);
  }
}
async function submitPrice() {
  if (!priceForm.product_id || priceForm.priceYuan === null) {
    window.$message?.warning("请填写商品 ID 与价格");
    return;
  }
  priceSaving.value = true;
  try {
    const { error } = await upsertSupplierPrice({
      account_id: priceTarget.value.id,
      product_id: priceForm.product_id,
      sku_id: priceForm.sku_id ?? 0,
      price: yuanToFen(priceForm.priceYuan),
    });
    if (!error) {
      window.$message?.success("专属价已保存（优先于基础供货价）");
      priceForm.product_id = null;
      priceForm.sku_id = null;
      priceForm.priceYuan = null;
      loadPrices(priceTarget.value.id);
    }
  } finally {
    priceSaving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div>
    <!-- 待审提醒（有待审申请时置顶；一键切到审核视图） -->
    <NAlert v-if="applyingCount > 0 && statusFilter !== 'applying'" type="warning" :bordered="false" class="mb-12px">
      <template #default>
        <div class="flex items-center justify-between gap-8px">
          <span>⏳ 有 <b>{{ applyingCount }}</b> 个对接申请待审核（前台用户提交）</span>
          <NButton size="tiny" type="warning" secondary @click="statusFilter = 'applying'">立即审核</NButton>
        </div>
      </template>
    </NAlert>

    <div class="mb-12px flex flex-wrap items-center justify-between gap-8px">
      <FilterTabs v-model:value="statusFilter" :options="statusTabs" size="small" />
      <NSpace>
        <NButton size="small" quaternary @click="openCallbacks">回调记录</NButton>
        <NButton v-if="canWrite()" size="small" type="primary" @click="openCreate">新增供货账号</NButton>
      </NSpace>
    </div>

    <NDataTable :columns="columns" :data="accounts" :loading="loading" size="small" :row-key="(r: any) => r.id" :max-height="540" />

    <!-- 密钥一次性回显 -->
    <NModal :show="!!secretOnce" preset="dialog" title="密钥（仅此一次展示，请立即保存）" style="width: 520px" @update:show="secretOnce = ''">
      <NAlert type="warning" :bordered="false" class="mb-8px">关闭后无法再次查看，丢失只能重置。</NAlert>
      <NInput :value="secretOnce" readonly type="textarea" :rows="2" />
    </NModal>

    <!-- 审核工作台：申请资料 + 意见 + 通过并开通 / 驳回 -->
    <NModal v-model:show="reviewModal" preset="card" :title="`对接申请审核 #${reviewTarget?.id || ''}`" style="width: 560px">
      <NDescriptions v-if="reviewTarget" :column="2" size="small" bordered label-placement="left" class="mb-12px">
        <NDescriptionsItem label="站点名" :span="2">{{ reviewTarget.display_name || reviewTarget.name }}</NDescriptionsItem>
        <NDescriptionsItem label="对接协议">
          <NTag size="small" :type="protocolMeta[reviewTarget.protocol]?.tag || 'default'" :bordered="false">
            {{ protocolMeta[reviewTarget.protocol]?.label || reviewTarget.protocol }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="申请人">用户 #{{ reviewTarget.owner_user_id || '—（后台建号）' }}</NDescriptionsItem>
        <NDescriptionsItem label="联系方式">{{ reviewTarget.contact || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="申请时间">{{ fmtTime(reviewTarget.created_at) }}</NDescriptionsItem>
        <NDescriptionsItem label="申请理由" :span="2">{{ reviewTarget.apply_reason || '—（未填写）' }}</NDescriptionsItem>
      </NDescriptions>
      <NAlert type="info" :bordered="false" class="mb-12px">
        {{ protocolMeta[reviewTarget?.protocol]?.hint }}
      </NAlert>
      <NForm label-placement="top">
        <NFormItem label="审核意见（驳回时必填；申请人可在前台查看）">
          <NInput v-model:value="reviewNote" type="textarea" :rows="3" placeholder="如：资料不完整、站点无法访问等；通过时可留空" />
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="flex justify-end gap-8px">
          <NButton @click="reviewModal = false">取消</NButton>
          <NButton type="error" secondary :loading="reviewSubmitting" @click="submitReview(false)">驳回</NButton>
          <NButton type="success" :loading="reviewSubmitting" @click="submitReview(true)">通过并开通</NButton>
        </div>
      </template>
    </NModal>

    <!-- 账户详情：对接信息（api_key 仅在此展示）+ IP 白名单管理 -->
    <NModal v-model:show="detailModal" preset="card" :title="`对接账户详情 #${detailTarget?.id || ''}`" style="width: 620px">
      <NDescriptions v-if="detailTarget" :column="2" size="small" bordered label-placement="left" class="mb-12px">
        <NDescriptionsItem label="站点名" :span="2">{{ detailTarget.display_name || detailTarget.name }}</NDescriptionsItem>
        <NDescriptionsItem label="对接协议">
          <NTag size="small" :type="protocolMeta[detailTarget.protocol]?.tag || 'default'" :bordered="false">
            {{ protocolMeta[detailTarget.protocol]?.label || detailTarget.protocol }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="状态">
          <NTag size="small" :type="detailTarget.status === 'approved' ? 'success' : detailTarget.status === 'applying' ? 'warning' : detailTarget.status === 'rejected' ? 'error' : 'default'" :bordered="false">
            {{ ({ approved: "已通过", applying: "待审核", rejected: "已驳回", disabled: "已禁用" } as any)[detailTarget.status] || detailTarget.status }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="app_id（api_key）" :span="2">
          <code class="text-13px">{{ detailTarget.api_key }}</code>
        </NDescriptionsItem>
        <NDescriptionsItem label="api_secret" :span="2">
          <span class="text-gray-400 text-13px">加密存储，不可查看（仅创建/重置时明文下发一次）</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="申请人">用户 #{{ detailTarget.owner_user_id || '—（后台建号）' }}</NDescriptionsItem>
        <NDescriptionsItem label="联系方式">{{ detailTarget.contact || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="余额">{{ formatMoney(detailTarget.balance_cache) }}</NDescriptionsItem>
        <NDescriptionsItem label="创建时间">{{ fmtTime(detailTarget.created_at) }}</NDescriptionsItem>
        <NDescriptionsItem label="回调地址" :span="2">{{ detailTarget.notify_url || '—（未配置）' }}</NDescriptionsItem>
        <NDescriptionsItem v-if="detailTarget.apply_reason" label="申请理由" :span="2">{{ detailTarget.apply_reason }}</NDescriptionsItem>
        <NDescriptionsItem v-if="detailTarget.review_note" label="审核意见" :span="2">{{ detailTarget.review_note }}</NDescriptionsItem>
      </NDescriptions>

      <!-- IP 白名单（空 = 所有 IP 放行；接口鉴权层强制） -->
      <NAlert type="info" :bordered="false" class="mb-8px">
        IP 白名单：不填 = 所有 IP 都可以请求本账户接口；填写后仅白名单内 IP 可调用（精确 IP 或 CIDR 网段，最多 20 条）。
      </NAlert>
      <div class="flex flex-wrap gap-6px mb-8px" v-if="detailWhitelist.length">
        <NTag v-for="ip in detailWhitelist" :key="ip" closable size="small" @close="detailWhitelist = detailWhitelist.filter((x: string) => x !== ip)">
          {{ ip }}
        </NTag>
      </div>
      <div v-else class="text-13px text-gray-400 mb-8px">（白名单为空：所有 IP 放行）</div>
      <div class="flex gap-8px">
        <NInput v-model:value="detailWhitelistInput" placeholder="添加 IP 或网段，如 1.2.3.4 / 10.0.0.0/24" @keyup.enter="addDetailWhitelistIP" />
        <NButton :disabled="!canWrite()" @click="addDetailWhitelistIP">添加</NButton>
        <NButton type="primary" :loading="detailWhitelistSaving" :disabled="!canWrite()" @click="saveDetailWhitelist">保存白名单</NButton>
      </div>
    </NModal>

    <!-- 新增 -->
    <NModal v-model:show="showForm" preset="card" title="新增供货账号" style="width: 540px">
      <NForm label-placement="left" label-width="110">
        <NFormItem label="名称" required>
          <NInput v-model:value="form.name" placeholder="下游客户标识" />
        </NFormItem>
        <NFormItem label="对接协议" required>
          <NSelect
            v-model:value="form.protocol"
            :options="[
              { label: 'ZCard（自有协议）', value: 'zcard' },
              { label: 'dujiao-next 兼容（对方不改代码）', value: 'dujiao_next' },
              { label: 'acg-faka 兼容（对方不改代码）', value: 'acg_faka' },
            ]"
          />
        </NFormItem>
        <NAlert type="info" :bordered="false" class="mb-12px">
          {{ protocolMeta[form.protocol]?.hint }}
        </NAlert>
        <NFormItem :label="form.protocol === 'acg_faka' ? '商户ID(app_id)' : 'api_key'" required>
          <NInput v-model:value="form.api_key" :placeholder="form.protocol === 'acg_faka' ? '数字 ID（对方后台填的商户ID）' : '随机字符串'" />
        </NFormItem>
        <NFormItem :label="form.protocol === 'acg_faka' ? '密钥(app_key)' : 'api_secret'" required>
          <NInput v-model:value="form.api_secret" placeholder="签名密钥" />
        </NFormItem>
        <NFormItem label="店铺名(回显)">
          <NInput v-model:value="form.display_name" placeholder="connect/ping 返回的 shopName/site_name" />
        </NFormItem>
        <NFormItem label="联系方式">
          <NInput v-model:value="form.contact" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton size="small" class="mr-8px" @click="showForm = false">取消</NButton>
        <NButton size="small" type="primary" :loading="saving" @click="submitForm">创建</NButton>
      </template>
    </NModal>

    <!-- 充值 -->
    <NModal v-model:show="showRecharge" preset="dialog" :title="`充值：${rechargeTarget?.name || ''}`" style="width: 420px">
      <NForm label-placement="top">
        <NFormItem label="当前余额">
          <span>{{ fenToYuan(rechargeTarget?.balance_cache || 0) }} 元</span>
        </NFormItem>
        <NFormItem label="充值金额（元）" required>
          <NInputNumber v-model:value="rechargeYuan" :min="0.01" :precision="2" class="w-full" />
        </NFormItem>
        <NFormItem label="备注">
          <NInput v-model:value="rechargeRemark" placeholder="将写入账本" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showRecharge = false">取消</NButton>
        <NButton type="primary" :loading="recharging" @click="submitRecharge">充值</NButton>
      </template>
    </NModal>

    <!-- 回调记录 -->
    <NModal v-model:show="showCallbacks" preset="card" title="下游回调记录（失败可重发）" style="width: 760px">
      <NDataTable
        :max-height="540"
        size="small"
        :loading="callbacksLoading"
        :data="callbacks"
        :columns="[
          { title: 'ID', key: 'id', width: 60 },
          { title: '订单', key: 'supply_order_id', width: 80 },
          { title: '状态', key: 'callback_status', width: 90, render: (r: any) => r.callback_status },
          { title: '重试', key: 'retry_count', width: 60 },
          { title: '最近错误', key: 'last_error', minWidth: 200, ellipsis: true, render: (r: any) => r.last_error || '-' },
          {
            title: '操作',
            key: 'actions',
            width: 80,
            render: (r: any) =>
              r.callback_status !== 'success' && canWrite()
                ? h(NButton, { size: 'tiny', quaternary: true, onClick: () => handleResend(r) }, { default: () => '重发' })
                : null,
          },
        ]"
      />
    </NModal>

    <!-- 专属价 -->
    <NModal v-model:show="showPrice" preset="dialog" :title="`专属价：${priceTarget?.name || ''}`" style="width: 440px">
      <NForm label-placement="top">
        <NAlert type="info" :bordered="false" class="mb-8px">专属价优先于商品基础供货价（该账号下单按此价扣款）。</NAlert>
        <NFormItem label="商品 ID" required>
          <NInputNumber v-model:value="priceForm.product_id" :min="1" class="w-full" />
        </NFormItem>
        <NFormItem label="SKU ID（可空 = 商品级默认价）">
          <NInputNumber v-model:value="priceForm.sku_id" :min="0" class="w-full" />
        </NFormItem>
        <NFormItem label="专属价（元）" required>
          <NInputNumber v-model:value="priceForm.priceYuan" :min="0.01" :precision="2" class="w-full" />
        </NFormItem>
      </NForm>
      <div class="mb-12px">
        <div class="mb-6px text-13px font-500">已有专属价（{{ priceRows.length }}）</div>
        <NDataTable
          size="small"
          :loading="priceLoading"
          :data="priceRows"
          max-height="220"
          :columns="[
            { title: '商品ID', key: 'product_id', width: 80 },
            { title: 'SKU', key: 'sku_id', width: 70, render: (r: any) => (r.sku_id ? r.sku_id : '商品级') },
            { title: '价格', key: 'price', width: 100, render: (r: any) => fenToYuan(r.price) + ' 元' },
            {
              title: '',
              key: 'actions',
              width: 60,
              render: (r: any) =>
                canWrite()
                  ? h(NPopconfirm, { onPositiveClick: () => handleDeletePrice(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, { default: () => '删除' }), default: () => '删除后恢复基础供货价？' })
                  : null,
            },
          ]"
        />
      </div>
      <template #action>
        <NButton @click="showPrice = false">取消</NButton>
        <NButton type="primary" :loading="priceSaving" @click="submitPrice">保存</NButton>
      </template>
    </NModal>

    <!-- 账本 -->
    <NModal v-model:show="showLedger" preset="card" :title="`账本：${ledgerTarget?.name || ''}`" style="width: 720px">
      <NDataTable
        :max-height="540"
        size="small"
        :loading="ledgerLoading"
        :data="ledgerRows"
        :columns="[
          { title: '时间', key: 'created_at', width: 160, render: (r: any) => (r.created_at ? new Date(r.created_at * 1000).toLocaleString() : '-') },
          { title: '类型', key: 'type', width: 110, render: (r: any) => ({ recharge: '充值', supply_pay: '供货扣款', supply_refund: '退款', adjust: '调账' } as any)[r.type] || r.type },
          { title: '金额', key: 'amount', width: 110, render: (r: any) => (r.amount >= 0 ? '+' : '') + fenToYuan(r.amount) },
          { title: '备注', key: 'remark', ellipsis: true, render: (r: any) => r.remark || '-' },
        ]"
      />
    </NModal>
  </div>
</template>
