<script setup lang="ts">
// 支付渠道管理（P2-09 T5 重写）——大厂交互：
// 卡片网格（品牌徽标/状态 Tag/已配置判定）+ 勾选式添加（批量接入 + 引导配置）
// + schema 驱动配置弹窗（敏感字段留空不修改、回调地址一键复制、手续费区）。
// 设计参照：1.x sysadmin 卡片式 + Filament 配置弹窗（回调地址展示先例）。
import { ref, reactive, computed, onMounted, h } from "vue";
import {
  NButton,
  NPopconfirm,
  NSwitch,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NInputNumber,
  NRadioGroup,
  NRadio,
  NAlert,
  NEmpty,
  NSpin,
  NDivider,
  NTag,
  NCheckbox,
  useMessage,
} from "naive-ui";
import { fetchChannels, fetchDrivers, fetchFieldOptions, createChannel, updateChannel, deleteChannel } from "@/service/api";
import PaymentsTab from "./components/payments-tab.vue";
import { checkAuth } from "@/directives";

defineOptions({ name: "PaymentChannelManagement" });

const message = useMessage();

interface ConfigFieldSchema {
  key: string;
  label: string;
  type: string;
  required: boolean;
  placeholder: string;
  help: string;
  sensitive: boolean;
  dynamic: boolean;
  multiple: boolean;
  options: { label: string; value: string }[];
  default: string;
}
interface DriverMeta {
  code: string;
  name: string;
  icon: string;
  description: string;
  fields: ConfigFieldSchema[];
}
interface ChannelRow {
  id: number;
  name: string;
  code: string;
  driver: string;
  config_json: string;
  configured_fields: string[];
  callback_url: string;
  enabled: boolean;
  fee: number;
  fee_type: string;
}

// ── 品牌徽标（品牌色字母徽章；未知驱动回落通用卡标）──
const driverBadges: Record<string, { char: string; bg: string; color?: string }> = {
  alipay: { char: "支", bg: "linear-gradient(135deg,#1677ff,#0e5fd8)", color: "#fff" },
  wechat: { char: "微", bg: "linear-gradient(135deg,#07c160,#06ad56)", color: "#fff" },
  epay: { char: "易", bg: "linear-gradient(135deg,#8b5cf6,#7c3aed)", color: "#fff" },
  epusdt: { char: "₮", bg: "linear-gradient(135deg,#26a17b,#1d8a68)", color: "#fff" },
  stripe: { char: "S", bg: "linear-gradient(135deg,#635bff,#5851ea)", color: "#fff" },
  paypal: { char: "P", bg: "linear-gradient(135deg,#003087,#012169)", color: "#fff" },
};
const badgeOf = (code: string) =>
  driverBadges[code] || { char: "💳", bg: "linear-gradient(135deg,#64748b,#475569)", color: "#fff" };

const loading = ref(false);
const saving = ref(false);
const channels = ref<ChannelRow[]>([]);
const drivers = ref<DriverMeta[]>([]);

// ── 添加渠道（勾选式批量接入）──
const addVisible = ref(false);
const driversLoading = ref(false);
const adding = ref(false);
const checkedDrivers = reactive<Record<string, boolean>>({});

// ── 配置弹窗 ──
const configVisible = ref(false);
const current = ref<ChannelRow | null>(null);
const currentFields = computed<ConfigFieldSchema[]>(() => {
  if (!current.value) return [];
  const d = drivers.value.find((x) => x.code === current.value?.driver);
  return d?.fields || [];
});
const form = reactive({
  name: "",
  enabled: true,
  fee_type: "fixed",
  fee: 0,
  values: {} as Record<string, any>,
});

// 动态选项（epusdt network/token——以网关 supported_assets 为准；失败回落静态 + 提示）
const dynamicOpts = reactive<Record<string, { options: { label: string; value: string }[]; fallback: boolean }>>({});
const dynamicLoading = reactive<Record<string, boolean>>({});

