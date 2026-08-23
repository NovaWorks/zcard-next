<script setup lang="ts">
import { ref, reactive, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { fetchSettings, updateSettings, listCurrencies, fetchTemplates } from "@/service/api";
import type { TemplateItem } from "@/service/api";
import { NCheckbox, NCheckboxGroup, NRadioButton, NRadioGroup, NSpace, NTabs as OuterTabs, NTabPane as OuterTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import { resolveMediaUrl } from "@/utils/media";
import CurrencyTab from "./components/currency-tab.vue";
import AuditTab from "./components/audit-tab.vue";
import ThemePickerModal from "./components/theme-picker-modal.vue";

defineOptions({ name: "SettingsManagement" });

const { te, t } = useI18n();
const loading = ref(false);
const activeGroup = ref("site");
const items = ref<any[]>([]);
const saving = ref(false);
// 已修改键集合（"group.key"）；驱动底部保存按钮可用态与批量提交。
const dirtyKeys = ref(new Set<string>());
const hasDirty = () => dirtyKeys.value.size > 0;
// 货币下拉选项（i18n.base_currency 联动）；无权限/失败时为空 → 回退文本输入。
const currencyOptions = ref<{ label: string; value: string }[]>([]);
// 可用模板清单（template 组主题弹窗数据源）；无权限/失败时为空 → 弹窗显示安装入口。
const templates = ref<TemplateItem[]>([]);

const groups = [
  { key: "site", label: "站点基础" },
  { key: "template", label: "模板" },
  { key: "trade", label: "交易" },
  { key: "security", label: "安全" },
  { key: "ops", label: "运维" },
  { key: "recharge", label: "充值" },
  { key: "supplier_recharge", label: "供货充值" },
  { key: "notify", label: "邮件短信" },
  { key: "service", label: "客户代码" },
  { key: "i18n", label: "语言货币" },
];

// ── 长文本/JSON 类设置键：渲染 textarea（JSON 键格式化展示 + 解析校验）──
const TEXTAREA_KEYS: Record<string, string[]> = {
  service: ["widget_script", "stats_script"],
  site: ["top_button", "robots_custom"],
  footer: ["nav", "social"],
  notify: ["sms_template_register", "sms_template_reset"],
};

// ── 多选类设置键：渲染 checkbox 勾选（数组值；其余 options 键为单选）──
const MULTI_KEYS: Record<string, string[]> = {
  security: ["register_method"],
};

function isTextareaKey(item: any) {
  if (TEXTAREA_KEYS[item.group]?.includes(item.key)) return true;
  // 公告文本类型：多行编辑（text/image/carousel 三态之一）
  return item.group === "ops" && item.key === "announcement" && announcementType() === "text";
}

function isMultiKey(item: any) {
  return !!MULTI_KEYS[item.group]?.includes(item.key);
}

/** JSON 对象/数组字段 → 格式化文本（供 textarea 编辑） */
function textareaValueOf(item: any) {
  const v = getVal(item);
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v ?? "");
  }
}

/** textarea 变更 → 写入（JSON 键尝试解析，解析失败按原文本存储） */
function setTextareaValue(item: any, text: string) {
  if (isTextareaKey(item)) {
    try {
      setVal(item, JSON.parse(text));
      return;
    } catch { /* 非 JSON 文本：按字符串存 */ }
  }
  setVal(item, text);
}

/** textarea 占位提示（客服/短信模板配置专用） */
function textareaPlaceholderOf(item: any) {
  if (item.key === "widget_script") return "粘贴 Chatwoot/Crisp 等第三方客服完整嵌入代码（含 <script> 标签）——前台右下角悬浮球";
  if (item.key === "stats_script") return "粘贴百度统计/Google Analytics/51la 等统计代码（含 <script> 标签）——前台页面最底部注入";
  if (item.key === "robots_custom") return "追加到 robots.txt 的规则（每行一条，如 Disallow: /member）；默认已放行全站并指向 sitemap";
  if (item.group === "ops" && item.key === "announcement") return "公告文本：显示在首页顶部公告条与公告弹窗；留空则回落最新公告文章";
  if (item.key === "sms_template_register" || item.key === "sms_template_reset") {
    return "短信模板内容（需与短信服务商控制台的模板一致）；变量：{code} 验证码 {minutes} 有效分钟 {site} 站点名";
  }
  return undefined;
}

