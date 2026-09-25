<script setup lang="ts">
// 上游商品导入弹窗（ D）：预览分类树 → 勾选商品 → 定价策略（四模式）→
// 类目映射（上游分类 → 本地分类）→ 存为连接默认。已导入商品标注（重导 = 更新）。
import { filterGroups, selectProducts, matchedRule, type CategoryRule } from "./import-selection";
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  NAlert, NButton, NCheckbox, NCheckboxGroup, NForm, NFormItem, NInputNumber,
  NModal, NSelect, NSpace, NSpin, NTag, NTreeSelect, NInput,
  type TreeSelectOption,
} from "naive-ui";
import { previewSupplyProducts, importSupplyProducts } from "@/service/api";
import { fetchCategories } from "@/service/api";
import { formatMoney, yuanToFen } from "@/utils/money";

// 上游分类节点（含商品）
interface PreviewCategory {
  code: string;
  name: string;
  products: {
    code: string;
    name: string;
    price_cents: number;
    cost_price_cents?: number;
    cost_is_minimum?: boolean;
    quote_status?: "pending" | "loading" | "ready" | "failed";
    is_active: boolean;
    stock: number;
    already_imported: boolean;
    is_locked?: boolean;
    category_protected?: boolean;
    local_category_id?: number;
  }[];
}

const props = defineProps<{ show: boolean; connection: any }>();
const emit = defineEmits<{ (e: "update:show", v: boolean): void; (e: "imported"): void; (e: "task-created", id: number): void }>();

const loading = ref(false);
const previewError = ref("");
const submitError = ref("");
const previewMessage = ref("");
const snapshotId = ref("");
let previewController: AbortController | undefined;
const nameLength = (name: string) => Array.from(name.trim()).length;
const invalidDrafts = computed(() => selectedCategories.value.filter(cat => drafts[cat.code] && (nameLength(drafts[cat.code].name) === 0 || nameLength(drafts[cat.code].name) > 100)));
const showNameErrors = ref(false);
const nameErrorSummary = ref<HTMLElement>();
function focusCategory(code: string) {
  keyword.value = "";
  nextTick(() => document.getElementById(`import-category-${code}`)?.focus());
}
function stopPreview() { previewController?.abort(); previewController = undefined; }
function pollDelay(signal: AbortSignal) {
  return new Promise<void>(resolve => {
    const done = () => { clearTimeout(timer); signal.removeEventListener("abort", done); resolve(); };
    const timer = setTimeout(done, 2000);
    signal.addEventListener("abort", done, { once: true });
    if (signal.aborted) done();
  });
}
let previewRequest = 0;
const quoteRequests = new Set<AbortController>();
const importing = ref(false);
let submission = { signature: "", key: "" };
const categories = ref<PreviewCategory[]>([]);
const localCategories = ref<any[]>([]);
const checked = ref<string[]>([]);
const expandedCats = ref<Set<string>>(new Set());

const pricing = reactive({
  mode: "channel",
  markupPercent: 10,
  markupAmountYuan: 1,
  saveDefault: false,
});
const categoryMapDraft = reactive<Record<string, number | null>>({});

