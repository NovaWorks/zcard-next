<script setup lang="ts">
import AccountSecurity from "@/components/security/account-security.vue";
import { ref, reactive, computed, onMounted, onBeforeUnmount } from "vue";
import { useRoute, onBeforeRouteLeave } from "vue-router";
import { useI18n } from "vue-i18n";
import { fetchSettings, updateSettings, listCurrencies, fetchTemplates } from "@/service/api";
import type { TemplateItem } from "@/service/api";
import { NCheckbox, NCheckboxGroup, NRadioButton, NRadioGroup, NSpace, NTabs as OuterTabs, NTabPane as OuterTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import { resolveMediaUrl } from "@/utils/media";
import { initCurrency } from "@/utils/money";
import EmailTest from "./components/email-test.vue";
import TelegramSettings from "./components/telegram-settings.vue";
import CurrencyTab from "./components/currency-tab.vue";
import TopButtonField from "./components/top-button-field.vue";
import GiftTiersField from "./components/gift-tiers-field.vue";
import LinkListField from "./components/link-list-field.vue";
import AuditTab from "./components/audit-tab.vue";
import UpdateTab from "./components/update-tab.vue";
import ThemeSettingsModal from "./components/theme-settings-modal.vue";
import ThemePickerModal from "./components/theme-picker-modal.vue";

defineOptions({ name: "SettingsManagement" });

const route = useRoute();
// 外层 tab 受控：支持 ?tab=update 直达（header 更新徽标跳转用）
const outerTab = ref((route.query.tab as string) || "settings");

const { te, t } = useI18n();
const loading = ref(false);
const activeGroup = ref("site");
const items = ref<any[]>([]);
const saving = ref(false);
const announcementEditor = ref<any | null>(null);
// 已修改键集合（"group.key"）；驱动底部保存按钮可用态与批量提交。
const dirtyKeys = ref(new Set<string>());
const telegramDirty = ref(false);
const telegramSaving = ref(false);
const hasDirty = () => dirtyKeys.value.size > 0 || telegramDirty.value;
let switching = false;
async function switchGroup(group: string) {
  if (switching || loading.value || saving.value || telegramSaving.value || group === activeGroup.value) return;
  switching = true;
  try {
    if (hasDirty() && !(await confirmDiscard())) return;
    telegramDirty.value = false;
    activeGroup.value = group;
    await loadSettings();
    if (group === "i18n") await loadCurrencies();
  } finally { switching = false; }
}
async function switchOuter(tab: string) {
  if (switching || loading.value || saving.value || telegramSaving.value || tab === outerTab.value) return;
  switching = true;
  try {
    if (hasDirty()) {
      if (!(await confirmDiscard())) return;
      telegramDirty.value = false;
      await loadSettings();
    }
    outerTab.value = tab;
  } finally { switching = false; }
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (hasDirty()) { event.preventDefault(); event.returnValue = ''; }
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload));
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload));
onBeforeRouteLeave(async () => !saving.value && !telegramSaving.value && (!hasDirty() || await confirmDiscard()));
// 货币选择仅列启用货币；读取失败保持下拉并提供重试。
const currencyOptions = ref<{ label: string; value: string }[]>([]);
const currenciesLoading = ref(false);
const currencyLoadError = ref("");
const currencyVersion = ref(0);
const localeOptions = [
  { label: "简体中文", value: "zh_CN" },
  { label: "English", value: "en" },
];
const enabledLocaleOptions = computed(() => {
  const enabled = getVal(items.value.find((it) => it.group === "i18n" && it.key === "enabled_locales"));
  return localeOptions.filter((option) => Array.isArray(enabled) && enabled.includes(option.value));
});
function setEnabledLocales(item: any, enabled: string[]) {
  if (!enabled.length) {
    window.$message?.warning("至少启用一种语言");
    return;
  }
  setVal(item, enabled);
  const defaultItem = items.value.find((it) => it.group === "i18n" && it.key === "default_locale");
  if (defaultItem && !enabled.includes(String(getVal(defaultItem)))) setVal(defaultItem, enabled[0]);
}