// 显示名分层解析：前端语言包（当前语言）→ 后端 label（中文兜底）→ key 本身。
function labelOf(item: any) {
  const k = `settings.${item.group}.${item.key}`;
  return te(k) ? t(k) : (item.label || item.key);
}

// ── 图片类设置键：走素材库选择（MediaField 预览 + 弹窗选择/上传）──
const IMAGE_KEYS: Record<string, string[]> = {
  site: ["logo"],
  template: ["bg_image"],
  ops: ["announcement"], // 仅 announcement_type 为 image/carousel 时是图片
};

/** 公告类型（ops.announcement_type；缺省 text） */
function announcementType(): string {
  const typeItem = items.value.find((x) => x.group === "ops" && x.key === "announcement_type");
  const t = typeItem ? getVal(typeItem) : "text";
  return typeof t === "string" ? t : "text";
}

/** carousel 公告：多图轮播（数组值入库，区别于单图字符串） */
function isCarouselKey(item: any) {
  return item.group === "ops" && item.key === "announcement" && announcementType() === "carousel";
}

function isImageKey(item: any) {
  if (!IMAGE_KEYS[item.group]?.includes(item.key)) return false;
  if (item.group === "ops" && item.key === "announcement") {
    return announcementType() === "image" || announcementType() === "carousel";
  }
  return true;
}

function imageValueOf(item: any) {
  const v = getVal(item);
  if (Array.isArray(v)) return v.map(String); // carousel 多图数组
  return typeof v === "string" && v ? [v] : [];
}

function setImageValue(item: any, urls: string[]) {
  setVal(item, isCarouselKey(item) ? urls : urls[0] ?? "");
}

// ── 模板选择（WP 主题式：模板组 pc/mobile_template → 弹窗选择）──
const TEMPLATE_KEYS: Record<string, string[]> = {
  template: ["pc_template", "mobile_template"],
};

function isTemplateKey(item: any) {
  return !!TEMPLATE_KEYS[item.group]?.includes(item.key);
}

// 当前模板展示名（templates 清单匹配；未收录回显原始值）
function currentTemplateName(item: any) {
  const v = getVal(item);
  const hit = templates.value.find((t: any) => t.key === v);
  return hit ? `${hit.name}${hit.version ? `（v${hit.version}）` : ""}` : String(v ?? "");
}

// 主题选择弹窗状态（目标字段 + 显隐）
const themePicker = reactive<{ show: boolean; item: any }>({ show: false, item: null });
function openThemePicker(item: any) {
  themePicker.item = item;
  themePicker.show = true;
}
function onThemeSelect(key: string) {
  setVal(themePicker.item, key);
}

async function loadTemplates() {
  try {
    const { data, error } = await fetchTemplates();
    if (!error && (data as any)?.templates?.length) {
      templates.value = (data as any).templates;
    }
  } catch {
    // 无 settings:read 权限或接口异常：保持空列表，渲染回退文本输入
  }
}

function getVal(item: any) {
  if (!item) return undefined;
  try {
    return JSON.parse(item.value_json);
  } catch {
    return item.value_json;
  }
}

/** 多选键取值（数组；旧单值字符串兼容为单元素数组） */
function multiValueOf(item: any) {
  const v = getVal(item);
  if (Array.isArray(v)) return v.map(String);
  return typeof v === "string" && v ? [v] : [];
}

