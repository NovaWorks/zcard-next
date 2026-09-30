<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from "vue";
import { api } from "@/api/client";
import { newRequestId } from "../../../packages/request-id";
const props = defineProps<{
  orderNo: string;
  metadata?: Record<string, string>;
}>();
type Snapshot = {
  state: string;
  phase: string;
  phone_number?: string;
  otp_code?: string;
  otp_message?: string;
  expires_at?: string;
  cancel_available_at?: string;
  can_cancel?: boolean;
  can_finish?: boolean;
  operation_status?: string;
  operation_action?: string;
  refund_status?: string;
  message?: string;
  active?: boolean;
};
const snapshot = ref<Snapshot | null>(null),
  error = ref(""),
  busy = ref(false),
  copied = ref(""),
  now = ref(Date.now());
let timer: ReturnType<typeof setTimeout> | undefined,
  clock: ReturnType<typeof setInterval> | undefined,
  closed = false,
  loadVersion = 0;
const pending = ref<{ action: "cancel" | "finish"; id: string }>();
const actionError = ref("");
const titles: Record<string, string> = {
  allocating: "获取号码中",
  waiting_sms: "等待短信",
  sms_received: "已收到短信",
  completed: "接码已结束",
  canceled: "已取消",
  expired: "租期已结束",
  rejected: "未能获取号码",
};
const title = computed(() =>
  snapshot.value?.phase === "review"
    ? "结果待核对"
    : titles[snapshot.value?.state || ""] || "确认结果中",
);
const remaining = computed(() => {
  const end = Date.parse(snapshot.value?.expires_at || "");
  if (!Number.isFinite(end)) return "";
  const n = Math.max(0, Math.floor((end - now.value) / 1000));
  return n
    ? `${Math.floor(n / 60)} 分 ${n % 60} 秒`
    : snapshot.value?.active
      ? "租期已到，正在确认最终结果"
      : "租期已结束";
});
const meta = computed(() =>
  ["country", "platform", "operator"]
    .map((k) => props.metadata?.[`${k}_name`] || props.metadata?.[`${k}_id`])
    .filter(Boolean)
    .join(" · "),
);
async function load() {
  if (closed) return;
  const version = ++loadVersion;
  if (timer) clearTimeout(timer);
  const r = await api.get<Snapshot>(
    `/orders/${encodeURIComponent(props.orderNo)}/sms`,
  );
  if (closed || version !== loadVersion) return;
  error.value = r.error || "";
  if (r.data) snapshot.value = r.data;
  if (r.error || r.data?.active)
    timer = setTimeout(load, document.hidden ? 30000 : r.error ? 10000 : 4000);
}
function visibilityChanged() {
  if (!document.hidden) void load();
}
async function copy(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value);
    copied.value = `${label}已复制`;
  } catch {
    copied.value = "复制失败，请手动选择复制";
  }
}
async function act(action: "cancel" | "finish") {
  if (busy.value) return;
  if (
    !pending.value &&
    !window.confirm(
      action === "cancel"
        ? "申请取消接码？供货确认退款后退还本次实付；请求受理不代表已退款。"
        : "结束本次接码？完成后将停止继续接收短信，已收到的短信仍可查看。",
    )
  )
    return;
  if (!pending.value) {
    try {
      pending.value = { action, id: newRequestId() };
    } catch (e) {
      actionError.value = e instanceof Error ? e.message : "无法生成操作标识";
      return;
    }
  }
  if (pending.value.action !== action) return;
  busy.value = true;
  const r = await api.post<Snapshot>(
    `/orders/${encodeURIComponent(props.orderNo)}/sms/${action}`,
    { request_id: pending.value.id },
  );
  busy.value = false;
  if (closed) return;
  actionError.value = r.error || "";
  if (
    r.status &&
    r.status >= 400 &&
    r.status < 500 &&
    r.status !== 408 &&
    r.status !== 429
  )
    pending.value = undefined;
  if (r.data) {
    snapshot.value = r.data;
    pending.value = undefined;
  }
  await load();
}
onMounted(() => {
  document.addEventListener("visibilitychange", visibilityChanged);
  void load();
  clock = setInterval(() => (now.value = Date.now()), 1000);
});
onBeforeUnmount(() => {
  document.removeEventListener("visibilitychange", visibilityChanged);
  closed = true;
  if (timer) clearTimeout(timer);
  if (clock) clearInterval(clock);
  snapshot.value = null;
});
</script>
<template>
  <section class="card sms-order" aria-labelledby="sms-title">
    <h3 id="sms-title">短信接码 · {{ title }}</h3>
    <p v-if="meta" class="muted">{{ meta }}</p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="actionError" role="alert" class="error">{{ actionError }}</p>
    <template v-if="snapshot">
      <div v-if="snapshot.phone_number" class="sms-field">
        <div>
          <span class="muted">号码</span
          ><strong>{{ snapshot.phone_number }}</strong>
        </div>
        <button
          class="btn secondary"
          @click="copy(snapshot.phone_number, '号码')"
        >
          复制号码
        </button>
      </div>
      <p v-if="remaining" class="muted">{{ remaining }}</p>
      <div v-if="snapshot.otp_code" class="sms-field">
        <div>
          <span class="muted">验证码</span
          ><strong>{{ snapshot.otp_code }}</strong>
        </div>
        <button
          class="btn secondary"
          @click="copy(snapshot.otp_code, '验证码')"
        >
          复制验证码
        </button>
      </div>
      <div v-if="snapshot.otp_message">
        <p>短信正文</p>
        <pre>{{ snapshot.otp_message }}</pre>
        <button
          class="btn secondary"
          @click="copy(snapshot.otp_message, '正文')"
        >
          复制正文
        </button>
      </div>
      <p v-if="snapshot.message">{{ snapshot.message }}</p>
      <p v-if="snapshot.refund_status === 'succeeded'">已退款至会员余额</p>
      <p v-if="snapshot.operation_status === 'pending'">
        {{
          snapshot.operation_action === "cancel" ? "取消" : "完成"
        }}请求已提交，等待确认。
      </p>
      <p v-else-if="snapshot.operation_status === 'rejected'">
        上一次操作未获允许，短信仍可查看。
      </p>
      <p
        v-if="snapshot.cancel_available_at && !snapshot.can_cancel"
        class="muted"
      >
        预计可申请取消时间：{{
          new Date(snapshot.cancel_available_at).toLocaleString()
        }}，最终以供货确认能力为准。
      </p>
      <div class="sms-actions">
        <button
          v-if="pending"
          class="btn secondary"
          :disabled="busy"
          @click="act(pending.action)"
        >
          重试确认{{ pending.action === "cancel" ? "取消" : "完成" }}结果
        </button>
        <button
          v-if="snapshot.can_cancel"
          class="btn secondary"
          :disabled="busy || !!pending"
          @click="act('cancel')"
        >
          申请取消</button
        ><button
          v-if="snapshot.can_finish"
          class="btn"
          :disabled="busy || !!pending"
          @click="act('finish')"
        >
          完成接码</button
        ><button class="btn secondary" :disabled="busy" @click="load">
          刷新状态
        </button>
      </div>
    </template>
    <p v-else class="muted">正在读取接码状态…</p>
    <p role="status" aria-live="polite">{{ copied }}</p>
  </section>
</template>
<style scoped>
.sms-order {
  padding: 24px;
}
.sms-field {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin: 16px 0;
  flex-wrap: wrap;
}
.sms-field strong {
  display: block;
  font-size: 1.3rem;
  overflow-wrap: anywhere;
}
.sms-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 16px;
}
.sms-order pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: inherit;
  line-height: 1.6;
}
.sms-order button {
  min-height: 44px;
}
</style>