// 可用模板清单（template 组主题弹窗数据源）；无权限/失败时为空 → 弹窗显示安装入口。
const templates = ref<TemplateItem[]>([]);

const groups = [
  { key: "site", label: "站点基础" },
  { key: "template", label: "模板" },
  { key: "promo", label: "推荐位" },
  { key: "footer", label: "页脚配置" },
  { key: "trade", label: "交易" },
  { key: "ticket", label: "工单 / TG通知" },
  { key: "security", label: "安全" },
  { key: "ops", label: "运维" },
  { key: "recharge", label: "充值" },
  { key: "supplier_recharge", label: "供货充值" },
  { key: "points", label: "积分" },
  { key: "withdraw", label: "提现" },
  { key: "affiliate", label: "分销设置" },
  { key: "supply", label: "货源" },
  { key: "notify", label: "邮件短信" },
  { key: "service", label: "客户代码" },
  { key: "i18n", label: "语言货币" },
];

// ── 长文本/JSON 类设置键：渲染 textarea（JSON 键格式化展示 + 解析校验）──
const TEXTAREA_KEYS: Record<string, string[]> = {
  service: ["widget_script", "stats_script"],
  site: ["robots_custom"],
  footer: ["about", "contact"],
  notify: ["sms_template_register", "sms_template_reset"],
};

// ── 结构化链接列表键：[{text,url}] / [{icon,url}]（LinkListField 行编辑，杜绝手写 JSON）──
const LINK_LIST_KEYS: Record<string, { icon?: boolean; textPlaceholder?: string; urlPlaceholder?: string; max?: number }> = {
  "footer.nav": { textPlaceholder: "导航文字", urlPlaceholder: "跳转链接 https://… 或站内路径 /products", max: 8 },
  "footer.social": { icon: true, urlPlaceholder: "主页链接 https://…", max: 8 },
  "promo.nav_recommend": { textPlaceholder: "推荐文字", urlPlaceholder: "跳转链接 https://… 或站内路径 /products", max: 3 },
};

function linkListOf(item: any) {
  return LINK_LIST_KEYS[`${item.group}.${item.key}`];
}

// ── 多选类设置键：渲染 checkbox 勾选（数组值；其余 options 键为单选）──
const MULTI_KEYS: Record<string, string[]> = {
  security: ["register_method"],
};

// ── 输入框占位提示（大厂模式：标签保持简短，填写说明放进框内 placeholder）──
const INPUT_PLACEHOLDERS: Record<string, Record<string, string>> = {
  notify: {
    sms_sign: "阿里云/腾讯云填签名名称；七牛填签名 ID",
    sms_sdk_app_id: "腾讯云必填，其余通道忽略",
    sms_template_code: "阿里云/腾讯云/七牛均需填写",
    smtp_host: "如 smtp.example.com（不含 https:// 或端口）",
    smtp_user: "邮件服务器的登录账号，通常为完整邮箱地址",
    smtp_from: "如 hello@example.com；留空使用 SMTP 用户名",
  },
  footer: {
    about: "页脚关于我们文案（留空不显示该栏）",
    contact: "如 邮箱 support@example.com · QQ 群 123456",
    icp: "如 京ICP备2026000000号-1（留空不显示）",
    agreement: "用户协议内容或指向文章的 slug",
  },
};

function smtpHelpOf(item: any): string | undefined {
  if (item.group !== "notify") return undefined;
  if (item.key === "smtp_security") {
    const mode = getVal(item);
    if (mode === "plain") return "普通 SMTP 不加密，账号密码也可能明文传输；适用于明确要求此方式的可信网络或中继。常见端口 25，请以服务商配置为准。";
    if (mode === "tls") return "连接建立后立即进行 TLS 握手（支持 TLS 1.2 / 1.3），常见端口 465；也可使用服务商指定的其他端口。";
    if (mode === "starttls") return "先建立 SMTP 连接，再强制升级为 TLS；服务器不支持升级时会报错。常见端口 587 或 25。";
    return "自动模式：465 使用 SSL/TLS，其他端口在服务器支持时升级为 STARTTLS。需要确定的连接行为时，请明确选择相应方式。";
  }
  if (item.key === "smtp_auth") return "自动选择服务器提供的 PLAIN、LOGIN 或 CRAM-MD5。免认证中继请选择「无需认证」；部分服务商要求填写 SMTP 专用授权码。";
  if (item.key === "smtp_tls_verify") return getVal(item)
    ? "校验邮件服务器证书的可信链、域名和有效期。自签名或证书链缺失会在测试发送中显示原因。"
    : "已关闭证书校验：TLS 仍加密传输，但不会验证服务器身份。仅用于你信任的自建服务；普通 SMTP 不使用此选项。";
  return undefined;
}

