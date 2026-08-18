<script setup lang="ts">
// 评价管理抽屉：真实评价审核流（pending→通过/拒绝）+ 虚拟评价创建。
// catalog:review_read 查看 / catalog:review_manage 审核+虚拟评价（超管专属）。
import { onMounted, ref, computed, h } from "vue";
import {
  NDrawer,
  NDrawerContent,
  NDataTable,
  NButton,
  NTag,
  NSpace,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NRate,
} from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchReviews, approveReview, rejectReview, createVirtualReview } from "@/service/api";
import { checkAuth } from "@/directives";

defineOptions({ name: "ReviewsDrawer" });

const props = defineProps<{
  show: boolean;
  product: any;
}>();

const emit = defineEmits(["update:show"]);

const loading = ref(false);
const reviews = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const statusFilter = ref<string | null>(null);

const canManage = computed(() => checkAuth("catalog:review_manage"));

const statusOptions = [
  { label: "全部", value: "" },
  { label: "待审核", value: "pending" },
  { label: "已通过", value: "approved" },
  { label: "已拒绝", value: "rejected" },
];

function statusTag(s: string) {
  const type = s === "approved" ? "success" : s === "pending" ? "warning" : "error";
  const text = s === "approved" ? "已通过" : s === "pending" ? "待审核" : "已拒绝";
  return h(NTag, { type, size: "small" }, { default: () => text });
}

const columns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 60 },
  { title: "商品ID", key: "product_id", width: 76 },
  { title: "用户ID", key: "user_id", width: 76 },
  {
    title: "评分",
    key: "rating",
    width: 90,
    render: (row) => h(NRate, { value: row.rating, size: "small", readonly: true }),
  },
  { title: "内容", key: "content", minWidth: 180, ellipsis: true },
  { title: "状态", key: "status", width: 84, render: (row) => statusTag(row.status) },
  {
    title: "时间",
    key: "created_at",
    width: 150,
    render: (row) => (row.created_at ? new Date(row.created_at * 1000).toLocaleString() : "-"),
  },
  {
    title: "操作",
    key: "actions",
    width: 130,
    render: (row) =>
      row.status === "pending" && canManage.value
        ? h(
            NSpace,
            { size: 4 },
            {
              default: () => [
                h(
                  NButton,
                  { size: "tiny", type: "success", secondary: true, onClick: () => handleApprove(row.id) },
                  { default: () => "通过" },
                ),
                h(
                  NButton,
                  { size: "tiny", type: "error", secondary: true, onClick: () => handleReject(row.id) },
                  { default: () => "拒绝" },
                ),
              ],
            },
          )
        : null,
  },
];

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchReviews({
      status: statusFilter.value || undefined,
      page: page.value,
      page_size: 20,
    });
    if (!error && data) {
      reviews.value = (data as any).reviews || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

async function handleApprove(id: number) {
  const { error } = await approveReview(id);
  if (!error) {
    window.$message?.success("已通过（前台可见）");
    load();
  }
}

async function handleReject(id: number) {
  const { error } = await rejectReview(id);
  if (!error) {
    window.$message?.success("已拒绝（前台不可见）");
    load();
  }
}

// ── 虚拟评价 ──
const showVirtual = ref(false);
const virtualSaving = ref(false);
const virtualForm = ref({ nickname: "", content: "", rating: 5, sort: 0 });

function openVirtual() {
  virtualForm.value = { nickname: "", content: "", rating: 5, sort: 0 };
  showVirtual.value = true;
}

async function handleVirtual() {
  if (!props.product || !virtualForm.value.content.trim()) return;
  virtualSaving.value = true;
  try {
    const { error } = await createVirtualReview({
      product_id: props.product.id,
      nickname: virtualForm.value.nickname.trim() || undefined,
      content: virtualForm.value.content.trim(),
      rating: virtualForm.value.rating,
      sort: virtualForm.value.sort,
    });
    if (!error) {
      window.$message?.success("虚拟评价已创建（与真实已通过评价合并展示）");
      showVirtual.value = false;
    }
  } finally {
    virtualSaving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <NDrawer
    :show="show"
    :width="860"
    :auto-focus="false"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <NDrawerContent :title="`评价管理${product ? '：' + product.name : ''}`" closable>
      <div class="mb-8px flex items-center gap-8px">
        <NSelect
          v-model:value="statusFilter"
          :options="statusOptions"
          class="w-120px"
          size="small"
          @update:value="
            page = 1;
            load();
          "
        />
        <span class="text-12px text-gray-400">共 {{ total }} 条真实评价</span>
        <NButton
          v-if="canManage && product"
          size="small"
          type="primary"
          class="ml-auto"
          @click="openVirtual"
        >
          新增虚拟评价
        </NButton>
      </div>
      <NDataTable :columns="columns" :data="reviews" :loading="loading" size="small" />
      <div class="mt-8px flex items-center justify-between">
        <span class="text-12px text-gray-400">第 {{ page }} 页</span>
        <div class="flex gap-8px">
          <NButton size="small" :disabled="page <= 1" @click="page--, load()">上一页</NButton>
          <NButton size="small" :disabled="reviews.length < 20" @click="page++, load()">下一页</NButton>
        </div>
      </div>
    </NDrawerContent>
  </NDrawer>

  <!-- 虚拟评价（catalog:review_manage 超管专属） -->
  <NModal
    v-model:show="showVirtual"
    preset="dialog"
    :title="`虚拟评价：${product?.name || ''}`"
    style="width: 460px"
  >
    <NForm label-placement="top">
      <NFormItem label="昵称">
        <NInput v-model:value="virtualForm.nickname" placeholder="选填，默认随机昵称" />
      </NFormItem>
      <NFormItem label="评分" required>
        <NRate v-model:value="virtualForm.rating" />
      </NFormItem>
      <NFormItem label="内容" required>
        <NInput
          v-model:value="virtualForm.content"
          type="textarea"
          :rows="3"
          placeholder="展示在前台商品评论区（与真实已通过评价合并）"
        />
      </NFormItem>
      <NFormItem label="排序">
        <NInputNumber v-model:value="virtualForm.sort" class="w-full" placeholder="小的靠前" />
      </NFormItem>
    </NForm>
    <template #action>
      <NButton @click="showVirtual = false">取消</NButton>
      <NButton type="primary" :loading="virtualSaving" @click="handleVirtual">创建</NButton>
    </template>
  </NModal>
</template>
