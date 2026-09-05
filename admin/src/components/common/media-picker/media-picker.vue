<script setup lang="ts">
/**
 * 素材管理选择器（ 前端面核心）：
 * 左：素材网格（选择/分页/搜索/上传/外链导入/批量移动/批量删除[引用确认两段式]）
 * 右：分类面板（全部/未分类/分类树；新建/重命名/删除[非空拒绝]）
 * 单例挂载（App.vue）；pickMedia() Promise API 呼出；管理操作就地生效。
 */
import { ref, reactive, computed, watch } from "vue";
import { NButton, NTag, NPopconfirm, NInput, NModal, NDropdown } from "naive-ui";
import TablePager from "@/components/common/table-pager.vue";
import type { UploadCustomRequestOptions, DropdownOption } from "naive-ui";
import { resolveMediaUrl } from "@/utils/media";
import {
  fetchMediaList,
  fetchMediaCategories,
  createMediaCategory,
  renameMediaCategory,
  moveMediaCategory,
  deleteMediaCategory,
  uploadMedia,
  importMediaFromURL,
  renameMedia,
  moveMedia,
  deleteMedia,
} from "@/service/api";
import type { MediaItem, MediaCategory } from "@/service/api";
import { mediaPickerState, settleMediaPicker } from "./index";