/** 加载动态字段选项；partial 取当前表单非敏感值（api_url 等），network 变化联动 token */
async function loadFieldOptions(f: ConfigFieldSchema) {
  const key = f.key;
  dynamicLoading[key] = true;
  const partial: Record<string, string | string[]> = {};
  for (const fld of currentFields.value) {
    if (fld.sensitive) continue;
    const v = form.values[fld.key];
    if (Array.isArray(v)) {
      if (v.length > 0) partial[fld.key] = v;
    } else if ((v || "").trim()) {
      partial[fld.key] = v as string;
    }
  }
  const { data, error } = await fetchFieldOptions(current.value?.driver || "", key, JSON.stringify(partial));
  if (!error && data) {
    dynamicOpts[key] = { options: data.options || [], fallback: !!data.fallback };
  } else {
    dynamicOpts[key] = { options: f.options || [], fallback: true };
  }
  dynamicLoading[key] = false;
}

/** 字段选项（动态优先，未加载回落 schema 静态 options） */
function fieldOptionsOf(f: ConfigFieldSchema) {
  if (f.dynamic && dynamicOpts[f.key]) return dynamicOpts[f.key].options;
  return f.options || [];
}

const driverOf = (code: string) => drivers.value.find((d) => d.code === code);
const isConfigured = (ch: ChannelRow) => (ch.configured_fields || []).length > 0;

async function loadList() {
  loading.value = true;
  try {
    const { data, error } = await fetchChannels();
    if (!error && data) channels.value = (data as any).channels || [];
  } finally {
    loading.value = false;
  }
}

async function loadDrivers() {
  const { data, error } = await fetchDrivers();
  if (!error && data) drivers.value = (data as any).drivers || [];
}

// ── 添加渠道：勾选驱动 → 批量创建 → 引导配置第一个 ──
function openAddDialog() {
  addVisible.value = true;
  driversLoading.value = true;
  checkedDrivers && Object.keys(checkedDrivers).forEach((k) => delete checkedDrivers[k]);
  loadDrivers().finally(() => (driversLoading.value = false));
}

const addableDrivers = computed(() =>
  drivers.value.filter((d) => !channels.value.some((c) => c.code === d.code)),
);

async function handleAdd() {
  const picked = drivers.value.filter((d) => checkedDrivers[d.code]);
  if (picked.length === 0) {
    message.warning("请至少选择一个支付渠道");
    return;
  }
  adding.value = true;
  const created: ChannelRow[] = [];
  for (const d of picked) {
    const { data, error } = await createChannel({
      name: d.name,
      code: d.code,
      driver: d.code,
      config_json: "{}",
      enabled: true,
      fee: 0,
      fee_type: "fixed",
    });
    if (!error && data) created.push(data);
  }
  adding.value = false;
  if (created.length === 0) {
    message.error("添加失败，请重试");
    return;
  }
  addVisible.value = false;
  message.success(`已添加 ${created.length} 个渠道`);
  await loadList();
  // 引导式：自动打开第一个新渠道的配置弹窗补凭据
  openConfig(created[0]);
}

// ── 配置弹窗：schema 驱动表单 ──
const feeLabel = computed(() => (form.fee_type === "percent" ? "比例（%）" : "固定金额（元）"));

function openConfig(ch: ChannelRow) {
  current.value = ch;
  form.name = ch.name;
  form.enabled = ch.enabled;
  form.fee_type = ch.fee_type || "fixed";
  // fee：fixed=分 → 元；percent=万分比 → 百分比（proto3 零值不输出——undefined 兜底 0）
  form.fee = Number(ch.fee || 0) / 100;
  form.values = {};
  // 脱敏回显：非敏感字段显值；敏感字段 **** → 显示为空（留空=不修改）
  let echo: Record<string, any> = {};
  try {
    echo = JSON.parse(ch.config_json || "{}");
  } catch {
    echo = {};
  }
  for (const f of currentFields.value) {
    const v = echo[f.key];
    if (Array.isArray(v) && v.length > 0) {
      form.values[f.key] = v; // 多选字段回显
    } else if (typeof v === "string" && v !== "****" && v !== "") {
      form.values[f.key] = v;
    }
  }
  // 动态选项字段（epusdt network/token）：打开即拉取网关 supported_assets
  for (const f of currentFields.value) {
    if (f.dynamic) loadFieldOptions(f);
  }
  configVisible.value = true;
}

const configuredKeys = computed(() => new Set(current.value?.configured_fields || []));

