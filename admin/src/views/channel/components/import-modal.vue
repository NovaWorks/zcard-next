<script setup lang="ts">
// 上游商品导入弹窗（P2-10 D）：预览分类树 → 勾选商品 → 定价策略（四模式）→
// 类目映射（上游分类 → 本地分类）→ 存为连接默认。已导入商品标注（重导 = 更新）。
import { computed, reactive, ref, watch } from "vue";
import {
  NAlert, NButton, NCheckbox, NCheckboxGroup, NForm, NFormItem, NInputNumber,
  NModal, NSelect, NSpace, NSpin, NTag,
} from "naive-ui";
import { previewSupplyProducts, importSupplyProducts, createCategory } from "@/service/api";
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
const importing = ref(false);
const categories = ref<PreviewCategory[]>([]);
const localCategories = ref<any[]>([]);
const checked = ref<string[]>([]);

const pricing = reactive({
  mode: "percent",
  markupPercent: 10,
  markupAmountYuan: 1,
  saveDefault: false,
});
const categoryMapDraft = reactive<Record<string, number>>({});

const localCategoryOptions = computed(() =>
  localCategories.value.map((c: any) => ({ label: c.name, value: c.id })),
);

watch(
  () => props.show,
  (v) => {
    if (v && props.connection) {
      loadPreview();
      loadLocalCategories();
    }
  },
);

async function loadPreview() {
  loading.value = true;
  checked.value = [];
  expandedCats.value = new Set();
  try {
    const { data, error } = await previewSupplyProducts(props.connection.id);
    if (!error && data) {
      categories.value = (data as any).categories || [];
      // 连接默认定价回填
      try {
        const def = JSON.parse(props.connection.settings || "{}").import_pricing;
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
        const saved = JSON.parse(props.connection.settings || "{}").category_map;
        if (saved) {
          for (const [k, v] of Object.entries(saved)) {
            if (Number(v) > 0) categoryMapDraft[k] = Number(v);
          }
        }
      } catch {
        /* 无映射 */
      }
    }
  } finally {
    loading.value = false;
  }
}

async function loadLocalCategories() {
  const { data, error } = await fetchCategories();
  if (!error && data) localCategories.value = (data as any).categories || [];
}

// ── 一键创建上游分类：上游有哪些分类就建哪些（同名复用已有分类）；创建后自动填入映射 ──
const creatingCats = ref(false);

async function createUpstreamCategories() {
  if (!categories.value.length) return;
  creatingCats.value = true;
  try {
    let created = 0;
    let reused = 0;
    for (const cat of categories.value) {
      // 已映射的直接跳过
      if (categoryMapDraft[cat.code]) continue;
      // 同名复用（本地分类按名字匹配）
      const exist = localCategories.value.find((c: any) => c.name === cat.name);
      let id = exist?.id;
      if (!id) {
        const { data, error } = await createCategory({ name: cat.name });
        if (error) continue;
        id = (data as any)?.id;
        if (!id) continue;
        created++;
        localCategories.value.push({ id, name: cat.name });
      } else {
        reused++;
      }
      categoryMapDraft[cat.code] = id;
    }
    window.$message?.success(`分类就绪：新建 ${created} 个，复用 ${reused} 个`);
  } finally {
    creatingCats.value = false;
  }
}

function checkedInCat(cat: PreviewCategory): string[] {
  const set = new Set(checked.value);
  return cat.products.filter((p) => set.has(p.code)).map((p) => p.code);
}

function toggleCat(cat: PreviewCategory, on: boolean) {
  const codes = cat.products.map((p) => p.code);
  const set = new Set(checked.value);
  codes.forEach((c) => (on ? set.add(c) : set.delete(c)));
  checked.value = [...set];
}

// ── 分类折叠（默认全收起，点行展开/收起；勾选不受折叠影响）──
const expandedCats = ref<Set<string>>(new Set());

function toggleExpand(code: string) {
  const next = new Set(expandedCats.value);
  if (next.has(code)) next.delete(code);
  else next.add(code);
  expandedCats.value = next;
}

const allExpanded = computed(
  () => categories.value.length > 0 && categories.value.every((c) => expandedCats.value.has(c.code)),
);

function toggleAllExpand() {
  expandedCats.value = new Set(allExpanded.value ? [] : categories.value.map((c) => c.code));
}

async function submit() {
  if (!checked.value.length) {
    window.$message?.warning("请先勾选要导入的商品");
    return;
  }
  importing.value = true;
  try {
    const payload: Record<string, unknown> = {
      codes: checked.value,
      pricing_mode: pricing.mode,
      save_default: pricing.saveDefault,
      category_map: Object.fromEntries(Object.entries(categoryMapDraft).filter(([, v]) => v > 0)),
    };
    if (pricing.mode === "percent" && pricing.markupPercent > 0) payload.markup_percent = pricing.markupPercent;
    if (pricing.mode === "fixed") payload.markup_amount_cents = yuanToFen(pricing.markupAmountYuan);
    const { data, error } = await importSupplyProducts(props.connection.id, payload as any);
    if (!error && data) {
      const d = data as any;
      window.$message?.success(`导入完成：新建 ${d.imported ?? 0}，更新 ${d.updated ?? 0}，失败 ${d.failed ?? 0}`);
      emit("imported");
      emit("update:show", false);
    }
  } finally {
    importing.value = false;
  }
}
</script>

