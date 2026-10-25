<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { NButton, NPopconfirm, NSpace, NTag } from "naive-ui";
import type { DataTableColumns, DataTableRowKey } from "naive-ui";
import { checkAuth } from "@/directives";
import { cardStatusText } from "@/utils/business-status";
import { formatMoney } from "@/utils/money";
import TablePager from "@/components/common/table-pager.vue";
import {
  assignCards, cancelImport, exportCards, fetchCards, fetchCategories,
  fetchImports, fetchInventoryTargets, fetchPremiumCards, fetchProducts,
  importConfirm, importPreview, previewAssignCards, previewCancelImport, toggleCard,
  type InventoryAssignPreview, type InventoryBatch, type InventoryPreview,
  type InventorySkuCount, type InventoryTargets,
} from "@/service/api";

defineOptions({ name: "InventoryManagement" });
const route = useRoute();
const selectedProduct = ref<number | null>(null);
const selectedSku = ref<number | null>(null); // null=全部，0=默认池
const filterCatId = ref<number | null>(null);
const products = ref<any[]>([]), categories = ref<any[]>([]);
const productsLoading = ref(false), productsLoadFailed = ref(false);
const targets = ref<InventoryTargets | null>(null), targetsLoading = ref(false), targetsError = ref("");
const inventoryTab = ref("cards"), cards = ref<any[]>([]), cardTotal = ref(0);
const cardPage = ref(1), pageSize = ref(20), loading = ref(false);
const checkedKeys = ref<DataTableRowKey[]>([]);
const batches = ref<InventoryBatch[]>([]), batchLoading = ref(false);
const batchSku = ref<number | "all" | "legacy">("all");
const premiumCards = ref<any[]>([]), premiumTotal = ref(0), premiumLoading = ref(false);
const exporting = ref(false);
let targetRequest = 0, cardRequest = 0, batchRequest = 0, premiumRequest = 0;
let routeSku: number | null = null;

