<template>
  <div class="min-h-500px flex-col gap-16px overflow-hidden">
    <NCard title="卡密库存" class="flex-1">
      <div class="mb-16px flex items-center gap-12px">
        <NButton type="primary" @click="showImport = true">导入卡密</NButton>
        <NSelect v-model:value="selectedProduct" :options="productOptions" placeholder="选择商品" class="w-240px" />
      </div>

      <NTabs type="line">
        <NTabPane name="cards" tab="卡密列表">
          <NDataTable :columns="cardColumns" :data="cards" :loading="loading" :pagination="cardPagination" remote />
        </NTabPane>
        <NTabPane name="batches" tab="导入批次">
          <NDataTable :columns="batchColumns" :data="batches" :loading="batchLoading" />
        </NTabPane>
      </NTabs>
    </NCard>

    <!-- 导入弹窗 -->
    <NModal v-model:show="showImport" preset="dialog" title="导入卡密" style="width: 640px">
      <NForm label-placement="left" label-width="80">
        <NFormItem label="商品">
          <NSelect v-model:value="importProductId" :options="productOptions" placeholder="选择商品" />
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
        <NButton type="primary" :loading="importing" :disabled="!previewResult" @click="handleImport">
          确认导入（{{ previewResult?.total || 0 }} 条）
        </NButton>
      </template>
      <NAlert v-if="previewResult" type="info" class="mt-12px">
        总计 {{ previewResult.total }} 条 | 重复 {{ previewResult.dup_in_file + previewResult.dup_in_db }} 条 |
        可导入 {{ previewResult.total - previewResult.dup_in_file - previewResult.dup_in_db }} 条
      </NAlert>
    </NModal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue';
import { NButton, NTag, NSpace, NPopconfirm, NAlert } from 'naive-ui';
import type { DataTableColumns } from 'naive-ui';
import { fetchProducts, importPreview, importConfirm, fetchImports, cancelImport, fetchCards } from '@/service/api';

defineOptions({ name: 'InventoryManagement' });

const loading = ref(false);
const batchLoading = ref(false);
const importing = ref(false);
const showImport = ref(false);
const selectedProduct = ref<number | null>(null);
const importProductId = ref<number | null>(null);
const importContent = ref('');
const previewResult = ref<any>(null);

const products = ref<any[]>([]);
const cards = ref<any[]>([]);
const batches = ref<any[]>([]);
const cardTotal = ref(0);
const cardPage = ref(1);
const pageSize = 20;

const productOptions = computed(() =>
  products.value.map(p => ({ label: p.name, value: p.id }))
);

const cardPagination = computed(() => ({
  page: cardPage.value,
  pageSize,
  itemCount: cardTotal.value,
  pageCount: Math.ceil(cardTotal.value / pageSize)
}));

const cardColumns: DataTableColumns<any> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '商品ID', key: 'product_id', width: 80 },
  {
    title: '内容',
    key: 'masked_content',
    minWidth: 120,
    render: (row) => row.masked_content || '****'
  },
  {
    title: '状态',
    key: 'status',
    width: 80,
    render: (row) => h(NTag, {
      type: row.status === 'available' ? 'success' : row.status === 'used' ? 'default' : 'warning',
      size: 'small'
    }, { default: () => row.status === 'available' ? '可用' : row.status === 'used' ? '已售' : row.status === 'reserved' ? '锁定' : '禁用' })
  },
  { title: '备注', key: 'note', width: 120 },
  {
    title: '创建时间',
    key: 'created_at',
    width: 160,
    render: (row) => row.created_at ? new Date(row.created_at * 1000).toLocaleString() : '-'
  }
];

const batchColumns: DataTableColumns<any> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '商品ID', key: 'product_id', width: 80 },
  { title: '总数', key: 'total', width: 80 },
  { title: '已导入', key: 'imported', width: 80 },
  { title: '跳过', key: 'skipped', width: 80 },
  {
    title: '状态',
    key: 'status',
    width: 80,
    render: (row) => h(NTag, {
      type: row.status === 'done' ? 'success' : row.status === 'processing' ? 'info' : 'default',
      size: 'small'
    }, { default: () => row.status })
  },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render: (row) => row.status === 'done'
      ? h(NPopconfirm, { onPositiveClick: () => handleCancel(row.id) }, {
          trigger: () => h(NButton, { size: 'small', type: 'warning' }, { default: () => '撤销' }),
          default: () => '撤销将删除本批可用卡密'
        })
      : null
  }
];

async function loadProducts() {
  const { data, error } = await fetchProducts({ page: 1, page_size: 100 });
  if (!error && data) {
    products.value = (data as any).products || [];
  }
}

async function loadCards() {
  if (!selectedProduct.value) return;
  loading.value = true;
  try {
    const { data, error } = await fetchCards({
      product_id: selectedProduct.value,
      page: cardPage.value,
      page_size: pageSize
    });
    if (!error && data) {
      cards.value = (data as any).cards || [];
      cardTotal.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
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

async function handlePreview() {
  if (!importProductId.value || !importContent.value.trim()) return;
  const lines = importContent.value.trim().split('\n').map(l => l.trim()).filter(Boolean);
  const { data, error } = await importPreview({
    product_id: importProductId.value,
    lines
  });
  if (!error && data) {
    previewResult.value = data;
  }
}

async function handleImport() {
  if (!importProductId.value || !importContent.value.trim()) return;
  importing.value = true;
  try {
    const lines = importContent.value.trim().split('\n').map(l => l.trim()).filter(Boolean);
    const { data, error } = await importConfirm({
      product_id: importProductId.value,
      lines
    });
    if (!error) {
      window.$message?.success(`导入成功：${(data as any)?.imported || 0} 条`);
      showImport.value = false;
      importContent.value = '';
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
    window.$message?.success('撤销成功');
    loadBatches();
  }
}

onMounted(() => {
  loadProducts();
  loadBatches();
});
</script>
