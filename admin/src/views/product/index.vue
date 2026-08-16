<script setup lang="ts">
import { ref, reactive, computed, onMounted, h } from "vue";
import { NButton, NTag, NSpace, NPopconfirm } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchProducts, createProduct, updateProduct, deleteProduct } from "@/service/api";

defineOptions({ name: "ProductManagement" });

const loading = ref(false);
const saving = ref(false);
const showCreate = ref(false);
const editingId = ref<number>(0);
const keyword = ref("");
const products = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = 20;

const formData = reactive({
  name: "",
  price_cents: 0,
  factory_price_cents: 0,
  stock_type: "card",
  status: 1,
});

const stockTypeOptions = [
  { label: "卡密", value: "card" },
  { label: "链接", value: "url" },
  { label: "兑换码", value: "code" },
];

const productEnabled = computed({
  get: () => formData.status === 1,
  set: (val: boolean) => {
    formData.status = val ? 1 : 0;
  },
});

const pagination = computed(() => ({
  page: page.value,
  pageSize,
  itemCount: total.value,
  pageCount: Math.ceil(total.value / pageSize),
}));

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "商品名", key: "name", minWidth: 160 },
  { title: "标识", key: "slug", width: 120 },
  {
    title: "售价",
    key: "price_cents",
    width: 100,
    render: (row) => `¥${(row.price_cents / 100).toFixed(2)}`,
  },
  {
    title: "成本",
    key: "factory_price_cents",
    width: 100,
    render: (row) =>
      row.factory_price_cents ? `¥${(row.factory_price_cents / 100).toFixed(2)}` : "-",
  },
  {
    title: "类型",
    key: "stock_type",
    width: 80,
    render: (row) => {
      const map: Record<string, string> = { card: "卡密", url: "链接", code: "兑换码" };
      return map[row.stock_type] || row.stock_type;
    },
  },
  {
    title: "状态",
    key: "status",
    width: 80,
    render: (row) =>
      h(
        NTag,
        {
          type: row.status === 1 ? "success" : row.status === 2 ? "warning" : "default",
          size: "small",
        },
        {
          default: () => (row.status === 1 ? "上架" : row.status === 2 ? "隐藏" : "下架"),
        },
      ),
  },
  {
    title: "操作",
    key: "actions",
    width: 160,
    render: (row) =>
      h(
        NSpace,
        { size: "small" },
        {
          default: () => [
            h(
              NButton,
              { size: "small", onClick: () => handleEdit(row) },
              { default: () => "编辑" },
            ),
            h(
              NPopconfirm,
              { onPositiveClick: () => handleDelete(row.id) },
              {
                trigger: () =>
                  h(NButton, { size: "small", type: "error" }, { default: () => "删除" }),
                default: () => "确定删除该商品？",
              },
            ),
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
      page: page.value,
      page_size: pageSize,
    });
    if (!error && data) {
      products.value = (data as any).products || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

function handlePageChange(p: number) {
  page.value = p;
  loadList();
}

function handleEdit(row: any) {
  editingId.value = row.id;
  Object.assign(formData, {
    name: row.name,
    price_cents: row.price_cents,
    factory_price_cents: row.factory_price_cents,
    stock_type: row.stock_type,
    status: row.status,
  });
  showCreate.value = true;
}

async function handleSave() {
  if (!formData.name || formData.price_cents <= 0) return;
  saving.value = true;
  try {
    const { error } = editingId.value
      ? await updateProduct(editingId.value, formData)
      : await createProduct(formData);
    if (!error) {
      showCreate.value = false;
      window.$message?.success(editingId.value ? "更新成功" : "创建成功");
      loadList();
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

onMounted(loadList);
</script>

<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="商品管理" class="flex-1">
      <div class="mb-16px flex items-center gap-12px">
        <NButton type="primary" @click="showCreate = true">新增商品</NButton>
        <NInput
          v-model:value="keyword"
          placeholder="搜索商品名"
          clearable
          class="w-200px"
          @keyup.enter="loadList"
        />
        <NButton @click="loadList">搜索</NButton>
      </div>

      <NDataTable
        :columns="columns"
        :data="products"
        :loading="loading"
        :pagination="pagination"
        remote
        @update:page="handlePageChange"
      />
    </NCard>

    <!-- 新增/编辑弹窗 -->
    <NModal
      v-model:show="showCreate"
      preset="dialog"
      :title="editingId ? '编辑商品' : '新增商品'"
      style="width: 600px"
    >
      <NForm :model="formData" label-placement="left" label-width="100">
        <NFormItem label="商品名称" path="name" :rule="{ required: true }">
          <NInput v-model:value="formData.name" placeholder="请输入商品名称" />
        </NFormItem>
        <NFormItem label="售价（分）" path="price_cents" :rule="{ required: true }">
          <NInputNumber v-model:value="formData.price_cents" :min="1" class="w-full" />
        </NFormItem>
        <NFormItem label="成本价（分）">
          <NInputNumber v-model:value="formData.factory_price_cents" :min="0" class="w-full" />
        </NFormItem>
        <NFormItem label="库存类型">
          <NSelect v-model:value="formData.stock_type" :options="stockTypeOptions" />
        </NFormItem>
        <NFormItem label="状态">
          <NSwitch v-model:value="productEnabled" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCreate = false">取消</NButton>
        <NButton type="primary" :loading="saving" @click="handleSave">确定</NButton>
      </template>
    </NModal>
  </div>
</template>
