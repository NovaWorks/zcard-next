<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NCheckboxGroup,
  NCollapse,
  NCollapseItem,
  NForm,
  NFormItem,
  NInput,
  NSpace,
  NSpin,
  NSwitch,
} from "naive-ui";
import { fetchSettings, updateSettings } from "@/service/api";
import { checkAuth } from "@/directives";
import TelegramDelivery from "./telegram-delivery.vue";

const emit = defineEmits<{ dirty: [value: boolean]; busy: [value: boolean] }>();
interface Target {
  chat_id: string;
  topic: string;
  key: number;
}
const loading = ref(true);
const failed = ref(false);
const saving = ref(false);
const enabled = ref(false);
const orderEnabled = ref(false);
const token = ref("");
const savedToken = ref(false);
const events = ref<string[]>(["order.paid"]);
const targets = ref<Target[]>([]);
const baseline = ref("");
let rowKey = 0;
const eventOptions = [
  { value: "order.created", label: "新订单" },
  { value: "order.paid", label: "付款成功" },
  { value: "order.delivered", label: "发货完成" },
  { value: "order.refunded", label: "退款成功" },
];
const state = () =>
  JSON.stringify({
    enabled: enabled.value,
    orderEnabled: orderEnabled.value,
    token: token.value,
    events: events.value,
    targets: targets.value.map((t) => ({ chat_id: t.chat_id, topic: t.topic })),
  });
const dirty = computed(() => !!baseline.value && state() !== baseline.value);
watch(dirty, (value) => emit("dirty", value));
watch(saving, (value) => emit("busy", value));
const writable = computed(() => checkAuth("settings:update"));
const chatError = (t: Target) => {
  const v = t.chat_id.trim();
  if (!/^(-?\d+|@[A-Za-z0-9_]{5,})$/.test(v) || v.length > 64)
    return "填写 Chat ID（可为负数）或 @用户名";
  if (!v.startsWith("@")) {
    try {
      const n = BigInt(v);
      if (n === 0n || n < -9223372036854775808n || n > 9223372036854775807n)
        return "Chat ID 超出有效范围";
    } catch {
      return "Chat ID 无效";
    }
  }
  return "";
};
const topicError = (t: Target) =>
  t.topic.trim() &&
  (!/^\d+$/.test(t.topic.trim()) ||
    Number(t.topic) < 1 ||
    Number(t.topic) > 2147483647)
    ? "Topic ID 须为 1–2147483647 的整数，未使用话题请留空"
    : "";
