<script setup lang="ts">
// 内容管理：横幅（storefront 首页消费）+ 文章/公告（content:read / write）。
import { computed, onMounted, ref, watch, h } from "vue";
import { NButton, NDataTable, NInput, NModal, NForm, NFormItem, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchBanners, createBanner, deleteBanner, fetchPosts, createPost, updatePost, publishPost, deletePost, fetchPostCategories, createPostCategory, updatePostCategory, deletePostCategory } from "@/service/api";
import { checkAuth } from "@/directives";
import MediaField from "@/components/common/media-picker/media-field.vue";
import MdHtmlEditor from "@/components/common/md-html-editor/index.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "ContentTab" });

const canWrite = () => checkAuth("content:write");

// ── 横幅 ──
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
const postSlugOptions = computed(() =>
  posts.value.map((p) => ({ label: `[${p.type === "notice" ? "公告" : "博客"}] ${zhValue(p.title_json)}`, value: p.slug })),
);
watch(
  () => bannerForm.value.link_type,
  (t) => {
    // 切换类型时清掉旧值，避免把商品 ID 当 slug 用
    if (t !== "url" && t !== "product" && t !== "category" && t !== "post") bannerForm.value.link_value = "";
  },
);

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

// ── 文章/公告 ──
const postLoading = ref(false);
const posts = ref<any[]>([]);
const showPost = ref(false);
const postSaving = ref(false);
const postForm = ref({ slug: "", type: "notice", title: "", summary: "", content: "", category_id: 0, is_published: true });
const editingPost = ref<any>(null);

// 发布状态快捷筛选（客户端过滤，带实时计数）
const postFilter = ref<"" | "published" | "draft">("");
const postTabs = [
  { label: "全部", value: "", type: "default" as const },
  { label: "已发布", value: "published", type: "success" as const },
  { label: "草稿", value: "draft", type: "warning" as const },
];
const postCounts = computed(() => ({
  "": posts.value.length,
  published: posts.value.filter((p) => p.is_published).length,
  draft: posts.value.filter((p) => !p.is_published).length,
}));
const filteredPosts = computed(() =>
  postFilter.value === "" ? posts.value : posts.value.filter((p) => (postFilter.value === "published" ? p.is_published : !p.is_published)),
);

// title_json/content_json → zh_CN 展示值
function zhValue(json: string): string {
  if (!json) return "-";
  try {
    const v = JSON.parse(json);
    return v.zh_CN || v.zh || Object.values(v)[0] || "-";
  } catch {
    return json;
  }
}

const postColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  { title: "slug", key: "slug", width: 130 },
  {
    title: "类型",
    key: "type",
    width: 70,
    render: (row) => h(NTag, { size: "small", type: row.type === "notice" ? "warning" : "info" }, { default: () => (row.type === "notice" ? "公告" : "博客") }),
  },
  { title: "标题", key: "title_json", minWidth: 160, ellipsis: true, render: (row) => zhValue(row.title_json) },
  {
    title: "栏目",
    key: "category_id",
    width: 90,
    render: (row) =>
      row.category_id
        ? h(NTag, { size: "small", type: "info" }, { default: () => categoryName(row.category_id) })
        : "-",
  },
  {
    title: "发布",
    key: "is_published",
    width: 80,
    render: (row) => h(NTag, { size: "small", type: row.is_published ? "success" : "default" }, { default: () => (row.is_published ? "已发布" : "草稿") }),
  },
  {
    title: "操作",
    key: "actions",
    width: 150,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        canWrite()
          ? h(NButton, { size: "tiny", onClick: () => openEditPost(row) }, { default: () => "编辑" })
          : null,
        canWrite()
          ? h(NButton, { size: "tiny", onClick: () => handlePublish(row) }, { default: () => (row.is_published ? "下架" : "发布") })
          : null,
        canWrite()
          ? h(NPopconfirm, { onPositiveClick: () => handleDeletePost(row.id) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }), default: () => "确定删除？" })
          : null,
      ]),
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