/** 单选键取值（数组意外值取首元素——防旧数据污染渲染） */
function radioValueOf(item: any) {
  const v = getVal(item);
  if (Array.isArray(v)) return String(v[0] ?? "");
  return String(v ?? "");
}

function setVal(item: any, val: any) {
  item.value_json = JSON.stringify(val);
  dirtyKeys.value.add(`${item.group}.${item.key}`);
}

async function loadSettings() {
  if (hasDirty()) {
    const ok = await confirmDiscard();
    if (!ok) return;
  }
  loading.value = true;
  items.value = [];
  dirtyKeys.value.clear();
  try {
    const { data, error } = await fetchSettings(activeGroup.value);
    if (!error && data) {
      items.value = (data as any).items || [];
    }
  } finally {
    loading.value = false;
  }
}

// 丢弃未保存修改的确认（naive dialog 回调式 → Promise）。
function confirmDiscard(): Promise<boolean> {
  return new Promise((resolve) => {
    window.$dialog?.warning({
      title: "有未保存的修改",
      content: "切换分组将丢弃当前未保存的修改，确定继续？",
      positiveText: "继续切换",
      negativeText: "取消",
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
      onMaskClick: () => resolve(false),
    });
  });
}

async function loadCurrencies() {
  try {
    const { data, error } = await listCurrencies();
    if (!error && (data as any)?.currencies?.length) {
      currencyOptions.value = ((data as any).currencies as any[]).map((c: any) => ({
        label: `${c.code}（${c.symbol ?? ""}）`,
        value: c.code,
      }));
    }
  } catch {
    // 无 settings:currency_read 权限或接口异常：保持空列表，渲染回退文本输入
  }
}

// 表单级保存：一次提交本组全部已修改项（后端单事务原子写入）。
async function saveAll() {
  if (!hasDirty()) return;
  saving.value = true;
  try {
    const pending = items.value
      .filter((it) => dirtyKeys.value.has(`${it.group}.${it.key}`))
      .map((it) => ({ group: it.group, key: it.key, value_json: it.value_json }));
    const { error } = await updateSettings(pending);
    if (!error) {
      window.$message?.success("设置已保存");
      dirtyKeys.value.clear();
    }
  } finally {
    saving.value = false;
  }
}

onMounted(() => {
  loadSettings();
  loadCurrencies();
  loadTemplates();
});
</script>

