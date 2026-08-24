<script setup lang="ts">
/**
 * 商品管理（P1-01 T2/T6 前端面，2026-08-17 表单补全）：
 * 全字段表单（分类/描述/封面+图集 media 上传/排序/上下架三态/发货模式/库存显示/积分价）
 * + SKU 规格管理子表格 + 下单控件配置（独立弹窗）。
 */
import { ref, reactive, computed, onMounted, h } from "vue";
import { useRoute } from "vue-router";
import { NButton, NTag, NSpace, NPopconfirm, NInputNumber, NPopover } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import {
  fetchProducts,
  fetchProduct,
  createProduct,
  updateProduct,
  deleteProduct,
  batchUpdateProductStatus,
  fetchCategories,
  fetchSupplyConnectionOptions,
} from "@/service/api";
import { formatMoney, centsToYuan, yuanToFen } from "@/utils/money";
import { checkAuth } from "@/directives";
import MediaField from "@/components/common/media-picker/media-field.vue";
import RichEditor from "@/components/common/rich-editor/index.vue";
import TablePager from "@/components/common/table-pager.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";
import SkuPanel from "./components/sku-panel.vue";
import ControlPanel from "./components/control-panel.vue";
import CategoryModal from "./components/category-modal.vue";
import ReviewsDrawer from "./components/reviews-drawer.vue";

defineOptions({ name: "ProductManagement" });

const route = useRoute();

const loading = ref(false);
const saving = ref(false);
const showCreate = ref(false);
const editingId = ref(0);
const keyword = ref("");
const products = ref<any[]>([]);
const categories = ref<any[]>([]);
const supplyConnections = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);

// 快捷筛选卡片（后端 status 口径：0=全部 1=上架 2=隐藏 -1=仅下架；low_stock=库存告急）
const statusFilter = ref<number | "low_stock">(0);
const statusTabs = [
  { label: "全部", value: 0, type: "default" as const },
  { label: "已上架", value: 1, type: "success" as const },
  { label: "已隐藏", value: 2, type: "warning" as const },
  { label: "已下架", value: -1, type: "error" as const },
  { label: "库存告急", value: "low_stock" as const, type: "warning" as const },
];

// 列表多选（批量上下架）
const showReviews = ref(false);
const reviewProduct = ref<any>(null);

function openReviews(row: any) {
  reviewProduct.value = row;
  showReviews.value = true;
}

const checkedKeys = ref<number[]>([]);

// ── 单元格价格编辑（售价/成本）：铅笔图标 → 气泡输入（大厂轻量编辑模式）──
// 金额纯文本居中展示；点笔弹 NPopover（受控显隐），气泡内输入金额 → 确定/Enter 保存、Esc 关闭。
const cellDraft = reactive<Record<string, number>>({});
const cellPopover = reactive<Record<string, boolean>>({}); // 受控气泡显隐

function draftKey(row: any, field: "price" | "cost") {
  return `${row.id}:${field}`;
}

async function commitCellEdit(row: any, field: "price" | "cost") {
  const key = draftKey(row, field);
  const draft = cellDraft[key];
  cellPopover[key] = false; // 无论成败先收气泡
  if (draft === undefined) return; // 未输入
  cellDraft[key] = undefined as any; // 清草稿防连发
  const centsKey = field === "price" ? "price_cents" : "factory_price_cents";
  const current = row[centsKey] || 0;
  if (field === "price" && draft <= 0) {
    window.$message?.warning("售价必须大于 0");
    return;
  }
  if (yuanToFen(draft) === current) return; // 值未变
  const payload: Record<string, any> = {
    // 携带行内既有字段（服务端 PUT 全量语义——防零值覆盖积分价等）
    factory_price_cents: row.factory_price_cents || 0,
    points_required: row.points_required || 0,
    status: row.status,
    sort: row.sort || 0,
    stock_visible: row.stock_visible !== false,
    delivery_mode: row.delivery_mode || "",
  };
  payload[centsKey] = yuanToFen(draft);
  const { error } = await updateProduct(row.id, payload);
  if (!error) {
    window.$message?.success(`「${row.name}」${field === "price" ? "售价" : "成本"}已更新`);
    loadList();
  }
}

