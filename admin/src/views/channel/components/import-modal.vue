<script setup lang="ts">
// 上游商品导入弹窗（ D）：预览分类树 → 勾选商品 → 定价策略（四模式）→
// 类目映射（上游分类 → 本地分类）→ 存为连接默认。已导入商品标注（重导 = 更新）。
import { computed, reactive, ref, watch } from "vue";
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
    is_active: boolean;
    stock: number;
    already_imported: boolean;
  }[];
}

const props = defineProps<{ show: boolean; connection: any }>();
const emit = defineEmits<{ (e: "update:show", v: boolean): void; (e: "imported"): void }>();

const loading = ref(false);
const previewError = ref("");
let previewRequest = 0;
const importing = ref(false);
const categories = ref<PreviewCategory[]>([]);
const localCategories = ref<any[]>([]);
const checked = ref<string[]>([]);
const expandedCats = ref<Set<string>>(new Set());

const pricing = reactive({
  mode: "percent",
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
const visibleCategories = computed(() => {
  const key = keyword.value.trim().toLowerCase();
  return categories.value.filter(cat => !key || cat.name.toLowerCase().includes(key) || cat.products.some(p => p.name.toLowerCase().includes(key)));
});
const draftCount = computed(() => new Set(selectedCategories.value.filter(cat => drafts[cat.code]).map(cat => JSON.stringify([drafts[cat.code].parent_id || 0, drafts[cat.code].name.trim()]))).size);
const mappedCount = computed(() => selectedCategories.value.filter(cat => !drafts[cat.code] && Number(categoryMapDraft[cat.code]) > 0).length);
function setMapping(code: string, value: number | null) {
  delete drafts[code];
  categoryMapDraft[code] = value;
}
function applyBatchCategory() {
  if (batchCategory.value === null) return;
  for (const cat of selectedCategories.value) setMapping(cat.code, batchCategory.value);
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
    }
  },
  { immediate: true },
);

