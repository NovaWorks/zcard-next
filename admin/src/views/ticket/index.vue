<script setup lang="ts">
// 工单工作台（P3-05）：urgent_paid 置顶由后端排序保证；前台筛选状态/类型/订单号。
// 会话式详情：用户左灰泡 / 客服右蓝泡 / 内部备注左橙虚线（仅 ticket:read 可见）。
import TablePager from "@/components/common/table-pager.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";
import { ref, computed, onMounted, h } from "vue";
import { NButton, NTag, NSpace, NPopconfirm, NModal, NDescriptions, NDescriptionsItem, NSelect, NInput, NSwitch } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { checkAuth } from "@/directives";
import { fetchTickets, fetchTicket, replyTicket, resolveTicket, closeTicket } from "@/service/api";

defineOptions({ name: "TicketManagement" });

const canWrite = () => checkAuth("ticket:write");

const loading = ref(false);
const tickets = ref<any[]>([]);
const page = ref(1);
const pageSize = ref(20);
const total = ref(0);
const statusFilter = ref<string>("");
const typeFilter = ref<string>("");
const orderNoFilter = ref<string>("");

// 状态筛选：空=未关闭（open+processing，工作台默认视角）
const statusTabs = [
  { label: "进行中", value: "", type: "info" as const },
  { label: "待回复", value: "open", type: "warning" as const },
  { label: "处理中", value: "processing", type: "info" as const },
  { label: "已解决", value: "resolved", type: "success" as const },
  { label: "已关闭", value: "closed", type: "default" as const },
];

const typeOptions = [
  { label: "全部类型", value: "" },
  { label: "售前咨询", value: "presale" },
  { label: "售后工单", value: "aftersale" },
];

const statusTextMap: Record<string, string> = { open: "待回复", processing: "处理中", resolved: "已解决", closed: "已关闭" };
function statusType(s: string): "success" | "warning" | "info" | "default" {
  if (s === "resolved") return "success";
  if (s === "open") return "warning";
  if (s === "processing") return "info";
  return "default";
}
function typeText(t: string) {
  return t === "presale" ? "售前" : t === "aftersale" ? "售后" : t || "-";
}
function priorityText(p: string) {
  const map: Record<string, string> = { urgent_paid: "已加急", high: "高", normal: "普通", low: "低" };
  return map[p] || p || "普通";
}
function priorityType(p: string): "error" | "warning" | "default" {
  if (p === "urgent_paid") return "error";
  if (p === "high") return "warning";
  return "default";
}

// SLA：加急工单 2 小时时限；未超时显示剩余小时，超时高亮
const nowTs = ref(Math.floor(Date.now() / 1000));
function slaText(row: any) {
  if (!row.sla_due_at) return "-";
  if (["resolved", "closed"].includes(row.status)) return "-";
  const remain = row.sla_due_at - nowTs.value;
  if (remain <= 0) return "已超时";
  return remain >= 3600 ? `剩 ${Math.floor(remain / 3600)} 小时` : `剩 ${Math.ceil(remain / 60)} 分钟`;
}

function contactText(row: any) {
  if (row.user_id) return `用户 #${row.user_id}`;
  return row.guest_contact || "游客";
}

function satisfactionText(v: number) {
  return v > 0 ? `${v} ⭐` : "-";
}

