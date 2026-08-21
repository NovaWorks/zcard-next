<script setup lang="ts">
import { ref, computed, onMounted, watch, h } from "vue";
import { NButton, NTag, NPopconfirm, NAlert } from "naive-ui";
import { checkAuth } from "@/directives";
import type { DataTableColumns } from "naive-ui";
import {
  fetchProducts,
  fetchCards,
  fetchCategories,
  importPreview,
  importConfirm,
  fetchImports,
  cancelImport,
  toggleCard,
  exportCards,
  fetchPremiumCards,
} from "@/service/api";
import TablePager from "@/components/common/table-pager.vue";
import { formatMoney } from "@/utils/money";

defineOptions({ name: "InventoryManagement" });

/** 导入批次状态 → 中文 */
function importStatusText(s?: string) {
  const map: Record<string, string> = {
    pending: "待处理",
    processing: "处理中",
    done: "已完成",
    failed: "失败",
    canceled: "已撤销",
  };
  return (s && map[s]) || s || "-";
}

const loading = ref(false);
const batchLoading = ref(false);
const importing = ref(false);
const showImport = ref(false);
const selectedProduct = ref<number | null>(null);
const importProductId = ref<number | null>(null);
const importContent = ref("");
const previewResult = ref<any>(null);

const products = ref<any[]>([]);
const categories = ref<any[]>([]);
// 分类联动选择（大厂导入流程：先筛分类 → 再选商品，商品下拉可模糊搜索）
const filterCatId = ref<number | null>(null); // 顶部筛选
const importCatId = ref<number | null>(null); // 导入弹窗
const cards = ref<any[]>([]);
const batches = ref<any[]>([]);
const cardTotal = ref(0);
const cardPage = ref(1);
const pageSize = ref(20);

// 分类树扁平（缩进展示层级；0 = 全部）
const categoryOptions = computed(() => {
  const map = new Map<number, any>();
  for (const c of categories.value) map.set(c.id, { ...c, depth: 0, children: [] });
  const roots: any[] = [];
  for (const node of map.values()) {
    const parent = map.get(node.parent_id);
    if (parent) {
      node.depth = parent.depth + 1;
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  const out: { label: string; value: number }[] = [{ label: "全部分类", value: 0 }];
  const walk = (nodes: any[]) => {
    for (const n of nodes) {
      out.push({ label: `${"　".repeat(n.depth)}${n.name}`, value: n.id });
      walk(n.children);
    }
  };
  walk(roots);
  return out;
});

// 商品选项（label 带售价区分同名商品；分类联动过滤）
function productOpts(catId: number | null) {
  return products.value
    .filter((p) => !catId || p.category_id === catId)
    .map((p) => ({ label: `${p.name}（${formatMoney(p.price_cents)}）`, value: p.id }));
}
const productOptions = computed(() => productOpts(filterCatId.value));
const importProductOptions = computed(() => productOpts(importCatId.value));

// 顶部/导入分类变化：当前商品不在新分类下则清空
function onFilterCatChange() {
  if (
    selectedProduct.value &&
    !productOptions.value.some((o) => o.value === selectedProduct.value)
  ) {
    selectedProduct.value = null;
    cards.value = [];
    cardTotal.value = 0;
  }
}
function onImportCatChange() {
  if (
    importProductId.value &&
    !importProductOptions.value.some((o) => o.value === importProductId.value)
  ) {
    importProductId.value = null;
  }
}

const cardColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "商品ID", key: "product_id", width: 80 },
  {
    title: "内容",
    key: "masked_content",
    minWidth: 120,
    render: (row) => row.masked_content || "****",
  },
  {
    title: "状态",
    key: "status",
    width: 80,
    render: (row) =>
      h(
        NTag,
        {
          type:
            row.status === "available" ? "success" : row.status === "used" ? "default" : "warning",
          size: "small",
        },
        {
          default: () =>
            row.status === "available"
              ? "可用"
              : row.status === "used"
                ? "已售"
                : row.status === "reserved"
                  ? "锁定"
                  : "禁用",
        },
      ),
  },
  { title: "备注", key: "note", width: 120 },
  {
    title: "创建时间",
    key: "created_at",
    width: 160,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
  {
    title: "操作",
    key: "actions",
    width: 90,
    render: (row) => {
      if (!checkAuth("inventory:write")) return null;
      if (row.status !== "available" && row.status !== "disabled") return null;
      const disabling = row.status === "available";
      return h(
        NPopconfirm,
        { onPositiveClick: () => handleToggleCard(row, disabling) },
        {
          trigger: () =>
            h(NButton, { size: "small", type: disabling ? "warning" : "success", quaternary: true }, { default: () => (disabling ? "禁用" : "启用") }),
          default: () => (disabling ? "禁用后该卡不可售，确定？" : "恢复该卡为可售，确定？"),
        },
      );
    },
  },
];

const batchColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "商品ID", key: "product_id", width: 80 },
  { title: "总数", key: "total", width: 80 },
  { title: "已导入", key: "imported", width: 80 },
  { title: "跳过", key: "skipped", width: 80 },
  {
    title: "状态",
    key: "status",
    width: 90,
    render: (row) =>
      h(
        NTag,
        {
          type:
            row.status === "done"
              ? "success"
              : row.status === "processing"
                ? "info"
                : row.status === "failed"
                  ? "error"
                  : "default",
          size: "small",
        },
        {
          default: () => importStatusText(row.status),
        },
      ),
  },
  {
    title: "操作",
    key: "actions",
    width: 80,
    render: (row) =>
      row.status === "done" && checkAuth("inventory:import")
        ? h(
            NPopconfirm,
            { onPositiveClick: () => handleCancel(row.id) },
            {
              trigger: () =>
                h(NButton, { size: "small", type: "warning" }, { default: () => "撤销" }),
              default: () => "撤销将删除本批可用卡密",
            },
          )
        : null,
  },
];