// priceLine 单条价格行：金额文本 + 铅笔（点击弹气泡输入改价）
function priceLine(row: any, field: "price" | "cost") {
  const centsKey = field === "price" ? "price_cents" : "factory_price_cents";
  const cents = row[centsKey] || 0;
  const key = draftKey(row, field);
  const label = field === "price" ? "售价" : "成本";
  return h("div", { class: "money-cell group inline-flex items-center gap-4px" }, [
    h("span", {}, cents ? formatMoney(cents) : "-"),
    // 改价铅笔仅 catalog:write 可见（渲染函数内求值，权限变更后随表格重渲染生效）
    checkAuth("catalog:write")
      ? h(
      NPopover,
      {
        show: !!cellPopover[key],
        placement: "left",
        trigger: "click", // click 触发：点外部/再点铅笔 → onUpdateShow(false) 自动收起
        onUpdateShow: (v: boolean) => {
          cellPopover[key] = v;
          if (v) cellDraft[key] = cents ? Number(centsToYuan(cents)) : 0; // 开气泡带入当前值
        },
      },
      {
        trigger: () =>
          h(
            "span",
            {
              class: "cursor-pointer text-12px text-gray-400 transition-colors hover:text-primary",
              title: `修改${label}`,
            },
            "✎",
          ),
        default: () =>
          h("div", { class: "flex items-center gap-8px" }, [
            h("span", { class: "text-13px whitespace-nowrap" }, `${label}(元)`),
            h(NInputNumber, {
              value: cellDraft[key] ?? 0,
              size: "small",
              min: field === "price" ? 0.01 : 0,
              precision: 2,
              showButton: false,
              placeholder: label,
              style: "width: 110px",
              autofocus: true,
              inputProps: {
                onKeyup: (e: KeyboardEvent) => {
                  if (e.key === "Enter") commitCellEdit(row, field);
                  else if (e.key === "Escape") {
                    cellDraft[key] = undefined as any;
                    cellPopover[key] = false;
                  }
                },
              },
              onUpdateValue: (v: number | null) => {
                cellDraft[key] = v ?? 0;
              },
            }),
            h(
              NButton,
              { size: "small", type: "primary", onClick: () => commitCellEdit(row, field) },
              { default: () => "确定" },
            ),
          ]),
      },
        )
      : null,
  ]);
}

// priceCell 价格块：售价/成本/加价三行合一（标签与数值紧邻，行内铅笔气泡改价）
function priceCell(row: any) {
  const price = row.price_cents || 0;
  const cost = row.factory_price_cents || 0;
  // (售价 − 成本) / 成本（成本基准；成本 0 无法计算）
  const markup = cost > 0 && price > 0 ? Math.round(((price - cost) / cost) * 100) : null;
  // 行结构：灰标签(定宽对齐) + 数值紧邻其后（大厂 definition-list 式，不两端撑开）
  const line = (label: string, value: any) =>
    h("div", { class: "flex items-center gap-6px leading-20px" }, [
      h("span", { class: "w-30px shrink-0 text-12px text-gray-400" }, label),
      value,
    ]);
  return h("div", { class: "flex flex-col gap-2px py-2px" }, [
    line("售价", priceLine(row, "price")),
    line("成本", priceLine(row, "cost")),
    line(
      "加价",
      markup === null
        ? h("span", { class: "text-gray-300" }, "-")
        : h(
            NTag,
            { size: "small", bordered: false, type: markup >= 0 ? "success" : "error" },
            { default: () => `${markup >= 0 ? "+" : ""}${markup}%` },
          ),
    ),
  ]);
}