async function loadPreview() {
  const requestId = ++previewRequest;
  const connection = props.connection;
  loading.value = true;
  previewError.value = "";
  categories.value = [];
  checked.value = [];
  expandedCats.value = new Set();
  for (const key of Object.keys(categoryMapDraft)) delete categoryMapDraft[key];
  for (const key of Object.keys(drafts)) delete drafts[key];
  keyword.value = "";
  batchCategory.value = null;
  resultMessage.value = "";
  Object.assign(pricing, { mode: "percent", markupPercent: 10, markupAmountYuan: 1, saveDefault: false });
  try {
    const { data, error } = await previewSupplyProducts(connection.id);
    if (requestId !== previewRequest) return;
    if (error) {
      const err = error as any;
      previewError.value = ["ECONNABORTED", "ETIMEDOUT"].includes(err.code)
        ? "加载上游商品超时，请重试；若持续失败，请检查上游响应速度或测试连接。"
        : err.response?.data?.message || err.message || "加载上游商品失败，请重试或测试连接。";
      return;
    }
    if (!error && data) {
      categories.value = (data as any).categories || [];
      // 连接默认定价回填
      try {
        const def = JSON.parse(connection.settings || "{}").import_pricing;
        if (def) {
          pricing.mode = def.mode || "percent";
          pricing.markupPercent = Number(def.markup_percent ?? 10);
          pricing.markupAmountYuan = Number(def.markup_amount_cents ?? 0) / 100;
        }
      } catch {
        /* 无默认 */
      }
      // 已持久化的类目映射回填（保存后全量同步沿用同一映射）
      try {
        const saved = JSON.parse(connection.settings || "{}").category_map;
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

async function loadLocalCategories() {
  const requestId = previewRequest;
  const { data, error } = await fetchCategories();
  if (requestId !== previewRequest) return;
  if (!error && data) localCategories.value = (data as any).categories || [];
}

function toggleCat(cat: PreviewCategory, on: boolean) {
  const codes = cat.products.map((p) => p.code);
  const set = new Set(checked.value);
  codes.forEach((c) => (on ? set.add(c) : set.delete(c)));
  checked.value = [...set];
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
  if (!checked.value.length) {
    window.$message?.warning("请先勾选要导入的商品");
    return;
  }
  if (importing.value) return;
  if (selectedCategories.value.some(cat => drafts[cat.code] && !drafts[cat.code].name.trim())) {
    window.$message?.warning("请填写待新建分类名称");
    return;
  }
  importing.value = true;
  try {
    const payload: Record<string, unknown> = {
      codes: checked.value,
      pricing_mode: pricing.mode,
      save_default: pricing.saveDefault,
      category_map: Object.fromEntries(selectedCategories.value
        .filter(cat => !drafts[cat.code] && categoryMapDraft[cat.code] != null)
        .map(cat => [cat.code, categoryMapDraft[cat.code]])),
      category_drafts: selectedCategories.value.filter(cat => drafts[cat.code]).map(cat => ({
        upstream_code: cat.code, name: drafts[cat.code].name.trim(), parent_id: drafts[cat.code].parent_id || 0,
      })),
    };
    if (pricing.mode === "percent" && pricing.markupPercent > 0) payload.markup_percent = pricing.markupPercent;
    if (pricing.mode === "fixed") payload.markup_amount_cents = yuanToFen(pricing.markupAmountYuan);
    const { data, error } = await importSupplyProducts(props.connection.id, payload as any);
    if (!error && data) {
      const d = data as any;
      emit("imported");
      if (Number(d.failed) > 0) {
        resultMessage.value = `分类映射已保存。新建商品 ${d.imported ?? 0}，更新 ${d.updated ?? 0}，失败 ${d.failed}。${d.error_context || ""} 可重试失败商品。`;
        for (const key of Object.keys(drafts)) delete drafts[key];
        for (const [key, value] of Object.entries(d.category_map || {})) categoryMapDraft[key] = Number(value);
        if (d.failed_codes?.length) checked.value = d.failed_codes;
        await loadLocalCategories();
        window.$message?.warning("部分商品导入失败，请查看结果并重试");
      } else {
        window.$message?.success(`导入完成：新建 ${d.imported ?? 0}，更新 ${d.updated ?? 0}`);
        emit("update:show", false);
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
    <NSpin :show="loading || importing">
      <div class="import-body" :inert="importing || undefined">
        <NAlert v-if="loading" type="info" :bordered="false">正在加载上游商品目录，商品较多时请稍候…</NAlert>
        <NAlert v-else-if="previewError" type="error" :bordered="false">
          {{ previewError }} <NButton size="small" @click="loadPreview">重新加载</NButton>
        </NAlert>
        <NAlert v-else-if="!categories.length" type="warning" :bordered="false">上游商品目录为空，请确认对接账号有可用商品。</NAlert>
        <NAlert v-if="resultMessage" type="warning" :bordered="false">{{ resultMessage }}</NAlert>
        <div class="import-toolbar">
          <NInput v-model:value="keyword" clearable placeholder="搜索上游分类或商品名称" aria-label="搜索上游分类或商品名称" />
          <NButton size="small" @click="toggleAllExpand">{{ allExpanded ? '全部收起' : '全部展开' }}</NButton>
        </div>
        <div class="text-12px text-gray-400">分类默认折叠；勾选整类包含该分类全部商品，搜索不会取消已选商品。</div>
        <div class="category-list">
          <div v-for="cat in visibleCategories" :key="cat.code" class="category-item">
            <div class="category-row">
              <div class="category-heading">
                <NButton text :aria-label="`${expandedCats.has(cat.code) ? '收起' : '展开'}${cat.name}`" :aria-expanded="expandedCats.has(cat.code)" @click="toggleExpand(cat.code)">{{ expandedCats.has(cat.code) ? '▼' : '▶' }}</NButton>
                <NCheckbox :checked="counts.get(cat.code) === cat.products.length && cat.products.length > 0"
                  :indeterminate="(counts.get(cat.code) || 0) > 0 && (counts.get(cat.code) || 0) < cat.products.length"
                  :aria-label="`选择${cat.name}全部商品`" @update:checked="(v: boolean) => toggleCat(cat, v)" />
                <button type="button" class="category-name" :title="cat.name" @click="toggleExpand(cat.code)">{{ cat.name }}</button>
                <NTag size="tiny" :bordered="false">{{ cat.products.length }} 件</NTag>
                <NTag v-if="counts.get(cat.code)" size="tiny" type="primary" :bordered="false">已选 {{ counts.get(cat.code) }}</NTag>
              </div>
              <div class="category-destination">
                <template v-if="drafts[cat.code]">
                  <div class="draft-name"><NTag size="small" type="warning">待新建</NTag><NInput v-model:value="drafts[cat.code].name" size="small" maxlength="100" placeholder="新分类名称" :aria-label="`${cat.name}的新分类名称`" /></div>
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
                <NCheckbox v-for="p in cat.products" :key="p.code" :value="p.code">
                  <span class="break-all" :class="{ 'text-gray-400': !p.is_active }">{{ p.name }}</span>
                  <span class="ml-4px text-12px text-gray-400">{{ formatMoney(p.price_cents) }} <template v-if="p.stock >= 0">· 库存 {{ p.stock }}</template></span>
                  <NTag v-if="p.already_imported" size="tiny" type="info" :bordered="false" class="ml-4px">已导入</NTag>
                  <NTag v-if="!p.is_active" size="tiny" type="warning" :bordered="false" class="ml-4px">已下架</NTag>
                </NCheckbox>
              </div>
            </NCheckboxGroup>
          </div>
          <div v-if="categories.length && !visibleCategories.length" class="p-16px text-gray-400">没有匹配的分类或商品</div>
        </div>
        <div class="mapping-actions">
          <NTreeSelect v-model:value="batchCategory" :options="[{ key: 0, label: '不归入分类' }, ...localCategoryOptions]" clearable filterable show-path size="small" placeholder="批量指定本地分类" aria-label="批量指定本地分类" />
          <NButton size="small" :disabled="!selectedCategories.length || batchCategory === null" @click="applyBatchCategory">应用到已选分类</NButton>
          <NButton v-auth="'catalog:category_write'" size="small" type="primary" secondary :disabled="!selectedCategories.length" @click="generateDrafts">生成映射草稿</NButton>
        </div>
        <div class="text-12px text-gray-400">只处理所选商品涉及的分类；草稿保存前不会出现在商城。保存后的映射也用于后续全量同步及该上游分类的其他已导入商品。</div>
          <NForm label-placement="top" size="small" class="pricing-grid">
            <NFormItem label="定价策略">
              <NSelect
                v-model:value="pricing.mode"
                :options="[
                  { label: '按加价比例（%）', value: 'percent' },
                  { label: '加固定金额（元）', value: 'fixed' },
                  { label: '原价导入（不加价）', value: 'equal' },
                  { label: '待定价（导入后不上架）', value: 'pending' },
                ]"
              />
            </NFormItem>
            <NFormItem v-if="pricing.mode === 'percent'" label="加价比例（%）">
              <NInputNumber v-model:value="pricing.markupPercent" :min="0" class="w-full" placeholder="10 = 加价 10%" />
            </NFormItem>
            <NFormItem v-if="pricing.mode === 'fixed'" label="加价金额（元）">
              <NInputNumber v-model:value="pricing.markupAmountYuan" :min="0.01" :precision="2" class="w-full" />
            </NFormItem>
            <NFormItem class="pricing-default">
              <div class="flex w-full flex-col gap-2px">
                <NCheckbox v-model:checked="pricing.saveDefault">存为该渠道默认</NCheckbox>
                <span class="text-12px text-gray-400">
                  勾选后本次加价规则将保存为渠道默认，下次打开本弹窗自动回填（只影响这里的勾选导入，不改渠道本身的加价设置）
                </span>
              </div>
            </NFormItem>
          </NForm>
      </div>
    </NSpin>
    <template #footer>
      <NSpace justify="space-between" align="center">
        <span class="text-12px">已选 {{ checked.length }} 件 · 涉及 {{ selectedCategories.length }} 类 · 已指定 {{ mappedCount }} 类 · 待新建 {{ draftCount }} 类</span>
        <NSpace>
          <NButton size="small" :disabled="importing" @click="emit('update:show', false)">取消</NButton>
          <NButton size="small" type="primary" :loading="importing" :disabled="loading || !!previewError || !checked.length" @click="submit">{{ resultMessage ? '重试失败商品' : '保存并导入' }}</NButton>
        </NSpace>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.import-body { max-height: 70vh; overflow: auto; display: flex; flex-direction: column; gap: 12px; }
.import-toolbar { display: flex; align-items: center; gap: 12px; }
.category-list { max-height: 42vh; min-height: 160px; overflow: auto; border: 1px solid var(--n-border-color); border-radius: 8px; }
.category-item + .category-item { border-top: 1px solid var(--n-border-color); }
.category-row { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 16px; padding: 12px; align-items: center; }
.category-heading { display: flex; align-items: center; gap: 8px; min-width: 0; }
.category-heading > :not(.category-name) { flex-shrink: 0; }
.category-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; font-weight: 600; cursor: pointer; }
.category-destination { min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.draft-name { display: flex; gap: 6px; }
.product-list { display: flex; flex-direction: column; gap: 8px; padding: 4px 12px 12px 40px; }
.mapping-actions { display: flex; align-items: center; gap: 8px; }
.mapping-actions > :first-child { flex: 1; min-width: 0; }
.pricing-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 24px; padding: 12px; border: 1px solid var(--n-border-color); border-radius: 8px; }
.pricing-default { grid-column: 1 / -1; }
@media (max-width: 640px) {
  .category-row { grid-template-columns: minmax(0, 1fr); gap: 10px; }
  .mapping-actions { flex-wrap: wrap; }
  .mapping-actions > :first-child { flex-basis: 100%; }
  .pricing-grid { grid-template-columns: minmax(0, 1fr); }
  .category-heading { gap: 5px; }
}
</style>