function safeID(value: unknown, zero = false): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= (zero ? 0 : 1);
}
function queryID(value: unknown, zero = false): number | null {
  if (typeof value !== "string" || !/^\d+$/.test(value)) return null;
  const id = Number(value);
  return safeID(id, zero) ? id : null;
}
function validateTargets(data: InventoryTargets, productId: number): InventoryTargets {
  if (data.product_id !== productId || !Array.isArray(data.targets) || !data.targets.length ||
      data.targets.some(t => !safeID(t.sku_id ?? 0, true)) || !safeID(data.revision ?? 0, true)) throw new Error("规格数据异常，请刷新后重试");
  return { ...data, targets: data.targets.map(t => ({ ...t, sku_id: t.sku_id ?? 0 })) };
}
const categoryOptions = computed(() => {
  const byId = new Map<number, any>();
  for (const c of categories.value) byId.set(c.id, { ...c, children: [] });
  const roots: any[] = [];
  for (const node of byId.values()) {
    const parent = byId.get(node.parent_id);
    if (parent) parent.children.push(node); else roots.push(node);
  }
  const out = [{ label: "全部分类", value: 0 }];
  const walk = (nodes: any[], depth = 0) => {
    for (const node of nodes) { out.push({ label: `${"　".repeat(depth)}${node.name}`, value: node.id }); walk(node.children, depth + 1); }
  };
  walk(roots);
  return out;
});
function productOptions(cat: number | null) {
  return products.value.filter(p => !cat || p.category_id === cat).map(p => ({ label: `${p.name}（${formatMoney(p.price_cents)}）`, value: p.id }));
}
const skuOptions = computed(() => (targets.value?.targets || []).map(t => ({ label: t.sku_name || (t.sku_id ? `规格 #${t.sku_id}` : "商品默认池"), value: t.sku_id! })));
const batchSkuOptions = computed(() => [{ label: "全部导入目标", value: "all" }, { label: "历史未记录", value: "legacy" }, ...skuOptions.value]);
function skuName(row: { sku_id?: number | null; sku_name?: string }) {
  return row.sku_name || (row.sku_id ? `已删除或未知规格 #${row.sku_id}` : "商品默认池");
}
function importSkuName(row: InventoryBatch) {
  return row.sku_id == null ? "历史未记录" : skuName(row);
}
function distribution(rows: InventorySkuCount[] = []) {
  return rows.map(r => `${skuName(r)}：可用 ${r.available || 0} / 预占 ${r.reserved || 0} / 已售 ${r.used || 0} / 禁用 ${r.disabled || 0}`).join("；") || "无剩余卡密记录";
}
function batchStatus(status = "") {
  return ({ pending: "待处理", processing: "处理中", done: "已完成", failed: "失败", canceled: "已撤销" } as Record<string, string>)[status] || status || "-";
}
async function loadTargets() {
  const seq = ++targetRequest, id = selectedProduct.value;
  targets.value = null; targetsError.value = "";
  if (!id) { targetsLoading.value = false; return; }
  targetsLoading.value = true;
  try {
    const { data, error } = await fetchInventoryTargets(id);
    if (seq !== targetRequest) return;
    if (error || !data) throw new Error("规格加载失败，请重试");
    targets.value = validateTargets(data, id);
    if (routeSku !== null && targets.value.targets.some(t => t.sku_id === routeSku)) selectedSku.value = routeSku;
    routeSku = null;
  } catch (err) {
    if (seq === targetRequest) targetsError.value = err instanceof Error ? err.message : "规格加载失败，请重试";
  } finally { if (seq === targetRequest) targetsLoading.value = false; }
}
async function loadCards() {
  const seq = ++cardRequest, id = selectedProduct.value;
  if (!id) { cards.value = []; cardTotal.value = 0; loading.value = false; return; }
  loading.value = true; cards.value = []; cardTotal.value = 0;
  try {
    const { data, error } = await fetchCards({ product_id: id, sku_id: selectedSku.value ?? undefined, page: cardPage.value, page_size: pageSize.value });
    if (seq !== cardRequest) return;
    if (!error && data) {
      if ((data.cards || []).some(c => !safeID(c.id))) { window.$message?.error("卡密标识超出安全范围，无法操作"); cards.value = []; return; }
      cards.value = data.cards || []; cardTotal.value = data.total || 0;
    }
  } finally { if (seq === cardRequest) loading.value = false; }
}
async function loadBatches() {
  const seq = ++batchRequest;
  batchLoading.value = true; batches.value = [];
  try {
    const { data, error } = await fetchImports({ product_id: selectedProduct.value ?? undefined,
      sku_id: typeof batchSku.value === "number" ? batchSku.value : undefined, legacy_only: batchSku.value === "legacy" || undefined });
    if (seq === batchRequest && !error && data) {
      const rows = data.batches || [];
      if (rows.some(r => !safeID(r.id) || !safeID(r.product_id) || (r.sku_id != null && !safeID(r.sku_id, true)))) {
        window.$message?.error("批次标识超出安全范围，无法操作"); return;
      }
      batches.value = rows;
    }
  } finally { if (seq === batchRequest) batchLoading.value = false; }
}
async function loadPremium() {
  const seq = ++premiumRequest, id = selectedProduct.value;
  if (!id) { premiumCards.value = []; premiumTotal.value = 0; premiumLoading.value = false; return; }
  premiumLoading.value = true; premiumCards.value = []; premiumTotal.value = 0;
  try {
    const { data, error } = await fetchPremiumCards(id, selectedSku.value ?? undefined);
    if (seq === premiumRequest && !error && data) { premiumCards.value = data.cards || []; premiumTotal.value = data.total || 0; }
  } finally { if (seq === premiumRequest) premiumLoading.value = false; }
}
async function refreshInventory() {
  checkedKeys.value = [];
  await Promise.all([loadCards(), loadBatches(), loadTargets(), inventoryTab.value === "premium" ? loadPremium() : Promise.resolve()]);
}
watch(selectedProduct, () => {
  selectedSku.value = null; batchSku.value = "all"; checkedKeys.value = []; cards.value = []; cardTotal.value = 0; cardPage.value = 1;
  void loadTargets(); void loadCards(); void loadBatches();
  premiumCards.value = []; premiumTotal.value = 0;
  if (inventoryTab.value === "premium") void loadPremium();
});
watch(selectedSku, () => { checkedKeys.value = []; cardPage.value = 1; void loadCards(); if (inventoryTab.value === "premium") void loadPremium(); });
watch(batchSku, () => { void loadBatches(); });
watch(inventoryTab, tab => { if (tab === "premium") void loadPremium(); });
function onFilterCatChange() {
  if (selectedProduct.value && !productOptions(filterCatId.value).some(p => p.value === selectedProduct.value)) selectedProduct.value = null;
}
async function loadProducts() {
  if (!checkAuth("catalog:read")) return;
  productsLoading.value = true; productsLoadFailed.value = false;
  try {
    const all: any[] = [];
    for (let page = 1; ; page++) {
      const { data, error } = await fetchProducts({ page, page_size: 500, options_only: true });
      if (error || !data) { productsLoadFailed.value = true; return; }
      const result = data as any, rows = result.products || [];
      if (rows.some((p: any) => !safeID(p.id))) { productsLoadFailed.value = true; return; }
      all.push(...rows);
      if (!rows.length || all.length >= Number(result.total || 0)) break;
    }
    products.value = all;
  } catch { productsLoadFailed.value = true; } finally { productsLoading.value = false; }
}
async function loadCategories() {
  if (!checkAuth("catalog:category_read")) return;
  const { data, error } = await fetchCategories();
  if (!error && data) categories.value = (data as any).categories || [];
}
async function handleExport() {
  const id = selectedProduct.value;
  const skuId = selectedSku.value;
  if (!id || exporting.value) return;
  exporting.value = true;
  try {
    const { data, error } = await exportCards(id, skuId ?? undefined);
    if (!error && data?.lines?.length) {
      const url = URL.createObjectURL(new Blob([data.lines.join("\n")], { type: "text/csv;charset=utf-8" }));
      const a = document.createElement("a"); a.href = url; a.download = `cards-product-${id}${skuId === null ? "" : `-sku-${skuId}`}.csv`; a.click(); URL.revokeObjectURL(url);
      window.$message?.success(`已导出 ${data.lines.length} 行`);
    } else if (!error) window.$message?.warning("当前筛选没有可导出的卡密");
  } finally { exporting.value = false; }
}
async function handleToggle(row: any) {
  const { error } = await toggleCard(row.id, row.status === "disabled");
  if (!error) { window.$message?.success("卡密状态已更新"); await refreshInventory(); }
}