function handleConfigSave() {
  if (!current.value) return;
  // 必填校验：未配置过的必填字段必须填写（数组=非空）
  for (const f of currentFields.value) {
    const v = form.values[f.key];
    const filled = Array.isArray(v) ? v.length > 0 : !!((v as string) || "").trim();
    if (f.required && !configuredKeys.value.has(f.key) && !filled) {
      message.warning(`请填写「${f.label}」`);
      return;
    }
  }
  // 构造凭据：非空且非掩码的字段；敏感字段留空=不覆盖（后端 **** 语义）
  const cfg: Record<string, any> = {};
  for (const f of currentFields.value) {
    const v = form.values[f.key];
    if (Array.isArray(v)) {
      if (v.length > 0) cfg[f.key] = v; // 多选数组原样保存
    } else {
      const sv = ((v as string) || "").trim();
      if (sv && sv !== "****") cfg[f.key] = sv;
    }
  }
  const payload: Record<string, any> = {
    name: form.name.trim(),
    enabled: form.enabled,
    fee_type: form.fee_type,
    fee: Math.round(form.fee * (form.fee_type === "percent" ? 100 : 100)),
  };
  if (Object.keys(cfg).length > 0) payload.config_json = JSON.stringify(cfg);
  saving.value = true;
  updateChannel(current.value.id, payload)
    .then(({ error }) => {
      if (!error) {
        message.success("保存成功，立即生效");
        configVisible.value = false;
        loadList();
      }
    })
    .finally(() => (saving.value = false));
}

// ── 启用/删除 ──
async function handleToggle(row: ChannelRow, val: boolean) {
  const { error } = await updateChannel(row.id, { enabled: val });
  if (!error) {
    row.enabled = val;
    message.success(val ? "已启用" : "已停用");
  }
}

async function handleDelete(row: ChannelRow) {
  const { error } = await deleteChannel(row.id);
  if (!error) {
    message.success("已删除");
    loadList();
  }
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    message.success("已复制");
  } catch {
    message.error("复制失败，请手动选择复制");
  }
}

onMounted(() => {
  loadList();
  loadDrivers();
});
</script>