const loading = ref(false);
const items = ref<MediaItem[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(24);
const keyword = ref("");

// 当前视图：all | uncategorized | category:<id>
type View = "all" | "uncategorized" | { category: number };
const view = ref<View>("all");
const viewKey = computed(() =>
  view.value === "all"
    ? "all"
    : view.value === "uncategorized"
      ? "uncategorized"
      : `cat:${view.value.category}`,
);
const currentCategoryId = computed(() =>
  typeof view.value === "object" ? view.value.category : 0,
);

// 分类
const categories = ref<MediaCategory[]>([]);
const showCatCreate = ref(false);
const newCatName = ref("");
const newCatParent = ref<number | null>(null);
const renamingCat = ref<MediaCategory | null>(null);
const renameCatText = ref("");
// 移动分类（大厂「移动到」树选择器模式：目标=根或任一非子孙分类）
const movingCat = ref<MediaCategory | null>(null);
const moveCatTarget = ref<number | null>(null); // null = 根分类
// 分类删除确认（下拉菜单无法包 Popconfirm → NModal）
const deleteCatConfirm = reactive({ show: false, cat: null as MediaCategory | null });

// 选择集（按 id；跨分类/跨页保留）
const selectedIds = ref<Set<number>>(new Set());
// 本页全选态（批量操作/全部删除入口）
const pageAllSelected = computed(
  () => items.value.length > 0 && items.value.every((i) => selectedIds.value.has(i.id)),
);

function togglePageAll() {
  const next = new Set(selectedIds.value);
  if (pageAllSelected.value) {
    for (const i of items.value) next.delete(i.id);
  } else {
    for (const i of items.value) next.add(i.id);
  }
  selectedIds.value = next;
}

// 上传 / 导入
const uploading = ref(0);
const showImport = ref(false);
const importURL = ref("");
const importing = ref(false);

// 删除确认（两段式：被引用清单）
const deleteConfirm = reactive({ show: false, ids: [] as number[], referenced: [] as MediaItem[] });

// 单素材改名
const renamingMedia = ref<MediaItem | null>(null);
const renameMediaText = ref("");

// 批量移动
const showMove = ref(false);
const moveTarget = ref<number | null>(null);

const treeCategories = computed(() => buildTree(categories.value));

function buildTree(
  rows: MediaCategory[],
): (MediaCategory & { children: MediaCategory[]; depth: number })[] {
  const map = new Map<number, MediaCategory & { children: MediaCategory[]; depth: number }>();
  for (const r of rows) map.set(r.id, { ...r, children: [], depth: 0 });
  const roots: (MediaCategory & { children: MediaCategory[]; depth: number })[] = [];
  for (const node of map.values()) {
    if (node.parent_id && map.has(node.parent_id)) {
      const parent = map.get(node.parent_id)!;
      node.depth = parent.depth + 1;
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
}

function flattenTree(nodes: { children: any[] }[], out: any[] = []) {
  for (const n of nodes) {
    out.push(n);
    if (n.children?.length) flattenTree(n.children, out);
  }
  return out;
}

async function loadList() {
  loading.value = true;
  try {
    const params: Record<string, any> = { page: page.value, page_size: pageSize.value };
    if (keyword.value) params.keyword = keyword.value;
    if (view.value === "uncategorized") params.uncategorized = true;
    else if (typeof view.value === "object") params.category_id = view.value.category;
    const { data, error } = await fetchMediaList(params);
    if (!error && data) {
      items.value = (data as any).items || [];
      total.value = (data as any).total || 0;
    }
  } finally {
    loading.value = false;
  }
}

async function loadCategories() {
  const { data, error } = await fetchMediaCategories();
  if (!error && data) categories.value = (data as any).categories || [];
}

function switchView(v: View) {
  view.value = v;
  page.value = 1;
  loadList();
}

watch(
  () => mediaPickerState.show,
  (show) => {
    if (show) {
      // 打开时复位视图与选择
      view.value = "all";
      page.value = 1;
      keyword.value = "";
      selectedIds.value = new Set();
      loadCategories();
      loadList();
    }
  },
);

function toggleSelect(item: MediaItem, e: MouseEvent) {
  e.stopPropagation();
  if (!mediaPickerState.multiple) {
    selectedIds.value = new Set([item.id]);
    return;
  }
  const next = new Set(selectedIds.value);
  if (next.has(item.id)) next.delete(item.id);
  else next.add(item.id);
  selectedIds.value = next;
}

function isSelected(item: MediaItem) {
  return selectedIds.value.has(item.id);
}

function handleConfirm() {
  const urls = items.value.filter((i) => selectedIds.value.has(i.id)).map((i) => i.url);
  if (!urls.length) {
    window.$message?.warning("请先选择图片");
    return;
  }
  settleMediaPicker(urls);
}

function handleCancel() {
  settleMediaPicker(null);
}

// ── 上传（进当前分类）──

async function customRequest({ file, onFinish, onError }: UploadCustomRequestOptions) {
  uploading.value += 1;
  try {
    const raw = file.file as File;
    const base64 = await fileToBase64(raw);
    const { data, error } = await uploadMedia({
      name: raw.name,
      content_type: raw.type,
      data_base64: base64,
      category_id: currentCategoryId.value || undefined,
    });
    if (error || !data) {
      onError();
      return;
    }
    window.$message?.success("上传成功");
    page.value = 1;
    await loadList();
    // 上传即选中（单选覆盖，多选追加）
    if (data.id) {
      if (mediaPickerState.multiple) selectedIds.value = new Set([...selectedIds.value, data.id]);
      else selectedIds.value = new Set([data.id]);
    }
    onFinish();
  } finally {
    uploading.value -= 1;
  }
}

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result);
      resolve(result.slice(result.indexOf(",") + 1));
    };
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

function beforeUpload({ file }: { file: { name: string } }) {
  const ext = file.name.split(".").pop()?.toLowerCase() || "";
  if (!["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(ext)) {
    window.$message?.error("仅支持图片文件（png/jpg/jpeg/gif/webp/svg）");
    return false;
  }
  return true;
}

// ── 外链导入 ──

async function handleImport() {
  if (!importURL.value.trim()) return;
  importing.value = true;
  try {
    const { data, error } = await importMediaFromURL({
      url: importURL.value.trim(),
      category_id: currentCategoryId.value || undefined,
    });
    if (!error) {
      window.$message?.success("导入成功");
      showImport.value = false;
      importURL.value = "";
      page.value = 1;
      loadList();
      if ((data as any)?.id) {
        if (mediaPickerState.multiple)
          selectedIds.value = new Set([...selectedIds.value, (data as any).id]);
        else selectedIds.value = new Set([(data as any).id]);
      }
    }
  } finally {
    importing.value = false;
  }
}

// ── 批量移动 ──

async function handleMove() {
  if (!selectedIds.value.size || moveTarget.value === null) return;
  const { error } = await moveMedia([...selectedIds.value], moveTarget.value);
  if (!error) {
    window.$message?.success("已移动");
    showMove.value = false;
    moveTarget.value = null;
    loadList();
  }
}

// ── 批量删除（两段式引用确认）──

async function handleDelete(force = false) {
  const ids = deleteConfirm.show ? deleteConfirm.ids : [...selectedIds.value];
  if (!ids.length) return;
  const { data, error } = await deleteMedia(ids, force);
  if (error) return;
  const reply = data as any;
  if (reply?.need_confirm) {
    deleteConfirm.ids = ids;
    deleteConfirm.referenced = reply.referenced || [];
    deleteConfirm.show = true;
    return;
  }
  window.$message?.success(`已删除 ${reply?.deleted ?? ids.length} 项`);
  deleteConfirm.show = false;
  selectedIds.value = new Set();
  loadList();
}

// ── 分类管理 ──

async function handleCreateCategory() {
  if (!newCatName.value.trim()) return;
  const { error } = await createMediaCategory({
    name: newCatName.value.trim(),
    parent_id: newCatParent.value || undefined,
  });
  if (!error) {
    window.$message?.success("分类已创建");
    newCatName.value = "";
    newCatParent.value = null;
    showCatCreate.value = false;
    loadCategories();
  }
}

async function handleRenameCategory() {
  if (!renamingCat.value || !renameCatText.value.trim()) return;
  const { error } = await renameMediaCategory(renamingCat.value.id, renameCatText.value.trim());
  if (!error) {
    window.$message?.success("已重命名");
    renamingCat.value = null;
    loadCategories();
  }
}

async function handleDeleteCategory(cat: MediaCategory) {
  const { error } = await deleteMediaCategory(cat.id);
  if (!error) {
    window.$message?.success("分类已删除");
    if (typeof view.value === "object" && view.value.category === cat.id) view.value = "all";
    deleteCatConfirm.show = false;
    loadCategories();
    loadList();
  }
}

// ── 分类行 ⋯ 菜单（重命名/移动/删除）──

const catMenuOptions: DropdownOption[] = [
  { label: "重命名", key: "rename" },
  { label: "移动分类到…", key: "move" },
  {
    label: "删除分类",
    key: "delete",
    divided: true,
    props: { style: "color: var(--error-color, #d03050)" },
  },
];

function handleCatMenu(key: string | number, cat: MediaCategory) {
  switch (key) {
    case "rename":
      renamingCat.value = cat;
      renameCatText.value = cat.name;
      break;
    case "move":
      movingCat.value = cat;
      moveCatTarget.value = cat.parent_id || null;
      break;
    case "delete":
      deleteCatConfirm.cat = cat;
      deleteCatConfirm.show = true;
      break;
  }
}

// descendantIds 自身 + 全部子孙（BFS）——移动目标的排除集（前端防环第一道；
// 服务端 MoveCategory 追溯父链 fail-closed 兜底）
function descendantIds(id: number): Set<number> {
  const out = new Set<number>([id]);
  const queue = [id];
  while (queue.length) {
    const cur = queue.shift()!;
    for (const c of categories.value) {
      if (c.parent_id === cur && !out.has(c.id)) {
        out.add(c.id);
        queue.push(c.id);
      }
    }
  }
  return out;
}

// moveCatTree 可选目标树（排除移动分类自身子树；含缩进深度）
const moveCatTree = computed(() => {
  if (!movingCat.value) return [];
  const excluded = descendantIds(movingCat.value.id);
  const filterTree = (nodes: any[]): any[] =>
    nodes
      .filter((n) => !excluded.has(n.id))
      .map((n) => ({ ...n, children: filterTree(n.children) }));
  return flattenTree(filterTree(treeCategories.value));
});

// 当前父分类名（移动弹窗位置提示）
const moveCatParentName = computed(() => {
  if (!movingCat.value) return "";
  if (!movingCat.value.parent_id) return "根分类";
  return categories.value.find((c) => c.id === movingCat.value!.parent_id)?.name || "根分类";
});

async function handleMoveCategory() {
  if (!movingCat.value) return;
  const target = moveCatTarget.value ?? 0; // null = 根
  if (target === movingCat.value.id) return;
  const { error } = await moveMediaCategory(movingCat.value.id, target);
  if (!error) {
    const to = target === 0 ? "根分类" : categories.value.find((c) => c.id === target)?.name || "";
    window.$message?.success(`已移动到「${to}」`);
    movingCat.value = null;
    loadCategories();
  }
}

// ── 单素材操作 ──

function copyURL(url: string) {
  navigator.clipboard?.writeText(url).then(
    () => window.$message?.success("链接已复制"),
    () => window.$message?.error("复制失败"),
  );
}

async function handleRenameMedia() {
  if (!renamingMedia.value || !renameMediaText.value.trim()) return;
  const { error } = await renameMedia(renamingMedia.value.id, renameMediaText.value.trim());
  if (!error) {
    window.$message?.success("已重命名");
    renamingMedia.value = null;
    loadList();
  }
}

const categorySelectOptions = computed(() => [
  { label: "未分类", value: 0 },
  ...flattenTree(treeCategories.value).map((c) => ({
    label: `${"　".repeat(c.depth)}${c.name}`,
    value: c.id,
  })),
]);
</script>

<template>
  <NModal
    :show="mediaPickerState.show"
    preset="card"
    :title="mediaPickerState.multiple ? '选择图片（可多选）' : '选择图片'"
    class="w-960px"
    :mask-closable="false"
    @update:show="(v: boolean) => !v && handleCancel()"
  >
    <div class="flex gap-16px" style="height: 560px">
      <!-- 左：素材网格 -->
      <div class="flex min-w-0 flex-1 flex-col gap-12px">
        <div class="flex flex-wrap items-center gap-8px">
          <NUpload
            :custom-request="customRequest"
            :show-file-list="false"
            accept="image/*"
            :before-upload="beforeUpload as any"
          >
            <NButton v-auth="'media:upload'" size="small" type="primary" :loading="uploading > 0">上传图片</NButton>
          </NUpload>
          <NButton v-auth="'media:upload'" size="small" @click="showImport = true">外链导入</NButton>
          <NButton size="small" quaternary @click="togglePageAll">
            {{ pageAllSelected ? "取消全选" : "全选本页" }}
          </NButton>
          <NButton v-auth="'media:write'" size="small" :disabled="!selectedIds.size" @click="showMove = true">
            移动到分类{{ selectedIds.size ? `（${selectedIds.size}）` : "" }}
          </NButton>
          <NPopconfirm @positive-click="handleDelete(false)">
            <template #trigger>
              <NButton v-auth="'media:delete'" size="small" type="error" ghost :disabled="!selectedIds.size">
                删除{{ selectedIds.size ? `（${selectedIds.size}）` : "" }}
              </NButton>
            </template>
            确定删除选中的 {{ selectedIds.size }} 项素材？（被商品引用的会再次确认）
          </NPopconfirm>
          <div class="ml-auto w-180px">
            <NInput
              v-model:value="keyword"
              size="small"
              placeholder="搜索素材名"
              clearable
              @keyup.enter="
                page = 1;
                loadList();
              "
            />
          </div>
        </div>

        <NScrollbar class="flex-1">
          <div v-if="loading" class="flex h-320px items-center justify-center text-gray-400">
            加载中…
          </div>
          <NEmpty
            v-else-if="!items.length"
            class="mt-80px"
            description="暂无素材，点击「上传图片」或「外链导入」"
          />
          <div v-else class="grid grid-cols-4 gap-10px sm:grid-cols-5">
            <div
              v-for="item in items"
              :key="item.id"
              class="group relative cursor-pointer overflow-hidden rounded-6px border"
              :class="isSelected(item) ? 'border-primary' : 'border-gray-200 dark:border-gray-700'"
              :style="isSelected(item) ? 'box-shadow: 0 0 0 2px var(--primary-color)' : ''"
              @click="toggleSelect(item, $event)"
            >
              <NImage
                :src="resolveMediaUrl(item.url)"
                width="100%"
                height="110"
                object-fit="cover"
                :preview-disabled="true"
              />
              <div class="flex items-center justify-between px-4px py-2px text-11px text-gray-500">
                <span class="truncate">{{ item.name }}</span>
                <NTag v-if="item.ref_count > 0" size="tiny" type="warning" :bordered="false"
                  >引用{{ item.ref_count }}</NTag
                >
              </div>
              <!-- 选择角标 -->
              <div
                class="absolute right-4px top-4px h-18px w-18px flex items-center justify-center rounded-full text-12px"
                :class="
                  isSelected(item)
                    ? 'bg-primary text-white'
                    : 'bg-black/30 text-transparent group-hover:bg-black/50'
                "
              >
                ✓
              </div>
              <!-- hover 操作 -->
              <div
                class="absolute inset-x-0 bottom-20px hidden justify-center gap-4px group-hover:flex"
              >
                <NButton size="tiny" quaternary @click.stop="copyURL(item.url)">复制链接</NButton>
                <NButton
                  v-auth="'media:write'"
                  size="tiny"
                  quaternary
                  @click.stop="((renamingMedia = item), (renameMediaText = item.name))"
                >
                  改名
                </NButton>
              </div>
            </div>
          </div>
        </NScrollbar>

        <TablePager
          v-model:page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[24, 48, 96]"
          @change="loadList"
        />
      </div>

      <!-- 右：分类面板 -->
      <div
        class="flex w-190px shrink-0 flex-col border-l border-gray-200 pl-12px dark:border-gray-700"
      >
        <div class="mb-8px flex items-center justify-between">
          <span class="text-13px font-medium">素材分类</span>
          <NButton
            v-auth="'media:write'"
            size="tiny"
            quaternary
            type="primary"
            @click="showCatCreate = !showCatCreate"
            >+ 新建</NButton
          >
        </div>

        <!-- 新建分类 -->
        <div v-if="showCatCreate" class="mb-8px flex flex-col gap-4px">
          <NInput
            v-model:value="newCatName"
            size="small"
            placeholder="分类名称"
            @keyup.enter="handleCreateCategory"
          />
          <NSelect
            v-model:value="newCatParent"
            size="small"
            placeholder="父分类（可选）"
            clearable
            :options="categorySelectOptions.filter((o: any) => o.value !== 0)"
          />
          <NButton size="tiny" type="primary" @click="handleCreateCategory">创建</NButton>
        </div>

        <NScrollbar class="flex-1">
          <div
            class="flex cursor-pointer items-center justify-between rounded-4px px-8px py-5px text-13px"
            :class="
              viewKey === 'all'
                ? 'bg-primary-100 font-medium text-primary dark:bg-darkBorderDark'
                : 'hover:bg-gray-100 dark:hover:bg-gray-800'
            "
            @click="switchView('all')"
          >
            <span>全部素材</span>
            <span class="text-11px text-gray-400">{{
              total && viewKey === "all" ? total : ""
            }}</span>
          </div>
          <div
            class="flex cursor-pointer items-center justify-between rounded-4px px-8px py-5px text-13px"
            :class="
              viewKey === 'uncategorized'
                ? 'bg-primary-100 font-medium text-primary dark:bg-darkBorderDark'
                : 'hover:bg-gray-100 dark:hover:bg-gray-800'
            "
            @click="switchView('uncategorized')"
          >
            <span>未分类</span>
          </div>

          <template v-for="cat in flattenTree(treeCategories)" :key="cat.id">
            <div
              class="group flex cursor-pointer items-center justify-between rounded-4px px-8px py-5px text-13px"
              :class="
                viewKey === `cat:${cat.id}`
                  ? 'bg-primary-100 font-medium text-primary dark:bg-darkBorderDark'
                  : 'hover:bg-gray-100 dark:hover:bg-gray-800'
              "
              :style="{ paddingLeft: `${8 + cat.depth * 12}px` }"
              @click="switchView({ category: cat.id })"
            >
              <span class="truncate">{{ cat.name }}</span>
              <span class="hidden group-hover:flex">
                <NDropdown
                  :options="catMenuOptions"
                  trigger="click"
                  @select="(key: string | number) => handleCatMenu(key, cat)"
                >
                  <NButton v-auth="'media:write'" size="tiny" quaternary @click.stop>⋯</NButton>
                </NDropdown>
              </span>
            </div>
          </template>
          <NEmpty v-if="!categories.length" size="small" class="mt-24px" description="暂无分类" />
        </NScrollbar>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-12px">
        <NButton @click="handleCancel">取消</NButton>
        <NButton type="primary" :disabled="!selectedIds.size" @click="handleConfirm">
          确定{{ selectedIds.size ? `（已选 ${selectedIds.size} 张）` : "" }}
        </NButton>
      </div>
    </template>

    <!-- 外链导入 -->
    <NModal v-model:show="showImport" preset="dialog" title="外链导入" style="width: 480px">
      <NInput
        v-model:value="importURL"
        placeholder="图片 URL（服务端 SSRF 防护 + 安全三件套校验）"
      />
      <template #action>
        <NButton @click="showImport = false">取消</NButton>
        <NButton type="primary" :loading="importing" @click="handleImport">导入</NButton>
      </template>
    </NModal>

    <!-- 批量移动 -->
    <NModal v-model:show="showMove" preset="dialog" title="移动到分类" style="width: 420px">
      <NSelect
        v-model:value="moveTarget"
        :options="categorySelectOptions"
        placeholder="选择目标分类"
      />
      <template #action>
        <NButton @click="showMove = false">取消</NButton>
        <NButton type="primary" @click="handleMove">移动</NButton>
      </template>
    </NModal>

    <!-- 引用确认删除（两段式第二段） -->
    <NModal v-model:show="deleteConfirm.show" preset="dialog" title="删除确认" style="width: 520px">
      <div class="mb-8px">
        以下 <b>{{ deleteConfirm.referenced.length }}</b> 张素材正被商品引用（共选中
        {{ deleteConfirm.ids.length }} 张），删除后相关商品图片将裂图：
      </div>
      <div class="max-h-200px overflow-auto rounded-4px bg-gray-50 p-8px dark:bg-gray-800">
        <div
          v-for="item in deleteConfirm.referenced"
          :key="item.id"
          class="flex items-center gap-8px py-2px text-12px"
        >
          <img :src="resolveMediaUrl(item.url)" class="h-24px w-24px rounded-2px object-cover" />
          <span class="truncate">{{ item.name }}</span>
          <NTag size="tiny" type="warning">引用 {{ item.ref_count }} 处</NTag>
        </div>
      </div>
      <template #action>
        <NButton @click="deleteConfirm.show = false">取消</NButton>
        <NButton type="error" @click="handleDelete(true)">仍要删除</NButton>
      </template>
    </NModal>

    <!-- 素材改名 -->
    <NModal
      :show="!!renamingMedia"
      preset="dialog"
      title="素材改名"
      style="width: 420px"
      @update:show="(v: boolean) => !v && (renamingMedia = null)"
    >
      <NInput v-model:value="renameMediaText" @keyup.enter="handleRenameMedia" />
      <template #action>
        <NButton @click="renamingMedia = null">取消</NButton>
        <NButton type="primary" @click="handleRenameMedia">确定</NButton>
      </template>
    </NModal>

    <!-- 分类重命名 -->
    <NModal
      :show="!!renamingCat"
      preset="dialog"
      title="分类重命名"
      style="width: 420px"
      @update:show="(v: boolean) => !v && (renamingCat = null)"
    >
      <NInput v-model:value="renameCatText" @keyup.enter="handleRenameCategory" />
      <template #action>
        <NButton @click="renamingCat = null">取消</NButton>
        <NButton type="primary" @click="handleRenameCategory">确定</NButton>
      </template>
    </NModal>

    <!-- 移动分类（树选择器；排除自身子树防环；根分类=移到顶层） -->
    <NModal
      :show="!!movingCat"
      preset="dialog"
      title="移动分类"
      style="width: 460px"
      @update:show="(v: boolean) => !v && (movingCat = null)"
    >
      <div class="mb-8px text-13px text-gray-500">
        「{{ movingCat?.name }}」当前位于：<b>{{ moveCatParentName }}</b
        >，选择新的上级分类：
      </div>
      <NScrollbar class="max-h-280px rounded-4px border border-gray-200 dark:border-gray-700">
        <div
          class="flex cursor-pointer items-center justify-between px-12px py-7px text-13px"
          :class="
            moveCatTarget === null
              ? 'bg-primary-100 font-medium text-primary'
              : 'hover:bg-gray-100 dark:hover:bg-gray-800'
          "
          @click="moveCatTarget = null"
        >
          <span>📁 根分类（作为顶级分类）</span>
          <span v-if="moveCatTarget === null" class="text-primary">✓</span>
        </div>
        <div
          v-for="cat in moveCatTree"
          :key="cat.id"
          class="flex cursor-pointer items-center justify-between py-7px pr-12px text-13px"
          :class="
            moveCatTarget === cat.id
              ? 'bg-primary-100 font-medium text-primary'
              : 'hover:bg-gray-100 dark:hover:bg-gray-800'
          "
          :style="{ paddingLeft: `${12 + cat.depth * 16}px` }"
          @click="moveCatTarget = cat.id"
        >
          <span class="truncate">{{ cat.name }}</span>
          <span v-if="moveCatTarget === cat.id" class="text-primary">✓</span>
        </div>
      </NScrollbar>
      <div class="mt-6px text-12px text-gray-400">不能移动到自身或其子分类下（防环）。</div>
      <template #action>
        <NButton @click="movingCat = null">取消</NButton>
        <NButton type="primary" @click="handleMoveCategory">移动到此处</NButton>
      </template>
    </NModal>

    <!-- 分类删除确认（⋯ 菜单无法包 Popconfirm → NModal） -->
    <NModal
      :show="deleteCatConfirm.show"
      preset="dialog"
      title="删除分类"
      style="width: 420px"
      @update:show="(v: boolean) => !v && (deleteCatConfirm.show = false)"
    >
      确定删除分类「{{ deleteCatConfirm.cat?.name }}」？分类下有素材或子分类时将被拒绝。
      <template #action>
        <NButton @click="deleteCatConfirm.show = false">取消</NButton>
        <NButton
          type="error"
          @click="deleteCatConfirm.cat && handleDeleteCategory(deleteCatConfirm.cat)"
          >删除</NButton
        >
      </template>
    </NModal>
  </NModal>
</template>