async function loadPosts() {
  postLoading.value = true;
  try {
    const { data, error } = await fetchPosts();
    if (!error && data) posts.value = (data as any).posts || [];
  } finally {
    postLoading.value = false;
  }
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

function openEditPost(row: any) {
  editingPost.value = row;
  postForm.value = {
    slug: row.slug,
    type: row.type,
    title: String(zhValue(row.title_json) === "-" ? "" : zhValue(row.title_json)),
    summary: String(zhValue(row.summary_json) === "-" ? "" : zhValue(row.summary_json)),
    content: String(zhValue(row.content_json) === "-" ? "" : zhValue(row.content_json)),
    category_id: row.category_id || 0,
    is_published: row.is_published,
  };
  showPost.value = true;
}

async function handlePost() {
  if (!postForm.value.slug || !postForm.value.title || !postForm.value.content) return;
  postSaving.value = true;
  try {
    if (editingPost.value) {
      const { error } = await updatePost(editingPost.value.id, {
        title_json: JSON.stringify({ zh_CN: postForm.value.title }),
        summary_json: postForm.value.summary ? JSON.stringify({ zh_CN: postForm.value.summary }) : undefined,
        content_json: JSON.stringify({ zh_CN: postForm.value.content }),
        category_id: postForm.value.category_id || undefined,
      });
      if (!error) {
        window.$message?.success("文章已更新");
        showPost.value = false;
        loadPosts();
      }
    } else {
      const { error } = await createPost({
        slug: postForm.value.slug,
        type: postForm.value.type,
        title_json: JSON.stringify({ zh_CN: postForm.value.title }),
        summary_json: postForm.value.summary ? JSON.stringify({ zh_CN: postForm.value.summary }) : undefined,
        content_json: JSON.stringify({ zh_CN: postForm.value.content }),
        category_id: postForm.value.category_id || undefined,
        is_published: postForm.value.is_published,
      });
      if (!error) {
        window.$message?.success("文章已创建");
        showPost.value = false;
        loadPosts();
      }
    }
  } finally {
    postSaving.value = false;
  }
}

async function handlePublish(row: any) {
  const { error } = await publishPost(row.id, !row.is_published);
  if (!error) {
    window.$message?.success(row.is_published ? "已下架" : "已发布");
    loadPosts();
  }
}

async function handleDeletePost(id: number) {
  const { error } = await deletePost(id);
  if (!error) {
    window.$message?.success("已删除");
    loadPosts();
  }
}

// ── 文章栏目 ──
const categoryLoading = ref(false);
const categories = ref<any[]>([]);
const showCategory = ref(false);
const categorySaving = ref(false);
const categoryForm = ref({ name: "", slug: "", sort: 0 });
const editingCategory = ref<any>(null);

function categoryName(id: number): string {
  return categories.value.find((c) => c.id === id)?.name || `#${id}`;
}

const categoryOptions = computed(() => categories.value.map((c) => ({ label: c.name, value: c.id })));

const categoryColumns: DataTableColumns<any> = [
  {
    title: "排序",
    key: "sort",
    width: 96,
    render: (row, rowIndex) =>
      h("div", { class: "flex items-center gap-4px" }, [
        h(
          NButton,
          { size: "tiny", quaternary: true, disabled: rowIndex === 0, onClick: () => moveCategory(row, -1) },
          { default: () => "↑" },
        ),
        h(
          NButton,
          { size: "tiny", quaternary: true, disabled: rowIndex === categories.value.length - 1, onClick: () => moveCategory(row, 1) },
          { default: () => "↓" },
        ),
        h("span", { class: "text-12px color-gray" }, String(row.sort ?? 0)),
      ]),
  },
  { title: "名称", key: "name", minWidth: 140, render: (row) => h("span", { class: "font-500" }, row.name) },
  { title: "slug", key: "slug", width: 130, ellipsis: true, render: (row) => h("span", { class: "text-12px color-gray" }, row.slug) },
  {
    title: "操作",
    key: "actions",
    width: 110,
    render: (row) =>
      h("div", { class: "flex gap-4px" }, [
        canWrite() ? h(NButton, { size: "tiny", onClick: () => openEditCategory(row) }, { default: () => "编辑" }) : null,
        canWrite()
          ? h(NPopconfirm, { onPositiveClick: () => handleDeleteCategory(row) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }), default: () => "确定删除该栏目？" })
          : null,
      ]),
  },
];

async function loadCategories() {
  categoryLoading.value = true;
  try {
    const { data, error } = await fetchPostCategories();
    if (!error && data) categories.value = (data as any).categories || [];
  } finally {
    categoryLoading.value = false;
  }
}

function openCreateCategory() {
  editingCategory.value = null;
  categoryForm.value = {
    name: "",
    slug: "",
    sort: categories.value.reduce((m, c) => Math.max(m, c.sort || 0), 0) + 1,
  };
  showCategory.value = true;
}

function openEditCategory(row: any) {
  editingCategory.value = row;
  categoryForm.value = { name: row.name, slug: row.slug, sort: row.sort };
  showCategory.value = true;
}

async function handleCategory() {
  if (!categoryForm.value.name) return;
  categorySaving.value = true;
  try {
    if (editingCategory.value) {
      const { error } = await updatePostCategory(editingCategory.value.id, { name: categoryForm.value.name, sort: categoryForm.value.sort });
      if (!error) {
        window.$message?.success("栏目已更新");
        showCategory.value = false;
        loadCategories();
      }
    } else {
      const { error } = await createPostCategory({ name: categoryForm.value.name, slug: categoryForm.value.slug, sort: categoryForm.value.sort });
      if (!error) {
        window.$message?.success("栏目已创建");
        showCategory.value = false;
        loadCategories();
      }
    }
  } finally {
    categorySaving.value = false;
  }
}

async function handleDeleteCategory(row: any) {
  if (posts.value.some((p) => p.category_id === row.id)) {
    window.$message?.warning("该栏目下存在文章，请先在文章中移除栏目");
    return;
  }
  const { error } = await deletePostCategory(row.id);
  if (!error) {
    window.$message?.success("已删除");
    loadCategories();
  }
}

