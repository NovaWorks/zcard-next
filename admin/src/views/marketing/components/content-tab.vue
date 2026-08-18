<script setup lang="ts">
// 内容管理：横幅（storefront 首页消费）+ 文章/公告（content:read / write）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NInput, NModal, NForm, NFormItem, NPopconfirm, NSelect, NSwitch, NTag } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchBanners, createBanner, deleteBanner, fetchPosts, createPost, publishPost, deletePost } from "@/service/api";
import { checkAuth } from "@/directives";
import MediaField from "@/components/common/media-picker/media-field.vue";

defineOptions({ name: "ContentTab" });

const canWrite = () => checkAuth("content:write");

// ── 横幅 ──
const bannerLoading = ref(false);
const banners = ref<any[]>([]);
const showBanner = ref(false);
const bannerSaving = ref(false);
const bannerForm = ref({ name: "", position: "top", title_json: "", image: [] as string[], link_type: "url", link_value: "", is_active: true, sort: 0 });

const bannerColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  { title: "名称", key: "name", width: 120 },
  { title: "位置", key: "position", width: 70 },
  { title: "图片", key: "image", width: 200, ellipsis: true, render: (row) => row.image || "-" },
  { title: "跳转", key: "link_value", width: 140, ellipsis: true, render: (row) => row.link_value || "-" },
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
const postForm = ref({ slug: "", type: "notice", title: "", summary: "", content: "", is_published: true });

const postColumns: DataTableColumns<any> = [
  { title: "ID", key: "id", width: 50 },
  { title: "slug", key: "slug", width: 130 },
  {
    title: "类型",
    key: "type",
    width: 70,
    render: (row) => h(NTag, { size: "small", type: row.type === "notice" ? "warning" : "info" }, { default: () => (row.type === "notice" ? "公告" : "博客") }),
  },
  { title: "标题", key: "title_json", minWidth: 160, ellipsis: true },
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

async function handlePost() {
  if (!postForm.value.slug || !postForm.value.title || !postForm.value.content) return;
  postSaving.value = true;
  try {
    const { error } = await createPost({
      slug: postForm.value.slug,
      type: postForm.value.type,
      title_json: JSON.stringify({ zh_CN: postForm.value.title }),
      summary_json: postForm.value.summary ? JSON.stringify({ zh_CN: postForm.value.summary }) : undefined,
      content_json: JSON.stringify({ zh_CN: postForm.value.content }),
      is_published: postForm.value.is_published,
    });
    if (!error) {
      window.$message?.success("文章已创建");
      showPost.value = false;
      loadPosts();
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

onMounted(() => {
  loadBanners();
  loadPosts();
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <div>
      <div class="mb-8px flex items-center gap-8px">
        <span class="text-13px font-500">首页横幅</span>
        <NButton v-if="canWrite()" size="tiny" type="primary" @click="showBanner = true">新增横幅</NButton>
      </div>
      <NDataTable :columns="bannerColumns" :data="banners" :loading="bannerLoading" size="small" />
    </div>
    <div>
      <div class="mb-8px flex items-center gap-8px">
        <span class="text-13px font-500">公告/文章</span>
        <NButton v-if="canWrite()" size="tiny" type="primary" @click="showPost = true">新增文章</NButton>
      </div>
      <NDataTable :columns="postColumns" :data="posts" :loading="postLoading" size="small" />
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
          <NSelect v-model:value="bannerForm.link_type" :options="[{ label: '外部链接', value: 'url' }, { label: '商品', value: 'product' }, { label: '分类', value: 'category' }]" />
        </NFormItem>
        <NFormItem label="跳转目标">
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

    <NModal v-model:show="showPost" preset="dialog" title="新增公告/文章" style="width: 560px">
      <NForm :model="postForm" label-placement="left" label-width="72">
        <NFormItem label="slug" required>
          <NInput v-model:value="postForm.slug" placeholder="如 notice-0818" />
        </NFormItem>
        <NFormItem label="类型">
          <NSelect v-model:value="postForm.type" :options="[{ label: '公告', value: 'notice' }, { label: '博客', value: 'blog' }]" />
        </NFormItem>
        <NFormItem label="标题" required>
          <NInput v-model:value="postForm.title" />
        </NFormItem>
        <NFormItem label="摘要">
          <NInput v-model:value="postForm.summary" />
        </NFormItem>
        <NFormItem label="正文" required>
          <NInput v-model:value="postForm.content" type="textarea" :rows="6" />
        </NFormItem>
        <NFormItem label="直接发布">
          <NSwitch v-model:value="postForm.is_published" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showPost = false">取消</NButton>
        <NButton type="primary" :loading="postSaving" @click="handlePost">创建</NButton>
      </template>
    </NModal>
  </div>
</template>
