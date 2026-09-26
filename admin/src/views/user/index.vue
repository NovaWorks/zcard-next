<script setup lang="ts">
// 前台用户管理（storefront 注册用户）：列表 / 关键词搜索 / 状态筛选 / 封禁解封。
// 权限：identity:user_read（查看）、identity:user_status（封禁/解封，超管专属）。
import { ref, reactive, onMounted, h } from "vue";
import { NButton, NInput, NInputNumber, NTag, NPopconfirm, NDataTable, NCard, NSpace, NModal, NDescriptions, NDescriptionsItem, NForm, NFormItem, NCheckbox, NSwitch, NTabs, NTabPane, NEmpty, NSelect } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchUsers, setUserStatus, fetchUserDetail, createUser, resetUserPassword } from "@/service/api";
import { fetchWalletBalance, adjustWalletBalance } from "@/service/api/wallet";
import { grantCoupon, fetchCoupons,fetchMemberLevels,assignUserLevel,configureInviteLevel } from "@/service/api/marketing";
import { checkAuth } from "@/directives";
import TablePager from "@/components/common/table-pager.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";
import { couponStatusText, supplierStatusText } from "@/utils/business-status";
import { orderStatusText, orderStatusType } from "@/utils/order-status";
import { formatMoney, fenToYuan, yuanToFen } from "@/utils/money";

defineOptions({ name: "UserManagement" });

const loading = ref(false);
const users = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);

const query = reactive({
  keyword: "",
  status: "" as "" | "active" | "banned",
  isSupplier: false,
});

const showDetail = ref(false);
const detailUser = ref<any>(null);
const detailLoading = ref(false);
const showLevel = ref(false);
const levelSaving = ref(false);
const levelLoading = ref(false);
const editingInvite = ref(false);
const levelOptions = ref<{ label: string; value: number }[]>([]);
const selectedLevel = ref(0);
const levelReason = ref('');
const levelSourceText: Record<string, string> = { manual: '后台指定', referral: '推荐注册赠送', auto: '自动计算', none: '普通会员' };
async function openLevel(invite = false) {
  if (levelLoading.value) return;
  levelLoading.value = true;
  try {
    const { data, error } = await fetchMemberLevels();
    if (error) return;
    editingInvite.value = invite;
    levelOptions.value = [
      { label: invite ? '关闭赠送（已获赠客户不受影响）' : '取消人工指定（保留推荐资格，恢复自动计算）', value: 0 },
      ...(!invite && Number(detailUser.value.user.referral_level_id) > 0 ? [{ label: '撤销推荐资格并恢复自动计算', value: -1 }] : []),
      ...((data as any)?.levels || []).filter((l: any) => l.enabled).map((l: any) => ({
        label: l.name + (checkAuth('memberlevel:view_discount') ? ` · ${l.discount > 0 && l.discount < 10000 ? `${Number((l.discount / 1000).toFixed(2))} 折` : '原价'}` : ''),
        value: Number(l.id)
      }))
    ];
    selectedLevel.value = Number(invite ? detailUser.value.user.invite_level_id || 0 : detailUser.value.user.manual_level_id || 0);
    levelReason.value = '';
    showLevel.value = true;
  } finally { levelLoading.value = false; }
}
async function saveLevel() {
  if (!levelReason.value.trim() || levelSaving.value) return;
  levelSaving.value = true;
  try {
    const id = detailUser.value.user.id;
    const result = editingInvite.value
      ? await configureInviteLevel(id, selectedLevel.value, levelReason.value.trim())
      : await assignUserLevel(id, Math.max(0, selectedLevel.value), levelReason.value.trim(), selectedLevel.value === -1);
    if (!result.error) {
      showLevel.value = false;
      await openDetail({ id });
      await load();
      window.$message?.success(editingInvite.value ? '推荐赠送配置已保存，仅影响之后注册的客户' : '客户等级已修改，已有订单金额不变');
    }
  } finally { levelSaving.value = false; }
}
const canCreate = () => checkAuth("identity:user_create");
const canResetPwd = () => checkAuth("identity:user_reset_pwd");