function formatTime(ts?: number) {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

const columns: DataTableColumns<any> = [
  { title: "工单号", key: "ticket_no", width: 170 },
  {
    title: "类型",
    key: "type",
    width: 70,
    render: (row) => h(NTag, { size: "small", type: row.type === "aftersale" ? "warning" : "info", bordered: false }, { default: () => typeText(row.type) }),
  },
  {
    title: "优先级",
    key: "priority",
    width: 84,
    render: (row) => h(NTag, { size: "small", type: priorityType(row.priority), bordered: row.priority === "urgent_paid" }, { default: () => priorityText(row.priority) }),
  },
  {
    title: "状态",
    key: "status",
    width: 84,
    render: (row) => h(NTag, { size: "small", type: statusType(row.status) }, { default: () => statusTextMap[row.status] || row.status }),
  },
  { title: "用户 / 联系方式", key: "contact", minWidth: 140, ellipsis: { tooltip: true }, render: (row) => contactText(row) },
  { title: "关联订单", key: "order_id", width: 110, render: (row) => (row.order_id ? `#${row.order_id}` : "-") },
  {
    title: "SLA",
    key: "sla",
    width: 100,
    render: (row) => {
      const t = slaText(row);
      if (t === "-") return "-";
      return h(NTag, { size: "small", type: t === "已超时" ? "error" : "info", bordered: false }, { default: () => t });
    },
  },
  { title: "创建时间", key: "created_at", width: 160, render: (row) => formatTime(row.created_at) },
  {
    title: "操作",
    key: "actions",
    width: 70,
    render: (row) => h(NButton, { size: "tiny", onClick: () => openDetail(row.ticket_no) }, { default: () => "详情" }),
  },
];

async function loadTickets() {
  loading.value = true;
  nowTs.value = Math.floor(Date.now() / 1000);
  try {
    const { data, error } = await fetchTickets({
      status: statusFilter.value || undefined,
      type: typeFilter.value || undefined,
      order_no: orderNoFilter.value.trim() || undefined,
      page: page.value,
      page_size: pageSize.value,
    });
    if (!error && data) {
      tickets.value = (data as any).tickets || [];
      total.value = Number((data as any).total || 0);
    }
  } finally {
    loading.value = false;
  }
}

function resetList() {
  page.value = 1;
  loadTickets();
}

// ── 详情 + 会话 ──
const showDetail = ref(false);
const detailLoading = ref(false);
const detail = ref<any>(null);
const messages = ref<any[]>([]);

const ticketClosed = computed(() => detail.value?.status === "closed");
const ticketResolved = computed(() => detail.value?.status === "resolved");

const replyContent = ref("");
const replyInternal = ref(false);
const replying = ref(false);
const acting = ref(false);

async function openDetail(ticketNo: string) {
  showDetail.value = true;
  detailLoading.value = true;
  replyContent.value = "";
  replyInternal.value = false;
  try {
    const { data, error } = await fetchTicket(ticketNo);
    if (!error && data) {
      detail.value = (data as any).ticket;
      messages.value = (data as any).messages || [];
    }
  } finally {
    detailLoading.value = false;
  }
}

async function handleReply() {
  if (!replyContent.value.trim() || !detail.value) return;
  replying.value = true;
  try {
    const { error } = await replyTicket(detail.value.ticket_no, { content: replyContent.value.trim(), is_internal: replyInternal.value });
    if (!error) {
      window.$message?.success(replyInternal.value ? "内部备注已添加" : "已回复（open 工单自动转处理中）");
      replyContent.value = "";
      await openDetail(detail.value.ticket_no);
      loadTickets();
    }
  } finally {
    replying.value = false;
  }
}

async function handleResolve() {
  if (!detail.value) return;
  acting.value = true;
  try {
    const { error } = await resolveTicket(detail.value.ticket_no);
    if (!error) {
      window.$message?.success("已标记解决（7 天后自动关闭）");
      await openDetail(detail.value.ticket_no);
      loadTickets();
    }
  } finally {
    acting.value = false;
  }
}

async function handleClose() {
  if (!detail.value) return;
  acting.value = true;
  try {
    const { error } = await closeTicket(detail.value.ticket_no);
    if (!error) {
      window.$message?.success("工单已关闭");
      await openDetail(detail.value.ticket_no);
      loadTickets();
    }
  } finally {
    acting.value = false;
  }
}

function senderName(m: any) {
  if (m.sender_type === "user") return detail.value?.user_id ? `用户 #${detail.value.user_id}` : "用户";
  if (m.sender_type === "admin") return "客服";
  if (m.sender_type === "system") return "系统";
  return m.sender_type;
}

onMounted(loadTickets);
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="工单管理" class="flex-1">
      <div class="mb-16px flex flex-wrap items-center justify-between gap-12px">
        <FilterTabs v-model:value="statusFilter" :options="statusTabs" @change="resetList" />
        <div class="flex flex-wrap items-center gap-8px">
          <!-- naive cssr 后置注入会覆盖 uno 宽度类，定宽须用内联 style -->
          <NSelect v-model:value="typeFilter" :options="typeOptions" size="small" style="width: 120px" @update:value="resetList" />
          <NInput
            v-model:value="orderNoFilter"
            size="small"
            placeholder="按订单号筛选"
            style="width: 180px"
            clearable
            @keyup.enter="resetList"
            @clear="resetList"
          />
          <NButton size="small" @click="resetList">查询</NButton>
          <NButton size="small" quaternary @click="loadTickets">刷新</NButton>
        </div>
      </div>

      <NDataTable :columns="columns" :data="tickets" :loading="loading" size="small" :max-height="540" />

      <TablePager v-model:page="page" v-model:page-size="pageSize" :total="total" @change="loadTickets" />
    </NCard>

    <!-- 工单详情 + 会话 -->
    <NModal v-model:show="showDetail" preset="card" :title="detail ? `工单 ${detail.ticket_no}` : '工单详情'" style="width: 720px; max-width: 96vw" display-directive="show">
      <NDescriptions v-if="detail" :column="3" bordered size="small">
        <NDescriptionsItem label="类型">{{ typeText(detail.type) }}</NDescriptionsItem>
        <NDescriptionsItem label="状态">
          <NTag size="small" :type="statusType(detail.status)">{{ statusTextMap[detail.status] || detail.status }}</NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="优先级">
          <NTag size="small" :type="priorityType(detail.priority)" :bordered="detail.priority === 'urgent_paid'">{{ priorityText(detail.priority) }}</NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="用户">{{ contactText(detail) }}</NDescriptionsItem>
        <NDescriptionsItem label="游客联系">{{ detail.guest_contact || "-" }}</NDescriptionsItem>
        <NDescriptionsItem label="关联订单">{{ detail.order_id ? `#${detail.order_id}` : "-" }}</NDescriptionsItem>
        <NDescriptionsItem label="创建时间">{{ formatTime(detail.created_at) }}</NDescriptionsItem>
        <NDescriptionsItem label="首次回复">{{ formatTime(detail.first_reply_at) }}</NDescriptionsItem>
        <NDescriptionsItem label="满意度">{{ satisfactionText(detail.satisfaction) }}</NDescriptionsItem>
      </NDescriptions>

      <div class="mt-16px mb-8px text-13px font-600">对话记录（{{ messages.length }} 条）</div>
      <div class="flex flex-col gap-10px max-h-360px overflow-y-auto p-4px">
        <template v-if="messages.length">
          <div v-for="m in messages" :key="m.id" class="msg-row" :class="{ mine: m.sender_type === 'admin' }">
            <div class="msg-meta">
              {{ senderName(m) }}
              <NTag v-if="m.is_internal" size="tiny" type="warning" :bordered="false" class="ml-4px">内部备注</NTag>
              <span class="ml-6px text-11px text-gray-400">{{ formatTime(m.created_at) }}</span>
            </div>
            <div class="msg-bubble" :class="{ internal: m.is_internal }">{{ m.content }}</div>
          </div>
        </template>
        <div v-else-if="!detailLoading" class="text-center text-12px text-gray-400">暂无消息</div>
      </div>

      <!-- 回复区（ticket:write；关闭后只读） -->
      <div v-if="canWrite() && !ticketClosed" class="mt-12px flex flex-col gap-8px">
        <NInput v-model:value="replyContent" type="textarea" :rows="3" :placeholder="replyInternal ? '内部备注内容（用户不可见）' : '输入回复内容…'" />
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-6px text-13px">
            <NSwitch v-model:value="replyInternal" size="small" />
            <span>内部备注</span>
          </div>
          <NSpace :size="8">
            <NPopconfirm v-if="!ticketResolved" @positive-click="handleResolve">
              <template #trigger>
                <NButton size="small" type="success" secondary :loading="acting">标记解决</NButton>
              </template>
              确认已解决该工单？用户可继续追加或重新开启。
            </NPopconfirm>
            <NPopconfirm @positive-click="handleClose">
              <template #trigger>
                <NButton size="small" type="warning" secondary :loading="acting">关闭工单</NButton>
              </template>
              确认关闭该工单？关闭后不可再回复。
            </NPopconfirm>
            <NButton size="small" type="primary" :loading="replying" :disabled="!replyContent.trim()" @click="handleReply">
              {{ replyInternal ? "添加备注" : "回复" }}
            </NButton>
          </NSpace>
        </div>
      </div>
      <div v-else-if="ticketClosed" class="mt-12px text-12px text-gray-400">工单已关闭，仅供查看。</div>
    </NModal>
  </div>
</template>

<style scoped>
/* 会话气泡（大厂客服工作台惯例）：用户左灰、客服右蓝、内部备注橙虚线 */
.msg-row { display: flex; flex-direction: column; align-items: flex-start; }
.msg-row.mine { align-items: flex-end; }
.msg-meta { font-size: 11px; color: #9ca3af; margin-bottom: 3px; }
.msg-bubble {
  max-width: 78%;
  padding: 8px 12px;
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  background: #f3f4f6;
  color: #1f2329;
  border-top-left-radius: 3px;
}
.msg-row.mine .msg-bubble {
  background: rgba(37, 99, 235, 0.9);
  color: #fff;
  border-top-left-radius: 10px;
  border-top-right-radius: 3px;
}
.msg-bubble.internal {
  background: rgba(240, 160, 32, 0.1);
  color: #9a6700;
  border: 1px dashed rgba(240, 160, 32, 0.55);
  border-top-left-radius: 3px;
}
</style>