<template>
  <NModal
    :show="props.show"
    preset="card"
    :title="`导入上游商品：${props.connection?.name || ''}`"
    style="width: 920px; max-width: 96vw"
    @update:show="emit('update:show', $event)"
  >
    <NSpin :show="loading">
      <NAlert v-if="!loading && !categories.length" type="warning" :bordered="false">
        上游未返回商品（检查连接凭据或先「测试连接」）
      </NAlert>

      <!-- 左：分类树勾选；右：定价与映射（左列 min-w-0：长分类/商品名截断不撑破弹窗） -->
      <div class="grid grid-cols-[minmax(0,1fr)_310px] gap-16px">
        <div class="max-h-480px min-w-0 overflow-auto pr-8px">
          <div class="sticky top-0 z-1 mb-4px flex items-center justify-between bg-white py-2px dark:bg-[#101014]">
            <span class="text-11px text-gray-400">分类默认折叠 · 点行展开，勾选整类无须展开</span>
            <NButton size="tiny" quaternary @click="toggleAllExpand">
              {{ allExpanded ? "全部收起" : "全部展开" }}
            </NButton>
          </div>
          <div v-for="cat in categories" :key="cat.code" class="mb-4px">
            <div
              class="flex cursor-pointer select-none items-center gap-4px rounded-4px px-4px py-3px hover:bg-gray-100 dark:hover:bg-gray-800"
              @click="toggleExpand(cat.code)"
            >
              <span
                class="w-12px shrink-0 text-10px text-gray-400 transition-transform duration-200"
                :class="{ 'rotate-90': expandedCats.has(cat.code) }"
              >▶</span>
              <span @click.stop>
                <NCheckbox
                  :checked="checkedInCat(cat).length === cat.products.length && cat.products.length > 0"
                  :indeterminate="checkedInCat(cat).length > 0 && checkedInCat(cat).length < cat.products.length"
                  @update:checked="(v: boolean) => toggleCat(cat, v)"
                />
              </span>
              <b class="min-w-0 truncate" :title="cat.name">{{ cat.name }}</b>
              <NTag size="tiny" :bordered="false" class="shrink-0">{{ cat.products.length }} 件</NTag>
              <NTag
                v-if="!expandedCats.has(cat.code) && checkedInCat(cat).length"
                size="tiny"
                type="primary"
                :bordered="false"
                class="shrink-0"
              >
                已选 {{ checkedInCat(cat).length }}
              </NTag>
            </div>
            <NCheckboxGroup v-if="expandedCats.has(cat.code)" v-model:value="checked">
              <div class="grid grid-cols-1 gap-2px py-2px pl-28px">
                <NCheckbox v-for="p in cat.products" :key="p.code" :value="p.code" class="py-1px">
                  <span class="break-all" :class="{ 'text-gray-400': !p.is_active }" :title="p.name">{{ p.name }}</span>
                  <span class="ml-4px text-12px text-gray-400">
                    {{ formatMoney(p.price_cents) }}
                    <template v-if="p.stock >= 0">· 库存 {{ p.stock }}</template>
                  </span>
                  <NTag v-if="p.already_imported" size="tiny" type="info" :bordered="false" class="ml-4px">已导入</NTag>
                  <NTag v-if="!p.is_active" size="tiny" type="warning" :bordered="false" class="ml-4px">已下架</NTag>
                </NCheckbox>
              </div>
            </NCheckboxGroup>
          </div>
        </div>

        <div>
          <NForm label-placement="top" size="small">
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
            <NFormItem>
              <div class="flex w-full flex-col gap-2px">
                <NCheckbox v-model:checked="pricing.saveDefault">存为该渠道默认</NCheckbox>
                <span class="text-12px text-gray-400">
                  勾选后本次加价规则将保存为渠道默认，下次打开本弹窗自动回填（只影响这里的勾选导入，不改渠道本身的加价设置）
                </span>
              </div>
            </NFormItem>
            <NFormItem>
              <template #label>
                <span class="mr-8px">类目映射（上游分类 → 本地分类，保存后全量同步沿用）</span>
                <NButton
                  v-auth="'catalog:category_write'"
                  size="tiny"
                  type="primary"
                  quaternary
                  :loading="creatingCats"
                  @click="createUpstreamCategories"
                >
                  一键创建上游分类
                </NButton>
              </template>
              <div class="flex w-full flex-col gap-6px">
                <div v-for="cat in categories" :key="cat.code" class="flex items-center gap-6px">
                  <span class="w-110px shrink-0 truncate text-12px" :title="cat.name">{{ cat.name }}</span>
                  <NSelect
                    v-model:value="categoryMapDraft[cat.code]"
                    size="small"
                    clearable
                    filterable
                    placement="bottom-start"
                    class="flex-1"
                    placeholder="本地分类（空=不设置）"
                    :options="localCategoryOptions"
                  />
                </div>
              </div>
            </NFormItem>
          </NForm>
        </div>
      </div>
    </NSpin>
    <template #footer>
      <NSpace justify="space-between" align="center">
        <span class="text-12px text-gray-400">已勾选 {{ checked.length }} 件</span>
        <NSpace>
          <NButton size="small" @click="emit('update:show', false)">取消</NButton>
          <NButton size="small" type="primary" :loading="importing" :disabled="!checked.length" @click="submit">
            导入所选
          </NButton>
        </NSpace>
      </NSpace>
    </template>
  </NModal>
</template>