async function openDetail(row: any) {
  showDetail.value = true;
  detailLoading.value = true;
  detailUser.value = null;
  try {
    const { data, error } = await fetchUserDetail(row.id);
    if (!error && data) detailUser.value = data;
  } finally {
    detailLoading.value = false;
  }
}

// ── 新增用户 ──
const showCreate = ref(false);
const createLoading = ref(false);
const createForm = reactive({ username: "", password: "", email: "" });
function openCreate() {
  createForm.username = "";
  createForm.password = "";
  createForm.email = "";
  showCreate.value = true;
}
async function submitCreate() {
  if (!createForm.username.trim() || !createForm.password) {
    window.$message?.warning("用户名与密码必填（密码≥6位）");
    return;
  }
  createLoading.value = true;
  try {
    const { error } = await createUser({
      username: createForm.username.trim(),
      password: createForm.password,
      email: createForm.email.trim() || undefined
    });
    if (!error) {
      window.$message?.success(`已创建用户 ${createForm.username}`);
      showCreate.value = false;
      page.value = 1;
      load();
    }
  } finally {
    createLoading.value = false;
  }
}

// ── 重置密码 ──
const showReset = ref(false);
const resetLoading = ref(false);
const resetTarget = ref<any>(null);
const newPassword = ref("");
function openReset(row: any) {
  resetTarget.value = row;
  newPassword.value = "";
  showReset.value = true;
}
async function submitReset() {
  if (newPassword.value.length < 6) {
    window.$message?.warning("新密码至少 6 位");
    return;
  }
  resetLoading.value = true;
  try {
    const { error } = await resetUserPassword(resetTarget.value.id, newPassword.value);
    if (!error) {
      window.$message?.success(`已重置 ${resetTarget.value.username} 的密码`);
      showReset.value = false;
    }
  } finally {
    resetLoading.value = false;
  }
}

