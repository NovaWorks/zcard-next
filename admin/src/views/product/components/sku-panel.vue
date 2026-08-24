<script setup lang="ts">
/**
 * SKU 规格管理（大厂电商规格模式重写，2026-08-17）：
 *   ① 规格定义区——动态「规格名：值1, 值2」行（如 时长：月卡,季卡,年卡）
 *   ② 生成组合——规格值笛卡尔积 → 未存在的组合自动补行（标记「新」）
 *   ③ 组合表格全行内编辑——售价/成本/库存位单元格直接输入（Enter/失焦暂存），
 *      新行逐条创建、已有行逐条更新、行内删除；不再用独立表单，杜绝溢出。
 */
import { ref, computed, watch, h } from "vue";
import { NButton, NTag, NSpace, NPopconfirm, NInput, NInputNumber, NEmpty } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { checkAuth } from "@/directives";
import { fetchSkus, createSku, updateSku, deleteSku } from "@/service/api";
import { centsToYuan, yuanToFen } from "@/utils/money";

const props = defineProps<{ productId: number }>();

interface SkuRow {
  id: number; // 0 = 新行
  name: string;
  spec_values: Record<string, string>;
  price_yuan: number;
  cost_yuan: number;
  stock_offset: number;
  dirty: boolean;
}

const loading = ref(false);
const rows = ref<SkuRow[]>([]);
const saving = ref(false);

// 规格定义（如 [{name:'时长', values:'月卡,季卡'}]）
const specs = ref<{ name: string; values: string }[]>([]);

const newRows = computed(() => rows.value.filter((r) => r.id === 0));