// 上移/下移：与相邻栏目互换 sort 并保存（sort 保持唯一递增，交换可靠）
async function moveCategory(row: any, dir: -1 | 1) {
  const idx = categories.value.findIndex((c) => c.id === row.id);
  const target = categories.value[idx + dir];
  if (!target) return;
  const rowSort = row.sort ?? 0;
  const targetSort = target.sort ?? 0;
  const pending = [
    updatePostCategory(row.id, { sort: targetSort }),
    updatePostCategory(target.id, { sort: rowSort }),
  ];
  const results = await Promise.all(pending);
  if (!results.some((r) => r.error)) {
    window.$message?.success("排序已更新");
    loadCategories();
  }
}

onMounted(() => {
  loadBanners();
  loadPosts();
  loadCategories();
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <div>
      <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
        <div class="flex flex-wrap items-center gap-12px">
          <span class="text-13px font-500">首页横幅</span>
          <FilterTabs v-model:value="bannerFilter" :options="bannerTabs" :counts="bannerCounts" size="small" />
        </div>
        <NButton v-if="canWrite()" size="tiny" type="primary" @click="showBanner = true">新增横幅</NButton>
      </div>
      <NDataTable :columns="bannerColumns" :data="filteredBanners" :loading="bannerLoading" size="small"  :max-height="540" />
    </div>
    <div>
      <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
        <div class="flex flex-wrap items-center gap-12px">
          <span class="text-13px font-500">公告/文章</span>
          <FilterTabs v-model:value="postFilter" :options="postTabs" :counts="postCounts" size="small" />
        </div>
        <NButton
          v-if="canWrite()"
          size="tiny"
          type="primary"
          @click="((editingPost = null), (postForm = { slug: '', type: 'notice', title: '', summary: '', content: '', category_id: 0, is_published: true }), (showPost = true))"
        >
          新增文章
        </NButton>
      </div>
      <NDataTable :columns="postColumns" :data="filteredPosts" :loading="postLoading" size="small"  :max-height="540" />
    </div>
    <div>
      <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
        <div class="flex flex-wrap items-center gap-12px">
          <span class="text-13px font-500">文章栏目</span>
          <span class="text-12px color-gray">用于前台文章页筛选；栏目下有文章时不可删除</span>
        </div>
        <NButton v-if="canWrite()" size="tiny" type="primary" @click="openCreateCategory">新增栏目</NButton>
      </div>
      <NDataTable :columns="categoryColumns" :data="categories" :loading="categoryLoading" size="small"  :max-height="540" />
    </div>

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

    <NModal
      v-model:show="showPost"
      preset="dialog"
      :title="editingPost ? `编辑文章：${editingPost.slug}` : '新增公告/文章'"
      style="width: 680px"
      display-directive="show"
    >
      <NForm :model="postForm" label-placement="left" label-width="72">
        <NFormItem label="slug" required>
          <NInput v-model:value="postForm.slug" placeholder="如 notice-0818" :disabled="!!editingPost" />
        </NFormItem>
        <NFormItem label="类型">
          <NSelect v-model:value="postForm.type" :options="[{ label: '公告', value: 'notice' }, { label: '博客', value: 'blog' }]" :disabled="!!editingPost" />
        </NFormItem>
        <NFormItem label="栏目">
          <NSelect v-model:value="postForm.category_id" :options="categoryOptions" clearable filterable placeholder="不选 = 未分类" />
        </NFormItem>
        <NFormItem label="标题" required>
          <NInput v-model:value="postForm.title" />
        </NFormItem>
        <NFormItem label="摘要">
          <NInput v-model:value="postForm.summary" />
        </NFormItem>
        <NFormItem label="正文" required>
          <div class="w-full">
            <MdHtmlEditor v-model="postForm.content" height="300px" />
          </div>
        </NFormItem>
        <NFormItem label="直接发布">
          <NSwitch v-model:value="postForm.is_published" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showPost = false">取消</NButton>
        <NButton type="primary" :loading="postSaving" @click="handlePost">{{ editingPost ? "保存" : "创建" }}</NButton>
      </template>
    </NModal>

    <NModal v-model:show="showCategory" preset="dialog" :title="editingCategory ? `编辑栏目：${editingCategory.name}` : '新增栏目'" style="width: 420px">
      <NForm :model="categoryForm" label-placement="left" label-width="72">
        <NFormItem label="名称" required>
          <NInput v-model:value="categoryForm.name" placeholder="如 使用帮助 / 平台公告" />
        </NFormItem>
        <NFormItem label="slug" :required="!editingCategory">
          <NInput v-model:value="categoryForm.slug" placeholder="如 help（唯一标识，创建后不可改）" :disabled="!!editingCategory" />
        </NFormItem>
        <NFormItem label="排序">
          <NInputNumber v-model:value="categoryForm.sort" class="w-full" :min="0" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showCategory = false">取消</NButton>
        <NButton type="primary" :loading="categorySaving" @click="handleCategory">{{ editingCategory ? "保存" : "创建" }}</NButton>
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