// ── 赠券（coupon 批次下拉数据源：券批次列表接口）──
const showGrant = ref(false);
const grantLoading = ref(false);
const grantTarget = ref<any>(null);
const grantBatchId = ref<string | null>(null);
const grantCount = ref(1);
const couponBatchOptions = ref<{ label: string; value: string }[]>([]);
async function openGrant(row: any) {
  grantTarget.value = row;
  grantBatchId.value = null;
  grantCount.value = 1;
  showGrant.value = true;
  if (!couponBatchOptions.value.length) {
    // 批次选项 = 未使用券的 batch_id 去重（余量即各批可选张数上限参考）
    const { data, error } = await fetchCoupons("unused", undefined, 1, 100);
    if (!error && data) {
      const seen = new Map<string, number>();
      for (const c of (data as any).coupons || []) {
        if (c.batch_id) seen.set(String(c.batch_id), (seen.get(String(c.batch_id)) || 0) + 1);
      }
      couponBatchOptions.value = [...seen.entries()].map(([id, n]) => ({ label: `批次 ${id}（未用 ${n} 张）`, value: id }));
    }
  }
}
async function submitGrant() {
  if (!grantBatchId.value) {
    window.$message?.warning("选择券批次");
    return;
  }
  grantLoading.value = true;
  try {
    const { error } = await grantCoupon(grantBatchId.value, grantTarget.value.id, grantCount.value);
    if (!error) {
      window.$message?.success(`已向 ${grantTarget.value.username} 赠送 ${grantCount.value} 张券`);
      showGrant.value = false;
    }
  } finally {
    grantLoading.value = false;
  }
}

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
  { title: "ID", key: "id", width: 64 },
  { title: "用户名", key: "username", width: 130, ellipsis: true, render: (row) => h("div", null, [h("div", null, row.username), row.is_supplier ? h(NTag, { size: "tiny", type: "info", bordered: false, class: "mt-2px" }, { default: () => `供货·${supplierStatusText(row.supplier_status)}` }) : null]) },
  { title: "邮箱", key: "email", width: 150, ellipsis: { tooltip: true }, render: (row) => row.email || "-" },
  { title: "等级", key: "level_name", width: 80, render: (row) => (row.level_id ? h(NTag, { size: "small", type: "warning", bordered: false }, { default: () => row.level_name || `#${row.level_id}` }) : "-") },
  { title: "余额", key: "balance_cents", width: 90, align: "right", render: (row) => (row.balance_cents ?? 0) === 0 ? "0" : h("b", null, fenToYuan(row.balance_cents ?? 0)) },
  { title: "积分", key: "points", width: 70, align: "right", render: (row) => String(row.points ?? 0) },
  { title: "订单", key: "order_count", width: 60, align: "right", render: (row) => String(row.order_count ?? 0) },
  { title: "消费额", key: "spent_cents", width: 90, align: "right", render: (row) => fenToYuan(row.spent_cents ?? 0) },
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
  { title: "最近登录", key: "last_login_at", width: 140, render: (row) => fmtTime(row.last_login_at) },
  {
    title: "操作",
    key: "actions",
    width: 210,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        h(NButton, { size: "tiny", onClick: () => openDetail(row) }, { default: () => "详情" }),
        canAdjust()
          ? h(NButton, { size: "tiny", type: "warning", quaternary: true, onClick: () => openAdjust(row) }, { default: () => "余额" })
          : null,
        canResetPwd()
          ? h(NButton, { size: "tiny", type: "warning", quaternary: true, onClick: () => openReset(row) }, { default: () => "改密" })
          : null,
        checkAuth("coupon:write")
          ? h(NButton, { size: "tiny", type: "info", quaternary: true, onClick: () => openGrant(row) }, { default: () => "赠券" })
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
      is_supplier: query.isSupplier || undefined,
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
        <NCheckbox v-model:checked="query.isSupplier" class="ml-8px" @update:checked="onSearch">仅供货商</NCheckbox>
        <div class="flex-1"></div>
        <NButton v-if="canCreate()" type="primary" size="small" @click="openCreate">新增用户</NButton>
      </NSpace>

      <FilterTabs v-model:value="query.status" :options="statusTabs" class="mb-12px" @change="onSearch" />

      <NDataTable :columns="columns" :data="users" :loading="loading" size="small" :row-key="(row: any) => row.id" :max-height="540" />

      <div class="mt-12px flex justify-end">
        <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="load" />
      </div>
    </NCard>

    <NModal v-model:show="showLevel" preset="dialog" style="width: 520px; max-width: calc(100vw - 32px)" :title="editingInvite ? '推荐注册赠送等级' : '修改客户等级'" :mask-closable="!levelSaving" :closable="!levelSaving">
      <NForm label-placement="top">
        <NFormItem :label="editingInvite ? '该客户邀请新人时赠送的等级' : '该客户的会员等级'">
          <NSelect v-model:value="selectedLevel" :options="levelOptions" :disabled="levelSaving" />
        </NFormItem>
        <NFormItem label="调整原因" required>
          <NInput v-model:value="levelReason" :maxlength="160" placeholder="请输入本次调整原因" :disabled="levelSaving" />
        </NFormItem>
        <p v-if="editingInvite">新客户通过此用户的推荐码注册后，立即获得所选等级。修改或关闭赠送不会改变已注册客户的等级，也不会改变此用户自己的等级。</p>
        <p v-else>后台指定优先于推荐等级和自动升级。取消人工指定会恢复正常计算，并不一定降为普通会员。已有订单金额不变。</p>
      </NForm>
      <template #action><NButton :loading="levelSaving" :disabled="!levelReason.trim()" type="primary" @click="saveLevel">保存</NButton></template>
    </NModal>
    <NModal v-model:show="showDetail" preset="card" title="用户详情" style="width: 960px; max-width: 96vw">
      <div v-if="detailLoading" class="py-40px text-center">加载中…</div>
      <template v-else-if="detailUser">
        <NDescriptions :column="3" bordered size="small">
          <NDescriptionsItem label="ID">{{ detailUser.user.id }}</NDescriptionsItem>
          <NDescriptionsItem label="用户名" :span="2">{{ detailUser.user.username }}</NDescriptionsItem>
          <NDescriptionsItem label="邮箱" :span="2">{{ detailUser.user.email || "-" }}</NDescriptionsItem>
          <NDescriptionsItem label="状态">
            <NTag size="small" :type="detailUser.user.status === 'active' ? 'success' : 'error'" :bordered="false">
              {{ detailUser.user.status === "active" ? "正常" : detailUser.user.status === "banned" ? "已封禁" : "已删除" }}
            </NTag>
          </NDescriptionsItem>
          <NDescriptionsItem label="会员等级">
            <NButton v-if="checkAuth('memberlevel:assign') && checkAuth('memberlevel:read')" size="tiny" class="mr-8px" :loading="levelLoading" @click="openLevel(false)">修改等级</NButton>
            <span class="text-12px opacity-60">{{ levelSourceText[detailUser.user.level_source] || (detailUser.user.manual_level_id ? '后台指定' : '自动计算') }}</span>
            <NTag v-if="detailUser.user.level_id" size="small" type="warning" :bordered="false">{{ detailUser.user.level_name || `#${detailUser.user.level_id}` }}</NTag>
            <template v-else>-</template>
          </NDescriptionsItem>
          <NDescriptionsItem label="注册获赠等级" :span="2">{{ detailUser.user.referral_level_name || '无' }}</NDescriptionsItem>
          <NDescriptionsItem label="推荐注册赠送" :span="3">
            <NSpace align="center">
              <span>{{ detailUser.user.invite_level_name || '未开启' }}</span>
              <NButton v-if="checkAuth('memberlevel:invite') && checkAuth('memberlevel:read')" size="small" :loading="levelLoading" @click="openLevel(true)">设置赠送等级</NButton>
              <span v-if="detailUser.user.promo_code">推荐码：{{ detailUser.user.promo_code }}</span>
            </NSpace>
          </NDescriptionsItem>
          <NDescriptionsItem label="钱包余额"><b>{{ fenToYuan(detailUser.user.balance_cents ?? 0) }}</b></NDescriptionsItem>
          <NDescriptionsItem label="积分"><b>{{ detailUser.user.points ?? 0 }}</b></NDescriptionsItem>
          <NDescriptionsItem label="订单数">{{ detailUser.user.order_count ?? 0 }}</NDescriptionsItem>
          <NDescriptionsItem label="累计消费"><b>{{ fenToYuan(detailUser.user.spent_cents ?? 0) }}</b></NDescriptionsItem>
          <NDescriptionsItem label="供货账户">
            <NTag v-if="detailUser.user.is_supplier" size="small" type="info" :bordered="false">{{ supplierStatusText(detailUser.user.supplier_status) }}</NTag>
            <template v-else>未开通</template>
          </NDescriptionsItem>
          <NDescriptionsItem label="邀请人">{{ detailUser.inviter_username || "-" }}</NDescriptionsItem>
          <NDescriptionsItem label="直接下级">{{ detailUser.invitees_count ?? 0 }} 人</NDescriptionsItem>
          <NDescriptionsItem label="最近登录">{{ fmtTime(detailUser.user.last_login_at) }}</NDescriptionsItem>
          <NDescriptionsItem label="注册时间">{{ fmtTime(detailUser.user.created_at) }}</NDescriptionsItem>
        </NDescriptions>

        <NTabs type="line" size="small" class="mt-12px">
          <NTabPane name="orders" :tab="`最近订单（${(detailUser.recent_orders || []).length}）`">
            <div v-if="(detailUser.recent_orders || []).length" class="max-h-220px overflow-auto">
              <div v-for="o in detailUser.recent_orders" :key="o.order_no" class="recent-order-row border-b border-gray-100 py-8px text-13px dark:border-gray-800">
                <span class="font-mono">{{ o.order_no }}</span>
                <div class="recent-order-products"><div v-for="(it, index) in o.items || []" :key="index">{{ it.name || `商品 #${it.product_id}` }}<span v-if="it.sku_name"> · {{ it.sku_name }}</span> ×{{ it.quantity }}</div><span v-if="!o.items?.length">—</span></div>
                <span class="whitespace-nowrap">{{ formatMoney(o.amount_cents) }}</span>
                <NTag size="tiny" :type="orderStatusType(o.status)" :bordered="false">{{ orderStatusText(o.status) }}</NTag>
                <span class="w-130px text-right opacity-60">{{ fmtTime(o.created_at) }}</span>
              </div>
            </div>
            <NEmpty v-else description="暂无订单" size="small" class="py-20px" />
          </NTabPane>
          <NTabPane name="coupons" :tab="`优惠券（${(detailUser.coupons || []).length}）`">
            <div v-if="(detailUser.coupons || []).length" class="max-h-220px overflow-auto">
              <div v-for="c in detailUser.coupons" :key="c.id" class="flex items-center gap-12px border-b border-gray-100 py-6px text-13px dark:border-gray-800">
                <span class="flex-1">{{ c.title || `#${c.id}` }}</span>
                <NTag size="tiny" :type="c.status === 'unused' ? 'success' : 'default'" :bordered="false">{{ couponStatusText(c.status) }}</NTag>
              </div>
            </div>
            <NEmpty v-else description="暂无优惠券" size="small" class="py-20px" />
          </NTabPane>
        </NTabs>
      </template>
      <template #footer>
        <NButton size="small" @click="showDetail = false">关闭</NButton>
      </template>
    </NModal>

    <!-- 新增用户 -->
    <NModal v-model:show="showCreate" preset="card" title="新增用户" style="width: 420px">
      <NForm label-placement="left" label-width="80">
        <NFormItem label="用户名" required>
          <NInput v-model:value="createForm.username" placeholder="3-30 字符" />
        </NFormItem>
        <NFormItem label="初始密码" required>
          <NInput v-model:value="createForm.password" type="password" show-password-on="click" placeholder="至少 6 位" />
        </NFormItem>
        <NFormItem label="邮箱">
          <NInput v-model:value="createForm.email" placeholder="可选" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton size="small" type="primary" :loading="createLoading" @click="submitCreate">创建</NButton>
      </template>
    </NModal>

    <!-- 重置密码 -->
    <NModal v-model:show="showReset" preset="card" :title="`重置密码：${resetTarget?.username || ''}`" style="width: 420px">
      <NForm label-placement="left" label-width="80">
        <NFormItem label="新密码" required>
          <NInput v-model:value="newPassword" type="password" show-password-on="click" placeholder="至少 6 位" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton size="small" type="primary" :loading="resetLoading" @click="submitReset">确认重置</NButton>
      </template>
    </NModal>

    <!-- 赠送优惠券 -->
    <NModal v-model:show="showGrant" preset="card" :title="`赠送优惠券：${grantTarget?.username || ''}`" style="width: 420px">
      <NForm label-placement="left" label-width="80">
        <NFormItem label="券批次" required>
          <NSelect v-model:value="grantBatchId" :options="couponBatchOptions" placeholder="选择券批次" />
        </NFormItem>
        <NFormItem label="数量" required>
          <NInputNumber v-model:value="grantCount" :min="1" :max="99" class="w-full" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton size="small" type="primary" :loading="grantLoading" @click="submitGrant">赠送</NButton>
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

<style scoped>
.recent-order-row { display: grid; grid-template-columns: minmax(140px,1fr) minmax(180px,1.5fr) auto auto 130px; align-items: center; gap: 12px; }
.recent-order-products { overflow-wrap: anywhere; white-space: normal; }
@media (max-width: 768px) { .recent-order-row { grid-template-columns: 1fr auto; } .recent-order-products { grid-column: 1 / -1; grid-row: 2; } }
</style>
