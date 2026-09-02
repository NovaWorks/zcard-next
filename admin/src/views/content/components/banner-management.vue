<script setup lang="ts">
// 首页横幅管理（storefront 首页消费）：表格 + 新增弹窗（原内容管理横幅区独立成页）。
import { computed, h, onMounted, ref, watch } from "vue";
import { NButton, NDataTable, NInput, NModal, NForm, NFormItem, NInputNumber, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchBanners, createBanner, deleteBanner, fetchPosts } from "@/service/api";
import { checkAuth } from "@/directives";
import MediaField from "@/components/common/media-picker/media-field.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "BannerManagement" });

const canWrite = () => checkAuth("content:write");

const bannerLoading = ref(false);
const banners = ref<any[]>([]);
const showBanner = ref(false);
const bannerSaving = ref(false);
const bannerForm = ref({ name: "", position: "top", title_json: "", image: [] as string[], link_type: "url", link_value: "", is_active: true, sort: 0 });

// 生效状态快捷筛选（客户端过滤，带实时计数）
const bannerFilter = ref<"" | "on" | "off">("");
const bannerTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "生效中", value: "on", type: "success" as const },
  { label: "已停用", value: "off", type: "default" as const },
];
const bannerCounts = computed(() => ({
  "": banners.value.length,
  on: banners.value.filter((b) => b.is_active).length,
  off: banners.value.filter((b) => !b.is_active).length,
}));
const filteredBanners = computed(() =>
  bannerFilter.value === "" ? banners.value : banners.value.filter((b) => (bannerFilter.value === "on" ? b.is_active : !b.is_active)),
);

// 跳转类型：post=文章详情、notice=点击打开公告弹窗（无需跳转目标）
const linkTypeOptions = [
  { label: "文章（/posts/:slug）", value: "post" },
  { label: "公告弹窗（点击弹出全局公告）", value: "notice" },
  { label: "外部链接", value: "url" },
  { label: "商品", value: "product" },
  { label: "分类", value: "category" },
];
const postSlugOptions = ref<{ label: string; value: string }[]>([]);
watch(
  () => bannerForm.value.link_type,
  (t) => {
    // 切换类型时清掉旧值，避免把商品 ID 当 slug 用
    if (t !== "url" && t !== "product" && t !== "category" && t !== "post") bannerForm.value.link_value = "";
  },
);

function zhValue(json: string): string {
  if (!json || json === "-") return "-";
  try {
    const v = JSON.parse(json);
    return typeof v === "object" && v ? v.zh_CN || v.en_US || Object.values(v)[0] || "-" : String(v);
  } catch {
    return json;
  }
}

const bannerColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  { title: "名称", key: "name", width: 120 },
  { title: "位置", key: "position", width: 70 },
  {
    title: "图片",
    key: "image",
    width: 200,
    render: (row) =>
      row.image
        ? h(
            "span",
            {
              class: "img-preview-trigger text-primary",
              style: "border-bottom:1px dotted var(--primary-color);cursor:pointer;position:relative;",
            },
            [
              row.image.length > 28 ? row.image.slice(0, 28) + "…" : row.image,
              h("img", {
                src: row.image,
                class: "img-preview-pop",
                style: "display:none;position:absolute;left:0;top:24px;z-index:100;width:360px;max-height:240px;object-fit:contain;border-radius:6px;box-shadow:0 4px 16px rgba(0,0,0,.25);background:#fff;padding:4px;",
              }),
            ],
          )
        : "-",
  },
  { title: "跳转", key: "link_value", width: 150, ellipsis: true, render: (row) => {
    const label = ({ url: "外链", product: "商品", category: "分类", post: "文章", notice: "公告弹窗", ad: "广告" } as Record<string, string>)[row.link_type] || row.link_type;
    return h("span", { class: "flex items-center gap-4px" }, [
      h(NTag, { size: "tiny", bordered: false, type: row.link_type === "notice" ? "warning" : "info" }, { default: () => label }),
      h("span", { class: "truncate" }, row.link_value || (row.link_type === "notice" ? "—" : "-")),
    ]);
  } },
  { title: "排序", key: "sort", width: 56 },
  {
    title: "状态",
    key: "is_active",
    width: 70,
    render: (row) => h(NTag, { size: "small", type: row.is_active ? "success" : "default" }, { default: () => (row.is_active ? "生效" : "停用") }),
  },
  {
    title: "操作",
    key: "actions",
    width: 70,
    render: (row) =>
      canWrite()
        ? h(NPopconfirm, { onPositiveClick: () => handleDeleteBanner(row.id) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }), default: () => "确定删除该横幅？" })
        : null,
  },
];