// 一次导入只对应一个规格；请求序号与输入快照共同隔离过期预览。
const showImport = ref(false), importing = ref(false), previewLoading = ref(false);
const importProductId = ref<number | null>(null), importSkuId = ref<number | null>(null), importCatId = ref<number | null>(null);
const importContent = ref(""), importTargets = ref<InventoryTargets | null>(null);
const importTargetsLoading = ref(false), importTargetsError = ref("");
const previewResult = ref<InventoryPreview | null>(null), previewKey = ref("");
let importTargetRequest = 0, previewRequest = 0, pendingImportSku: number | null = null;
let previewTimer: ReturnType<typeof setTimeout> | null = null;
const chosenImportTarget = computed(() => importTargets.value?.targets.find(t => t.sku_id === importSkuId.value));
const importAllowed = computed(() => !!chosenImportTarget.value?.can_import && !importTargetsLoading.value && !importTargetsError.value);
const importSkuOptions = computed(() => (importTargets.value?.targets || []).map(t => ({
  label: `${skuName(t)}${t.can_import ? "" : `（${t.reason || "不可导入"}）`}`, value: t.sku_id!, disabled: !t.can_import,
})));
const importKey = computed(() => JSON.stringify([importProductId.value, importSkuId.value, importContent.value]));
const previewImportable = computed(() => Math.max(0, (previewResult.value?.total || 0) - (previewResult.value?.dup_in_file || 0) - (previewResult.value?.dup_in_db || 0) - (previewResult.value?.invalid || 0)));
const validPreview = computed(() => !!previewResult.value && previewKey.value === importKey.value);
function invalidatePreview() {
  previewRequest++; previewResult.value = null; previewKey.value = ""; previewLoading.value = false;
  if (previewTimer) { clearTimeout(previewTimer); previewTimer = null; }
}
async function loadImportTargets() {
  const seq = ++importTargetRequest, id = importProductId.value;
  invalidatePreview(); importTargets.value = null; importTargetsError.value = ""; importSkuId.value = null;
  if (!id || !showImport.value) { importTargetsLoading.value = false; return; }
  importTargetsLoading.value = true;
  try {
    const { data, error } = await fetchInventoryTargets(id);
    if (seq !== importTargetRequest || !showImport.value) return;
    if (error || !data) throw new Error("规格加载失败，请重试；未加载前不能导入");
    importTargets.value = validateTargets(data, id);
    const requested = pendingImportSku; pendingImportSku = null;
    if (requested !== null && importTargets.value.targets.some(t => t.sku_id === requested && t.can_import)) importSkuId.value = requested;
    else if (!importTargets.value.has_skus && importTargets.value.targets.some(t => t.sku_id === 0 && t.can_import)) importSkuId.value = 0;
  } catch (err) {
    if (seq === importTargetRequest) importTargetsError.value = err instanceof Error ? err.message : "规格加载失败，请重试";
  } finally { if (seq === importTargetRequest) importTargetsLoading.value = false; }
}
function openImport(productId = selectedProduct.value, skuId = selectedSku.value) {
  if (importing.value) return;
  pendingImportSku = skuId;
  showImport.value = true;
  if (importProductId.value === productId) void loadImportTargets(); else importProductId.value = productId;
}
watch(importProductId, () => { void loadImportTargets(); });
watch(showImport, value => { if (!value) { importTargetRequest++; invalidatePreview(); } });
watch([importSkuId, importContent], () => {
  invalidatePreview();
  if (showImport.value && importAllowed.value && importContent.value.trim()) previewTimer = setTimeout(() => { void handlePreview(); }, 500);
});
function onImportCatChange() {
  if (importProductId.value && !productOptions(importCatId.value).some(p => p.value === importProductId.value)) importProductId.value = null;
}
function importLines() { return importContent.value.split("\n").map(l => l.trim()).filter(Boolean); }
async function handlePreview() {
  if (!showImport.value || !importAllowed.value || !importProductId.value || importSkuId.value === null || !importContent.value.trim() || importing.value) return;
  const seq = ++previewRequest, key = importKey.value;
  const input = { product_id: importProductId.value, sku_id: importSkuId.value, lines: importLines() };
  previewLoading.value = true;
  try {
    const { data, error } = await importPreview(input);
    if (seq !== previewRequest || key !== importKey.value || !showImport.value) return;
    if (!error && data) {
      if ((data.sku_id ?? 0) !== input.sku_id || !safeID(data.revision ?? 0, true)) {
        window.$message?.error("预览目标已变化，请重新加载规格"); return;
      }
      previewResult.value = data; previewKey.value = key;
    }
  } finally { if (seq === previewRequest) previewLoading.value = false; }
}
async function handleImport() {
  if (importing.value || !validPreview.value || !importAllowed.value || !importProductId.value || importSkuId.value === null) return;
  const input = { product_id: importProductId.value, sku_id: importSkuId.value, lines: importLines(), expected_revision: previewResult.value!.revision ?? 0 };
  importing.value = true;
  try {
    const { data, error } = await importConfirm(input);
    if (!error && data) {
      window.$message?.success(`导入完成：新增 ${data.imported || 0} 条，重复跳过 ${data.skipped || 0} 条，失败 ${data.failed || 0} 条`);
      showImport.value = false; importContent.value = ""; invalidatePreview(); await refreshInventory();
    } else { invalidatePreview(); void loadImportTargets(); }
  } finally { importing.value = false; }
}