// statsCell 库存/已售块：两行，标签定宽 + 数值紧邻（与价格块视觉一致）
function statsCell(row: any) {
  const isCard = row.stock_type === "card";
  const stock = row.stock ?? 0; // -1 = 不限（链接/兑换码类不入卡池）
  const sold = row.sold_count ?? 0;
  const line = (label: string, value: any) =>
    h("div", { class: "flex items-center gap-6px leading-20px" }, [
      h("span", { class: "w-30px shrink-0 text-12px text-gray-400" }, label),
      value,
    ]);
  // 库存颜色：卡密类 0=红（缺货）、≤10=橙（低库存预警）、其余常规；链接/兑换码=不限
  const stockNode = !isCard
    ? h("span", {}, "不限")
    : stock <= 0
      ? h("span", { class: "font-medium text-red-500" }, "0 件")
      : h("span", { class: stock <= 10 ? "text-orange-500" : "" }, `${stock} 件`);
  return h("div", { class: "flex flex-col gap-2px py-2px" }, [
    line("库存", stockNode),
    line("已售", h("span", {}, `${sold} 件`)),
  ]);
}

// 分类管理弹窗态
const showCategory = ref(false);

// 分步表单（大厂发布商品模式：基础 → 价格库存 → 商品描述 → 规格与控件 → 高级设置）
const step = ref(1);
const stepCount = 5;

const formData = reactive({
  name: "",
  category_id: null as number | null,
  description: "",
  cover: [] as string[],
  images: [] as string[],
  price_yuan: 0,
  factory_price_yuan: 0,
  points_required: 0,
  stock_type: "card",
  delivery_mode: "status",
  stock_visible: true,
  dedup: true,
  sort: 0,
  status: 1,
});

const stockTypeOptions = [
  { label: "卡密", value: "card" },
  { label: "链接", value: "url" },
  { label: "兑换码", value: "code" },
];
const deliveryModeOptions = [
  { label: "标记发货（卡密保留）", value: "status" },
  { label: "即删发货（发出即删）", value: "delete" },
];

// 分类树（NTreeSelect 层级数据：label/key/children，无限深度——parent_id 建链）
const categoryTreeOptions = computed(() => {
  const map = new Map<number, any>();
  for (const c of categories.value)
    map.set(c.id, { label: c.name, key: c.id, parent_id: c.parent_id || 0, children: [] });
  const roots: any[] = [];
  for (const node of map.values()) {
    const parent = map.get(node.parent_id);
    if (parent) parent.children.push(node);
    else roots.push(node);
  }
  return roots;
});

function stepPrev() {
  if (step.value > 1) step.value -= 1;
}

// 分步守卫：离开「基础信息」须有名称；离开「价格库存」须有售价
function stepNext() {
  if (step.value === 1 && !formData.name.trim()) {
    window.$message?.warning("请先填写商品名称");
    return;
  }
  if (step.value === 2 && formData.price_yuan <= 0) {
    window.$message?.warning("请先填写有效售价");
    return;
  }
  if (step.value < stepCount) step.value += 1;
}

