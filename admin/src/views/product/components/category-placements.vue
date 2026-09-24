<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import {
  NAlert,
  NButton,
  NCheckbox,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NInput,
  NPagination,
  NSpin,
  NTag,
} from "naive-ui";
import {
  fetchCategoryPlacements,
  saveCategoryPlacements,
  fetchProducts,
  type CategoryPlacementRow,
  type PlacementProduct,
} from "@/service/api/catalog";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";

const props = defineProps<{
  show: boolean;
  categoryId: number;
  categoryName: string;
  categories?: { id: number; name: string; parent_id?: number }[];
}>();
const emit = defineEmits<{ (e: "update:show", v: boolean): void; (e: "saved"): void }>();
const rows = ref<CategoryPlacementRow[]>([]);
const candidates = ref<PlacementProduct[]>([]);
const version = ref(0),
  initial = ref(""),
  loading = ref(false),
  searching = ref(false),
  saving = ref(false);
const keyword = ref(""),
  page = ref(1),
  total = ref(0),
  errorText = ref(""),
  searchError = ref("");
const loaded = ref(false),
  saved = ref(false),
  dragIndex = ref<number | null>(null);
const canWrite = computed(() => checkAuth("catalog:category_write"));
const payload = computed(() => rows.value.map((r) => r.placement));
const dirty = computed(() => loaded.value && JSON.stringify(payload.value) !== initial.value);
const selected = computed(() => new Set(rows.value.map((r) => r.placement.product_id)));
const pinnedCount = computed(() => rows.value.filter((r) => r.placement.is_pinned).length);
let generation = 0,
  searchSequence = 0;