<template>
  <div class="min-h-500px">
    <!-- 页头：标题 + 说明 + 添加入口 -->
    <div class="mb-16px flex items-center justify-between gap-16px">
      <div>
        <div class="text-16px font-600">支付渠道</div>
        <div class="text-12px opacity-60 mt-4px">接入支付渠道并在下方完成凭据配置，保存后立即生效</div>
      </div>
      <NButton v-auth="'payment:write'" type="primary" @click="openAddDialog">添加渠道</NButton>
    </div>

    <NTabs type="line">
      <NTabPane name="channels" tab="渠道管理">
            <!-- 渠道卡片网格 -->
    <NSpin :show="loading">
      <div v-if="channels.length === 0 && !loading">
        <NEmpty description="尚未接入任何支付渠道" style="padding: 48px 0">
          <template #extra>
            <NButton v-auth="'payment:write'" type="primary" @click="openAddDialog">添加第一个支付渠道</NButton>
          </template>
        </NEmpty>
      </div>
      <div
        class="grid gap-16px"
        style="grid-template-columns: repeat(auto-fill, minmax(300px, 1fr))"
      >
        <div
          v-for="ch in channels"
          :key="ch.id"
          class="rounded-12px border border-#e5e7eb dark:border-#333 p-20px flex flex-col gap-12px transition-shadow hover:shadow-md"
        >
          <!-- 头部：徽标 + 名称 + 状态 -->
          <div class="flex items-start gap-12px">
            <div
              class="w-44px h-44px rounded-12px flex items-center justify-center text-20px font-700 shrink-0"
              :style="{ background: badgeOf(ch.code).bg, color: badgeOf(ch.code).color }"
            >
              {{ badgeOf(ch.code).char }}
            </div>
            <div class="flex-1 min-w-0">
              <div class="font-600 truncate">{{ ch.name }}</div>
              <div class="text-12px font-mono opacity-50 truncate">{{ ch.code }}</div>
            </div>
            <NSwitch size="small" :value="ch.enabled" @update:value="(v: boolean) => handleToggle(ch, v)" />
          </div>

          <!-- 状态 Tags：启用 + 已配置 -->
          <div class="flex gap-8px">
            <NTag size="small" :type="ch.enabled ? 'success' : 'default'" :bordered="false">
              {{ ch.enabled ? "已启用" : "已停用" }}
            </NTag>
            <NTag size="small" :type="isConfigured(ch) ? 'success' : 'warning'" :bordered="false">
              {{ isConfigured(ch) ? "已配置" : "待配置" }}
            </NTag>
            <NTag v-if="(ch.fee || 0) > 0" size="small" type="info" :bordered="false">
              {{ ch.fee_type === "percent" ? `费率 ${((ch.fee || 0) / 100).toFixed(2)}%` : `手续费 ${((ch.fee || 0) / 100).toFixed(2)} 元` }}
            </NTag>
          </div>

          <!-- 驱动说明 -->
          <div class="text-12px opacity-60 flex-1">{{ driverOf(ch.driver)?.description || ch.driver }}</div>

          <!-- 操作 -->
          <div class="flex items-center gap-8px pt-4px border-t border-#f1f5f9 dark:border-#333">
            <NButton v-auth="'payment:write'" size="small" type="primary" secondary class="flex-1" @click="openConfig(ch)">
              {{ isConfigured(ch) ? "配置" : "去配置" }}
            </NButton>
            <NPopconfirm :on-positive-click="() => handleDelete(ch)">
              <template #trigger>
                <NButton v-auth="'payment:delete'" size="small" type="error" secondary>删除</NButton>
              </template>
              删除后该渠道将无法继续收款，确定删除？
            </NPopconfirm>
          </div>
        </div>
      </div>
    </NSpin>
      </NTabPane>
      <NTabPane v-if="checkAuth('payment:read_detail')" name="payments" tab="支付单/退款单">
        <PaymentsTab />
      </NTabPane>
    </NTabs>

    <!-- 添加渠道：勾选式批量接入 -->
    <NModal v-model:show="addVisible" preset="card" title="添加支付渠道" style="width: 560px">
      <div class="text-12px opacity-60 mb-12px">选择要接入的支付渠道（可多选），创建后自动进入凭据配置</div>
      <NSpin :show="driversLoading">
        <div class="flex flex-col gap-8px max-h-360px overflow-auto pr-4px">
          <NCheckbox
            v-for="d in addableDrivers"
            :key="d.code"
            v-model:checked="checkedDrivers[d.code]"
            class="rounded-8px border border-#e5e7eb dark:border-#333 px-12px py-10px"
            :class="{ 'opacity-40': !checkedDrivers[d.code] }"
          >
            <div class="flex items-center gap-10px">
              <div
                class="w-32px h-32px rounded-8px flex items-center justify-center text-14px font-700 shrink-0"
                :style="{ background: badgeOf(d.code).bg, color: badgeOf(d.code).color }"
              >
                {{ badgeOf(d.code).char }}
              </div>
              <div class="min-w-0">
                <div class="text-13px font-500">{{ d.name }}</div>
                <div class="text-12px opacity-50 truncate">{{ d.description }}</div>
              </div>
            </div>
          </NCheckbox>
          <div v-if="addableDrivers.length === 0" class="text-center text-12px opacity-50 py-16px">
            全部渠道均已接入
          </div>
        </div>
      </NSpin>
      <template #footer>
        <div class="flex justify-end gap-8px">
          <NButton @click="addVisible = false">取消</NButton>
          <NButton type="primary" :loading="adding" :disabled="Object.values(checkedDrivers).filter(Boolean).length === 0" @click="handleAdd">
            添加所选（{{ Object.values(checkedDrivers).filter(Boolean).length }}）
          </NButton>
        </div>
      </template>
    </NModal>

    <!-- 配置弹窗：schema 驱动 -->
    <NModal v-model:show="configVisible" preset="card" :title="`配置「${current?.name || ''}」`" style="width: 640px">
      <div class="text-12px opacity-60 mb-16px">填写该支付通道所需的参数，保存后立即生效</div>

      <NForm label-placement="left" label-width="110" class="config-form">
        <NFormItem label="渠道名称">
          <NInput v-model:value="form.name" placeholder="渠道显示名称" />
        </NFormItem>
        <NFormItem label="启用">
          <NSwitch v-model:value="form.enabled" />
        </NFormItem>

        <!-- 手续费 -->
        <NDivider title-placement="left" style="margin: 4px 0 16px">手续费</NDivider>
        <NFormItem label="计费方式">
          <NRadioGroup v-model:value="form.fee_type">
            <NRadio value="fixed">固定金额</NRadio>
            <NRadio value="percent">按比例</NRadio>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="feeLabel">
          <NInputNumber
            v-model:value="form.fee"
            :min="0"
            :max="form.fee_type === 'percent' ? 100 : 1000000"
            :step="form.fee_type === 'percent' ? 0.1 : 0.01"
            :precision="2"
            style="width: 200px"
          />
          <span class="ml-8px text-12px opacity-50">{{ form.fee_type === "percent" ? "%" : "元" }}</span>
        </NFormItem>

        <!-- 渠道参数（schema 驱动） -->
        <template v-if="currentFields.length > 0">
          <NDivider title-placement="left" style="margin: 4px 0 16px">渠道参数</NDivider>
          <NFormItem
            v-for="f in currentFields"
            :key="f.key"
            :label="f.label"
            :required="f.required && !configuredKeys.has(f.key)"
          >
            <template v-if="f.type === 'select'">
              <NSelect
                v-model:value="form.values[f.key]"
                :options="fieldOptionsOf(f)"
                :multiple="f.multiple"
                :collapse-tags="f.multiple"
                :max-tag-count="f.multiple ? 3 : undefined"
                :loading="!!dynamicLoading[f.key]"
                :placeholder="f.multiple ? '选择支持的选项（可多选）' : f.placeholder || '请选择'"
                style="width: 100%"
                @update:value="
                  (v) => {
                    // 级联：network 变化 → 刷新 token 选项（已选链的代币并集）
                    if (f.dynamic && f.key === 'network') {
                      const tokenField = currentFields.find((x) => x.key === 'token');
                      if (tokenField) loadFieldOptions(tokenField);
                    }
                  }
                "
              />
              <div v-if="f.dynamic && dynamicOpts[f.key]?.fallback" class="text-12px opacity-50 mt-4px">
                无法连接网关，当前为内置选项（配置网关地址后自动刷新）
              </div>
            </template>
            <template v-else-if="f.type === 'textarea'">
              <NInput v-model:value="form.values[f.key]" type="textarea" :rows="4" :placeholder="f.placeholder || ''" />
            </template>
            <template v-else-if="f.type === 'number'">
              <NInputNumber v-model:value="form.values[f.key] as any" style="width: 100%" />
            </template>
            <template v-else>
              <NInput
                v-model:value="form.values[f.key]"
                :type="f.sensitive ? 'password' : 'text'"
                show-password-on="click"
                :placeholder="f.sensitive && configuredKeys.has(f.key) ? '留空表示不修改' : f.placeholder"
              />
            </template>
            <div v-if="f.help" class="text-12px opacity-50 mt-4px">{{ f.help }}</div>
            <div v-else-if="f.sensitive && configuredKeys.has(f.key)" class="text-12px opacity-50 mt-4px">
              已配置，留空保持不变
            </div>
          </NFormItem>
        </template>

        <!-- 回调地址（配置到支付平台 webhook/notify） -->
        <template v-if="current?.callback_url">
          <NDivider title-placement="left" style="margin: 4px 0 16px">回调地址</NDivider>
          <NAlert type="info" :show-icon="true" title="异步通知回调地址" style="margin-bottom: 16px">
            <div class="text-12px opacity-70 mb-8px">将该地址配置到支付平台的 webhook / 异步通知，用于接收支付结果</div>
            <div class="flex items-center gap-8px">
              <code class="flex-1 text-12px break-all select-all bg-#f1f5f9 dark:bg-#333 rounded-6px px-8px py-6px">
                {{ current.callback_url }}
              </code>
              <NButton size="small" secondary type="primary" @click="copyText(current.callback_url)">复制</NButton>
            </div>
          </NAlert>
        </template>
      </NForm>

      <template #footer>
        <div class="flex justify-end gap-8px">
          <NButton @click="configVisible = false">取消</NButton>
          <NButton v-auth="'payment:write'" type="primary" :loading="saving" @click="handleConfigSave">保存</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>