async function loadBanners() {
  bannerLoading.value = true;
  try {
    const { data, error } = await fetchBanners();
    if (!error && data) banners.value = (data as any).banners || [];
  } finally {
    bannerLoading.value = false;
  }
}

// 跳转类型=文章时需要 slug 下拉（横幅与文章松耦合：仅打开弹窗时拉一次）
async function loadPostSlugOptions() {
  const { data, error } = await fetchPosts();
  if (!error && data) {
    postSlugOptions.value = ((data as any).posts || []).map((p: any) => ({
      label: `[${p.type === "notice" ? "公告" : "博客"}] ${zhValue(p.title_json)}`,
      value: p.slug,
    }));
  }
}

function openCreateBanner() {
  bannerForm.value = { name: "", position: "top", title_json: "", image: [], link_type: "url", link_value: "", is_active: true, sort: 0 };
  if (bannerForm.value.link_type === "post") loadPostSlugOptions();
  showBanner.value = true;
}

async function handleBanner() {
  if (!bannerForm.value.name || !bannerForm.value.image.length) return;
  bannerSaving.value = true;
  try {
    const title = bannerForm.value.title_json || JSON.stringify({ zh_CN: bannerForm.value.name });
    const { error } = await createBanner({ ...bannerForm.value, image: bannerForm.value.image[0], title_json: title });
    if (!error) {
      window.$message?.success("横幅已创建（storefront 首页即时消费）");
      showBanner.value = false;
      loadBanners();
    }
  } finally {
    bannerSaving.value = false;
  }
}

async function handleDeleteBanner(id: number) {
  const { error } = await deleteBanner(id);
  if (!error) {
    window.$message?.success("已删除");
    loadBanners();
  }
}

onMounted(loadBanners);
</script>

<template>
  <div class="flex flex-col gap-12px">
    <div class="flex items-center justify-between gap-12px">
      <FilterTabs v-model:value="bannerFilter" :options="bannerTabs" :counts="bannerCounts" size="small" />
      <NButton v-if="canWrite()" size="small" type="primary" @click="openCreateBanner">新增横幅</NButton>
    </div>
    <NDataTable :columns="bannerColumns" :data="filteredBanners" :loading="bannerLoading" size="small" :max-height="560" />

    <NModal v-model:show="showBanner" preset="dialog" title="新增横幅" style="width: 520px">
      <NForm :model="bannerForm" label-placement="left" label-width="72">
        <NFormItem label="名称" required>
          <NInput v-model:value="bannerForm.name" />
        </NFormItem>
        <NFormItem label="位置">
          <NSelect v-model:value="bannerForm.position" :options="[{ label: '顶部', value: 'top' }, { label: '中部', value: 'middle' }, { label: '底部', value: 'bottom' }]" />
        </NFormItem>
        <NFormItem label="图片" required>
          <MediaField v-model:value="bannerForm.image" />
        </NFormItem>
        <NFormItem label="跳转类型">
          <NSelect v-model:value="bannerForm.link_type" :options="linkTypeOptions" />
        </NFormItem>
        <NFormItem v-if="bannerForm.link_type === 'post'" label="跳转文章">
          <NSelect
            v-model:value="bannerForm.link_value"
            :options="postSlugOptions"
            filterable
            placeholder="选择点击横幅后打开的文章"
            @focus="!postSlugOptions.length && loadPostSlugOptions()"
          />
        </NFormItem>
        <NFormItem v-else-if="bannerForm.link_type !== 'notice'" label="跳转目标">
          <NInput v-model:value="bannerForm.link_value" placeholder="URL 或商品/分类 ID" />
        </NFormItem>
        <NFormItem label="排序">
          <NInputNumber v-model:value="bannerForm.sort" class="w-full" />
        </NFormItem>
        <NFormItem label="生效">
          <NSwitch v-model:value="bannerForm.is_active" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showBanner = false">取消</NButton>
        <NButton type="primary" :loading="bannerSaving" @click="handleBanner">创建</NButton>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
/* 横幅图片悬停预览（原生 :hover 命中测试，无 JS 依赖） */
:deep(.img-preview-trigger:hover .img-preview-pop) {
  display: block !important;
}
</style>