function inputPlaceholderOf(item: any) {
  return INPUT_PLACEHOLDERS[item.group]?.[item.key];
}

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
  if (item.group === 'ops' && item.key === 'announcement') {
    setVal(item, text);
    return;
  }
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
  if (item.group === "ops" && item.key === "announcement") return "支持 Markdown：# 标题、**加粗**、列表、链接、图片及表格；留空则回落公告文章";
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

// 这些外观项统一由主题自定义编辑；旧设置保留给未发布主题和历史客户端兼容。
const THEME_APPEARANCE_KEYS = new Set([
  "bg_image", "bg_image_mobile", "category_nav_style", "default_view",
  "per_row", "per_page", "sort_by", "show_stock", "show_sales",
]);

// ── 模板选择（WP 主题式：模板组 pc/mobile_template → 弹窗选择）──
const TEMPLATE_KEYS: Record<string, string[]> = {
  template: ["pc_template"],
};

function isTemplateKey(item: any) {
  return !!TEMPLATE_KEYS[item.group]?.includes(item.key);
}

// 当前模板展示名（templates 清单匹配；未收录回显原始值）
function currentTemplateName(item: any) {
  const v = getVal(item);
  const hit = templates.value.find((t: any) => t.key === v);
  return hit ? hit.name : String(v ?? "");
}