// 历史分配只修改当前归属，原导入批次保持不变。
const showAssign = ref(false), assignProductId = ref<number | null>(null), assignSkuId = ref<number | null>(null);
const assignTargets = ref<InventoryTargets | null>(null), assignTargetsLoading = ref(false), assignError = ref("");
const assignBusy = ref(false), assignPreviewLoading = ref(false), assignPreview = ref<InventoryAssignPreview | null>(null);
const assignCardIds = ref<number[]>([]), assignBatchId = ref<number | null>(null);
let assignRequest = 0, assignTargetRequest = 0;
const assignOptions = computed(() => (assignTargets.value?.targets || []).filter(t => t.can_import && (t.sku_id || 0) > 0).map(t => ({ label: skuName(t), value: t.sku_id! })));
async function openAssign(batch?: InventoryBatch) {
  const productId = batch?.product_id ?? selectedProduct.value;
  if (!productId || !safeID(productId) || (!batch && !checkedKeys.value.length)) return;
  assignProductId.value = productId; assignBatchId.value = batch?.id ?? null;
  assignCardIds.value = batch ? [] : checkedKeys.value.filter(id => safeID(id)) as number[];
  assignSkuId.value = null; assignPreview.value = null; assignError.value = ""; assignTargets.value = null; showAssign.value = true;
  const seq = ++assignTargetRequest; assignTargetsLoading.value = true;
  try {
    const { data, error } = await fetchInventoryTargets(productId);
    if (seq !== assignTargetRequest || !showAssign.value) return;
    if (error || !data) throw new Error("目标规格加载失败，请重试");
    assignTargets.value = validateTargets(data, productId);
    if (!assignOptions.value.length) assignError.value = "该商品没有可接收卡密的本地规格，请先保存规格及发货来源";
  } catch (err) { if (seq === assignTargetRequest) assignError.value = err instanceof Error ? err.message : "加载失败"; }
  finally { if (seq === assignTargetRequest) assignTargetsLoading.value = false; }
}
watch(assignSkuId, () => { assignRequest++; assignPreview.value = null; assignPreviewLoading.value = false; });
watch(showAssign, value => { if (!value) { assignRequest++; assignTargetRequest++; assignPreview.value = null; } });
async function handleAssignPreview() {
  if (!assignProductId.value || !assignSkuId.value || assignBusy.value) return;
  const seq = ++assignRequest; assignPreviewLoading.value = true; assignError.value = "";
  try {
    const { data, error } = await previewAssignCards({ product_id: assignProductId.value, sku_id: assignSkuId.value,
      ...(assignBatchId.value ? { import_id: assignBatchId.value } : { card_ids: assignCardIds.value }) });
    if (seq !== assignRequest || !showAssign.value) return;
    if (!error && data && Array.isArray(data.card_ids) && data.card_ids.every(id => safeID(id)) && safeID(data.revision ?? 0, true)) assignPreview.value = { ...data, card_ids: [...data.card_ids] };
    else { assignPreview.value = null; assignError.value = "归属预览失败，请核对库存后重试"; }
  } finally { if (seq === assignRequest) assignPreviewLoading.value = false; }
}
async function handleAssign() {
  if (!assignProductId.value || !assignSkuId.value || !assignPreview.value?.card_ids.length || assignBusy.value) return;
  const input = { product_id: assignProductId.value, sku_id: assignSkuId.value, card_ids: [...assignPreview.value.card_ids], expected_revision: assignPreview.value.revision ?? 0,
    request_key: globalThis.crypto?.randomUUID?.() || `inventory-${Date.now()}-${Math.random().toString(36).slice(2)}` };
  assignBusy.value = true;
  try {
    const { data, error } = await assignCards(input);
    if (!error && data) { window.$message?.success(`已分配 ${data.assigned || 0} 张卡密，原导入批次保留`); showAssign.value = false; await refreshInventory(); }
    else { assignPreview.value = null; assignError.value = "库存或设置可能已变化，请重新预览后确认"; }
  } finally { assignBusy.value = false; }
}