function specKey(spec: Record<string, string>) {
  return Object.entries(spec || {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}:${v}`)
    .join("|");
}

async function load() {
  if (!props.productId) return;
  loading.value = true;
  try {
    const { data, error } = await fetchSkus(props.productId);
    if (!error && data) {
      rows.value = ((data as any).skus || []).map((s: any) => ({
        id: s.id,
        name: s.name,
        spec_values: s.spec_values || {},
        price_yuan: s.price_cents ? Number(centsToYuan(s.price_cents)) : 0,
        cost_yuan: s.cost_cents ? Number(centsToYuan(s.cost_cents)) : 0,
        stock_offset: s.stock_offset || 0,
        dirty: false,
      }));
      // 规格定义从已有 SKU 反推（继续加值/加维度）
      const dims = new Map<string, Set<string>>();
      for (const r of rows.value) {
        for (const [k, v] of Object.entries(r.spec_values)) {
          if (!dims.has(k)) dims.set(k, new Set());
          dims.get(k)!.add(v);
        }
      }
      specs.value = [...dims.entries()].map(([name, set]) => ({
        name,
        values: [...set].join(","),
      }));
    }
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.productId,
  (v) => {
    if (v) {
      rows.value = [];
      specs.value = [];
      load();
    }
  },
  { immediate: true },
);

function addSpec() {
  specs.value.push({ name: "", values: "" });
}

function removeSpec(i: number) {
  specs.value.splice(i, 1);
}

// 生成组合：笛卡尔积 → 跳过已存在（按 spec_values 判重），新行标记 id=0
function generate() {
  const dims: { name: string; values: string[] }[] = specs.value
    .map((s) => ({
      name: s.name.trim(),
      values: s.values
        .split(/[,，]/)
        .map((v) => v.trim())
        .filter(Boolean),
    }))
    .filter((d) => d.name && d.values.length);
  if (!dims.length) {
    window.$message?.warning("请先填写规格名与规格值（如 时长：月卡,季卡）");
    return;
  }
  const existing = new Set(rows.value.map((r) => specKey(r.spec_values)));
  let combos: Record<string, string>[] = [{}];
  for (const d of dims) {
    const next: Record<string, string>[] = [];
    for (const base of combos) {
      for (const v of d.values) next.push({ ...base, [d.name]: v });
    }
    combos = next;
  }
  let added = 0;
  for (const combo of combos) {
    if (existing.has(specKey(combo))) continue;
    existing.add(specKey(combo));
    rows.value.push({
      id: 0,
      name: Object.values(combo).join(" · "),
      spec_values: combo,
      price_yuan: 0,
      cost_yuan: 0,
      stock_offset: 0,
      dirty: true,
    });
    added++;
  }
  if (!added) window.$message?.info("组合均已存在，无新增");
  else window.$message?.success(`已生成 ${added} 个新组合，填写价格后保存`);
}

// 单行保存（新→创建；已有→更新）
async function saveRow(row: SkuRow) {
  if (!row.name.trim()) {
    window.$message?.warning("规格名不能为空");
    return;
  }
  saving.value = true;
  try {
    const payload = {
      name: row.name.trim(),
      spec_values: row.spec_values,
      price_cents: yuanToFen(row.price_yuan || 0),
      cost_cents: yuanToFen(row.cost_yuan || 0),
      stock_offset: row.stock_offset || 0,
    };
    const { data, error } = row.id
      ? await updateSku(row.id, payload)
      : await createSku(props.productId, payload);
    if (!error) {
      if (!row.id && (data as any)?.id) row.id = (data as any).id;
      row.dirty = false;
      window.$message?.success("已保存");
    }
  } finally {
    saving.value = false;
  }
}

// 保存全部新行（批量逐条创建）
async function saveAllNew() {
  const pending = rows.value.filter((r) => r.id === 0);
  if (!pending.length) return;
  for (const row of pending) {
    if (!row.name.trim()) {
      window.$message?.warning("存在未命名组合，请补全规格名");
      return;
    }
  }
  saving.value = true;
  try {
    for (const row of pending) {
      await saveRowQuiet(row);
    }
    window.$message?.success(`已保存 ${pending.length} 个新组合`);
  } finally {
    saving.value = false;
  }
}

async function saveRowQuiet(row: SkuRow) {
  const payload = {
    name: row.name.trim(),
    spec_values: row.spec_values,
    price_cents: yuanToFen(row.price_yuan || 0),
    cost_cents: yuanToFen(row.cost_yuan || 0),
    stock_offset: row.stock_offset || 0,
  };
  const { data, error } = row.id
    ? await updateSku(row.id, payload)
    : await createSku(props.productId, payload);
  if (!error) {
    if (!row.id && (data as any)?.id) row.id = (data as any).id;
    row.dirty = false;
  }
}

async function handleDelete(row: SkuRow) {
  if (row.id) {
    const { error } = await deleteSku(row.id);
    if (error) return;
  }
  rows.value = rows.value.filter((r) => r !== row);
  window.$message?.success("已删除");
}

// 行内数字单元格（Enter/失焦暂存并标脏；宽度自适应列宽）
function numCell(
  row: SkuRow,
  field: "price_yuan" | "cost_yuan" | "stock_offset",
  placeholder: string,
) {
  return h(NInputNumber, {
    value: row[field],
    size: "small",
    min: 0,
    precision: field === "stock_offset" ? 0 : 2,
    placeholder,
    showButton: false,
    style: "width: 100%",
    onUpdateValue: (v: number | null) => {
      row[field] = v || 0;
      row.dirty = true;
    },
  });
}

const columns: DataTableColumns<SkuRow> = [
  {
    title: "规格组合",
    key: "spec_values",
    minWidth: 150,
    render: (row) =>
      h(
        NSpace,
        { size: 4 },
        {
          default: () =>
            Object.entries(row.spec_values || {}).length
              ? Object.entries(row.spec_values).map(([k, v]) =>
                  h(NTag, { size: "small", bordered: false }, { default: () => `${k}:${v}` }),
                )
              : h("span", { class: "text-gray-400" }, "单规格"),
        },
      ),
  },
  {
    title: "规格名",
    key: "name",
    minWidth: 110,
    render: (row) =>
      h(NInput, {
        value: row.name,
        size: "small",
        placeholder: "如：月卡",
        onUpdateValue: (v: string) => {
          row.name = v;
          row.dirty = true;
        },
      }),
  },
  {
    title: "售价(元)",
    key: "price",
    width: 104,
    // 售价 0 = 继承商品价（列头过窄截断，口径移入 placeholder 提示）
    render: (row) => numCell(row, "price_yuan", "0=继承"),
  },
  {
    title: "成本(元)",
    key: "cost",
    width: 96,
    render: (row) => numCell(row, "cost_yuan", "成本"),
  },
  {
    title: "库存位",
    key: "stock_offset",
    width: 80,
    render: (row) => numCell(row, "stock_offset", "0"),
  },
  {
    title: "状态",
    key: "state",
    width: 52,
    render: (row) =>
      row.id === 0
        ? h(NTag, { size: "small", type: "info" }, { default: () => "新" })
        : row.dirty
          ? h(NTag, { size: "small", type: "warning" }, { default: () => "改" })
          : h(NTag, { size: "small", type: "success", bordered: false }, { default: () => "存" }),
  },
  {
    title: "操作",
    key: "actions",
    width: 118,
    render: (row) =>
      h(
        NSpace,
        { size: "small" },
        {
          default: () => [
            checkAuth("catalog:sku_write")
              ? h(
                  NButton,
                  {
                    size: "small",
                    type: "primary",
                    ghost: true,
                    disabled: !row.dirty,
                    loading: saving.value,
                    onClick: () => saveRow(row),
                  },
                  { default: () => "保存" },
                )
              : null,
            checkAuth("catalog:sku_write")
              ? h(
                  NPopconfirm,
                  { onPositiveClick: () => handleDelete(row) },
                  {
                    trigger: () =>
                      h(
                        NButton,
                        { size: "small", type: "error", quaternary: true },
                        { default: () => "删除" },
                      ),
                    default: () => "确定删除该规格？",
                  },
                )
              : null,
          ],
        },
      ),
  },
];
</script>

<template>
  <div>
    <!-- ① 规格定义区（顶部标签对齐控件面板风格） -->
    <NCard size="small" class="mb-12px" title="规格设置">
      <template #header-extra>
        <NButton v-auth="'catalog:sku_write'" size="tiny" quaternary type="primary" @click="addSpec">+ 添加规格</NButton>
      </template>
      <NEmpty
        v-if="!specs.length"
        size="small"
        description="尚无规格维度——添加「规格名 + 规格值」后生成组合，如 时长：月卡,季卡,年卡"
      />
      <div v-for="(spec, i) in specs" :key="i" class="mb-8px flex flex-wrap items-center gap-8px">
        <span class="text-12px text-gray-400">规格{{ i + 1 }}</span>
        <NInput
          v-model:value="spec.name"
          size="small"
          placeholder="规格名（如 时长）"
          class="w-130px shrink-0"
        />
        <NInput
          v-model:value="spec.values"
          size="small"
          placeholder="规格值，逗号分隔（如 月卡,季卡,年卡）"
          class="min-w-220px flex-1"
        />
        <NButton v-auth="'catalog:sku_write'" size="small" quaternary type="error" @click="removeSpec(i)">删除</NButton>
      </div>
      <div class="mt-8px flex flex-wrap items-center gap-8px">
        <NButton v-auth="'catalog:sku_write'" size="small" type="primary" ghost @click="generate">生成规格组合</NButton>
        <NButton
          v-auth="'catalog:sku_write'"
          size="small"
          type="primary"
          :disabled="!newRows.length"
          :loading="saving"
          @click="saveAllNew"
        >
          保存全部新组合{{ newRows.length ? `（${newRows.length}）` : "" }}
        </NButton>
        <span class="text-12px text-gray-400">售价填 0 = 继承商品价；已有组合自动跳过</span>
      </div>
    </NCard>

    <!-- ② 组合表格（行内编辑；scroll-x 兜底不截断） -->
    <NDataTable
        :max-height="540"
      :columns="columns"
      :data="rows"
      :loading="loading"
      :bordered="false"
      size="small"
      :scroll-x="700"
    />
    <NEmpty
      v-if="!rows.length && !loading"
      size="small"
      class="mt-16px"
      description="暂无规格组合——定义规格后点击「生成规格组合」"
    />
  </div>
</template>