// ── 卡密禁用/启用（inventory:write 超管专属）──
async function handleToggleCard(row: any, disabling: boolean) {
  const { error } = await toggleCard(row.id, !disabling);
  if (!error) {
    window.$message?.success(disabling ? "已禁用" : "已启用");
    loadCards();
  }
}

// ── 导出（card:export 超管专属；CSV 行由后端 CsvSafe 处理）──
const exporting = ref(false);
async function handleExport() {
  if (!selectedProduct.value) {
    window.$message?.warning("请先选择商品");
    return;
  }
  exporting.value = true;
  try {
    const { data, error } = await exportCards(selectedProduct.value);
    if (!error && data?.lines?.length) {
      const blob = new Blob([(data.lines as string[]).join("\n")], { type: "text/csv;charset=utf-8" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `cards-product-${selectedProduct.value}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      window.$message?.success(`已导出 ${data.lines.length} 行`);
    } else if (!error) {
      window.$message?.warning("该商品没有可导出的卡密");
    }
  } finally {
    exporting.value = false;
  }
}

// ── 靓号库（card:premium 超管专属）──
const premiumCards = ref<any[]>([]);
const premiumTotal = ref(0);
const premiumLoading = ref(false);
const premiumColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "商品ID", key: "product_id", width: 80 },
  { title: "号码", key: "masked_content", minWidth: 120, render: (row) => row.masked_content || "****" },
  { title: "预选加价", key: "draft_premium", width: 100, render: (row) => formatMoney(row.draft_premium || 0) },
  { title: "预选成本", key: "draft_cost", width: 100, render: (row) => formatMoney(row.draft_cost || 0) },
  { title: "靓号价", key: "price_cents", width: 100, render: (row) => (row.price_cents ? formatMoney(row.price_cents) : "商品价+加价") },
  { title: "状态", key: "status", width: 80, render: (row) => row.status },
];
async function loadPremium() {
  if (!selectedProduct.value) return;
  premiumLoading.value = true;
  try {
    const { data, error } = await fetchPremiumCards(selectedProduct.value);
    if (!error && data) {
      premiumCards.value = (data as any).cards || [];
      premiumTotal.value = (data as any).total || 0;
    }
  } finally {
    premiumLoading.value = false;
  }
}

async function loadCards() {
  if (!selectedProduct.value) {
    cards.value = [];
    cardTotal.value = 0;
    return;
  }
  loading.value = true;
  try {
    const { data, error } = await fetchCards({
      product_id: selectedProduct.value,
      page: cardPage.value,
      page_size: pageSize.value,
    });
    if (!error && data) {
      cards.value = (data as any).cards || [];
      cardTotal.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

function resetCards() {
  cardPage.value = 1;
  loadCards();
}

async function loadProducts() {
  const { data, error } = await fetchProducts({ page: 1, page_size: 100 });
  if (!error && data) {
    products.value = (data as any).products || [];
  }
}

async function loadBatches() {
  batchLoading.value = true;
  try {
    const { data, error } = await fetchImports();
    if (!error && data) {
      batches.value = (data as any).batches || [];
    }
  } finally {
    batchLoading.value = false;
  }
}

// 自动预览：产品/内容变化后 500ms 防抖触发（输入即出统计，无需手动点预览）
let previewTimer: ReturnType<typeof setTimeout> | null = null;
watch([importProductId, importContent], () => {
  previewResult.value = null; // 旧预览随内容变化失效
  if (previewTimer) clearTimeout(previewTimer);
  previewTimer = setTimeout(() => {
    if (importProductId.value && importContent.value.trim()) handlePreview();
  }, 500);
});

// 预览统计（proto3 零值不输出——undefined 兜底 0，杜绝 NaN）
const previewDup = () => (previewResult.value?.dup_in_file || 0) + (previewResult.value?.dup_in_db || 0);
const previewImportable = () =>
  (previewResult.value?.total || 0) - previewDup();

async function handlePreview() {
  if (!importProductId.value || !importContent.value.trim()) return;
  const lines = importContent.value
    .trim()
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const { data, error } = await importPreview({
    product_id: importProductId.value,
    lines,
  });
  if (!error && data) {
    previewResult.value = data;
  }
}

async function handleImport() {
  if (!importProductId.value || !importContent.value.trim()) return;
  importing.value = true;
  try {
    const lines = importContent.value
      .trim()
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean);
    const { data, error } = await importConfirm({
      product_id: importProductId.value,
      lines,
    });
    if (!error) {
      const imported = (data as any)?.imported || 0;
      const skipped = (data as any)?.skipped || 0;
      const failed = (data as any)?.failed || 0;
      window.$message?.success(
        `导入完成：新增 ${imported} 条` +
          (skipped > 0 ? `，重复跳过 ${skipped} 条` : "") +
          (failed > 0 ? `，失败 ${failed} 条` : ""),
      );
      showImport.value = false;
      importContent.value = "";
      previewResult.value = null;
      loadBatches();
    }
  } finally {
    importing.value = false;
  }
}

async function handleCancel(id: number) {
  const { error } = await cancelImport(id);
  if (!error) {
    window.$message?.success("撤销成功");
    loadBatches();
  }
}

async function loadCategories() {
  const { data, error } = await fetchCategories();
  if (!error && data) categories.value = (data as any).categories || [];
}

onMounted(() => {
  loadProducts();
  loadCategories();
  loadBatches();
});
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="卡密库存" class="flex-1">
      <div class="mb-16px flex items-center gap-12px">
        <NButton v-auth="'inventory:import'" type="primary" @click="showImport = true">导入卡密</NButton>
        <NButton v-auth="'card:export'" :loading="exporting" @click="handleExport">导出</NButton>
        <NSelect
          v-model:value="filterCatId"
          :options="categoryOptions"
          placeholder="全部分类"
          class="w-160px"
          @update:value="onFilterCatChange"
        />
        <NSelect
          v-model:value="selectedProduct"
          :options="productOptions"
          placeholder="搜索/选择商品"
          class="w-280px"
          filterable
          clearable
          :consistent-menu-width="false"
          @update:value="resetCards"
        />
      </div>

      <NTabs type="line">
        <NTabPane name="cards" tab="卡密列表">
          <NEmpty
            v-if="!selectedProduct"
            size="small"
            class="my-24px"
            description="先在上方选择商品查看卡密"
          />
          <template v-else>
            <NDataTable :columns="cardColumns" :data="cards" :loading="loading" />
            <TablePager
              v-model:page="cardPage"
              v-model:page-size="pageSize"
              :total="cardTotal"
              @change="loadCards"
            />
          </template>
        </NTabPane>
        <NTabPane name="batches" tab="导入批次">
          <NDataTable :columns="batchColumns" :data="batches" :loading="batchLoading" />
        </NTabPane>
        <NTabPane name="premium" tab="靓号库" @update:appears="loadPremium">
          <NEmpty
            v-if="!selectedProduct"
            size="small"
            class="my-24px"
            description="先在上方选择商品查看靓号卡"
          />
          <template v-else>
            <NDataTable :columns="premiumColumns" :data="premiumCards" :loading="premiumLoading" />
            <div v-if="premiumTotal" class="mt-8px text-12px text-gray-400">共 {{ premiumTotal }} 张</div>
          </template>
        </NTabPane>
      </NTabs>
    </NCard>

    <!-- 导入弹窗 -->
    <NModal v-model:show="showImport" preset="dialog" title="导入卡密" style="width: 640px">
      <NForm label-placement="left" label-width="80">
        <NFormItem label="商品">
          <div class="flex w-full items-center gap-8px">
            <NSelect
              v-model:value="importCatId"
              :options="categoryOptions"
              placeholder="全部分类"
              class="w-140px shrink-0"
              @update:value="onImportCatChange"
            />
            <NSelect
              v-model:value="importProductId"
              :options="importProductOptions"
              placeholder="输入名称模糊搜索 / 选择商品"
              class="flex-1"
              filterable
              clearable
              :consistent-menu-width="false"
            />
          </div>
        </NFormItem>
        <NFormItem label="卡密内容">
          <NInput
            v-model:value="importContent"
            type="textarea"
            placeholder="每行一条卡密；靓号格式：卡密---价格---备注"
            :rows="10"
          />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showImport = false">取消</NButton>
        <NButton @click="handlePreview">预览</NButton>
        <NButton
          type="primary"
          :loading="importing"
          :disabled="!importProductId || !importContent.trim()"
          @click="handleImport"
        >
          确认导入{{ previewResult ? "（可导入 " + previewImportable() + " 条）" : "" }}
        </NButton>
      </template>
      <NAlert v-if="previewResult" type="info" class="mt-12px">
        总计 {{ previewResult.total || 0 }} 条 | 重复 {{ previewDup() }} 条 | 可导入
        {{ previewImportable() }} 条
      </NAlert>
    </NModal>
  </div>
</template>
