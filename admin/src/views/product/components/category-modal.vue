<script setup lang="ts">
/**
 * 分类管理共用视图：独立页面与商品页弹窗复用，保留同一套分类操作。
 * 新建/变更后向父组件抛 refresh 事件（表单下拉联动刷新）。
 */
import { ref, reactive, computed, watch } from "vue";
import MediaField from "@/components/common/media-picker/media-field.vue";
import CategoryIcon from "@/components/common/category-icon.vue";
import { checkAuth } from "@/directives";
import { NCard, NButton, NTag, NInput, NInputNumber, NSelect, NModal, NDropdown, NTooltip, NCheckbox, NPopconfirm, NPopover } from "naive-ui";
import type { DropdownOption } from "naive-ui";
import {
  fetchCategories,
  createCategory,
  updateCategory,
  deleteCategory,
  reorderCategories,
  mergeCategories,
} from "@/service/api";

const props = withDefaults(defineProps<{ show?: boolean; embedded?: boolean }>(), { show: false, embedded: false });
const canWrite = computed(() => checkAuth("catalog:category_write"));
const canDelete = computed(() => checkAuth("catalog:category_delete"));
const search = ref("");
const emit = defineEmits<{
  (e: "update:show", v: boolean): void;
  (e: "refresh"): void;
  (e: "created", id: number): void;
}>();

const loading = ref(false);
const categories = ref<any[]>([]);

// 新建
const showCreate = ref(false);
const newName = ref("");
const newParent = ref<number | null>(null);
const newIcon = ref("");
const creating = ref(false);

// 重命名 / 删除确认
const renaming = ref<any | null>(null);
const renameText = ref("");
const deleteConfirm = reactive({ show: false, cat: null as any });

// ── 图标（本地图标库：内置 emoji 分组，即选即用；前台分类树/胶囊展示）──
const ICON_GROUPS: { label: string; icons: string[] }[] = [
  { label: "常用", icons: ["🏷️", "🛒", "🎁", "⭐", "🔥", "💼", "📦", "💰", "🆕", "💡"] },
  { label: "游戏", icons: ["🎮", "🕹️", "🎰", "🏆", "🎯", "🃏", "🎲", "👾", "🤖", "⚔️"] },
  { label: "影音", icons: ["🎬", "🎵", "📺", "🎧", "📷", "🎤", "🎨", "📚", "🎹", "🎬"] },
  { label: "软件", icons: ["💻", "🖥️", "⌨️", "⚙️", "🔧", "🧩", "💾", "📀", "🧠", "🛡️"] },
  { label: "通讯", icons: ["📱", "☎️", "📞", "💬", "📧", "📡", "🌐", "✉️", "📮", "📡"] },
  { label: "生活", icons: ["🏠", "🚗", "✈️", "🍔", "👕", "💊", "🎓", "⚽", "🐾", "🌸"] },
  { label: "金融", icons: ["💳", "💵", "🏦", "📈", "🪙", "💎", "🧾", "💹", "🤑", "📉"] },
];
const iconPicking = ref<any | null>(null); // 正在选图标的分类（null=面板关闭）

// 自定义图片图标（MediaField 数组值；选定即写入 newIcon=URL——icon 字段 emoji/URL 同存，前台按形态渲染）
const customIconImage = ref<string[]>([]);
// 行内快改面板的自定义图片（应用到 iconPicking 当前分类）
const rowIconImage = ref<string[]>([]);
watch(rowIconImage, (v) => {
  const url = (v || [])[0];
  if (url && iconPicking.value && typeof iconPicking.value === "number") {
    const cat = flatTree.value.find((c: any) => c.id === iconPicking.value);
    if (cat) {
      applyIcon(cat, url);
      rowIconImage.value = [];
    }
  }
});
watch(customIconImage, (v) => {
  const url = (v || [])[0];
  if (url) {
    newIcon.value = url;
    iconPicking.value = null;
  }
});

async function applyIcon(cat: any, icon: string) {
  const { error } = await updateCategory(cat.id, { icon }); // 空串=清除（服务端 optional 语义）
  if (!error) {
    const source = categories.value.find(c => c.id === cat.id);
    if (source) source.icon = icon;
    window.$message?.success(icon ? "图标已更新" : "图标已清除");
    iconPicking.value = null;
    emit("refresh");
  }
}