// 主题选择弹窗状态（目标字段 + 显隐）
const themeOptions = reactive({show:false,key:'classic'});
const themePicker = reactive<{ show: boolean; item: any }>({ show: false, item: null });
function openThemePicker(item: any) {
  themePicker.item = item;
  themePicker.show = true;
}
function onThemeSelect(key: string) {
  // The picker persists activation before emitting; preserve other unsaved settings.
  if (!themePicker.item) return;
  themePicker.item.value_json = JSON.stringify(key);
  dirtyKeys.value.delete(`${themePicker.item.group}.${themePicker.item.key}`);
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
  loading.value = true;
  items.value = [];
  dirtyKeys.value.clear();
  try {
    const { data, error } = await fetchSettings(activeGroup.value);
    if (!error && data) {
      items.value = ((data as any).items || []).filter(
        (item: any) => (item.group !== "template" || !THEME_APPEARANCE_KEYS.has(item.key)) &&
          (item.group !== "notify" || (item.key !== "smtp" && item.key !== "telegram" && !item.key.startsWith("telegram_"))),
      );
      if (activeGroup.value === "notify") {
        const order = ["smtp_host", "smtp_port", "smtp_security", "smtp_auth", "smtp_user", "smtp_password", "smtp_from", "smtp_name", "smtp_tls_verify"];
        const rank = (key: string) => order.includes(key) ? order.indexOf(key) : order.length;
        items.value.sort((a, b) => rank(a.key) - rank(b.key));
      }
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
  currenciesLoading.value = true;
  currencyLoadError.value = "";
  currencyOptions.value = [];
  try {
    const { data, error } = await listCurrencies();
    if (error || !(data as any)?.currencies) {
      currencyLoadError.value = "货币列表加载失败，请检查读取权限后重试";
      return;
    }
    currencyOptions.value = ((data as any).currencies as any[])
      .filter((c: any) => c.enabled)
      .map((c: any) => ({ label: `${c.code}（${c.symbol ?? ""}）`, value: c.code }));
    if (!currencyOptions.value.length) currencyLoadError.value = "暂无启用货币，请先在「货币」页创建并启用";
  } catch {
    currencyLoadError.value = "货币列表加载失败，请重试";
  } finally {
    currenciesLoading.value = false;
  }
}

async function currenciesUpdated() {
  await loadCurrencies();
  if (activeGroup.value === "i18n") await loadSettings();
}

// 表单级保存：一次提交本组全部已修改项（后端单事务原子写入）。
async function saveAll() {
  if (!dirtyKeys.value.size) return;
  saving.value = true;
  try {
    const pending = items.value
      .filter((it) => dirtyKeys.value.has(`${it.group}.${it.key}`))
      .map((it) => ({ group: it.group, key: it.key, value_json: it.value_json }));
    const { data, error } = await updateSettings(pending);
    if (!error) {
      window.$message?.success("设置已保存");
      dirtyKeys.value.clear();
      if (pending.some((it) => it.group === "i18n" && it.key === "base_currency")) {
        await Promise.all([initCurrency(true), loadCurrencies()]);
        currencyVersion.value += 1;
      }
      if (data?.admin_base_path) window.location.replace(`${data.admin_base_path}/settings`);
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
      <OuterTabs :value="outerTab" type="line" @update:value="switchOuter">
        <OuterTabPane name="settings" tab="参数设置">
          <NTabs :value="activeGroup" type="line" @update:value="switchGroup">
            <NTabPane v-for="g in groups" :key="g.key" :name="g.key" :tab="g.label" />
          </NTabs>

          <div v-if="loading" class="py-40px text-center">
            <NSpin size="large" />
          </div>

          <template v-else>
            <AccountSecurity v-if="activeGroup === 'security'" />
            <NAlert v-if="activeGroup === 'notify'" type="info" class="mb-16px max-w-760px" title="邮件连接与域名签名">
              按邮件服务商提供的方式选择普通 SMTP、SSL/TLS 或 STARTTLS，再保存配置并点击「测试邮件发送」。
              DKIM 的 Selector 和 TXT 公钥记录配置在发件域名的 DNS，签名私钥由邮件服务器管理；它与 SMTP 连接方式分别配置。
              测试成功代表邮件服务器已接受，最终到达收件箱还需检查投递日志和 SPF / DKIM / DMARC。
            </NAlert>
            <!-- 页脚配置分区说明：每个设置项对应前台页脚的哪个区块（大厂模式：先给全局地图再进表单） -->
            <div v-if="activeGroup === 'footer'" class="footer-map mt-12px">
              <div class="text-13px font-600 text-gray-700">页脚设置 ↔ 前台位置对照</div>
              <div class="mt-6px grid gap-x-24px gap-y-4px text-12px text-gray-500 sm:grid-cols-2">
                <span>· <b>页脚关于 / 社交链接</b> → 品牌栏（Logo 下方文案与图标）</span>
                <span>· <b>页脚导航</b> → 快速导航栏（未配置时显示默认链接）</span>
                <span>· <b>联系方式</b> → 「联系我们」栏（留空整栏隐藏）</span>
                <span>· <b>用户协议 / ICP 备案号</b> → 底部版权行</span>
                <span class="sm:col-span-2 text-gray-400">· 「帮助中心」「会员服务」两栏为系统内置导航，暂不支持自定义</span>
              </div>
            </div>

            <div v-if="activeGroup === 'template'" class="mt-12px text-13px text-gray-500">
              PC 和手机共用一个响应式主题。背景图、分类样式、商品布局和库存/销量显示统一在「主题自定义」中设置，修改或删除后点击「发布生效」。切换主题请点击「管理主题」；评价等业务开关仍在系统设置中管理。
            </div>

            <h3 v-if="activeGroup === 'ticket'" class="mt-16px font-600">工单设置</h3>
            <NForm label-placement="left" label-width="172" class="mt-16px max-w-760px settings-form" :class="{ 'settings-form-wide': ['ops', 'recharge', 'supplier_recharge'].includes(activeGroup), 'settings-form-stock': activeGroup === 'supply', 'settings-form-smtp': activeGroup === 'notify', 'settings-form-i18n': activeGroup === 'i18n' }">
              <NFormItem v-for="item in items" :key="item.key" :label="labelOf(item)">
                <div class="flex w-full items-center gap-8px">
                  <template v-if="linkListOf(item)">
                    <LinkListField
                      class="flex-1"
                      v-bind="linkListOf(item)"
                      :value="item.value_json"
                      @update="(v: any) => setVal(item, v)"
                    />
                  </template>
                  <template v-else-if="item.group === 'site' && item.key === 'top_button'">
                    <TopButtonField class="flex-1" :value="item.value_json" @update="(v: any) => setVal(item, v)" />
                  </template>
                  <template v-else-if="(item.group === 'recharge' || item.group === 'supplier_recharge') && item.key === 'gift_tiers'">
                    <GiftTiersField class="flex-1" :supplier="item.group === 'supplier_recharge'" :value="item.value_json" @update="(v: any) => setVal(item, v)" />
                  </template>
                  <template v-else-if="isTemplateKey(item)">
                    <div class="flex w-full flex-wrap items-center gap-8px">
                      <span class="min-w-0 flex-1 truncate text-13px">{{ currentTemplateName(item) }}</span>
                      <NButton size="small" @click="openThemePicker(item)">管理主题</NButton>
                      <NButton size="small" @click="themeOptions.key=getVal(item)||'classic';themeOptions.show=true">主题自定义</NButton>
                    </div>
                  </template>
                  <template v-else-if="isTextareaKey(item)">
                    <div class="w-full">
                    <NInput
                      :value="textareaValueOf(item)"
                      type="textarea"
                      :rows="item.group === 'ops' && item.key === 'announcement' ? 12 : item.key === 'widget_script' || item.key === 'stats_script' ? 6 : 4"
                      class="w-full"
                      :placeholder="textareaPlaceholderOf(item)"
                      @update:value="(v: string) => setTextareaValue(item, v)"
                    />
                    <div v-if="item.group === 'ops' && item.key === 'announcement'" class="mt-6px text-12px text-gray-500">
                      <div class="flex flex-wrap items-center justify-between gap-8px">
                        <span>支持 Markdown 格式。弹窗显示排版后的内容，首页公告条显示文字摘要。</span>
                        <NButton size="small" @click="announcementEditor = item">大窗口编辑</NButton>
                      </div>
                    </div>
                    </div>
                  </template>
                  <template v-else-if="isCarouselKey(item)">
                    <MediaField
                      class="flex-1"
                      multiple
                      :value="imageValueOf(item)"
                      tip="推荐宽 1200 px × 高 400 px（3:1）；默认主题会随屏幕裁切，文字和主体请居中，多张图片建议尺寸一致。 可多选并调整顺序。"
                      @update:value="(urls: string[]) => setImageValue(item, urls)"
                    />
                  </template>
                  <template v-else-if="isImageKey(item)">
                    <MediaField
                      class="flex-1"
                      :value="imageValueOf(item)"
                      :tip="item.group === 'ops' && item.key === 'announcement' ? '推荐宽 1200 px × 高 400 px（3:1）；默认主题会随屏幕裁切，文字和主体请居中，多张图片建议尺寸一致。' : '从素材库选择或上传；选中后即时预览'"
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
                  <template v-else-if="item.group === 'notify' && ['smtp_security', 'smtp_auth'].includes(item.key)">
                    <NSelect :value="String(getVal(item) ?? 'auto')" class="min-w-0 flex-1" :options="item.options"
                      :input-props="{ 'aria-label': labelOf(item) }" @update:value="(v: string) => setVal(item, v)" />
                  </template>
                  <template v-else-if="item.group === 'i18n' && item.key === 'default_locale'">
                    <NSelect
                      :value="String(getVal(item) ?? '')"
                      class="min-w-0 flex-1"
                      :options="enabledLocaleOptions"
                      :input-props="{ 'aria-label': labelOf(item) }"
                      @update:value="(v: string) => setVal(item, v)"
                    />
                  </template>
                  <template v-else-if="item.group === 'i18n' && item.key === 'enabled_locales'">
                    <div class="min-w-0 flex-1">
                      <NSelect
                        :value="multiValueOf(item)"
                        multiple
                        :options="localeOptions"
                        :input-props="{ 'aria-label': labelOf(item) }"
                        @update:value="(v: string[]) => setEnabledLocales(item, v)"
                      />
                      <p class="mt-6px text-12px text-gray-500">启用两种语言后，默认主题显示语言切换器；默认语言用于首次访问。</p>
                    </div>
                  </template>
                  <template v-else-if="item.group === 'i18n' && (item.key === 'base_currency' || item.key === 'display_currency')">
                    <div class="min-w-0 flex-1">
                      <NSelect
                        :value="String(getVal(item) ?? '')"
                        filterable
                        :loading="currenciesLoading"
                        :disabled="currenciesLoading || !!currencyLoadError"
                        :input-props="{ 'aria-label': labelOf(item) }"
                        :options="item.key === 'display_currency' ? [{ label: '跟随基础货币（结算币）', value: '' }, ...currencyOptions] : currencyOptions"
                        @update:value="(v: string) => setVal(item, v)"
                      />
                      <div v-if="currencyLoadError" role="alert" class="mt-6px flex flex-wrap items-center gap-8px text-12px text-red-600">
                        <span>{{ currencyLoadError }}</span><NButton size="tiny" @click="loadCurrencies">重试</NButton>
                      </div>
                      <p v-if="item.key === 'base_currency'" class="mt-6px text-12px text-gray-500">基础货币也是默认结算货币。已有商品、订单或资金记录时禁止直接切换，现有价格与余额需要专门迁移。</p>
                      <p v-else class="mt-6px text-12px text-gray-500">仅换算前台显示金额，实际结算使用基础货币。</p>
                    </div>
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
                  <template v-else-if="typeof getVal(item) === 'boolean'">
                    <NSwitch :value="getVal(item)" :aria-label="labelOf(item)" @update:value="(v: boolean) => setVal(item, v)" />
                  </template>
                  <template v-else-if="item.group === 'supply' && item.key === 'low_stock_threshold'">
                    <div class="min-w-0 flex-1">
                      <NInputNumber :value="getVal(item)" :min="1" :max="1000000" :precision="0" class="w-full max-w-200px" :input-props="{ 'aria-label': labelOf(item) }" @update:value="(v: number | null) => v !== null && setVal(item, v)" />
                      <p class="mt-6px text-12px text-gray-500">按实际发货规格判断。例如设置 5：库存 0–4 件提醒，5 件不提醒。TG 推送在「工单 / TG通知」单独开启；加速检查在货源渠道的定时计划中设置。</p>
                    </div>
                  </template>
                  <template v-else-if="typeof getVal(item) === 'number'">
                    <!-- 数字类设置：紧凑定宽输入（大厂模式——数值框不占满整行，标签后短输入即可） -->
                    <NInputNumber
                      :value="getVal(item)"
                      class="w-200px"
                      @update:value="(v: number | null) => v !== null && setVal(item, v)"
                    />
                  </template>
                  <template v-else-if="item.group === 'site' && item.key === 'admin_path'">
                    <div class="flex-1">
                      <NInput :value="String(getVal(item) ?? '')" placeholder="例如 /manage-8x2k" @update:value="(v: string) => setVal(item, v)" />
                      <p class="mt-6px text-12px text-gray-500">保存后立即跳转至新入口，原 /admin 入口关闭。留空使用启动配置中的路径（默认 /admin）。</p>
                    </div>
                  </template>
                  <template v-else>
                    <NInput
                      :value="String(getVal(item) ?? '')"
                      class="flex-1"
                      :type="item.secret ? 'password' : 'text'"
                      :show-password-on="item.secret ? 'click' : undefined"
                      :placeholder="inputPlaceholderOf(item)"
                      @update:value="(v: string) => setVal(item, v)"
                    />
                  </template>
                </div>
                <template v-if="smtpHelpOf(item)" #feedback>
                  <span class="text-13px leading-6">{{ smtpHelpOf(item) }}</span>
                </template>
              </NFormItem>
            </NForm>

            <div class="mt-24px flex items-center justify-end gap-8px border-t pt-16px max-w-760px">
              <span v-if="dirtyKeys.size" class="text-12px text-gray-400">{{ dirtyKeys.size }} 项修改未保存</span>
              <NButton
                v-auth="'settings:update'"
                type="primary"
                :loading="saving"
                :disabled="!dirtyKeys.size"
                @click="saveAll"
              >
                {{ activeGroup === 'ticket' ? '保存工单设置' : '保存更改' }}
              </NButton>
            </div>
            <EmailTest v-if="activeGroup === 'notify'" :unsaved="dirtyKeys.size > 0 || saving" />
            <TelegramSettings v-if="activeGroup === 'ticket'" @dirty="telegramDirty = $event" @busy="telegramSaving = $event" />
          </template>
        </OuterTabPane>
        <OuterTabPane v-if="checkAuth('settings:currency_read')" name="currency" tab="货币">
          <CurrencyTab :key="currencyVersion" @updated="currenciesUpdated" />
        </OuterTabPane>
        <OuterTabPane v-if="checkAuth('audit:read')" name="audit" tab="审计日志">
          <AuditTab />
        </OuterTabPane>
        <OuterTabPane v-if="checkAuth('system:update')" name="update" tab="系统更新">
          <UpdateTab />
        </OuterTabPane>
      </OuterTabs>
    </NCard>

    <NModal :show="!!announcementEditor" preset="card" title="编辑公告" class="announcement-editor-modal"
      :style="{ width: 'min(1100px, calc(100vw - 32px))' }"
      @update:show="(show: boolean) => { if (!show) announcementEditor = null; }">
      <template v-if="announcementEditor">
        <NInput :value="textareaValueOf(announcementEditor)" type="textarea" class="announcement-editor-input"
          placeholder="输入 Markdown 公告内容" :input-props="{ style: { height: '60vh', minHeight: '240px' } }"
          @update:value="(v: string) => setTextareaValue(announcementEditor, v)" />
        <div class="mt-12px flex flex-wrap items-center justify-between gap-12px">
          <span class="text-13px text-gray-500">支持 Markdown。完成编辑后，点击页面上的「保存更改」发布。</span>
          <NButton type="primary" @click="announcementEditor = null">完成编辑</NButton>
        </div>
      </template>
    </NModal>

    <ThemeSettingsModal v-model:show="themeOptions.show" :theme-key="themeOptions.key" />
    <!-- 主题选择弹窗（模板字段点击「选择主题」打开） -->
    <ThemePickerModal
      :show="themePicker.show"
      :current="themePicker.item ? getVal(themePicker.item) : undefined"
      @update:show="(v: boolean) => (themePicker.show = v)"
      @select="onThemeSelect"
      @installed="loadTemplates"
    />
  </div>
</template>

<style scoped>
.settings-form-wide { max-width: 1100px; }
@media (max-width: 640px) {
  .settings-form-wide :deep(.n-form-item) { grid-template-columns: minmax(0, 1fr); }
  .settings-form-stock :deep(.n-form-item), .settings-form-smtp :deep(.n-form-item), .settings-form-i18n :deep(.n-form-item) { grid-template-columns: minmax(0, 1fr); grid-template-areas: "label" "blank" "feedback"; grid-template-rows: auto auto auto; }
  .settings-form-stock :deep(.n-form-item-label), .settings-form-smtp :deep(.n-form-item-label), .settings-form-i18n :deep(.n-form-item-label) { display: flex; text-align: left; padding-bottom: 6px; }
  .settings-form-wide :deep(.n-form-item-label), .settings-form-stock :deep(.n-form-item-label), .settings-form-smtp :deep(.n-form-item-label), .settings-form-i18n :deep(.n-form-item-label) { justify-content: flex-start; }
}
/* 页脚分区说明卡（浅蓝信息底，与 naive 信息-alert 同语系） */
.footer-map {
  max-width: 760px;
  padding: 10px 14px;
  border: 1px solid #bfdbfe;
  border-radius: 8px;
  background: #eff6ff;
}

/* 长标签（如「单 IP 待付款订单上限」）换行时保持舒适行高并顶部对齐控件，
   避免默认垂直居中导致的多行标签上下悬空 */
.settings-form :deep(.n-form-item-label) {
  white-space: normal;
  line-height: 1.5;
  padding-top: 9px;
  align-items: flex-start;
}
.settings-form :deep(.n-form-item-label__text) {
  white-space: normal;
}
</style>