const drafts = reactive<Record<string, { name: string; parent_id: number | null }>>({});
const keyword = ref("");
const batchCategory = ref<number | null>(null);
const resultMessage = ref("");
const selectedCodes = computed(() => new Set(checked.value));
const counts = computed(() => new Map(categories.value.map(cat => [cat.code, cat.products.filter(p => selectedCodes.value.has(p.code)).length])));
const selectedCategories = computed(() => categories.value.filter(cat => (counts.value.get(cat.code) || 0) > 0));
const visibleCategories = computed(() => filterGroups(categories.value, keyword.value));
const allProducts = computed(() => categories.value.flatMap(c => c.products));
const visibleProducts = computed(() => visibleCategories.value.flatMap(c => c.products));
const hiddenSelected = computed(() => checked.value.filter(code => !visibleProducts.value.some(p => p.code === code)).length);
const lockedCount = computed(() => allProducts.value.filter(p => p.is_locked).length);
const categoryRules = ref<CategoryRule[]>([]);
const rulesOpen = ref(false);
const saveCategoryRules = ref(false);
const saveCategoryMapping = ref(false);
const productCategories = reactive<Record<string, number>>({});
const rulePage=ref(1);
const ruleResults = computed(() => allProducts.value.filter(p => selectedCodes.value.has(p.code)).map(p => {
  const index = p.category_protected ? -1 : matchedRule(p.name, categoryRules.value);
  return { ...p, rule: index, category: productCategories[p.code] ?? (p.category_protected ? Number(p.local_category_id || 0) : index >= 0 ? categoryRules.value[index].category_id : null) };
}));
const visibleRuleResults=computed(()=>ruleResults.value.slice((rulePage.value-1)*50,rulePage.value*50));
watch(()=>ruleResults.value.length,()=>{rulePage.value=Math.min(rulePage.value,Math.max(1,Math.ceil(ruleResults.value.length/50)));});
function selectAll(filtered = false, onlyNew = false) {
  const items = filtered ? visibleProducts.value : allProducts.value;
  checked.value = selectProducts(onlyNew ? [] : checked.value, items.filter(p => !onlyNew || !p.already_imported));
}
function addRule() { categoryRules.value.push({ keywords: [], excludes: [], category_id: 0, match_all: false }); }
function words(value: string) { return [...new Set(value.split(/[,，;；\n]+/).map(s => s.trim()).filter(Boolean))]; }
const draftCount = computed(() => new Set(selectedCategories.value.filter(cat => drafts[cat.code]).map(cat => JSON.stringify([drafts[cat.code].parent_id || 0, drafts[cat.code].name.trim()]))).size);
const mappedCount = computed(() => selectedCategories.value.filter(cat => !drafts[cat.code] && Number(categoryMapDraft[cat.code]) > 0).length);
function setMapping(code: string, value: number | null) {
  delete drafts[code];
  categoryMapDraft[code] = value;
}
function applyBatchCategory() {
  if (batchCategory.value === null) return;
  for (const code of checked.value) productCategories[code] = batchCategory.value;
}
function generateDrafts() {
  let ambiguous = 0;
  for (const cat of selectedCategories.value) {
    if (categoryMapDraft[cat.code] != null || drafts[cat.code]) continue;
    const matches = localCategories.value.filter(c => c.name === cat.name);
    if (matches.length === 1) categoryMapDraft[cat.code] = matches[0].id;
    else if (matches.length > 1) ambiguous++;
    else drafts[cat.code] = { name: cat.name, parent_id: null };
  }
  if (ambiguous) window.$message?.warning(`${ambiguous} 个分类有多个同名项，请按完整路径手动选择`);
}