const visible = computed({
  get: () => props.embedded || props.show,
  set: (v: boolean) => emit("update:show", v),
});

// 树形化（含深度）
const tree = computed(() => {
  const map = new Map<number, any>();
  for (const c of categories.value) map.set(c.id, { ...c, depth: 0, children: [] });
  const roots: any[] = [];
  for (const node of map.values()) {
    if (node.parent_id && map.has(node.parent_id)) {
      const parent = map.get(node.parent_id)!;
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
});

const flatTree = computed(() => {
  const out: any[] = [];
  const walk = (nodes: any[], depth = 0, parentPath = "", ancestorHidden = false) => {
    for (const n of nodes) {
      const path = [parentPath, n.name].filter(Boolean).join(" / ");
      out.push({ ...n, depth, path, ancestorHidden });
      walk(n.children, depth + 1, path, ancestorHidden || n.hide);
    }
  };
  walk(tree.value);
  return out;
});

const filteredTree = computed(() => {
  const query = search.value.trim().toLocaleLowerCase();
  return query ? flatTree.value.filter(c => c.path.toLocaleLowerCase().includes(query)) : flatTree.value;
});

const parentOptions = computed(() => [
  { label: "顶级分类", value: 0 },
  ...flatTree.value.map((c) => ({ label: c.path, value: c.id })),
]);

async function load() {
  loading.value = true;
  try {
    const { data, error } = await fetchCategories();
    if (!error && data) categories.value = (data as any).categories || [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.embedded || props.show,
  (v) => {
    if (v) {
      load();
      resetCreate();
    }
  },
  { immediate: true },
);

function resetCreate() {
  newName.value = "";
  newParent.value = null;
  newIcon.value = "";
  customIconImage.value = [];
  iconPicking.value = null;
  showCreate.value = false;
}

async function handleCreate() {
  if (!newName.value.trim() || creating.value) return;
  creating.value = true;
  try {
    const parentId = newParent.value || 0;
    // 默认排到同级最后（sort = 同级最大 + 1）
    const maxSort = categories.value
      .filter((c) => (c.parent_id || 0) === parentId)
      .reduce((m, c) => Math.max(m, c.sort || 0), 0);
    const { data, error } = await createCategory({
      name: newName.value.trim(),
      parent_id: newParent.value || undefined,
      icon: newIcon.value || undefined,
      sort: maxSort + 1,
    });
    if (!error) {
      window.$message?.success("分类已创建");
      const id = (data as any)?.id || 0;
      resetCreate();
      await load();
      emit("refresh");
      if (id) emit("created", id); // 新建即选中（「建完就用」）
    }
  } finally {
    creating.value = false;
  }
}

async function handleRename() {
  if (!renaming.value || !renameText.value.trim()) return;
  const { error } = await updateCategory(renaming.value.id, { name: renameText.value.trim() });
  if (!error) {
    window.$message?.success("已重命名");
    renaming.value = null;
    load();
    emit("refresh");
  }
}

async function handleDelete() {
  const cat = deleteConfirm.cat;
  if (!cat) return;
  const { error } = await deleteCategory(cat.id);
  if (!error) {
    window.$message?.success("分类已删除");
    deleteConfirm.show = false;
    load();
    emit("refresh");
  }
}

const visibilityBusy = ref<number | null>(null);
async function toggleVisibility(cat: any) {
  if (visibilityBusy.value !== null) return;
  visibilityBusy.value = cat.id;
  try {
    const { error } = await updateCategory(cat.id, { hide: !cat.hide });
    if (!error) {
      await load();
      emit("refresh");
      window.$message?.success(cat.hide ? "已恢复分类显示，商品沿用原上下架状态" : "已隐藏分类、子分类及其商品");
    }
  } finally { visibilityBusy.value = null; }
}

const menuOptions = computed<DropdownOption[]>(() => [
  ...(canWrite.value ? [
    { label: "添加子分类", key: "child" },
    { label: "重命名", key: "rename" },
    { label: "调整上级分类", key: "parent" },
  ] : []),
  ...(canDelete.value ? [{ label: "删除分类", key: "delete", divided: true, props: { style: "color: var(--error-color, #d03050)" } }] : []),
]);
const moving = ref<any | null>(null);
const moveParent = ref<number | null>(0);
const moveSaving = ref(false);
const moveOptions = computed(() => {
  const blocked = new Set<number>();
  const children = new Map<number, number[]>();
  for (const cat of categories.value) {
    const parent = cat.parent_id || 0;
    if (!children.has(parent)) children.set(parent, []);
    children.get(parent)!.push(cat.id);
  }
  const pending = moving.value ? [moving.value.id] : [];
  while (pending.length) {
    const id = pending.pop()!;
    if (blocked.has(id)) continue;
    blocked.add(id);
    pending.push(...(children.get(id) || []));
  }
  return parentOptions.value.map(option => ({ ...option, disabled: blocked.has(option.value) }));
});
async function saveParent() {
  if (!moving.value || moveSaving.value) return;
  moveSaving.value = true;
  try {
    const { error } = await updateCategory(moving.value.id, { parent_id: moveParent.value || 0 });
    if (!error) {
      moving.value = null;
      await load();
      emit("refresh");
      window.$message?.success("上级分类已更新");
    }
  } finally { moveSaving.value = false; }
}

function handleMenu(key: string | number, cat: any) {
  if (key === "child") {
    resetCreate();
    newParent.value = cat.id;
    showCreate.value = true;
  } else if (key === "parent") {
    moving.value = cat;
    moveParent.value = cat.parent_id || 0;
  } else if (key === "rename") {
    renaming.value = cat;
    renameText.value = cat.name;
  } else if (key === "delete") {
    deleteConfirm.cat = cat;
    deleteConfirm.show = true;
  }
}

// ── 拖拽（HTML5 DnD）：拖到行上/下边缘 = 排序插入；拖到行中间 = 设为该分类子级；拖到顶部释放区 = 顶级 ──
const dragId = ref<number | null>(null);
const dragOverId = ref<number | null>(null);
const dropPos = ref<"before" | "after" | "child" | null>(null);
const dropToRoot = ref(false);

function onDragStart(cat: any) {
  dragId.value = cat.id;
}

function onDragOver(cat: any, e: DragEvent) {
  dragOverId.value = cat.id;
  const el = e.currentTarget as HTMLElement;
  const ratio = e.offsetY / el.offsetHeight;
  if (ratio < 0.3) dropPos.value = "before";
  else if (ratio > 0.7) dropPos.value = "after";
  else dropPos.value = "child";
}

function onDragLeave() {
  dragOverId.value = null;
  dropPos.value = null;
}

function resetDragState() {
  dragId.value = null;
  dragOverId.value = null;
  dropPos.value = null;
  dropToRoot.value = false;
}

// 目标是否非法（自身/自身后代）
function invalidTarget(draggedId: number, targetId: number) {
  if (draggedId === targetId) return true;
  const byId = new Map<number, any>();
  for (const c of categories.value) byId.set(c.id, { ...c, children: [] as any[] });
  for (const node of byId.values()) {
    if (node.parent_id && byId.has(node.parent_id)) byId.get(node.parent_id)!.children.push(node);
  }
  const stack = [draggedId];
  while (stack.length) {
    const cur = stack.pop()!;
    if (cur === targetId) return true;
    for (const child of byId.get(cur)?.children || []) stack.push(child.id);
  }
  return false;
}

// 行样式：拖拽悬停时上/下边缘显示蓝色插入线（box-shadow 不改变布局），中间蓝底
function rowClass(cat: any) {
  if (dragId.value === cat.id) return "opacity-40";
  if (dragOverId.value !== cat.id || !dropPos.value) return "";
  return dropPos.value === "child" ? "bg-blue-50 dark:bg-blue-900/30" : "";
}

function rowStyle(cat: any) {
  const shadow =
    dragOverId.value === cat.id && dropPos.value === "before"
      ? "inset 0 2px 0 0 #4098ff"
      : dragOverId.value === cat.id && dropPos.value === "after"
        ? "inset 0 -2px 0 0 #4098ff"
        : "";
  return {
    paddingLeft: `${10 + cat.depth * 16}px`,
    ...(shadow ? { boxShadow: shadow } : {}),
  };
}

// 拖拽排序：把被拖分类插入到目标行之前/之后（同层级 = 目标行的父级）
async function reorderByDrop(draggedId: number, target: any, pos: "before" | "after") {
  const parentId = target.parent_id || 0;
  // 目标层级不能是被拖分类自身或其子孙（会成环）
  if (invalidTarget(draggedId, parentId)) {
    window.$message?.warning("不能把分类移到自身或它的子分类下");
    return;
  }
  const dragged = categories.value.find((c) => c.id === draggedId);
  if (!dragged) return;
  // 目标层级全部兄弟（不含被拖分类；服务端顺序即 sort 升序）
  const siblings = categories.value.filter((c) => (c.parent_id || 0) === parentId && c.id !== draggedId);
  const targetIdx = siblings.findIndex((c) => c.id === target.id);
  if (targetIdx < 0) return;
  siblings.splice(pos === "before" ? targetIdx : targetIdx + 1, 0, dragged);
  const ids = siblings.map((c) => c.id);
  // 顺序无变化（拖回原位）则跳过
  const curIds = categories.value
    .filter((c) => (c.parent_id || 0) === parentId)
    .map((c) => c.id);
  if (ids.join(",") === curIds.join(",")) return;
  const { error } = await reorderCategories(parentId, ids);
  if (!error) {
    window.$message?.success("排序已保存");
    load();
    emit("refresh");
  }
}

async function onDrop(target: any) {
  const from = dragId.value;
  const pos = dropPos.value;
  resetDragState();
  if (!from || !target) return;
  if (pos === "before" || pos === "after") {
    await reorderByDrop(from, target, pos);
    return;
  }
  if (invalidTarget(from, target.id)) {
    window.$message?.warning("不能把分类移到自身或它的子分类下");
    return;
  }
  const { error } = await updateCategory(from, { parent_id: target.id });
  if (!error) {
    window.$message?.success(`已移到「${target.name}」下`);
    load();
    emit("refresh");
  }
}

async function onDropToRoot() {
  const from = dragId.value;
  resetDragState();
  if (!from) return;
  const { error } = await updateCategory(from, { parent_id: 0 });
  if (!error) {
    window.$message?.success("已设为顶级分类");
    load();
    emit("refresh");
  }
}

// ── 批量删除：选择模式（逆序删除保证子分类先于父分类）──
const batchMode = ref(false);
const batchChecked = ref<Set<number>>(new Set());

function toggleBatchMode() {
  batchMode.value = !batchMode.value;
  batchChecked.value.clear();
}

function toggleBatchCheck(id: number, on: boolean) {
  if (on) batchChecked.value.add(id);
  else batchChecked.value.delete(id);
  // 触发 Set 响应（替换引用）
  batchChecked.value = new Set(batchChecked.value);
}

async function handleBatchDelete() {
  const ids = [...batchChecked.value];
  if (!ids.length) return;
  // flatTree 是先序（父在前）——逆序后子分类先删，父分类随后可删
  const order = [...flatTree.value].reverse();
  const sorted = order.filter((c) => batchChecked.value.has(c.id));
  let ok = 0;
  const failed: string[] = [];
  for (const cat of sorted) {
    const { error } = await deleteCategory(cat.id);
    if (!error) ok++;
    else failed.push(cat.name);
  }
  if (ok) window.$message?.success(`已删除 ${ok} 个分类`);
  if (failed.length) window.$message?.warning(`${failed.length} 个未删除（有商品或子分类）：${failed.slice(0, 3).join("、")}${failed.length > 3 ? "…" : ""}`);
  batchMode.value = false;
  batchChecked.value.clear();
  load();
  emit("refresh");
}


const showMerge = ref(false);
const mergeTarget = ref<number | null>(null);
const merging = ref(false);
const mergePreview = ref<{ categories: number; products: number; children: number } | null>(null);
const mergeSources = ref<number[]>([]);
const mergeOptions = computed(() => {
  const excluded = new Set(mergeSources.value);
  for (const cat of flatTree.value) if (excluded.has(cat.parent_id)) excluded.add(cat.id);
  const names = new Map<number, string>();
  return flatTree.value.filter(cat => {
    names.set(cat.id, [names.get(cat.parent_id), cat.name].filter(Boolean).join(' / '));
    return !excluded.has(cat.id);
  }).map(cat => ({ value: cat.id, label: names.get(cat.id)! }));
});
watch(mergeTarget, () => { mergePreview.value = null; });
function openMerge() {
  mergeSources.value = [...batchChecked.value];
  mergeTarget.value = null;
  mergePreview.value = null;
  showMerge.value = true;
}
async function runMerge(preview: boolean) {
  if (!mergeTarget.value || merging.value) return;
  merging.value = true;
  try {
    const { data, error } = await mergeCategories({ source_ids: mergeSources.value, target_id: mergeTarget.value, preview });
    if (error || !data) return;
    if (preview) mergePreview.value = data;
    else {
      window.$message?.success(`已合并 ${data.categories} 个分类，迁移 ${data.products} 件商品`);
      showMerge.value = false;
      batchChecked.value = new Set();
      batchMode.value = false;
      await load();
      emit("refresh");
    }
  } finally { merging.value = false; }
}

// 排序输入框：失焦/回车保存（hide 未传保持原状）
async function onSortBlur(cat: any) {
  const v = cat.sort;
  if (typeof v !== "number" || v < 0) {
    cat.sort = 0;
    return;
  }
  const { error } = await updateCategory(cat.id, { sort: v });
  if (!error) {
    window.$message?.success("排序已保存");
  }
  load();
  emit("refresh");
}
</script>

<template>
  <component :is="embedded ? NCard : NModal"
    v-bind="embedded ? { title: '商品分类' } : { show: visible, preset: 'card', title: '分类管理', style: 'width: 960px; max-width: 94vw' }"
    class="category-manager" :class="{ 'category-manager-page': embedded }"
    @update:show="visible = $event">
    <div class="mb-12px flex flex-wrap items-center gap-8px">
      <NInput v-model:value="search" clearable placeholder="搜索分类名称或完整路径" :input-props="{ 'aria-label': '搜索分类' }" class="min-w-180px flex-1" />
      <NButton :loading="loading" @click="load">刷新</NButton>
    </div>
    <div class="mb-12px flex flex-wrap items-center justify-between gap-8px">
      <div class="flex flex-wrap items-center gap-8px">
        <span class="text-13px text-gray-500">共 {{ flatTree.length }} 个分类<template v-if="search.trim()">，找到 {{ filteredTree.length }} 个</template></span>
        <template v-if="batchMode">
          <NTag size="small" :bordered="false">已选 {{ batchChecked.size }}</NTag>
          <NButton v-auth="'catalog:category_delete'" size="tiny" :disabled="!batchChecked.size" @click="openMerge">合并所选</NButton>
          <NPopconfirm @positive-click="handleBatchDelete">
            <template #trigger>
              <NButton v-auth="'catalog:category_delete'" size="tiny" type="error" :disabled="!batchChecked.size">
                删除所选（{{ batchChecked.size }}）
              </NButton>
            </template>
            删除选中的 {{ batchChecked.size }} 个分类？有商品或子分类的会自动跳过。
          </NPopconfirm>
        </template>
      </div>
      <div class="flex flex-wrap items-center gap-8px">
        <NButton v-auth="'catalog:category_write'" size="small" quaternary @click="toggleBatchMode">
          {{ batchMode ? "退出批量" : "批量管理" }}
        </NButton>
        <NButton v-auth="'catalog:category_write'" size="small" type="primary" @click="showCreate = !showCreate">
          {{ showCreate ? "收起" : "新建分类" }}
        </NButton>
      </div>
    </div>

    <!-- 新建 -->
    <div v-if="showCreate" class="category-create mb-12px flex flex-wrap items-center gap-8px">
      <NPopover trigger="manual" :show="iconPicking === 'new'" placement="bottom-start" style="max-width: 360px">
        <template #trigger>
          <button
            type="button"
            class="cat-icon-btn shrink-0"
            title="选择图标（可选）"
            @click="iconPicking = iconPicking === 'new' ? null : 'new'"
          >
            <CategoryIcon :icon="newIcon" fallback="➕" />
          </button>
        </template>
        <div class="w-320px">
          <div v-for="g in ICON_GROUPS" :key="g.label" class="mb-6px">
            <div class="mb-2px text-11px text-gray-400">{{ g.label }}</div>
            <div class="flex flex-wrap gap-4px">
              <button
                v-for="ic in g.icons"
                :key="ic"
                type="button"
                class="icon-cell"
                :class="{ active: newIcon === ic }"
                @click="newIcon = ic; iconPicking = null"
              >
                {{ ic }}
              </button>
            </div>
          </div>
          <div class="mt-6px border-t border-gray-100 pt-6px dark:border-gray-700">
            <div class="mb-4px text-11px text-gray-400">自定义图片（上传后优先于 emoji）</div>
            <MediaField v-model:value="customIconImage" />
          </div>
          <div class="flex justify-end border-t border-gray-100 pt-6px dark:border-gray-700">
            <NButton size="tiny" quaternary @click="newIcon = ''; iconPicking = null">不使用图标</NButton>
          </div>
        </div>
      </NPopover>
      <NInput
        v-model:value="newName"
        size="small"
        placeholder="分类名称"
        class="flex-1"
        @keyup.enter="handleCreate"
      />
      <NSelect
        v-model:value="newParent"
        size="small"
        placeholder="父分类"
        class="w-280px"
        filterable
        :options="parentOptions"
      />
      <NButton size="small" type="primary" :loading="creating" @click="handleCreate">创建</NButton>
    </div>

    <!-- 树列表（行可拖拽：上/下边缘=排序插入，行中间=设为子级；顶部释放区=顶级） -->
    <div v-if="canWrite" class="mb-4px text-12px text-gray-400">搜索时暂停拖拽；也可通过操作菜单调整上级。拖到行上/下边缘 = 排序；拖到行中间 = 设为子级；拖到顶部虚线区 = 设为顶级</div>
    <div
      v-if="canWrite && !search.trim()"
      class="mb-4px rounded-4px border border-dashed px-10px py-6px text-center text-12px"
      :class="dropToRoot ? 'border-blue-400 bg-blue-50 text-blue-500' : 'border-gray-300 text-gray-400 dark:border-gray-600'"
      @dragover.prevent="dropToRoot = true"
      @dragleave="dropToRoot = false"
      @drop.prevent="onDropToRoot"
    >
      {{ dropToRoot ? '松开设为顶级分类' : '拖拽分类到此处 = 设为顶级' }}
    </div>
    <NScrollbar x-scrollable :style="{ maxHeight: embedded ? 'calc(100vh - 330px)' : '340px', minHeight: '160px' }" class="rounded-4px border border-gray-200 dark:border-gray-700">
      <NEmpty
        v-if="!filteredTree.length && !loading"
        size="small"
        class="mt-40px"
        :description="search.trim() ? '未找到分类，请换个关键词或清空搜索' : '暂无分类'"
      />
      <div
        v-for="cat in filteredTree"
        :key="cat.id"
        :draggable="canWrite && !search.trim()"
        class="category-manage-row group flex cursor-grab items-center gap-8px rounded-4px py-7px pr-8px text-13px hover:bg-gray-100 dark:hover:bg-gray-800 active:cursor-grabbing"
        :class="rowClass(cat)"
        :style="rowStyle(cat)"
        @dragstart="onDragStart(cat)"
        @dragend="resetDragState"
        @dragover.prevent="onDragOver(cat, $event)"
        @dragleave="onDragLeave"
        @drop.prevent="onDrop(cat)"
      >
        <!-- 批量模式勾选框 -->
        <NCheckbox
          v-if="batchMode"
          :checked="batchChecked.has(cat.id)"
          class="shrink-0"
          @update:checked="(v: boolean) => toggleBatchCheck(cat.id, v)"
        />
        <!-- 图标槽：点击开本地图标库（选择即存；支持清除）；前台分类树/胶囊同源展示 -->
        <NPopover v-if="!batchMode && canWrite" trigger="manual" :show="iconPicking === cat.id" placement="right" style="max-width: 360px">
          <template #trigger>
            <button
              type="button"
              class="cat-icon-btn shrink-0"
              :title="cat.icon ? '更换图标' : '设置图标'"
              @click.stop="iconPicking = iconPicking === cat.id ? null : cat.id"
            >
              <CategoryIcon :icon="cat.icon" fallback="➕" />
            </button>
          </template>
          <div class="w-320px">
            <div v-for="g in ICON_GROUPS" :key="g.label" class="mb-6px">
              <div class="mb-2px text-11px text-gray-400">{{ g.label }}</div>
              <div class="flex flex-wrap gap-4px">
                <button
                  v-for="ic in g.icons"
                  :key="ic"
                  type="button"
                  class="icon-cell"
                  :class="{ active: cat.icon === ic }"
                  @click="applyIcon(cat, ic)"
                >
                  {{ ic }}
                </button>
              </div>
            </div>
            <div class="mt-6px border-t border-gray-100 pt-6px dark:border-gray-700">
              <div class="mb-4px text-11px text-gray-400">自定义图片</div>
              <MediaField v-model:value="rowIconImage" />
            </div>
            <div class="flex items-center justify-between border-t border-gray-100 pt-6px dark:border-gray-700">
              <NButton v-auth="'catalog:category_write'" size="tiny" quaternary type="error" @click="applyIcon(cat, '')">
                🗑 清除图标
              </NButton>
              <NButton size="tiny" quaternary @click="iconPicking = null">关闭</NButton>
            </div>
          </div>
        </NPopover>
        <CategoryIcon v-else :icon="cat.icon" />
        <!-- 名称列：占满剩余宽度，超长截断不撑破行；悬浮显示全名 -->
        <div class="category-name flex min-w-0 flex-1 items-center gap-6px">
          <NTooltip  placement="top" :show-arrow="false">
            <template #trigger>
              <span class="category-full-name" :title="cat.path">{{ cat.name }}</span>
            </template>
            {{ cat.name }}
          </NTooltip>
          <span v-if="cat.depth > 0" class="category-path">{{ cat.path }}</span>
        </div>
        <!-- 商品数/排序/操作：固定列宽，不随名称长度漂移 -->
        <NTag size="small" :type="cat.hide || cat.ancestorHidden ? 'warning' : 'success'" :bordered="false">{{ cat.hide ? '已隐藏' : cat.ancestorHidden ? '随父级隐藏' : '显示中' }}</NTag>
        <NPopconfirm v-if="!batchMode" @positive-click="toggleVisibility(cat)">
          <template #trigger><NButton v-auth="'catalog:category_write'" size="small" secondary :loading="visibilityBusy === cat.id" :disabled="visibilityBusy !== null || (cat.ancestorHidden && !cat.hide)">{{ cat.hide ? '恢复显示' : '隐藏下架' }}</NButton></template>
          {{ cat.hide ? `恢复「${cat.name}」的显示？若父级仍隐藏，本分类仍不会展示。商品保留原来的上下架状态。` : `隐藏「${cat.name}」及全部子分类？其中商品将从商城和供货目录隐藏，无法新下单。已有订单继续处理。` }}
        </NPopconfirm>
        <span title="该分类及下级分类的未删除商品总数（含隐藏、下架商品）" class="w-76px shrink-0 text-right text-12px text-gray-500">{{ cat.product_count || 0 }} 件</span>
        <NInputNumber
          v-model:value="cat.sort"
          :disabled="!canWrite"
          :input-props="{ 'aria-label': `${cat.name}排序` }"
          size="tiny"
          :min="0"
          :show-button="false"
          class="w-56px shrink-0"
          placeholder="排序"
          @blur="onSortBlur(cat)"
          @keyup.enter="onSortBlur(cat)"
        />
        <NDropdown v-if="menuOptions.length"
          class="shrink-0"
          :options="menuOptions"
          trigger="click"
          @select="(key: string | number) => handleMenu(key, cat)"
        >
          <NButton size="small" quaternary :aria-label="`${cat.name}操作`">操作</NButton>
        </NDropdown>
      </div>
    </NScrollbar>

    <NModal :show="!!moving" preset="card" title="调整上级分类" style="width: 480px; max-width: 94vw"
      :closable="!moveSaving" :mask-closable="!moveSaving" :close-on-esc="!moveSaving" @update:show="!$event && !moveSaving && (moving = null)">
      <p class="mb-12px">为「{{ moving?.name }}」选择上级分类，子分类将随其一起移动。</p>
      <NSelect v-model:value="moveParent" :options="moveOptions" filterable :disabled="moveSaving" placeholder="选择上级分类" />
      <template #footer><div class="flex justify-end gap-8px">
        <NButton :disabled="moveSaving" @click="moving = null">取消</NButton>
        <NButton type="primary" :loading="moveSaving" @click="saveParent">保存</NButton>
      </div></template>
    </NModal>

    <!-- 重命名 -->
    <NModal
      :show="!!renaming"
      preset="dialog"
      title="分类重命名"
      style="width: 400px"
      @update:show="(v: boolean) => !v && (renaming = null)"
    >
      <NInput v-model:value="renameText" @keyup.enter="handleRename" />
      <template #action>
        <NButton @click="renaming = null">取消</NButton>
        <NButton v-auth="'catalog:category_write'" type="primary" @click="handleRename">确定</NButton>
      </template>
    </NModal>

    <NModal :show="showMerge" preset="card" title="批量合并分类" style="width: 560px; max-width: 94vw" :closable="!merging" :mask-closable="false" :close-on-esc="!merging" @update:show="!merging && (showMerge = $event)">
      <div class="mb-12px">已选 {{ mergeSources.length }} 个来源分类。商品和上游映射迁移到目标分类，未选中的子分类移到目标下面，随后删除来源分类。</div>
      <NSelect v-model:value="mergeTarget" :options="mergeOptions" filterable :disabled="merging" placeholder="选择合并到的本地分类" />
      <div v-if="mergePreview" class="mt-12px">将删除 {{ mergePreview.categories }} 个来源分类、迁移 {{ mergePreview.products }} 件商品、移动 {{ mergePreview.children }} 个子分类。商品沿用目标分类的展示设置。</div>
      <template #footer>
        <div class="flex justify-end gap-8px">
          <NButton :disabled="merging" @click="showMerge = false">取消</NButton>
          <NButton v-if="!mergePreview" type="primary" :disabled="!mergeTarget" :loading="merging" @click="runMerge(true)">预览影响范围</NButton>
          <NButton v-else type="error" :loading="merging" @click="runMerge(false)">确认合并</NButton>
        </div>
      </template>
    </NModal>

    <!-- 删除确认 -->
    <NModal
      :show="deleteConfirm.show"
      preset="dialog"
      title="删除分类"
      style="width: 400px"
      @update:show="(v: boolean) => !v && (deleteConfirm.show = false)"
    >
      确定删除分类「{{ deleteConfirm.cat?.name }}」？分类下仍有商品时将无法删除。
      <template #action>
        <NButton @click="deleteConfirm.show = false">取消</NButton>
        <NButton v-auth="'catalog:category_delete'" type="error" @click="handleDelete">删除</NButton>
      </template>
    </NModal>
  </component>
</template>

<style scoped>
.category-manage-row { min-width: 830px; }
.category-path { display: none; }
@media (max-width: 640px) {
  .category-manage-row { min-width: 0; flex-wrap: wrap; padding: 12px 8px !important; gap: 8px; border-bottom: 1px solid var(--n-border-color); }
  .category-manage-row > .flex-1 { flex-basis: calc(100% - 70px); }
  .category-name { flex-direction: column; align-items: flex-start; }
  .category-path { display: block; color: var(--n-text-color-3); font-size: 12px; overflow-wrap: anywhere; }
  .category-create > .n-input { min-width: 180px; }
  .category-create > .n-select { width: 100%; }
}
.category-full-name { min-width: 0; white-space: normal; overflow-wrap: anywhere; line-height: 1.6; }
/* 图标槽：行内小按钮（空=虚框加号提示可设置） */
.cat-icon-btn {
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 15px;
  border-radius: 6px;
  border: 1px dashed var(--n-border-color, #d9d9d9);
  background: transparent;
  cursor: pointer;
  transition: all 0.15s;
}

.cat-icon-btn:hover {
  border-color: #4098ff;
  background: rgba(64, 152, 255, 0.08);
}
/* 图标库网格格仔 */
.icon-cell {
  width: 30px;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 17px;
  border-radius: 6px;
  border: 1px solid transparent;
  background: transparent;
  cursor: pointer;
  transition: all 0.12s;
}
.icon-cell:hover {
  background: rgba(64, 152, 255, 0.1);
}
.icon-cell.active {
  background: rgba(64, 152, 255, 0.16);
  border-color: #4098ff;
}
</style>