const columns: DataTableColumns<any> = [
  { type: "selection" },
  { title: "ID", key: "id", width: 56 },
  {
    title: "封面",
    key: "cover",
    width: 72,
    render: (row) =>
      row.cover
        ? h("img", { src: row.cover, class: "h-44px w-44px rounded-4px object-cover" })
        : // 暂无主图占位（大厂缩略图占位惯例：浅灰底 + 相机 SVG + 提示文案）
          h(
            "div",
            {
              class:
                "flex h-44px w-44px flex-col items-center justify-center gap-2px rounded-4px border border-dashed border-gray-300 bg-gray-50 dark:border-gray-600 dark:bg-gray-800",
              title: "暂无主图",
            },
            [
              h(
                "svg",
                {
                  viewBox: "0 0 24 24",
                  width: "14",
                  height: "14",
                  fill: "none",
                  stroke: "currentColor",
                  "stroke-width": "1.5",
                },
                [
                  h("path", {
                    d: "M3 8.5A2.5 2.5 0 0 1 5.5 6h1.2a2 2 0 0 0 1.7-1l.5-.8A2 2 0 0 1 10.6 3h2.8a2 2 0 0 1 1.7 1.2l.5.8a2 2 0 0 0 1.7 1h1.2A2.5 2.5 0 0 1 21 8.5v8A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5v-8Z",
                    "stroke-linejoin": "round",
                  }),
                  h("circle", { cx: "12", cy: "12.5", r: "3.2" }),
                ],
              ),
              h("span", { class: "text-10px leading-10px text-gray-400" }, "暂无主图"),
            ],
          ),
  },
  { title: "商品名", key: "name", minWidth: 140 },
  {
    title: "分类",
    key: "category_id",
    width: 84,
    render: (row) => categories.value.find((c: any) => c.id === row.category_id)?.name || "-",
  },
  {
    // 价格块：售价/成本/加价三行合一（标签与数值紧邻，行内铅笔气泡改价）
    title: "价格",
    key: "pricing",
    width: 168,
    render: (row) => priceCell(row),
  },
  {
    // 库存/已售块（与价格块同风格）：卡密类=可用数（0 红）；链接/兑换码类=不限
    title: "库存 / 已售",
    key: "stats",
    width: 112,
    render: (row) => statsCell(row),
  },
  {
    title: "货源",
    key: "upstream_source_id",
    width: 150,
    render: (row) => {
      if (!row.upstream_source_id) {
        return h(NTag, { size: "small", bordered: false }, { default: () => "自营" });
      }
      const conn = supplyConnections.value.find((c: any) => c.id === row.upstream_source_id);
      const name = conn?.name || `#${row.upstream_source_id}`;
      // 上游商品链接（渠道配置 product_url_template：{base}/{code} 占位）
      let link = "";
      if (conn) {
        try {
          const tpl = JSON.parse(conn.settings || "{}").product_url_template;
          if (tpl && row.upstream_product_code) {
            link = tpl.replaceAll("{base}", conn.base_url).replaceAll("{code}", row.upstream_product_code);
          }
        } catch {
          /* 无模板 */
        }
      }
      return h("div", { class: "flex items-center gap-4px" }, [
        h(NTag, { size: "small", bordered: false, type: "info" }, { default: () => `代发 · ${name}` }),
        link
          ? h(
              "a",
              { href: link, target: "_blank", rel: "noopener noreferrer", title: link, class: "text-12px text-blue-500 hover:underline" },
              "↗",
            )
          : null,
      ]);
    },
  },
  {
    title: "类型",
    key: "stock_type",
    width: 64,
    render: (row) => {
      const map: Record<string, string> = { card: "卡密", url: "链接", code: "兑换码" };
      return map[row.stock_type] || row.stock_type;
    },
  },
  {
    title: "状态",
    key: "status",
    width: 84,
    render: (row) =>
      // 标签即开关（大厂模式）：点击标签 Popconfirm 确认上下架
      h(
        NPopconfirm,
        {
          onPositiveClick: () =>
            handleBatchStatus(
              [row.id],
              row.status === 1 ? 0 : 1,
              row.status === 1 ? "下架" : "上架",
            ),
        },
        {
          trigger: () =>
            h(
              NTag,
              {
                type: row.status === 1 ? "success" : row.status === 2 ? "warning" : "default",
                size: "small",
                class: "cursor-pointer",
                style: "cursor: pointer",
              },
              {
                default: () =>
                  `${row.status === 1 ? "上架" : row.status === 2 ? "隐藏" : "下架"}${row.status === 2 ? " ↻" : ""}`,
              },
            ),
          default: () => `是否${row.status === 1 ? "下架" : "上架"}「${row.name}」？`,
        },
      ),
  },
  {
    title: "操作",
    key: "actions",
    width: 130,
    render: (row) =>
      h(
        NSpace,
        { size: "small" },
        {
          default: () => [
            checkAuth("catalog:write")
              ? h(
                  NButton,
                  { size: "small", onClick: () => handleEdit(row) },
                  { default: () => "编辑" },
                )
              : null,
            checkAuth("catalog:review_read")
              ? h(
                  NButton,
                  { size: "small", quaternary: true, onClick: () => openReviews(row) },
                  { default: () => "评价" },
                )
              : null,
            checkAuth("catalog:delete")
              ? h(
                  NPopconfirm,
                  { onPositiveClick: () => handleDelete(row.id) },
                  {
                    trigger: () =>
                      h(NButton, { size: "small", type: "error" }, { default: () => "删除" }),
                    default: () => "确定删除该商品？",
                  },
                )
              : null,
          ],
        },
      ),
  },
];

