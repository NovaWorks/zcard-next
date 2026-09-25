<script setup lang="ts">
import { computed, reactive, ref, watch, onUnmounted } from "vue";
import {
  NAlert,
  NButton,
  NDatePicker,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpace,
  NSpin,
  NTag,
} from "naive-ui";
import { fetchDeliverySources, setDeliverySource } from "@/service/api/catalog";
const props = defineProps<{ show: boolean; productId: number; readonly?: boolean }>();
const emit = defineEmits<{ (e: "update:show", value: boolean): void; (e: "saved"): void }>();
const loading = ref(false),
  saving = ref(false),
  error = ref(""),
  dirty = ref(false);
const reply = ref<any>({ sources: [], receipts: [], revision: 0 });
const sku = ref(0),
  origin = ref("first");
const form = reactive({
  mode: "upstream",
  content: "",
  procurement_id: 0,
  content_index: 0,
  expires_at: 0,
  max_deliveries: 0,
  upstream_order_id: "",
});
const current = computed(() =>
  reply.value.sources.find((s: any) => Number(s.sku_id || 0) === sku.value),
);
const receipts = computed(() =>
  reply.value.receipts.filter((r: any) => Number(r.sku_id || 0) === sku.value),
);
const busy = computed(() => ["submitting", "polling", "uncertain"].includes(current.value?.status));
const names: Record<string, string> = {
  unconfigured: "未配置",
  empty: "等待首次采购",
  submitting: "首次采购中",
  polling: "查询原采购结果",
  ready: "可重复发放",
  paused: "已暂停",
  uncertain: "需要核实原采购",
  failed: "需要处理",
  expired: "已过期",
};
function fill() {
  const s = current.value;
  form.mode = s?.mode || "upstream";
  form.content = "";
  form.procurement_id = 0;
  form.content_index = 0;
  form.expires_at = Number(s?.expires_at || 0);
  form.max_deliveries = Number(s?.max_deliveries || 0);
  form.upstream_order_id = s?.upstream_order_id || "";
  origin.value = s?.has_content ? "keep" : receipts.value.length ? "history" : "first";
  dirty.value = false;
}
let loadRequest=0;
async function load() {
  const request=++loadRequest; const productId=props.productId;
  loading.value = true;
  error.value = "";
  try {
    const { data, error: e } = await fetchDeliverySources(productId);
    if(request!==loadRequest || productId!==props.productId || !props.show)return;
    if (e) {
      error.value = "发货设置加载失败，请重试";
      return;
    }
    reply.value = data;
    reply.value.sources ||= [];
    reply.value.receipts ||= [];
    if (!reply.value.sources.some((s: any) => Number(s.sku_id || 0) === sku.value))
      sku.value = Number(reply.value.sources[0]?.sku_id || 0);
    fill();
  } finally {
    if(request===loadRequest)loading.value = false;
  }
}
let poll: ReturnType<typeof setInterval> | undefined;
function selectReceipt(v: string | number | null) {
  const [id, index] = String(v || "").split(":");
  form.procurement_id = Number(id || 0);
  form.content_index = Number(index || 0);
  dirty.value = true;
}
watch(
  () => [props.show,props.productId] as const,
  ([show]) => {
    clearInterval(poll);
    loadRequest++;
    if (show) {
      sku.value = 0;
      load();
      poll = setInterval(async () => {
        if (loading.value || saving.value || !busy.value) return;
        const pid = props.productId;
        const { data, error: e } = await fetchDeliverySources(pid);
        if (!e && props.show && pid === props.productId) {
          if(dirty.value && Number(data.revision)!==Number(reply.value.revision)){error.value="设置已被更新，当前草稿已保留，请重新加载后核对";return;}
          reply.value = data;
          if (!dirty.value) fill();
        }
      }, 5000);
    }
  },
  {immediate:true},
);
onUnmounted(() => clearInterval(poll));
function changeSku(value: number) {
  if (dirty.value) {
    window.$dialog?.warning({
      title: "切换规格？",
      content: "当前规格的未保存设置将丢弃。",
      positiveText: "丢弃并切换",
      negativeText: "继续编辑",
      onPositiveClick: () => {
        sku.value = value;
        fill();
      },
    });
  } else {
    sku.value = value;
    fill();
  }
}
function close() {
  if (saving.value) return;
  if (dirty.value) {
    window.$dialog?.warning({
      title: "放弃未保存设置？",
      content: "已经保存的发货设置仍会保留。",
      positiveText: "放弃并关闭",
      negativeText: "继续编辑",
      onPositiveClick: () => emit("update:show", false),
    });
  } else emit("update:show", false);
}
async function save(action = "configure") {
  if (props.readonly || saving.value) return;
  if (action === "configure" && form.mode === "reuse") {
    if (origin.value === "manual" && !form.content.trim()) {
      error.value = "请填写发货内容";
      return;
    }
    if (origin.value === "history" && !form.procurement_id) {
      error.value = "请选择一份历史采购内容";
      return;
    }
  }
  saving.value = true;
  error.value = "";
  try {
    const { data, error: e } = await setDeliverySource(props.productId, {
      sku_id: sku.value,
      mode: form.mode,
      expected_revision: reply.value.revision,
      action,
      content: origin.value === "manual" ? form.content : "",
      procurement_id: origin.value === "history" ? form.procurement_id : 0,
      content_index: form.content_index,
      first_purchase: origin.value === "first",
      expires_at: form.expires_at,
      max_deliveries: form.max_deliveries,
      upstream_order_id: form.upstream_order_id,
    });
    if (e) {
      error.value = (e as any)?.response?.data?.message || "保存失败，请检查设置；草稿已保留";
      return;
    }
    reply.value = data;
    reply.value.sources ||= [];
    reply.value.receipts ||= [];
    fill();
    emit("saved");
    window.$message?.success("发货设置已保存");
  } finally {
    saving.value = false;
  }
}
function submit() {
  if (form.mode === "upstream" && current.value?.mode !== "upstream") {
    window.$dialog?.warning({
      title: "切回每单上游采购？",
      content: "后续新订单将逐单向上游采购并产生费用。已有订单继续按原方式处理。",
      positiveText: "切回每单采购",
      negativeText: "取消",
      onPositiveClick: () => save(),
    });
  } else {
    save();
  }
}
</script>
<template>
  <NModal
    :show="show"
    preset="card"
    title="商品发货设置"
    style="width: 740px; max-width: 96vw"
    :mask-closable="false"
    @update:show="close"
  >
    <NSpin :show="loading">
      <div class="delivery-settings">
        <NAlert v-if="error" type="error"
          >{{ error }} <NButton v-if="!dirty" text @click="load">重新加载</NButton></NAlert
        >
        <NAlert v-if="readonly" type="warning">商品已锁定，可查看，解锁后才能修改发货设置。</NAlert>
        <NFormItem label="商品规格"
          ><NSelect
            :value="sku" :disabled="saving||loading"
            :options="
              reply.sources.map((s: any) => ({ label: s.sku_name, value: Number(s.sku_id || 0) }))
            "
            @update:value="changeSku"
        /></NFormItem>
        <NSpace v-if="current"
          ><NTag>{{ names[current.status] || current.status }}</NTag
          ><span v-if="current.id"
            >内容版本 #{{ current.id }} · 已发放 {{ current.deliveries || 0 }} 次</span
          ></NSpace
        >
        <NAlert v-if="current?.error" type="warning">{{ current.error }}</NAlert>
        <NFormItem label="发货来源"
          ><NSelect
            v-model:value="form.mode"
            :disabled="readonly || busy"
            :options="[
              { label: '每单向上游采购', value: 'upstream' },
              { label: '我的卡密：每条只卖一次', value: 'local' },
              { label: '重复发货：复用同一份内容', value: 'reuse' },
            ]"
            @update:value="dirty = true"
        /></NFormItem>
        <NAlert v-if="form.mode === 'local'" type="info"
          >请在「卡密库存」中为本规格导入卡密。缺货时停止接单，不会自动向上游采购；售价由你自行管理。</NAlert
        >
        <template v-if="form.mode === 'reuse'">
          <NAlert type="info"
            >每个规格独立保存内容，每单限购一份。内容准备好后直接本地发货；过期或停用不会自动重新采购。库存、售价不跟随上游同步。</NAlert
          >
          <NFormItem label="内容来源"
            ><NSelect
              v-model:value="origin"
              :disabled="readonly || busy"
              :options="[
                ...(current?.has_content ? [{ label: '保留当前内容', value: 'keep' }] : []),
                { label: '选择历史采购内容', value: 'history' },
                { label: '填写自己的账号 / 卡密', value: 'manual' },
                { label: '首次有已付款订单时采购一次', value: 'first' },
              ]"
              @update:value="dirty = true"
          /></NFormItem>
          <NFormItem v-if="origin === 'manual'" label="固定发货内容"
            ><NInput
              v-model:value="form.content"
              :disabled="readonly || busy"
              type="textarea"
              :rows="5"
              placeholder="账号、密码及使用说明；内容加密保存，仅发给已付款买家"
              @update:value="dirty = true"
          /></NFormItem>
          <NFormItem v-if="origin === 'history'" label="选择一份已购内容"
            ><NSelect
              :value="form.procurement_id ? `${form.procurement_id}:${form.content_index}` : null"
              :disabled="readonly || busy"
              :options="
                receipts.map((r: any) => ({
                  label: r.label,
                  value: `${r.procurement_id}:${r.content_index}`,
                }))
              "
              placeholder="仅显示同商品同规格的成功采购（最近100笔订单）"
              @update:value="selectReceipt"
          /></NFormItem>
          <NAlert v-if="origin === 'first'" type="warning"
            >首次采购只买 1
            份，会产生上游费用；多位买家共享同一次采购结果。响应不明确时暂停核实，不会再次采购。</NAlert
          >
          <NFormItem label="内容有效期（留空表示不设期限）"
            ><NDatePicker
              :value="form.expires_at ? form.expires_at * 1000 : null"
              :disabled="readonly || busy"
              type="datetime"
              clearable
              @update:value="
                (v) => {
                  form.expires_at = v ? Math.floor(v / 1000) : 0;
                  dirty = true;
                }
              "
          /></NFormItem>
          <NFormItem label="最多发放次数（0 表示不限）"
            ><NInputNumber
              v-model:value="form.max_deliveries"
              :disabled="readonly || busy"
              :min="0"
              :max="100000000"
              :precision="0"
              @update:value="dirty = true"
          /></NFormItem>
          <template v-if="current?.status === 'uncertain'"
            ><NFormItem label="原上游订单号"
              ><NInput
                v-model:value="form.upstream_order_id"
                :disabled="readonly"
                placeholder="核对上游扣款后填写原订单号，只查原单，不重新购买" /></NFormItem
            ><NButton
              :disabled="readonly || !form.upstream_order_id"
              :loading="saving"
              @click="save('reconcile')"
              >核查原采购结果</NButton
            ></template
          >
          <NSpace v-if="current?.has_content"
            ><NButton
              v-if="current.status === 'ready'"
              :disabled="readonly"
              :loading="saving"
              @click="save('pause')"
              >暂停复用，停止新销售</NButton
            ><NButton
              v-if="current.status === 'paused'"
              :disabled="readonly"
              :loading="saving"
              @click="save('resume')"
              >恢复复用</NButton
            ></NSpace
          >
        </template>
        <NAlert v-if="busy" type="warning"
          >首次采购结果尚未确认，暂不能更换来源。可先在商品列表下架，停止新订单。</NAlert
        >
      </div>
    </NSpin>
    <template #footer
      ><NSpace justify="end"
        ><NButton @click="close">关闭</NButton
        ><NButton
          type="primary"
          :disabled="readonly || busy || loading"
          :loading="saving"
          @click="submit"
          >保存发货设置</NButton
        ></NSpace
      ></template
    >
  </NModal>
</template>
<style scoped>
.delivery-settings {
  display: grid;
  gap: 12px;
  max-height: 70dvh;
  overflow: auto;
}
.delivery-settings :deep(.n-form-item) {
  margin-bottom: 0;
}
@media (max-width: 600px) {
  .delivery-settings {
    max-height: 65dvh;
  }
}
</style>
