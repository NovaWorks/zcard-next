<script setup lang="ts">
// 文章管理（公告/博客）：列表为主视图，栏目作为分类维度收纳进工具栏——
// 状态筛选 + 栏目筛选 + 「栏目管理」入口（弹窗表格含排序/增删改），对齐
// WordPress/掘金 后台「内容列表 + 分类管理入口」的大厂布局。
import { computed, h, onMounted, ref } from "vue";
import { NButton, NDataTable, NInput, NModal, NForm, NFormItem, NInputNumber, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchPosts, createPost, updatePost, publishPost, deletePost, fetchPostCategories, createPostCategory, updatePostCategory, deletePostCategory } from "@/service/api";
import { checkAuth } from "@/directives";
import MdHtmlEditor from "@/components/common/md-html-editor/index.vue";
import FilterTabs from "@/components/common/filter-tabs.vue";

defineOptions({ name: "ArticleManagement" });

const canWrite = () => checkAuth("content:write");

// ── 文章列表 ──
const postLoading = ref(false);
const posts = ref<any[]>([]);
const showPost = ref(false);
const postSaving = ref(false);
const postForm = ref({ slug: "", type: "notice", title: "", summary: "", content: "", category_id: 0, is_published: true });
const editingPost = ref<any>(null);

// 筛选：发布状态（带计数）+ 栏目（下拉，全部栏目=空）
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
const categoryFilter = ref<number | null>(null);
const filteredPosts = computed(() =>
  posts.value.filter(
    (p) =>
      (postFilter.value === "" || (postFilter.value === "published" ? p.is_published : !p.is_published)) &&
      (!categoryFilter.value || p.category_id === categoryFilter.value),
  ),
);

// title_json/content_json → zh_CN 展示值
function zhValue(json: string): string {
  if (!json || json === "-") return "-";
  try {
    const v = JSON.parse(json);
    return typeof v === "object" && v ? v.zh_CN || v.en_US || Object.values(v)[0] || "-" : String(v);
  } catch {
    return json;
  }
}

// ── 文章栏目（分类维度；管理入口=工具栏「栏目管理」弹窗）──
const categories = ref<any[]>([]);
const showCategoryMgr = ref(false);
const showCategory = ref(false);
const categorySaving = ref(false);
const categoryForm = ref({ name: "", slug: "", sort: 0 });
const editingCategory = ref<any>(null);

function categoryName(id: number): string {
  return categories.value.find((c) => c.id === id)?.name || `#${id}`;
}

const categoryOptions = computed(() => categories.value.map((c) => ({ label: c.name, value: c.id })));
const categoryFilterOptions = computed(() => [{ label: "全部栏目", value: 0 }, ...categoryOptions.value]);

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

async function loadPosts() {
  postLoading.value = true;
  try {
    const { data, error } = await fetchPosts();
    if (!error && data) posts.value = (data as any).posts || [];
  } finally {
    postLoading.value = false;
  }
}

async function loadCategories() {
  const { data, error } = await fetchPostCategories();
  if (!error && data) categories.value = (data as any).categories || [];
}

function openCreatePost() {
  editingPost.value = null;
  postForm.value = { slug: "", type: "notice", title: "", summary: "", content: "", category_id: categoryFilter.value || 0, is_published: true };
  showPost.value = true;
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

// ── 栏目管理（弹窗表格）──
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
    title: "文章数",
    key: "post_count",
    width: 80,
    render: (row) => h("span", { class: "text-12px color-gray" }, String(posts.value.filter((p) => p.category_id === row.id).length)),
  },
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
    if (categoryFilter.value === row.id) categoryFilter.value = null;
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
  loadPosts();
  loadCategories();
});
</script>

<template>
  <div class="flex flex-col gap-12px">
    <!-- 工具栏：状态筛选 | 栏目筛选 + 栏目管理 + 新增 -->
    <div class="flex items-center justify-between gap-12px flex-wrap">
      <FilterTabs v-model:value="postFilter" :options="postTabs" :counts="postCounts" size="small" />
      <div class="flex items-center gap-8px">
        <NSelect
          v-model:value="categoryFilter"
          :options="categoryFilterOptions"
          size="small"
          style="width: 150px"
          placeholder="全部栏目"
        />
        <NButton v-if="canWrite()" size="small" @click="showCategoryMgr = true">栏目管理</NButton>
        <NButton v-if="canWrite()" size="small" type="primary" @click="openCreatePost">新增文章</NButton>
      </div>
    </div>

    <NDataTable :columns="postColumns" :data="filteredPosts" :loading="postLoading" size="small" :max-height="560" />

    <!-- 新增/编辑文章 -->
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

    <!-- 栏目管理（分类维度集中管理：排序/增删改） -->
    <NModal v-model:show="showCategoryMgr" preset="dialog" title="栏目管理" style="width: 660px">
      <div class="flex flex-col gap-8px">
        <div class="text-12px text-gray-400">栏目是文章的分类维度，前台文章页按栏目筛选；排序即前台展示顺序。</div>
        <div class="flex justify-end">
          <NButton v-if="canWrite()" size="tiny" dashed @click="openCreateCategory">+ 新增栏目</NButton>
        </div>
        <NDataTable :columns="categoryColumns" :data="categories" size="small" :max-height="360" />
      </div>
      <template #action>
        <NButton @click="showCategoryMgr = false">关闭</NButton>
      </template>
    </NModal>

    <!-- 新增/编辑栏目 -->
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