function destination(t: Target) {
  const raw = t.chat_id.trim();
  return {
    chat_id: raw.startsWith("@") ? raw.toLowerCase() : BigInt(raw).toString(),
    topic_id: t.topic.trim() ? Number(t.topic) : 0,
  };
}
const duplicate = computed(() => {
  const keys = targets.value
    .filter((t) => !chatError(t) && !topicError(t))
    .map((t) => JSON.stringify(destination(t)));
  return new Set(keys).size !== keys.length;
});
const tokenError = computed(() =>
  token.value && !/^\d+:[A-Za-z0-9_-]{20,}$/.test(token.value)
    ? "Token 格式不正确，请从 @BotFather 复制完整 Token"
    : "",
);
const validation = computed(() => {
  if (tokenError.value) return tokenError.value;
  if (targets.value.some((t) => chatError(t) || topicError(t)))
    return "请修正接收位置中标出的错误";
  if (duplicate.value)
    return "接收位置重复：同一 Chat ID 和 Topic ID 只需填写一次";
  if (
    enabled.value &&
    orderEnabled.value &&
    (!targets.value.length ||
      (!savedToken.value && !token.value) ||
      !events.value.length)
  )
    return "启用订单通知需要 Token、至少一个接收位置和一个通知事件";
  return "";
});
const savedTargets = ref<{ chat_id: string; topic_id: number }[]>([]);
async function load() {
  loading.value = true;
  failed.value = false;
  try {
    const { data, error } = await fetchSettings("notify");
    if (error || !data) {
      failed.value = true;
      return;
    }
    const values: Record<string, any> = {};
    for (const item of (data as any).items || [])
      values[item.key] = JSON.parse(item.value_json);
    enabled.value = values.telegram_enabled === true;
    orderEnabled.value = values.telegram_order_enabled === true;
    savedToken.value = values.telegram_bot_token === "****";
    token.value = "";
    events.value = values.telegram_events || ["order.paid"];
    const list = Array.isArray(values.telegram_targets)
      ? values.telegram_targets
      : [
          ...new Set<string>(
            (values.telegram_chat_ids || "")
              .split(",")
              .map((s: string) => s.trim())
              .filter(Boolean),
          ),
        ].map((chat_id) => ({ chat_id, topic_id: 0 }));
    targets.value = list.map((t: any) => ({
      chat_id: String(t.chat_id),
      topic: t.topic_id ? String(t.topic_id) : "",
      key: ++rowKey,
    }));
    savedTargets.value = targets.value
      .filter((t) => !chatError(t) && !topicError(t))
      .map(destination);
    if (!targets.value.length)
      targets.value.push({ chat_id: "", topic: "", key: ++rowKey });
    baseline.value = state();
  } catch {
    failed.value = true;
  } finally {
    loading.value = false;
  }
}
async function save() {
  if (validation.value || !dirty.value || saving.value) return;
  saving.value = true;
  try {
    const values: Record<string, unknown> = {
      telegram_enabled: enabled.value,
      telegram_order_enabled: orderEnabled.value,
      telegram_events: events.value,
      telegram_targets: targets.value.map(destination),
    };
    if (token.value) values.telegram_bot_token = token.value;
    const { error } = await updateSettings(
      Object.entries(values).map(([key, value]) => ({
        group: "notify",
        key,
        value_json: JSON.stringify(value),
      })),
    );
    if (!error) {
      if (token.value) savedToken.value = true;
      token.value = "";
      savedTargets.value = targets.value.map(destination);
      baseline.value = state();
      window.$message?.success("TG订单通知设置已保存");
    }
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>

<template>
  <NCard title="TG订单通知" class="mt-24px" size="small">
    <NSpin v-if="loading" />
    <NAlert v-else-if="failed" type="error" title="读取 TG 配置失败">
      已有配置未被修改。<NButton class="ml-12px" @click="load"
        >重新加载</NButton
      >
    </NAlert>
    <template v-else>
      <p class="mb-16px">
        向商家个人、管理群或指定话题发送主站订单提醒。工单设置与此处分别保存。
      </p>
      <NForm
        label-placement="top"
        :disabled="!writable || saving"
        class="tg-form"
      >
        <div class="tg-switches">
          <NFormItem label="Telegram 通知通道"
            ><NSwitch v-model:value="enabled" aria-label="Telegram 通知通道"
          /></NFormItem>
          <NFormItem label="主站订单通知"
            ><NSwitch
              v-model:value="orderEnabled"
              aria-label="主站订单通知"
            /><span class="ml-8px"
              >关闭只停止订单通知，其他 TG 告警由通道开关控制</span
            ></NFormItem
          >
        </div>
        <NFormItem
          label="Bot Token"
          :validation-status="tokenError ? 'error' : undefined"
          :feedback="
            tokenError ||
            (savedToken
              ? '已配置，留空保留原 Token'
              : '从 @BotFather 获取；Token 加密保存')
          "
        >
          <NInput
            v-model:value="token"
            type="password"
            show-password-on="click"
            autocomplete="new-password"
            placeholder="填写或替换 Bot Token"
            aria-label="Bot Token"
          />
        </NFormItem>
        <NFormItem label="通知事件"
          ><NCheckboxGroup v-model:value="events"
            ><NSpace
              ><NCheckbox
                v-for="event in eventOptions"
                :key="event.value"
                :value="event.value"
                :label="event.label" /></NSpace></NCheckboxGroup
        ></NFormItem>
        <div class="mb-12px font-600">接收位置（最多 20 个）</div>
        <p class="mb-12px">
          Topic ID
          只在发往指定话题时填写；留空按普通方式发送。配置话题后发送失败，不会自动改投群主聊天。
        </p>
        <div
          v-for="(target, index) in targets"
          :key="target.key"
          class="tg-target"
        >
          <NFormItem
            :label="`接收位置 ${index + 1} · Chat ID`"
            :validation-status="
              dirty && chatError(target) ? 'error' : undefined
            "
            :feedback="dirty ? chatError(target) : ''"
          >
            <NInput
              v-model:value="target.chat_id"
              placeholder="例如 -1001234567890"
              :aria-label="`Chat ID ${index + 1}`"
            />
          </NFormItem>
          <NFormItem
            label="Topic ID（选填）"
            :validation-status="topicError(target) ? 'error' : undefined"
            :feedback="topicError(target)"
          >
            <NInput
              v-model:value="target.topic"
              placeholder="不使用话题请留空"
              :aria-label="`Topic ID ${index + 1}`"
            />
          </NFormItem>
          <NButton
            :disabled="!writable || saving"
            @click="targets.splice(index, 1)"
            >删除位置 {{ index + 1 }}</NButton
          >
        </div>
        <NButton
          :disabled="!writable || saving || targets.length >= 20"
          @click="targets.push({ chat_id: '', topic: '', key: ++rowKey })"
          >添加接收位置</NButton
        >
      </NForm>
      <NCollapse class="mt-16px"
        ><NCollapseItem title="如何填写 Chat ID 和 Topic ID？" name="help">
          <p>
            先将机器人加入接收群，并允许发送消息；个人接收需先向机器人发送
            /start。使用话题需在群里开启话题功能。
          </p>
          <p>
            Chat ID 是群或接收人的编号，Topic ID
            是该群内话题的编号，不是群编号，也不是话题中任意一条消息的编号。可从现有机器人配置或消息更新中的
            chat.id、message_thread_id 获取。
          </p>
          <p>
            也可复制话题内消息的链接：例如 t.me/your_group/123/456，其中
            @your_group 是接收群、123 是 Topic ID、456 是消息编号；若链接含
            thread=123，则话题编号为
            123。只有一个末尾数字的普通消息链接不能直接当成话题编号。
          </p>
          <p>先保存，再在下面按接收位置测试，确认消息进入正确话题。</p>
        </NCollapseItem></NCollapse
      >
      <NAlert v-if="dirty && validation" class="mt-16px" type="warning">{{
        validation
      }}</NAlert>
      <NSpace class="mt-16px" align="center">
        <NButton
          v-if="writable"
          type="primary"
          :loading="saving"
          :disabled="!dirty || !!validation"
          @click="save"
          >保存 TG 配置</NButton
        >
        <span v-if="dirty">TG 配置有未保存修改</span>
      </NSpace>
      <TelegramDelivery
        :unsaved="dirty || saving"
        :targets="savedTargets"
        :enabled="enabled && orderEnabled && savedToken"
      />
    </template>
  </NCard>
</template>

<style scoped>
.tg-form {
  max-width: 960px;
}
.tg-switches {
  display: flex;
  flex-wrap: wrap;
  gap: 0 24px;
}
.tg-target {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr) auto;
  gap: 12px;
  align-items: start;
  margin: 12px 0;
}
.tg-target > :last-child {
  margin-top: 30px;
}
@media (max-width: 640px) {
  .tg-target {
    grid-template-columns: minmax(0, 1fr);
    padding-bottom: 16px;
    border-bottom: 1px solid var(--n-border-color);
  }
  .tg-target > :last-child {
    margin-top: 0;
    justify-self: start;
  }
}
</style>