function categoryPath(id?: number) {
  const parts: string[] = [];
  const seen = new Set<number>();
  let node = props.categories?.find((c) => c.id === id);
  while (node && !seen.has(node.id)) {
    parts.unshift(node.name);
    seen.add(node.id);
    node = props.categories?.find((c) => c.id === node!.parent_id);
  }
  return parts.join(" / ") || "未分类";
}
function accept(data: { version: number; items: CategoryPlacementRow[] }) {
  version.value = Number(data.version || 0);
  rows.value = (data.items || []).map((row) => ({
    ...row,
    placement: {
      product_id: row.placement.product_id,
      is_pinned: !!row.placement.is_pinned,
      is_recommended: !!row.placement.is_recommended,
      position: row.placement.position || 0,
    },
  }));
  initial.value = JSON.stringify(payload.value);
  loaded.value = true;
}
async function load() {
  const seq = ++generation;
  loading.value = true;
  errorText.value = "";
  try {
    const { data, error } = await fetchCategoryPlacements(props.categoryId);
    if (seq !== generation || !props.show) return;
    if (error || !data) {
      errorText.value = "无法读取分类设置，请重试。";
      return;
    }
    accept(data);
  } finally {
    if (seq === generation) loading.value = false;
  }
}
async function search(reset = true) {
  if (reset) page.value = 1;
  const seq = ++searchSequence;
  searching.value = true;
  searchError.value = "";
  try {
    const { data, error } = await fetchProducts({
      category_id: props.categoryId,
      keyword: keyword.value || undefined,
      options_only: true,
      page: page.value,
      page_size: 8,
    });
    if (seq !== searchSequence || !props.show) return;
    if (error || !data) {
      searchError.value = "商品加载失败，请重试。";
      return;
    }
    candidates.value = (data as any).products || [];
    total.value = (data as any).total || 0;
  } finally {
    if (seq === searchSequence) searching.value = false;
  }
}
watch(
  () => [props.show, props.categoryId],
  () => {
    generation++;
    searchSequence++;
    if (!props.show || !props.categoryId) return;
    loaded.value = false;
    rows.value = [];
    candidates.value = [];
    keyword.value = "";
    saved.value = false;
    initial.value = "";
    load();
    search();
  },
  { immediate: true },
);
function add(p: PlacementProduct) {
  if (!loaded.value || saving.value || p.is_locked || selected.value.has(p.id) || !canWrite.value)
    return;
  const position =
    Math.max(
      -1,
      ...rows.value.filter((r) => r.placement.is_pinned).map((r) => r.placement.position),
    ) + 1;
  rows.value.push({
    product: p,
    placement: { product_id: p.id, is_pinned: true, is_recommended: true, position },
    unavailable_reason: p.status !== 1 ? "商品未上架，恢复上架后展示" : "",
  });
  rows.value.sort(
    (a, b) =>
      Number(b.placement.is_pinned) - Number(a.placement.is_pinned) ||
      a.placement.position - b.placement.position,
  );
  saved.value = false;
}
function remove(index: number) {
  if (rows.value[index].product.is_locked || saving.value) return;
  rows.value.splice(index, 1);
  saved.value = false;
}
function toggle(index: number, field: "is_pinned" | "is_recommended", value: boolean) {
  const row = rows.value[index];
  if (row.product.is_locked || saving.value) return;
  row.placement[field] = value;
  if (!row.placement.is_pinned && !row.placement.is_recommended) {
    rows.value.splice(index, 1);
    return;
  }
  if (field === "is_pinned" && value)
    row.placement.position =
      Math.max(
        -1,
        ...rows.value
          .filter((r) => r !== row && r.placement.is_pinned)
          .map((r) => r.placement.position),
      ) + 1;
  rows.value.sort(
    (a, b) =>
      Number(b.placement.is_pinned) - Number(a.placement.is_pinned) ||
      a.placement.position - b.placement.position,
  );
  saved.value = false;
}
function move(from: number, to: number) {
  if (saving.value || from === to || to < 0 || to >= pinnedCount.value || !canWrite.value) return;
  const lo = Math.min(from, to),
    hi = Math.max(from, to);
  if (rows.value.slice(lo, hi + 1).some((r) => r.product.is_locked)) {
    window.$message?.info("此调整会改变锁定商品的顺序，请先解锁该商品");
    return;
  }
  // Reuse existing rank slots: unrelated locked rows retain their positions.
  const slots = rows.value
    .slice(lo, hi + 1)
    .map((r) => r.placement.position)
    .sort((a, b) => a - b);
  const [row] = rows.value.splice(from, 1);
  rows.value.splice(to, 0, row);
  rows.value.slice(lo, hi + 1).forEach((r, i) => (r.placement.position = slots[i]));
  saved.value = false;
}
function drop(index: number) {
  if (dragIndex.value !== null) move(dragIndex.value, index);
  dragIndex.value = null;
}
function close() {
  if (saving.value) return;
  if (dirty.value) {
    window.$dialog?.warning({
      title: "放弃未保存的设置？",
      content: "本次调整尚未生效，关闭将放弃这些修改。",
      positiveText: "放弃修改",
      negativeText: "继续编辑",
      onPositiveClick: () => emit("update:show", false),
    });
    return;
  }
  emit("update:show", false);
}
async function save() {
  if (!dirty.value || saving.value || !canWrite.value) return;
  saving.value = true;
  errorText.value = "";
  try {
    const { data, error } = await saveCategoryPlacements(
      props.categoryId,
      version.value,
      payload.value,
    );
    if (error || !data) {
      errorText.value =
        (error as any)?.response?.data?.message ||
        "保存未成功，当前选择和顺序已保留。请检查网络后重试。";
      return;
    }
    accept(data);
    saved.value = true;
    emit("saved");
    window.$message?.success(`已保存「${props.categoryName}」的置顶与推荐`);
  } finally {
    saving.value = false;
  }
}
function reload() {
  if (dirty.value) {
    window.$dialog?.warning({
      title: "重新加载最新设置？",
      content: "重新加载将放弃当前草稿，读取其他管理员保存的最新设置。",
      positiveText: "重新加载",
      negativeText: "保留草稿",
      onPositiveClick: load,
    });
  } else load();
}
onBeforeRouteLeave(() => {
  if (!props.show || (!dirty.value && !saving.value)) return true;
  if (saving.value) return false;
  return new Promise<boolean>((resolve) => {
    window.$dialog?.warning({
      title: "离开并放弃修改？",
      content: "当前分类设置尚未保存。",
      positiveText: "放弃并离开",
      negativeText: "继续编辑",
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
      onMaskClick: () => resolve(false),
    });
  });
});
function beforeUnload(e: BeforeUnloadEvent) {
  if (props.show && (dirty.value || saving.value)) {
    e.preventDefault();
    e.returnValue = "";
  }
}
window.addEventListener("beforeunload", beforeUnload);
onBeforeUnmount(() => {
  generation++;
  searchSequence++;
  window.removeEventListener("beforeunload", beforeUnload);
});
</script>