async function loadList() {
  loading.value = true;
  try {
    const { data, error } = await fetchProducts({
      keyword: keyword.value || undefined,
      status: statusFilter.value === "low_stock" ? undefined : statusFilter.value || undefined,
      low_stock_only: statusFilter.value === "low_stock" || undefined,
      page: page.value,
      page_size: pageSize.value,
    });
    if (!error && data) {
      products.value = (data as any).products || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

// 筛选/搜索变化：回第 1 页重查（停留旧页可能超出范围显示空列表）
function onSearch() {
  page.value = 1;
  loadList();
}

async function loadCategories() {
  const { data, error } = await fetchCategories();
  if (!error && data) categories.value = (data as any).categories || [];
}

async function loadConnections() {
  const { data, error } = await fetchSupplyConnectionOptions();
  if (!error && data) supplyConnections.value = (data as any).connections || [];
}

// 批量上下架（多选工具栏与行内切换共用；status 1=上架 0=下架）
async function handleBatchStatus(ids: number[], status: number, label: string) {
  if (!ids.length) return;
  const { data, error } = await batchUpdateProductStatus(ids, status);
  if (!error) {
    window.$message?.success(`已${label} ${(data as any)?.updated ?? ids.length} 件商品`);
    checkedKeys.value = [];
    loadList();
  }
}

function resetForm() {
  editingId.value = 0;
  step.value = 1; // 新开弹窗回到第一步
  Object.assign(formData, {
    name: "",
    category_id: null,
    description: "",
    cover: [],
    images: [],
    price_yuan: 0,
    factory_price_yuan: 0,
    points_required: 0,
    stock_type: "card",
    delivery_mode: "status",
    stock_visible: true,
    dedup: true,
    sort: 0,
    status: 1,
  });
}

async function handleEdit(row: any) {
  // 编辑取详情（列表行可能缺全字段）
  const { data, error } = await fetchProduct(row.id);
  const p = !error && data ? data : row;
  editingId.value = p.id;
  Object.assign(formData, {
    name: p.name,
    category_id: p.category_id || null,
    description: p.description || "",
    cover: p.cover ? [p.cover] : [],
    images: p.images || [],
    price_yuan: Number(centsToYuan(p.price_cents)),
    factory_price_yuan: p.factory_price_cents ? Number(centsToYuan(p.factory_price_cents)) : 0,
    points_required: p.points_required || 0,
    stock_type: p.stock_type,
    delivery_mode: p.delivery_mode || "status",
    stock_visible: p.stock_visible !== false,
    dedup: p.dedup !== false,
    sort: p.sort || 0,
    status: p.status,
  });
  step.value = 1; // 编辑也从第一步进入
  showCreate.value = true;
}

function buildPayload() {
  // 元 → 分（铁律 15：提交统一 *100，经 utils/money 防浮点）
  return {
    name: formData.name,
    category_id: formData.category_id || 0,
    description: formData.description,
    cover: formData.cover[0] || "",
    images: formData.images,
    price_cents: yuanToFen(formData.price_yuan),
    factory_price_cents: yuanToFen(formData.factory_price_yuan || 0),
    points_required: formData.points_required || 0,
    stock_type: formData.stock_type,
    delivery_mode: formData.delivery_mode,
    stock_visible: formData.stock_visible,
    dedup: formData.dedup,
    sort: formData.sort || 0,
    status: formData.status,
  };
}

async function handleSave() {
  if (!formData.name || formData.price_yuan <= 0) return;
  saving.value = true;
  try {
    const payload = buildPayload();
    const { error } = editingId.value
      ? await updateProduct(editingId.value, payload)
      : await createProduct(payload);
    if (!error) {
      window.$message?.success(editingId.value ? "更新成功" : "创建成功");
      showCreate.value = false;
      resetForm();
      loadList();
    }
  } finally {
    saving.value = false;
  }
}

// 创建态「保存并配置」（第 4 步 CTA）：先建商品（SKU/控件 API 依赖 product_id），
// 弹窗原地转编辑态并停留在本步——规格/控件面板随即激活，一次流程走完
async function saveAndContinue() {
  if (!formData.name || formData.price_yuan <= 0) {
    window.$message?.warning("请先完成商品名称与售价");
    return;
  }
  saving.value = true;
  try {
    const { data, error } = await createProduct(buildPayload());
    if (!error) {
      const created = (data as any) || {};
      if (created.id) {
        editingId.value = created.id;
        window.$message?.success("商品已创建，可继续配置规格与控件");
        loadList();
      }
    }
  } finally {
    saving.value = false;
  }
}

async function handleDelete(id: number) {
  const { error } = await deleteProduct(id);
  if (!error) {
    window.$message?.success("删除成功");
    loadList();
  }
}

onMounted(() => {
  if (route.query.low_stock === "1") statusFilter.value = "low_stock";
  loadList();
  loadCategories();
  loadConnections();
});
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="商品管理" class="flex-1">
      <div class="mb-16px flex items-center gap-12px">
        <NButton
          v-auth="'catalog:write'"
          type="primary"
          @click="
            resetForm();
            showCreate = true;
          "
        >
          新增商品
        </NButton>
        <NButton
          v-auth="'catalog:category_write'"
          @click="showCategory = true"
        >
          分类管理
        </NButton>
        <NInput
          v-model:value="keyword"
          placeholder="搜索商品名"
          clearable
          class="w-200px"
          @keyup.enter="onSearch"
        />
        <NButton @click="onSearch">搜索</NButton>
      </div>

      <FilterTabs v-model:value="statusFilter" :options="statusTabs" class="mb-12px" @change="onSearch" />

      <!-- 批量操作条（勾选后出现） -->
      <div
        v-if="checkedKeys.length"
        class="mb-12px flex items-center gap-8px rounded-6px bg-primary-50 px-12px py-8px dark:bg-gray-800"
      >
        <span class="text-13px"
          >已选 <b>{{ checkedKeys.length }}</b> 件</span
        >
        <NPopconfirm @positive-click="handleBatchStatus([...checkedKeys], 1, '上架')">
          <template #trigger>
            <NButton v-auth="'catalog:write'" size="small" type="success">批量上架</NButton>
          </template>
          确定上架选中的 {{ checkedKeys.length }} 件商品？
        </NPopconfirm>
        <NPopconfirm @positive-click="handleBatchStatus([...checkedKeys], 0, '下架')">
          <template #trigger>
            <NButton v-auth="'catalog:write'" size="small" type="warning">批量下架</NButton>
          </template>
          确定下架选中的 {{ checkedKeys.length }} 件商品？
        </NPopconfirm>
        <NButton size="small" quaternary @click="checkedKeys = []">取消选择</NButton>
      </div>

      <NDataTable
        :columns="columns"
        :data="products"
        :loading="loading"
        :row-key="(row: any) => row.id"
        :checked-row-keys="checkedKeys"
        @update:checked-row-keys="(keys: any) => (checkedKeys = keys)"
      />

      <!-- 可复用分页条（共N条/首页/页码/每页条数/跳页/末页） -->
      <TablePager
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="total"
        @change="loadList"
      />
    </NCard>

    <!-- 新增/编辑弹窗（分步表单：基础 → 价格库存 → 商品描述 → 规格与控件 → 高级设置） -->
    <NModal
      v-model:show="showCreate"
      preset="card"
      :title="editingId ? '编辑商品' : '新增商品'"
      class="w-780px"
      :style="step === 3 || step === 4 ? 'width: 880px' : undefined"
      :class="step === 3 ? 'product-editor-step-modal' : undefined"
    >
      <NSteps
        :current="step"
        size="small"
        class="mb-20px px-12px"
        @update:current="(v: number) => (step = v)"
      >
        <NStep title="基础信息" />
        <NStep title="价格库存" />
        <NStep title="商品描述" />
        <NStep title="规格与控件" />
        <NStep title="高级设置" />
      </NSteps>

      <!-- 商品描述步：完全展开（编辑器整高可见，不内滚）；其余步骤限高内滚 -->
      <NScrollbar v-if="step !== 3" class="max-h-460px px-12px">
        <!-- 第 1 步：基础信息 -->
        <NForm v-if="step === 1" :model="formData" label-placement="left" label-width="100">
          <NFormItem label="商品名称" path="name" :rule="{ required: true }">
            <NInput v-model:value="formData.name" placeholder="请输入商品名称" />
          </NFormItem>
          <NFormItem label="商品分类">
            <div class="flex w-full items-center gap-8px">
              <NTreeSelect
                v-model:value="formData.category_id"
                :options="categoryTreeOptions"
                placeholder="按层级选择分类（可空）"
                clearable
                default-expand-all
                class="flex-1"
              />
              <NButton
                v-auth="'catalog:category_write'"
                size="small"
                quaternary
                type="primary"
                @click="showCategory = true"
              >
                管理分类
              </NButton>
            </div>
          </NFormItem>
          <NFormItem label="库存类型">
            <NSelect v-model:value="formData.stock_type" :options="stockTypeOptions" />
          </NFormItem>
          <NFormItem label="状态">
            <NRadioGroup v-model:value="formData.status">
              <NSpace>
                <NRadio :value="1">上架</NRadio>
                <NRadio :value="0">下架</NRadio>
                <NRadio :value="2">隐藏（会员可见）</NRadio>
              </NSpace>
            </NRadioGroup>
          </NFormItem>
        </NForm>

        <!-- 第 2 步：价格库存 -->
        <NForm v-else-if="step === 2" :model="formData" label-placement="left" label-width="100">
          <NFormItem label="售价（元）" path="price_yuan" :rule="{ required: true }">
            <NInputNumber
              v-model:value="formData.price_yuan"
              :min="0.01"
              :precision="2"
              :step="0.01"
              class="w-full"
            />
          </NFormItem>
          <NFormItem label="成本价（元）">
            <NInputNumber
              v-model:value="formData.factory_price_yuan"
              :min="0"
              :precision="2"
              :step="0.01"
              class="w-full"
            />
          </NFormItem>
          <NFormItem label="积分价">
            <NInputNumber
              v-model:value="formData.points_required"
              :min="0"
              :precision="0"
              class="w-full"
            />
            <span class="ml-8px text-12px text-gray-400 whitespace-nowrap">0 = 不参与积分商城</span>
          </NFormItem>
          <NFormItem label="库存可见">
            <NSwitch v-model:value="formData.stock_visible" />
            <span class="ml-8px text-12px text-gray-400">关闭后前台不显示剩余库存</span>
          </NFormItem>
          <NFormItem label="排序">
            <NInputNumber v-model:value="formData.sort" :precision="0" class="w-full" />
            <span class="ml-8px text-12px text-gray-400 whitespace-nowrap">小值靠前</span>
          </NFormItem>
        </NForm>

        <!-- 高级设置（第 5 步，内滚容器内） -->
        <NForm v-else :model="formData" label-placement="left" label-width="100">
          <NFormItem label="发货模式">
            <NSelect v-model:value="formData.delivery_mode" :options="deliveryModeOptions" />
          </NFormItem>
          <NFormItem label="导入去重">
            <NSwitch v-model:value="formData.dedup" />
            <span class="ml-8px text-12px text-gray-400">卡密导入时按内容去重</span>
          </NFormItem>
        </NForm>
      </NScrollbar>

      <!-- 第 3 步：商品描述（完全展开——封面/图集/编辑器整高可见，不内滚；弹窗整体上移） -->
      <div v-if="step === 3" class="px-12px">
        <NForm :model="formData" label-placement="top">
          <div class="flex gap-24px">
            <NFormItem label="封面图" class="w-240px">
              <MediaField v-model:value="formData.cover" tip="建议 1:1，列表缩略图" />
            </NFormItem>
            <NFormItem label="详情图集" class="flex-1">
              <MediaField v-model:value="formData.images" multiple tip="详情页轮播，可多选" />
            </NFormItem>
          </div>
          <NFormItem label="商品描述">
            <div class="w-full">
              <RichEditor
                v-model="formData.description"
                height="420px"
                placeholder="商品详情（所见即所得；插图走素材库）"
              />
              <div class="mt-4px text-12px text-gray-400">
                输出 HTML 入库前经服务端白名单 sanitize（防 XSS）；图片统一素材库可复用。
              </div>
            </div>
          </NFormItem>
        </NForm>
      </div>

      <!-- 第 4 步：规格与控件（面板内嵌——编辑态直接生效；创建态先保存商品） -->
      <div v-if="step === 4" class="px-12px">
        <template v-if="editingId">
          <NCard size="small" title="SKU 多规格" class="mb-12px">
            <SkuPanel :product-id="editingId" />
          </NCard>
          <NCard size="small" title="下单收集控件">
            <ControlPanel :product-id="editingId" />
          </NCard>
        </template>
        <NCard v-else size="small" class="py-40px">
          <NEmpty description="SKU 规格与下单控件挂在已保存的商品上">
            <template #extra>
              <NButton type="primary" :loading="saving" @click="saveAndContinue">
                保存商品并配置
              </NButton>
            </template>
          </NEmpty>
        </NCard>
      </div>

      <template #footer>
        <div class="flex items-center justify-between">
          <span class="text-12px text-gray-400">第 {{ step }} / {{ stepCount }} 步</span>
          <NSpace>
            <NButton v-if="step > 1" @click="stepPrev">上一步</NButton>
            <NButton v-if="step < stepCount" type="primary" @click="stepNext">下一步</NButton>
            <NButton v-else type="primary" :loading="saving" @click="handleSave">
              {{ editingId ? "保存" : "创建" }}
            </NButton>
          </NSpace>
        </div>
      </template>
    </NModal>

    <!-- 分类管理（新建后自动选中当前表单分类） -->
    <CategoryModal
      v-model:show="showCategory"
      @refresh="loadCategories"
      @created="(id: number) => (formData.category_id = id)"
    />
  <!-- 评价管理抽屉 -->
  <ReviewsDrawer
    v-model:show="showReviews"
    :product="reviewProduct"
  />
  </div>
</template>

<style>
/* 商品描述步（第 3 步）：编辑器整高展开后弹窗整体上移，保证底部按钮可见 */
.product-editor-step-modal {
  transform: translateY(-4vh);
}
</style>