const localCategoryOptions = computed(() => {
  const nodes = new Map<number, TreeSelectOption>();
  for (const category of localCategories.value) {
    nodes.set(category.id, { label: category.name, key: category.id });
  }
  const roots: TreeSelectOption[] = [];
  for (const category of localCategories.value) {
    const node = nodes.get(category.id)!;
    const parent = nodes.get(category.parent_id);
    if (parent && parent !== node) {
      (parent.children ??= []).push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
});

watch(
  () => [props.show, props.connection?.id] as const,
  ([show]) => {
    if (show && props.connection) {
      loadPreview();
      loadLocalCategories();
    } else {
      previewRequest++;
      stopPreview();
      stopQuotes();
    }
  },
  { immediate: true },
);

async function loadPreview(refresh = false, preserve = false) {
  const requestId = ++previewRequest;
  stopPreview();
  const controller = new AbortController();
  previewController = controller;
  stopQuotes();
  const connection = props.connection;
  const preserveExisting = preserve && categories.value.length > 0;
  if (refresh) void loadLocalCategories();
  loading.value = true;
  previewError.value = "";
  previewMessage.value = "正在加载上游目录，可以关闭窗口后再回来查看";
  if (!preserveExisting) {
    submitError.value = "";
    snapshotId.value = "";
    showNameErrors.value = false;
    categories.value = [];
    checked.value = [];
    expandedCats.value = new Set();
    for (const key of Object.keys(categoryMapDraft)) delete categoryMapDraft[key];
    for (const key of Object.keys(drafts)) delete drafts[key];
    keyword.value = "";
    batchCategory.value = null;
    resultMessage.value = "";
    for (const key of Object.keys(productCategories)) delete productCategories[key];
    categoryRules.value = [];
    saveCategoryMapping.value = false;
    saveCategoryRules.value = false;
    rulesOpen.value = false;
    Object.assign(pricing, { mode: "channel", markupPercent: 10, markupAmountYuan: 1, saveDefault: false });
  }
  try {
    let pollToken = "";
    let data: any;
    let error: any;
    while (!controller.signal.aborted) {
      const response = await previewSupplyProducts(connection.id, undefined, controller.signal, { async: true, snapshot_id: pollToken || undefined, refresh: !pollToken && refresh });
      if (requestId !== previewRequest || controller.signal.aborted) return;
      data = response.data; error = response.error;
      if (error || !data) break;
      pollToken = data.snapshot_id || pollToken;
      if (data.status === "failed") { previewError.value = data.message || "目录加载失败，请重试"; return; }
      if (data.status === "ready" || !data.status) break;
      previewMessage.value = data.message || `正在加载目录，已读取 ${data.loaded_count || 0} 件商品…`;
      await pollDelay(controller.signal);
    }
    if (requestId !== previewRequest) return;
    if (error) {
      const err = error as any;
      previewError.value = ["ECONNABORTED", "ETIMEDOUT"].includes(err.code)
        ? "加载上游商品超时，请重试；若持续失败，请检查上游响应速度或测试连接。"
        : err.response?.data?.message || err.message || "加载上游商品失败，请重试或测试连接。";
      return;
    }
    if (!error && data) {
      snapshotId.value = data.snapshot_id || "";
      categories.value = ((data as any).categories || []).map((cat: PreviewCategory) => ({
        ...cat, products: cat.products.map(p => ({ ...p, quote_status: p.quote_status || "pending" })),
      }));
      if (preserveExisting) {
        const available = new Set(allProducts.value.filter(p => !p.is_locked).map(p => p.code));
        const before = checked.value.length;
        checked.value = checked.value.filter(code => available.has(code));
        if (checked.value.length < before) resultMessage.value = `目录已更新，移除了 ${before - checked.value.length} 件已不可用或锁定的商品；其他选择和草稿已保留`;
        return;
      }
      // 连接默认定价回填
      try {
        const def = JSON.parse(connection.settings || "{}").import_pricing;
        if (def) {
          pricing.mode = def.mode || "channel";
          pricing.markupPercent = Number(def.markup_percent ?? 10);
          pricing.markupAmountYuan = Number(def.markup_amount_cents ?? 0) / 100;
        }
      } catch {
        /* 无默认 */
      }
      // 已持久化的类目映射回填（保存后全量同步沿用同一映射）
      try {
        const settings = JSON.parse(connection.settings || "{}");
        categoryRules.value = (settings.category_rules || []).map((r: any) => ({ ...r, category_id: Number(r.category_id), excludes: r.excludes || [], match_all: !!r.match_all }));
        const saved = settings.category_map;
        if (saved) {
          for (const [k, v] of Object.entries(saved)) {
            if (Number(v) >= 0) categoryMapDraft[k] = Number(v);
          }
        }
      } catch {
        /* 无映射 */
      }
    }
  } catch {
    if (requestId === previewRequest) previewError.value = "加载上游商品失败，请重试或测试连接。";
  } finally {
    if (requestId === previewRequest) loading.value = false;
  }
}

type PreviewItem = PreviewCategory["products"][number];
const expandedProducts = computed(() => visibleCategories.value
  .filter(cat => expandedCats.value.has(cat.code)).flatMap(cat => cat.products));

function stopQuotes() {
  for (const controller of quoteRequests) controller.abort();
  quoteRequests.clear();
}
onBeforeUnmount(() => { previewRequest++; stopPreview(); stopQuotes(); });

// At most two visible-category products query the upstream concurrently. The
// catalog stays usable, and closing/switching the modal cancels stale requests.
function pumpQuotes() {
  if (!props.show || loading.value || importing.value) return;
  for (const p of expandedProducts.value) {
    if (quoteRequests.size >= 2) break;
    if (p.quote_status === "pending") void loadQuote(p);
  }
}
async function loadQuote(p: PreviewItem) {
  const requestId = previewRequest;
  const controller = new AbortController();
  quoteRequests.add(controller);
  p.quote_status = "loading";
  try {
    const { data, error } = await previewSupplyProducts(props.connection.id, p.code, controller.signal, { snapshot_id: snapshotId.value || undefined });
    if (requestId !== previewRequest) return;
    const quoted = (data as { categories?: PreviewCategory[] } | null)?.categories
      ?.flatMap(cat => cat.products).find(item => item.code === p.code);
    if (!error && quoted?.quote_status === "ready" && Number(quoted.cost_price_cents) > 0) {
      p.cost_price_cents = Number(quoted.cost_price_cents);
      p.cost_is_minimum = quoted.cost_is_minimum;
      p.quote_status = "ready";
    } else {
      p.quote_status = "failed";
    }
  } catch {
    if (requestId === previewRequest) p.quote_status = "failed";
  } finally {
    quoteRequests.delete(controller);
    if (requestId === previewRequest) pumpQuotes();
  }
}
function retryQuote(p: PreviewItem) { p.quote_status = "pending"; pumpQuotes(); }
function refreshCosts() {
  for (const p of expandedProducts.value) if (p.quote_status !== "loading") p.quote_status = "pending";
  pumpQuotes();
}
watch([expandedProducts, loading, importing], pumpQuotes);

async function loadLocalCategories() {
  const requestId = previewRequest;
  const { data, error } = await fetchCategories();
  if (requestId !== previewRequest) return;
  if (!error && data) localCategories.value = (data as any).categories || [];
}

function toggleCat(cat: PreviewCategory, on: boolean) {
  checked.value = selectProducts(checked.value, cat.products, on);
}

// ── 分类折叠（默认全收起，点行展开/收起；勾选不受折叠影响）──
function toggleExpand(code: string) {
  const next = new Set(expandedCats.value);
  if (next.has(code)) next.delete(code);
  else next.add(code);
  expandedCats.value = next;
}

const allExpanded = computed(
  () => categories.value.length > 0 && visibleCategories.value.every((c) => expandedCats.value.has(c.code)),
);

function toggleAllExpand() {
  expandedCats.value = new Set(allExpanded.value ? [] : visibleCategories.value.map((c) => c.code));
}

async function submit() {
  if (loading.value || previewError.value) return;
  showNameErrors.value = true;
  if (invalidDrafts.value.length) {
    await nextTick(); nameErrorSummary.value?.focus(); return;
  }
  if (!checked.value.length) {
    window.$message?.warning("请先勾选要导入的商品");
    return;
  }
  if (importing.value) return;
  if (selectedCategories.value.some(cat => drafts[cat.code] && !drafts[cat.code].name.trim())) {
    window.$message?.warning("请填写待新建分类名称");
    return;
  }
  if (categoryRules.value.some(r => !r.category_id || !r.keywords.length)) {
    window.$message?.warning("请为每条分类规则填写关键词和目标分类"); return;
  }
  submitError.value = "";
  importing.value = true;
  try {
    const payload: Record<string, unknown> = {
      codes: checked.value,
      snapshot_id: snapshotId.value || undefined,
      product_categories: Object.fromEntries(checked.value.filter(c => productCategories[c] !== undefined).map(c => [c, productCategories[c]])),
      category_rules: categoryRules.value,
      save_category_rules: saveCategoryRules.value,
      selected_categories_only: !saveCategoryMapping.value,
      pricing_mode: pricing.mode,
      save_default: pricing.saveDefault,
      category_map: Object.fromEntries(selectedCategories.value
        .filter(cat => !drafts[cat.code] && categoryMapDraft[cat.code] != null)
        .map(cat => [cat.code, categoryMapDraft[cat.code]])),
      category_drafts: selectedCategories.value.filter(cat => drafts[cat.code]).map(cat => ({
        upstream_code: cat.code, name: drafts[cat.code].name.trim(), parent_id: drafts[cat.code].parent_id || 0,
      })),
    };
    if (pricing.mode === "percent") payload.markup_percent = pricing.markupPercent;
    if (pricing.mode === "fixed") payload.markup_amount_cents = yuanToFen(pricing.markupAmountYuan);
    const signature = JSON.stringify(payload);
    if (signature !== submission.signature) submission = { signature, key: crypto.randomUUID() };
    payload.request_key = submission.key;
    const { data, error } = await importSupplyProducts(props.connection.id, payload as any);
    if (error) {
      const err = error as any;
      const message = err.response?.data?.message || err.message || "提交失败，选择和草稿已保留，请重试";
      if (/目录|货源账号已变化/.test(message)) previewError.value = message;
      else submitError.value = message;
      return;
    }
    if (!error && data) {
      const task = (data as any).task;
      if (task?.id) {
        emit("imported");
        emit("task-created", Number(task.id));
        emit("update:show", false);
        window.$message?.success("导入任务已创建，可以关闭页面，后台会继续处理");
      }
    }
  } finally {
    importing.value = false;
  }
}
</script>

<template>
  <NModal :show="props.show" preset="card" :title="`导入上游商品：${props.connection?.name || ''}`"
    style="width: 1000px; max-width: 96vw" :closable="!importing" :mask-closable="false" :close-on-esc="!importing"
    @update:show="!importing && emit('update:show', $event)">
    <NSpin :show="importing">
      <div class="import-body" :inert="importing || undefined">
        <NAlert v-if="loading" type="info" :bordered="false" role="status" aria-live="polite">{{ previewMessage }}<div>关闭窗口不影响后台加载，完成后重新打开即可查看。</div></NAlert>
        <NAlert v-else-if="previewError" type="error" :bordered="false" role="alert">
          {{ previewError }} <NButton size="small" @click="loadPreview(true, true)">重新加载</NButton>
        </NAlert>
        <NAlert v-else-if="!categories.length" type="warning" :bordered="false">上游商品目录为空，请确认对接账号有可用商品。</NAlert>
        <NAlert v-if="submitError" type="error" :bordered="false" role="alert">{{ submitError }}</NAlert>
        <NAlert v-if="resultMessage" type="warning" :bordered="false">{{ resultMessage }}</NAlert>
        <div v-if="showNameErrors && invalidDrafts.length" ref="nameErrorSummary" tabindex="-1" role="alert" class="category-name-errors">
          <strong>{{ invalidDrafts.length }} 个分类名称需要修改（1–100 个字符）</strong>
          <div v-for="cat in invalidDrafts" :key="cat.code"><NButton text type="error" @click="focusCategory(cat.code)">{{ cat.name }}：{{ nameLength(drafts[cat.code].name) }} 个字符，点击修改</NButton></div>
        </div>
        <NSpace class="selection-toolbar">
          <NButton size="small" :disabled="loading || !!previewError" @click="selectAll()">全选全部商品（{{ allProducts.length }} 件）</NButton>
          <NButton v-if="keyword.trim()" size="small" @click="selectAll(true)">全选搜索结果（{{ visibleProducts.length }} 件）</NButton>
          <NButton size="small" :disabled="loading || !!previewError" @click="selectAll(false, true)">仅选未导入商品</NButton>
          <NButton size="small" :disabled="!checked.length" @click="checked = []">清空选择</NButton>
          <NButton size="small" :disabled="loading" @click="rulesOpen = !rulesOpen">自动分类（{{ categoryRules.length }} 条规则）</NButton>
        </NSpace>
        <div v-if="rulesOpen" class="classification-panel">
          <NAlert type="info" :bordered="false">按商品名称匹配，排在前面的规则优先。下方结果可逐件修改，手工选择优先；只影响本次选中商品。</NAlert>
          <div v-for="(rule, i) in categoryRules" :key="i" class="rule-row">
            <NInput :value="rule.keywords.join('，')" placeholder="关键词，多个用逗号分隔" :aria-label="`规则${i+1}关键词`" @update:value="v => rule.keywords = words(v)" />
            <NInput :value="rule.excludes.join('，')" placeholder="排除词（可选）" :aria-label="`规则${i+1}排除词`" @update:value="v => rule.excludes = words(v)" />
            <NTreeSelect :value="rule.category_id || null" :options="localCategoryOptions" filterable show-path placeholder="目标分类" @update:value="v => rule.category_id = Number(v)" />
            <NCheckbox v-model:checked="rule.match_all">全部关键词</NCheckbox>
            <NButton size="small" :disabled="i === 0" @click="[categoryRules[i-1], categoryRules[i]] = [categoryRules[i], categoryRules[i-1]]">上移</NButton>
            <NButton size="small" @click="categoryRules.splice(i, 1)">删除</NButton>
          </div>
          <NSpace><NButton size="small" @click="addRule">添加规则</NButton><NCheckbox v-model:checked="saveCategoryRules">记住此货源的分类规则</NCheckbox></NSpace>
          <NSpace justify="space-between"><span>分类预览：共 {{ruleResults.length}} 件</span><NSpace><NButton size="small" :disabled="rulePage<=1" @click="rulePage--">上一页</NButton><span>{{rulePage}} / {{Math.max(1,Math.ceil(ruleResults.length/50))}}</span><NButton size="small" :disabled="rulePage*50>=ruleResults.length" @click="rulePage++">下一页</NButton></NSpace></NSpace>
          <div class="rule-preview">
            <div v-for="p in visibleRuleResults" :key="p.code" class="rule-preview-row">
              <span>{{ p.name }}<small>{{ productCategories[p.code] !== undefined ? ' · 手工指定' : p.category_protected ? ' · 保留手动分类' : p.rule >= 0 ? ` · 规则 ${p.rule + 1}` : ' · 未命中，沿用分类映射或原分类' }}</small></span>
              <NTreeSelect :value="p.category" :options="[{key:0,label:'未分类'}, ...localCategoryOptions]" clearable filterable show-path placeholder="沿用分类" @update:value="v => v === null ? delete productCategories[p.code] : productCategories[p.code] = Number(v)" />
            </div>
          </div>
        </div>
        <div class="import-toolbar">
          <NInput v-model:value="keyword" clearable placeholder="搜索上游分类或商品名称" aria-label="搜索上游分类或商品名称" />
          <NButton size="small" @click="toggleAllExpand">{{ allExpanded ? '全部收起' : '全部展开' }}</NButton>
          <NButton size="small" :disabled="!expandedProducts.length || loading" @click="refreshCosts">刷新成本</NButton>
        </div>
        <div class="text-12px text-gray-400">展开分类后查询账号成本，已按渠道汇率换算，不含加价；多规格显示最低成本。正式导入会在后台逐件核价；锁定或被人工修改的商品会跳过。分类名命中显示整类，商品名命中只显示匹配商品；搜索不会取消已选商品。锁定商品不可勾选。</div>
        <div class="category-list">
          <div v-for="cat in visibleCategories" :key="cat.code" class="category-item">
            <div class="category-row">
              <div class="category-heading">
                <NButton text :aria-label="`${expandedCats.has(cat.code) ? '收起' : '展开'}${cat.name}`" :aria-expanded="expandedCats.has(cat.code)" @click="toggleExpand(cat.code)">{{ expandedCats.has(cat.code) ? '▼' : '▶' }}</NButton>
                <NCheckbox :checked="cat.products.some(p => !p.is_locked) && cat.products.filter(p => !p.is_locked).every(p => selectedCodes.has(p.code))"
                  :indeterminate="cat.products.some(p=>!p.is_locked&&selectedCodes.has(p.code)) && !cat.products.filter(p=>!p.is_locked).every(p=>selectedCodes.has(p.code))"
                  :aria-label="`选择${cat.name}全部商品`" @update:checked="(v: boolean) => toggleCat(cat, v)" />
                <button type="button" class="category-name" :title="cat.name" @click="toggleExpand(cat.code)">{{ cat.name }}</button>
                <NTag size="tiny" :bordered="false">{{ cat.products.length }} 件</NTag>
                <NTag v-if="counts.get(cat.code)" size="tiny" type="primary" :bordered="false">已选 {{ counts.get(cat.code) }}</NTag>
              </div>
              <div class="category-destination">
                <template v-if="drafts[cat.code]">
                  <div class="draft-name"><NTag size="small" type="warning">待新建</NTag><NInput v-model:value="drafts[cat.code].name" size="small" :input-props="{ id: `import-category-${cat.code}` }" :status="showNameErrors && (nameLength(drafts[cat.code].name) === 0 || nameLength(drafts[cat.code].name) > 100) ? 'error' : undefined" placeholder="新分类名称（最多 100 字）" :aria-label="`${cat.name}的新分类名称`" /></div>
                  <small :class="{ 'text-red-500': nameLength(drafts[cat.code].name) > 100 }">{{ nameLength(drafts[cat.code].name) }} / 100 字</small>
                  <NTreeSelect v-model:value="drafts[cat.code].parent_id" :options="localCategoryOptions" clearable filterable show-path size="small" placeholder="创建位置：顶级分类" :aria-label="`${cat.name}的父分类`" />
                  <NButton text size="tiny" @click="delete drafts[cat.code]">取消新建，改选已有分类</NButton>
                </template>
                <NTreeSelect v-else :value="categoryMapDraft[cat.code]" :options="[{ key: 0, label: '不归入分类（清除映射）' }, ...localCategoryOptions]"
                  clearable filterable show-path size="small" placeholder="沿用原映射；无映射则未分类" :aria-label="`${cat.name}的本地分类`"
                  @update:value="(v: number | null) => setMapping(cat.code, v)" />
              </div>
            </div>
            <NCheckboxGroup v-if="expandedCats.has(cat.code)" v-model:value="checked">
              <div class="product-list">
                <div v-for="p in cat.products" :key="p.code" class="product-item">
                <NCheckbox :value="p.code" :disabled="p.is_locked" :aria-disabled="p.is_locked">
                  <span class="break-all" :class="{ 'text-gray-400': !p.is_active }">{{ p.name }}</span>
                  <span class="ml-4px text-12px" aria-live="polite">
                    <template v-if="p.quote_status === 'ready'">成本 {{ formatMoney(p.cost_price_cents ?? 0) }}{{ p.cost_is_minimum ? ' 起' : '' }}</template>
                    <template v-else-if="p.quote_status === 'failed'">成本查询失败</template>
                    <template v-else>成本查询中…</template>
                    <template v-if="p.stock >= 0"> · 库存 {{ p.stock }}</template>
                  </span>
                  <NTag v-if="p.is_locked" size="tiny" type="warning">已锁定，跳过</NTag>
                  <NTag v-if="p.already_imported" size="tiny" type="info" :bordered="false" class="ml-4px">已导入</NTag>
                  <NTag v-if="!p.is_active" size="tiny" type="warning" :bordered="false" class="ml-4px">已下架</NTag>
                </NCheckbox>
                <NButton v-if="p.quote_status === 'failed'" size="small" :aria-label="`重试${p.name}成本`" @click="retryQuote(p)">重试</NButton>
                </div>
              </div>
            </NCheckboxGroup>
          </div>
          <div v-if="categories.length && !visibleCategories.length" class="p-16px text-gray-400">没有匹配的分类或商品</div>
        </div>
        <div class="mapping-actions">
          <NTreeSelect v-model:value="batchCategory" :options="[{ key: 0, label: '不归入分类' }, ...localCategoryOptions]" clearable filterable show-path size="small" placeholder="批量指定本地分类" aria-label="批量指定本地分类" />
          <NButton size="small" :disabled="!selectedCategories.length || batchCategory === null" @click="applyBatchCategory">应用到已选商品</NButton>
          <NCheckbox v-model:checked="saveCategoryMapping">将分类行设置保存为整个上游分类的默认映射</NCheckbox>
          <NButton v-auth="'catalog:category_write'" size="small" type="primary" secondary :disabled="!selectedCategories.length" @click="generateDrafts">生成映射草稿</NButton>
        </div>
        <div class="text-12px text-gray-400">只处理所选商品涉及的分类；草稿保存前不会出现在商城。保存后的映射也用于后续全量同步及该上游分类的其他已导入商品。</div>
          <NAlert :type="pricing.mode === 'channel' ? 'info' : 'warning'" class="mb-12px">
            <template v-if="pricing.mode === 'channel'">
              跟随渠道：账号报价 × {{ connection.exchange_rate || 1 }} ×（1 + {{ connection.price_markup_percent || 0 }}%）+ {{ formatMoney(connection.price_markup_amount || 0) }}，再按渠道取整规则计算；商品和规格使用同一规则。
            </template>
            <template v-else>当前为独立导入策略，不叠加渠道加价。规则会保存到所选商品，后续同步继续使用；待定价商品保持现价和下架状态。重新导入会更新未受人工改价保护的商品规则。</template>
          </NAlert>
          <NForm label-placement="left" size="small" class="pricing-grid">
            <NFormItem label="定价策略" :show-feedback="false">
              <NSelect
                v-model:value="pricing.mode"
                :options="[
                  { label: '跟随渠道定价', value: 'channel' },
                  { label: '独立加价比例（%）', value: 'percent' },
                  { label: '加固定金额（元）', value: 'fixed' },
                  { label: '账号报价导入（不加价）', value: 'equal' },
                  { label: '待定价（导入后不上架）', value: 'pending' },
                ]"
              />
            </NFormItem>
            <NFormItem v-if="pricing.mode === 'percent'" label="加价比例（%）" :show-feedback="false">
              <NInputNumber v-model:value="pricing.markupPercent" :min="0" class="w-full" placeholder="10 = 加价 10%" />
            </NFormItem>
            <NFormItem v-if="pricing.mode === 'fixed'" label="加价金额（元）" :show-feedback="false">
              <NInputNumber v-model:value="pricing.markupAmountYuan" :min="0.01" :precision="2" class="w-full" />
            </NFormItem>
            <div class="pricing-default">
                <NCheckbox v-model:checked="pricing.saveDefault">记住下次导入策略</NCheckbox>
                <span class="text-12px text-gray-400">
                  仅记住导入选项，不修改渠道加价；已有的独立导入策略会保留。
                </span>
            </div>
          </NForm>
      </div>
    </NSpin>
    <template #footer>
      <NSpace justify="space-between" align="center">
        <span class="text-12px">已选 {{ checked.length }} 件（搜索范围外 {{ hiddenSelected }} 件；锁定跳过 {{ lockedCount }} 件） · 涉及 {{ selectedCategories.length }} 类 · 已指定 {{ mappedCount }} 类 · 待新建 {{ draftCount }} 类</span>
        <NSpace>
          <NButton size="small" :disabled="importing" @click="emit('update:show', false)">取消</NButton>
          <NButton size="small" type="primary" :loading="importing" :disabled="loading || !!previewError || !checked.length" @click="submit">开始导入</NButton>
        </NSpace>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.import-body { height: min(78vh, calc(100dvh - 160px)); min-height: 0; display: flex; flex-direction: column; gap: 6px; }
.import-body > :not(.category-list) { flex-shrink: 0; }
.import-toolbar { display: flex; align-items: center; gap: 12px; }
.category-list { flex: 1 1 0; min-height: 0; overflow: auto; overscroll-behavior-y: contain; scrollbar-gutter: stable; border: 1px solid var(--n-border-color); border-radius: 8px; }
.category-item + .category-item { border-top: 1px solid var(--n-border-color); }
.category-row { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 12px; padding: 7px 10px; align-items: center; }
.category-heading { display: flex; align-items: center; gap: 8px; min-width: 0; }
.category-heading > :not(.category-name) { flex-shrink: 0; }
.category-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; font-weight: 600; cursor: pointer; }
.category-destination { min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.category-name-errors { margin: 8px 0; padding: 12px; border: 1px solid #d03050; border-radius: 6px; overflow-wrap: anywhere; }
.category-name-errors :deep(.n-button) { height: auto; max-width: 100%; }
.category-name-errors :deep(.n-button__content) { white-space: normal; overflow-wrap: anywhere; text-align: left; }
.draft-name { display: flex; gap: 6px; }
.product-list { display: flex; flex-direction: column; gap: 5px; padding: 2px 10px 8px 38px; }
.product-item { display: flex; align-items: center; gap: 8px; }
.product-item > .n-checkbox { flex: 1; min-width: 0; }
.mapping-actions { display: flex; align-items: center; gap: 8px; }
.mapping-actions > :first-child { flex: 1; min-width: 0; }
.pricing-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 6px 16px; padding: 8px 10px; border: 1px solid var(--n-border-color); border-radius: 8px; }
.pricing-default { grid-column: 1 / -1; display: flex; flex-wrap: wrap; align-items: center; gap: 2px 10px; }
@media (max-width: 640px) {
  .import-toolbar { flex-wrap: wrap; gap: 6px; }
  .import-toolbar > :first-child { flex-basis: 100%; }
  .category-row { grid-template-columns: minmax(0, 1fr); gap: 10px; }
  .mapping-actions { flex-wrap: wrap; }
  .mapping-actions > :first-child { flex-basis: 100%; }
  .pricing-grid { grid-template-columns: minmax(0, 1fr); }
  .category-heading { gap: 5px; }
}
@media (max-width: 640px), (max-height: 600px) {
  .import-body { height: auto; max-height: calc(100dvh - 180px); overflow: auto; }
  .category-list { flex: 0 0 auto; height: 48dvh; min-height: 180px; }
}
</style>

<style scoped>
.selection-toolbar { position: sticky; top: 0; z-index: 1; }
.classification-panel { display: grid; gap: 8px; max-height: 48vh; overflow: auto; }
.rule-row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.rule-row > .n-input, .rule-row > .n-tree-select { width: 180px; flex: 1 1 150px; }
.rule-preview { max-height: 200px; overflow: auto; }
.rule-preview-row { display: grid; grid-template-columns: minmax(0,1fr) 240px; gap: 8px; padding: 5px 0; }
@media(max-width: 600px) { .rule-preview-row { grid-template-columns: 1fr; } }
</style>