<template>
  <div class="min-h-500px">
    <NCard title="系统设置">
      <OuterTabs type="line">
        <OuterTabPane name="settings" tab="参数设置">
          <NTabs v-model:value="activeGroup" type="line" @update:value="loadSettings">
            <NTabPane v-for="g in groups" :key="g.key" :name="g.key" :tab="g.label" />
          </NTabs>

          <div v-if="loading" class="py-40px text-center">
            <NSpin size="large" />
          </div>

          <template v-else>
            <NForm label-placement="left" label-width="140" class="mt-16px max-w-640px">
              <NFormItem v-for="item in items" :key="item.key" :label="labelOf(item)">
                <div class="flex w-full items-center gap-8px">
                  <template v-if="isTemplateKey(item)">
                    <div class="flex w-full items-center gap-8px">
                      <span class="min-w-0 flex-1 truncate text-13px">{{ currentTemplateName(item) }}</span>
                      <NButton size="small" @click="openThemePicker(item)">选择主题</NButton>
                    </div>
                  </template>
                  <template v-else-if="isTextareaKey(item)">
                    <NInput
                      :value="textareaValueOf(item)"
                      type="textarea"
                      :rows="item.key === 'widget_script' || item.key === 'stats_script' ? 6 : 4"
                      class="w-full"
                      :placeholder="textareaPlaceholderOf(item)"
                      @update:value="(v: string) => setTextareaValue(item, v)"
                    />
                  </template>
                  <template v-else-if="isCarouselKey(item)">
                    <MediaField
                      class="flex-1"
                      multiple
                      :value="imageValueOf(item)"
                      tip="多图轮播：依次在首页顶部轮播展示，可多选并调整顺序"
                      @update:value="(urls: string[]) => setImageValue(item, urls)"
                    />
                  </template>
                  <template v-else-if="isImageKey(item)">
                    <MediaField
                      class="flex-1"
                      :value="imageValueOf(item)"
                      tip="从素材库选择或上传；选中后即时预览"
                      @update:value="(urls: string[]) => setImageValue(item, urls)"
                    />
                  </template>
                  <template v-else-if="isMultiKey(item) && item.options?.length">
                    <!-- 多选枚举：checkbox 勾选（数组值入库） -->
                    <NCheckboxGroup
                      :value="multiValueOf(item)"
                      class="flex-1"
                      @update:value="(v: (string | number)[]) => setVal(item, v.map(String))"
                    >
                      <NSpace wrap :size="12">
                        <NCheckbox v-for="o in item.options" :key="o.value" :value="o.value" :label="o.label" />
                      </NSpace>
                    </NCheckboxGroup>
                  </template>
                  <template v-else-if="item.options?.length">
                    <!-- 低基数枚举：单选按钮组直接可见，免下拉展开 -->
                    <NRadioGroup
                      :value="radioValueOf(item)"
                      class="flex-1"
                      @update:value="(v: string) => setVal(item, v)"
                    >
                      <NSpace wrap :size="4">
                        <NRadioButton v-for="o in item.options" :key="o.value" :value="o.value">
                          {{ o.label }}
                        </NRadioButton>
                      </NSpace>
                    </NRadioGroup>
                  </template>
                  <template v-else-if="activeGroup === 'i18n' && item.key === 'base_currency' && currencyOptions.length">
                    <NSelect
                      :value="String(getVal(item) ?? '')"
                      class="flex-1"
                      filterable
                      :options="currencyOptions"
                      @update:value="(v: string) => setVal(item, v)"
                    />
                  </template>
                  <template v-else-if="typeof getVal(item) === 'boolean'">
                    <NSwitch :value="getVal(item)" @update:value="(v: boolean) => setVal(item, v)" />
                  </template>
                  <template v-else-if="typeof getVal(item) === 'number'">
                    <NInputNumber
                      :value="getVal(item)"
                      class="flex-1"
                      @update:value="(v: number | null) => v !== null && setVal(item, v)"
                    />
                  </template>
                  <template v-else>
                    <NInput
                      :value="String(getVal(item) ?? '')"
                      class="flex-1"
                      :type="item.secret ? 'password' : 'text'"
                      :show-password-on="item.secret ? 'click' : undefined"
                      @update:value="(v: string) => setVal(item, v)"
                    />
                  </template>
                </div>
              </NFormItem>
            </NForm>

            <div class="mt-24px flex items-center justify-end gap-8px border-t pt-16px max-w-640px">
              <span v-if="hasDirty()" class="text-12px text-gray-400">{{ dirtyKeys.size }} 项修改未保存</span>
              <NButton
                v-auth="'settings:update'"
                type="primary"
                :loading="saving"
                :disabled="!hasDirty()"
                @click="saveAll"
              >
                保存更改
              </NButton>
            </div>
          </template>
        </OuterTabPane>
        <OuterTabPane v-if="checkAuth('settings:currency_read')" name="currency" tab="货币">
          <CurrencyTab />
        </OuterTabPane>
        <OuterTabPane v-if="checkAuth('audit:read')" name="audit" tab="审计日志">
          <AuditTab />
        </OuterTabPane>
      </OuterTabs>
    </NCard>

    <!-- 主题选择弹窗（模板字段点击「选择主题」打开） -->
    <ThemePickerModal
      :show="themePicker.show"
      :current="themePicker.item ? getVal(themePicker.item) : undefined"
      @update:show="(v: boolean) => (themePicker.show = v)"
      @select="onThemeSelect"
    />
  </div>
</template>