const showCancel = ref(false), cancelBusy = ref(false), cancelLoading = ref(false), cancelError = ref("");
const cancelId = ref<number | null>(null), cancelData = ref<{ batch: InventoryBatch; revision: string } | null>(null);
let cancelRequest = 0;
async function openCancel(id: number) { cancelId.value = id; showCancel.value = true; await loadCancelPreview(); }
async function loadCancelPreview() {
  if (!cancelId.value) return;
  const seq = ++cancelRequest; cancelLoading.value = true; cancelData.value = null; cancelError.value = "";
  try {
    const { data, error } = await previewCancelImport(cancelId.value);
    if (seq !== cancelRequest || !showCancel.value) return;
    if (!error && data?.batch?.id === cancelId.value && data.revision) cancelData.value = data;
    else cancelError.value = "撤销预览失败，请重试";
  } finally { if (seq === cancelRequest) cancelLoading.value = false; }
}
watch(showCancel, value => { if (!value) { cancelRequest++; cancelData.value = null; } });
async function handleCancel() {
  if (!cancelId.value || !cancelData.value || cancelBusy.value) return;
  const id = cancelId.value, revision = cancelData.value.revision;
  cancelBusy.value = true;
  try {
    const { error } = await cancelImport(id, revision);
    if (!error) { window.$message?.success("批次剩余可用卡已撤销"); showCancel.value = false; await refreshInventory(); }
    else { cancelData.value = null; cancelError.value = "批次库存可能已变化，请重新预览后确认"; }
  } finally { cancelBusy.value = false; }
}