<template>
  <NDrawer
    :show="show"
    width="min(760px, 100vw)"
    :mask-closable="!saving"
    :close-on-esc="!saving"
    @update:show="!$event && close()"
  >
    <NDrawerContent title="置顶与推荐" :closable="!saving" class="category-placements">
      <div class="placement-context">
        <strong>当前分类：{{ categoryName }}</strong>
        <p>可选择本分类及全部子分类的商品；设置仅在此分类生效。</p>
        <p>置顶只影响商城的综合排序；推荐标签与首页推荐独立。</p>
      </div>
      <NAlert v-if="errorText" type="error" class="mb-12px" role="alert"
        >{{ errorText }} <NButton text @click="reload">重新加载</NButton></NAlert
      >
      <NAlert v-if="saved" type="success" class="mb-12px"
        >设置已保存。未上架或分类隐藏的商品暂不展示。<a
          :href="`/?category_id=${categoryId}&sort=default`"
          target="_blank"
          rel="noopener noreferrer"
          >查看商城效果 ↗</a
        ></NAlert
      >
      <NSpin :show="loading">
        <div class="placement-heading">
          <h3>已设置 {{ rows.length }} 件</h3>
          <span
            >{{ pinnedCount }} 件置顶 ·
            {{ rows.filter((r) => r.placement.is_recommended).length }} 件推荐</span
          >
        </div>
        <NEmpty
          v-if="loaded && !rows.length"
          description="还没有置顶或推荐商品，从下方选择商品即可开始。"
          class="py-20px"
        />
        <div
          v-for="(row, index) in rows"
          :key="row.placement.product_id"
          class="placement-row"
          :class="{ 'placement-row-locked': row.product.is_locked }"
          @dragover.prevent
          @drop.prevent="drop(index)"
        >
          <button
            v-if="row.placement.is_pinned"
            class="drag-handle"
            :draggable="canWrite && !saving && !row.product.is_locked"
            :disabled="!canWrite || saving || row.product.is_locked"
            :aria-label="`拖动调整${row.product.name}顺序，也可使用上移下移按钮`"
            @dragstart="dragIndex = index"
            @dragend="dragIndex = null"
          >
            ⠿
          </button>
          <div class="placement-product">
            <div class="placement-name">
              <NTag v-if="row.placement.is_pinned" size="small">第 {{ index + 1 }} 位</NTag
              ><strong>{{ row.product.name }}</strong
              ><NTag v-if="row.product.is_locked" size="small">已锁定</NTag>
            </div>
            <p>{{ categoryPath(row.product.category_id) }}</p>
            <p v-if="row.unavailable_reason" class="placement-note">{{ row.unavailable_reason }}</p>
            <p v-if="row.product.is_locked">先在商品管理中解锁，才能调整此商品的设置。</p>
            <div class="placement-actions">
              <NCheckbox
                :checked="row.placement.is_pinned"
                :disabled="!canWrite || saving || row.product.is_locked"
                @update:checked="toggle(index, 'is_pinned', $event)"
                >本分类置顶</NCheckbox
              >
              <NCheckbox
                :checked="row.placement.is_recommended"
                :disabled="!canWrite || saving || row.product.is_locked"
                @update:checked="toggle(index, 'is_recommended', $event)"
                >显示推荐标签</NCheckbox
              >
              <template v-if="row.placement.is_pinned">
                <NButton
                  size="small"
                  :disabled="!canWrite || saving || row.product.is_locked || index === 0"
                  @click="move(index, index - 1)"
                  >上移</NButton
                >
                <NButton
                  size="small"
                  :disabled="
                    !canWrite || saving || row.product.is_locked || index === pinnedCount - 1
                  "
                  @click="move(index, index + 1)"
                  >下移</NButton
                >
                <NButton
                  size="small"
                  :disabled="!canWrite || saving || row.product.is_locked || index === 0"
                  @click="move(index, 0)"
                  >移到最前</NButton
                >
              </template>
              <NButton
                text
                :disabled="!canWrite || saving || row.product.is_locked"
                @click="remove(index)"
                >移除设置</NButton
              >
            </div>
          </div>
        </div>
      </NSpin>
      <section class="placement-search" aria-label="选择分类商品">
        <h3>添加商品</h3>
        <div class="placement-search-bar">
          <NInput
            v-model:value="keyword"
            placeholder="搜索本分类及子分类商品"
            clearable
            @keyup.enter="search()"
          /><NButton :loading="searching" @click="search()">搜索</NButton>
        </div>
        <NAlert v-if="searchError" type="error"
          >{{ searchError }}<NButton text @click="search(false)">重试</NButton></NAlert
        >
        <NSpin :show="searching">
          <NEmpty
            v-if="!searching && !searchError && !candidates.length"
            description="没有找到商品，请换个关键词或检查商品所属分类。"
            class="py-20px"
          />
          <div v-for="p in candidates" :key="p.id" class="placement-candidate">
            <img v-if="p.cover" :src="p.cover" alt="" />
            <div v-else class="cover-placeholder" aria-hidden="true">—</div>
            <div class="placement-product">
              <strong>{{ p.name }}</strong>
              <p>{{ categoryPath(p.category_id) }} · {{ formatMoney(p.price_cents || 0) }}</p>
              <p v-if="p.is_locked">已锁定，请先在商品管理中解锁</p>
              <p v-else-if="p.status !== 1">未上架 · 设置后暂不展示</p>
            </div>
            <NButton
              size="small"
              :disabled="!canWrite || !loaded || saving || p.is_locked || selected.has(p.id)"
              @click="add(p)"
              >{{ selected.has(p.id) ? "已添加" : p.is_locked ? "已锁定" : "置顶并推荐" }}</NButton
            >
          </div>
        </NSpin>
        <NPagination
          v-model:page="page"
          :page-size="8"
          :item-count="total"
          simple
          :disabled="searching"
          @update:page="search(false)"
        />
      </section>
      <template #footer
        ><div class="placement-footer">
          <span role="status">{{
            saving
              ? "正在保存…"
              : dirty
                ? "有未保存的修改"
                : loaded
                  ? "设置已同步"
                  : "正在读取设置…"
          }}</span
          ><NButton :disabled="saving" @click="close">关闭</NButton
          ><NButton
            v-if="canWrite"
            type="primary"
            :loading="saving"
            :disabled="!dirty || loading"
            @click="save"
            >保存设置</NButton
          >
        </div></template
      >
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.placement-context {
  padding: 14px 16px;
  border-radius: 8px;
  background: var(--n-color-modal);
  border: 1px solid var(--n-divider-color);
  margin-bottom: 16px;
}
.placement-context p,
.placement-product p {
  margin: 5px 0;
  font-size: 13px;
  opacity: 0.75;
  overflow-wrap: anywhere;
}
.placement-heading,
.placement-name,
.placement-actions,
.placement-search-bar,
.placement-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.placement-heading {
  justify-content: space-between;
}
.placement-heading span,
.placement-footer span {
  font-size: 13px;
  opacity: 0.75;
}
h3 {
  margin: 12px 0;
  font-size: 15px;
}
.placement-row,
.placement-candidate {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 14px 0;
  border-bottom: 1px solid var(--n-divider-color);
}
.placement-row-locked {
  background: var(--n-color-modal);
}
.placement-product {
  flex: 1;
  min-width: 0;
}
.placement-product strong {
  overflow-wrap: anywhere;
}
.placement-actions {
  margin-top: 10px;
}
.placement-note {
  font-weight: 500;
}
.drag-handle {
  border: 0;
  background: transparent;
  color: inherit;
  cursor: grab;
  font-size: 24px;
  min-width: 32px;
  min-height: 40px;
}
.drag-handle:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}
.drag-handle:focus-visible {
  outline: 2px solid currentColor;
}
.placement-search {
  margin-top: 24px;
}
.placement-search-bar {
  flex-wrap: nowrap;
  margin-bottom: 8px;
}
.placement-candidate {
  align-items: center;
}
.placement-candidate img,
.cover-placeholder {
  width: 42px;
  height: 42px;
  object-fit: cover;
  border-radius: 5px;
  flex-shrink: 0;
}
.cover-placeholder {
  display: grid;
  place-items: center;
  background: var(--n-color-modal);
}
.placement-footer {
  width: 100%;
  justify-content: flex-end;
}
.placement-footer span {
  margin-right: auto;
}
.placement-search :deep(.n-pagination) {
  margin-top: 14px;
}
@media (max-width: 600px) {
  .placement-candidate {
    flex-wrap: wrap;
  }
  .placement-candidate > .n-button {
    margin-left: 54px;
    min-height: 44px;
  }
  .placement-actions :deep(.n-button) {
    min-height: 44px;
  }
  .placement-actions :deep(.n-checkbox) {
    min-height: 36px;
  }
  .placement-footer {
    gap: 8px;
  }
  .placement-footer span {
    width: 100%;
  }
  .placement-context {
    padding: 12px;
  }
  .drag-handle {
    min-width: 24px;
  }
}
</style>