const cardColumns = computed<DataTableColumns<any>>(() => [
  ...(checkAuth("inventory:write") ? [{ type: "selection" as const, disabled: (r: any) => r.status !== "available" || (r.sku_id || 0) !== 0 || !!r.lottery_draw_no }] : []),
  { title: "ID", key: "id", width: 75 }, { title: "规格", key: "sku_name", minWidth: 150, render: skuName },
  { title: "原批次", key: "import_id", width: 90, render: r => r.import_id || "-" },
  { title: "内容", key: "masked_content", minWidth: 120, render: r => r.masked_content || "****" },
  { title: "状态", key: "status", width: 90, render: r => h(NTag, { size: "small", type: r.status === "available" ? "success" : "default" }, { default: () => r.lottery_draw_no ? "已发奖" : cardStatusText(r.status) }) },
  { title: "备注", key: "note", minWidth: 120, ellipsis: { tooltip: true } },
  { title: "创建时间", key: "created_at", width: 165, render: r => r.created_at ? new Date(r.created_at * 1000).toLocaleString() : "-" },
  { title: "操作", key: "actions", width: 90, render: r => checkAuth("inventory:write") && ["available", "disabled"].includes(r.status)
    ? h(NPopconfirm, { onPositiveClick: () => handleToggle(r) }, { trigger: () => h(NButton, { size: "small", quaternary: true }, { default: () => r.status === "available" ? "禁用" : "启用" }), default: () => "确认更改该卡状态？" }) : null },
]);
const batchColumns: DataTableColumns<InventoryBatch> = [
  { title: "ID", key: "id", width: 75 }, { title: "商品", key: "product_name", minWidth: 120, render: r => r.product_name || `商品 #${r.product_id}` },
  { title: "导入目标（原始记录）", key: "sku_name", minWidth: 170, render: importSkuName },
  { title: "当前卡密分布", key: "remaining", minWidth: 340, render: r => distribution(r.remaining) },
  { title: "总数 / 导入 / 跳过 / 失败", key: "imported", width: 180, render: r => `${r.total || 0} / ${r.imported || 0} / ${r.skipped || 0} / ${r.failed || 0}` },
  { title: "状态", key: "status", width: 90, render: r => batchStatus(r.status) },
  { title: "操作", key: "actions", width: 180, render: r => h(NSpace, { size: "small" }, { default: () => [
    checkAuth("inventory:write") ? h(NButton, { size: "small", onClick: () => openAssign(r) }, { default: () => "分配默认池" }) : null,
    r.status === "done" && checkAuth("inventory:import") ? h(NButton, { size: "small", type: "warning", onClick: () => openCancel(r.id) }, { default: () => "撤销批次" }) : null,
  ] }) },
];
const premiumColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 75 }, { title: "规格", key: "sku_name", minWidth: 150, render: skuName },
  { title: "号码", key: "masked_content", minWidth: 120, render: r => r.masked_content || "****" },
  { title: "预选加价", key: "draft_premium", width: 100, render: r => formatMoney(r.draft_premium || 0) },
  { title: "预选成本", key: "draft_cost", width: 100, render: r => formatMoney(r.draft_cost || 0) },
  { title: "靓号价", key: "price_cents", width: 100, render: r => r.price_cents ? formatMoney(r.price_cents) : "商品价+加价" },
  { title: "状态", key: "status", width: 90, render: r => r.lottery_draw_no ? "已发奖" : cardStatusText(r.status) },
];
onMounted(() => {
  void loadProducts(); void loadCategories(); void loadBatches();
  const id = queryID(route.query.product_id), sku = queryID(route.query.sku_id, true);
  if (route.query.product_id != null && id === null) window.$message?.error("商品标识无效或超出安全范围");
  routeSku = sku;
  if (id !== null) { selectedProduct.value = id; if (route.query.open_import === "1" && checkAuth("inventory:import")) openImport(id, sku); }
});
onBeforeUnmount(() => { if (previewTimer) clearTimeout(previewTimer); targetRequest++; cardRequest++; batchRequest++; premiumRequest++; importTargetRequest++; previewRequest++; assignRequest++; assignTargetRequest++; cancelRequest++; });
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="卡密库存" class="min-w-0 flex-1">
      <div class="mb-16px flex flex-wrap items-center gap-12px">
        <NButton v-auth="'inventory:import'" type="primary" @click="openImport()">导入卡密</NButton>
        <NButton v-auth="'card:export'" :disabled="!selectedProduct" :loading="exporting" @click="handleExport">导出当前规格</NButton>
        <template v-if="checkAuth('catalog:read')">
          <NSelect v-model:value="filterCatId" :options="categoryOptions" placeholder="全部分类" aria-label="库存分类" class="w-150px" @update:value="onFilterCatChange" />
          <NSelect v-model:value="selectedProduct" :options="productOptions(filterCatId)" :loading="productsLoading" placeholder="搜索/选择商品" aria-label="库存商品" class="w-full sm:w-260px" filterable clearable />
        </template>
        <NInputNumber v-else v-model:value="selectedProduct" :min="1" :max="Number.MAX_SAFE_INTEGER" :precision="0" placeholder="输入商品 ID 查询" :input-props="{ 'aria-label': '库存商品 ID' }" class="w-full sm:w-230px" />
        <NSelect v-model:value="selectedSku" :options="skuOptions" :disabled="!selectedProduct || targetsLoading || !!targetsError" :loading="targetsLoading" placeholder="全部规格" aria-label="库存规格" class="w-full sm:w-230px" clearable />
      </div>
      <NAlert v-if="productsLoadFailed" type="error" class="mb-12px">商品选项加载失败或标识超出安全范围。<NButton text @click="loadProducts">重新加载</NButton></NAlert>
      <NAlert v-if="targetsError" type="error" class="mb-12px">{{ targetsError }} <NButton text @click="loadTargets">重试规格</NButton></NAlert>
      <p v-if="targets" class="mb-12px text-13px">{{ targets.product_name }} · 默认池与每个规格独立管理，选择具体规格不会使用其他规格的卡密。</p>
      <NTabs v-model:value="inventoryTab" type="line">
        <NTabPane name="cards" tab="卡密列表">
          <NEmpty v-if="!selectedProduct" class="my-24px" description="先选择商品或输入商品 ID 查看卡密" />
          <template v-else>
            <NButton v-if="checkAuth('inventory:write')" class="mb-12px" :disabled="!checkedKeys.length" @click="openAssign()">分配选中的默认池卡密</NButton>
            <NDataTable v-model:checked-row-keys="checkedKeys" :row-key="(r: any) => r.id" :columns="cardColumns" :data="cards" :loading="loading" :scroll-x="900" :max-height="540" />
            <TablePager v-model:page="cardPage" v-model:page-size="pageSize" :total="cardTotal" @change="loadCards" />
          </template>
        </NTabPane>
        <NTabPane name="batches" tab="导入批次">
          <NSelect v-model:value="batchSku" :options="batchSkuOptions" aria-label="批次导入目标" class="mb-12px w-full sm:w-250px" />
          <p class="mb-12px text-13px">按原始导入目标筛选；当前卡密可能已分配到其他规格，原批次来源保持不变。</p>
          <NDataTable :columns="batchColumns" :data="batches" :loading="batchLoading" :scroll-x="1150" :max-height="540" />
        </NTabPane>
        <NTabPane v-if="checkAuth('card:premium')" name="premium" tab="靓号库">
          <NEmpty v-if="!selectedProduct" class="my-24px" description="先选择商品查看靓号卡" />
          <template v-else><NDataTable :columns="premiumColumns" :data="premiumCards" :loading="premiumLoading" :scroll-x="720" :max-height="540" /><p class="mt-8px">共 {{ premiumTotal }} 张</p></template>
        </NTabPane>
      </NTabs>
    </NCard>
    <NModal v-model:show="showImport" preset="dialog" title="按规格导入卡密" style="width: min(680px, calc(100vw - 32px))" :mask-closable="!importing" :close-on-esc="!importing" :closable="!importing">
      <NForm label-placement="top">
        <NFormItem label="商品">
          <div v-if="checkAuth('catalog:read')" class="flex w-full flex-wrap gap-8px">
            <NSelect v-model:value="importCatId" :options="categoryOptions" :disabled="importing" aria-label="导入分类" class="w-full sm:w-140px" @update:value="onImportCatChange" />
            <NSelect v-model:value="importProductId" :options="productOptions(importCatId)" :disabled="importing" :loading="productsLoading" placeholder="选择商品" aria-label="导入商品" class="min-w-0 flex-1" filterable clearable />
          </div>
          <NInputNumber v-else v-model:value="importProductId" :min="1" :max="Number.MAX_SAFE_INTEGER" :precision="0" :disabled="importing" :input-props="{ 'aria-label': '导入商品 ID' }" placeholder="商品 ID" class="w-full" />
        </NFormItem>
        <NFormItem label="目标规格">
          <NSelect v-model:value="importSkuId" :options="importSkuOptions" :disabled="importing || importTargetsLoading || !!importTargetsError" :loading="importTargetsLoading" placeholder="请选择本次导入的规格" aria-label="导入规格" />
        </NFormItem>
        <NAlert v-if="importTargetsError" type="error" class="mb-12px">{{ importTargetsError }} <NButton text :disabled="importing" @click="loadImportTargets">重试</NButton></NAlert>
        <NAlert v-else-if="importTargets && !importAllowed" type="info" class="mb-12px">{{ chosenImportTarget?.reason || '请选择可导入的本地卡密规格；每批只能对应一个规格。' }}</NAlert>
        <NFormItem label="卡密内容（每行一条）"><NInput v-model:value="importContent" type="textarea" :input-props="{ 'aria-label': '导入卡密内容' }" :disabled="importing" :rows="8" placeholder="每行一条卡密；靓号格式：卡密---价格---备注" /></NFormItem>
      </NForm>
      <NAlert v-if="validPreview" type="info" class="mb-12px">目标：{{ importTargets?.product_name }} / {{ chosenImportTarget ? skuName(chosenImportTarget) : '' }}；总计 {{ previewResult?.total || 0 }} 条，重复 {{ (previewResult?.dup_in_file || 0) + (previewResult?.dup_in_db || 0) }} 条，无效 {{ previewResult?.invalid || 0 }} 条，可导入 {{ previewImportable }} 条。</NAlert>
      <template #action>
        <NButton :disabled="importing" @click="showImport = false">取消</NButton>
        <NButton :disabled="!importAllowed || !importContent.trim() || importing" :loading="previewLoading" @click="handlePreview">预览</NButton>
        <NButton type="primary" :disabled="!validPreview || !importAllowed || previewImportable <= 0" :loading="importing" @click="handleImport">确认导入</NButton>
      </template>
    </NModal>
    <NModal v-model:show="showAssign" preset="dialog" title="分配默认池卡密到规格" style="width: min(680px, calc(100vw - 32px))" :mask-closable="!assignBusy" :close-on-esc="!assignBusy" :closable="!assignBusy">
      <p class="mb-12px">只分配同商品默认池中可用、未被占用的卡密。已售、预占及其他规格的卡不移动；原导入批次保持不变。</p>
      <NFormItem label="目标规格"><NSelect v-model:value="assignSkuId" :options="assignOptions" :disabled="assignBusy || assignTargetsLoading" :loading="assignTargetsLoading" placeholder="选择接收卡密的本地规格" aria-label="归属目标规格" /></NFormItem>
      <NAlert v-if="assignError" type="error" class="mb-12px">{{ assignError }}</NAlert>
      <NAlert v-if="assignPreview" type="info" class="mb-12px">将分配 {{ assignPreview.eligible_count || 0 }} 张到「{{ assignPreview.sku_name }}」，排除 {{ assignPreview.excluded_count || 0 }} 张；确认时将重新校验库存状态。</NAlert>
      <template #action><NButton :disabled="assignBusy" @click="showAssign = false">取消</NButton><NButton :disabled="!assignSkuId || assignBusy" :loading="assignPreviewLoading" @click="handleAssignPreview">预览归属</NButton><NButton type="primary" :disabled="!assignPreview?.card_ids.length" :loading="assignBusy" @click="handleAssign">确认分配</NButton></template>
    </NModal>
    <NModal v-model:show="showCancel" preset="dialog" title="撤销原始导入批次" style="width: min(680px, calc(100vw - 32px))" :mask-closable="!cancelBusy" :close-on-esc="!cancelBusy" :closable="!cancelBusy">
      <NSpin :show="cancelLoading"><NAlert type="warning" class="mb-12px">此操作会删除原批次在所有当前规格中的剩余可用卡，范围不受当前页面的规格筛选限制。已预占、已售和禁用卡保留。</NAlert>
        <template v-if="cancelData"><p>原导入目标：{{ importSkuName(cancelData.batch) }}</p><p class="mt-12px">{{ distribution(cancelData.batch.remaining) }}</p></template>
        <NAlert v-if="cancelError" type="error" class="mt-12px">{{ cancelError }} <NButton text @click="loadCancelPreview">重新预览</NButton></NAlert>
      </NSpin>
      <template #action><NButton :disabled="cancelBusy" @click="showCancel = false">取消</NButton><NButton type="warning" :disabled="!cancelData || cancelLoading" :loading="cancelBusy" @click="handleCancel">确认撤销整批可用卡</NButton></template>
    </NModal>
  </div>
</template>
