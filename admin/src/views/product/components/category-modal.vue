<script setup lang="ts">
/**
 * 商品分类管理（P1-01 T2 前端面）：树形列表 + 新建（含父分类）/重命名/删除（非空拒绝）。
 * 新建/变更后向父组件抛 refresh 事件（表单下拉联动刷新）。
 */
import { ref, reactive, computed, watch } from "vue";
import { NButton, NTag, NInput, NInputNumber, NSelect, NModal, NDropdown, NTooltip } from "naive-ui";
import type { DropdownOption } from "naive-ui";
import {
  fetchCategories,
  createCategory,
  updateCategory,
  deleteCategory,
  reorderCategories,
} from "@/service/api";

const props = defineProps<{ show: boolean }>();
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
const creating = ref(false);

// 重命名 / 删除确认
const renaming = ref<any | null>(null);
const renameText = ref("");
const deleteConfirm = reactive({ show: false, cat: null as any });

const visible = computed({
  get: () => props.show,
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
      node.depth = parent.depth + 1;
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
});

const flatTree = computed(() => {
  const out: any[] = [];
  const walk = (nodes: any[]) => {
    for (const n of nodes) {
      out.push(n);
      walk(n.children);
    }
  };
  walk(tree.value);
  return out;
});

const parentOptions = computed(() => [
  { label: "顶级分类", value: 0 },
  ...flatTree.value.map((c) => ({ label: `${"　".repeat(c.depth)}${c.name}`, value: c.id })),
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
  () => props.show,
  (v) => {
    if (v) {
      load();
      resetCreate();
    }
  },
);

function resetCreate() {
  newName.value = "";
  newParent.value = null;
  showCreate.value = false;
}

async function handleCreate() {
  if (!newName.value.trim()) return;
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
  const { error } = await updateCategory(renaming.value.id, { name: renameText.value.trim(), parent_id: -1 });
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

const menuOptions: DropdownOption[] = [
  { label: "重命名", key: "rename" },
  {
    label: "删除分类",
    key: "delete",
    divided: true,
    props: { style: "color: var(--error-color, #d03050)" },
  },
];

function handleMenu(key: string | number, cat: any) {
  if (key === "rename") {
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

// 名称截断时悬浮显示全名（scrollWidth > clientWidth 即溢出）
function onNameEnter(cat: any, e: MouseEvent) {
  const el = e.target as HTMLElement;
  cat._overflow = el.scrollWidth > el.clientWidth;
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
  <NModal v-model:show="visible" preset="card" title="分类管理" style="width: 640px">
    <div class="mb-12px flex items-center justify-between">
      <span class="text-13px text-gray-500">共 {{ flatTree.length }} 个分类</span>
      <NButton v-auth="'catalog:category_write'" size="small" type="primary" @click="showCreate = !showCreate">
        {{ showCreate ? "收起" : "新建分类" }}
      </NButton>
    </div>

    <!-- 新建 -->
    <div v-if="showCreate" class="mb-12px flex items-center gap-8px">
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
        class="w-140px"
        :options="parentOptions"
      />
      <NButton size="small" type="primary" :loading="creating" @click="handleCreate">创建</NButton>
    </div>

    <!-- 树列表（行可拖拽：上/下边缘=排序插入，行中间=设为子级；顶部释放区=顶级） -->
    <div class="mb-4px text-11px text-gray-400">拖到行上/下边缘 = 排序；拖到行中间 = 设为子级；拖到顶部虚线区 = 设为顶级</div>
    <div
      class="mb-4px rounded-4px border border-dashed px-10px py-6px text-center text-12px"
      :class="dropToRoot ? 'border-blue-400 bg-blue-50 text-blue-500' : 'border-gray-300 text-gray-400 dark:border-gray-600'"
      @dragover.prevent="dropToRoot = true"
      @dragleave="dropToRoot = false"
      @drop.prevent="onDropToRoot"
    >
      {{ dropToRoot ? '松开设为顶级分类' : '拖拽分类到此处 = 设为顶级' }}
    </div>
    <NScrollbar class="max-h-340px rounded-4px border border-gray-200 dark:border-gray-700">
      <NEmpty
        v-if="!flatTree.length && !loading"
        size="small"
        class="mt-40px"
        description="暂无分类"
      />
      <div
        v-for="cat in flatTree"
        :key="cat.id"
        draggable="true"
        class="group flex cursor-grab items-center gap-8px rounded-4px py-7px pr-8px text-13px hover:bg-gray-100 dark:hover:bg-gray-800 active:cursor-grabbing"
        :class="rowClass(cat)"
        :style="rowStyle(cat)"
        @dragstart="onDragStart(cat)"
        @dragover.prevent="onDragOver(cat, $event)"
        @dragleave="onDragLeave"
        @drop.prevent="onDrop(cat)"
      >
        <!-- 名称列：占满剩余宽度，超长截断不撑破行；悬浮显示全名 -->
        <div class="flex min-w-0 flex-1 items-center gap-6px">
          <NTag v-if="cat.hide" size="tiny" :bordered="false" class="shrink-0">隐藏</NTag>
          <NTooltip :disabled="!cat._overflow" placement="top" :show-arrow="false">
            <template #trigger>
              <span class="min-w-0 truncate" @mouseenter="onNameEnter(cat, $event)">{{ cat.name }}</span>
            </template>
            {{ cat.name }}
          </NTooltip>
        </div>
        <!-- 商品数/排序/操作：固定列宽，不随名称长度漂移 -->
        <span class="w-76px shrink-0 text-right text-12px text-gray-400">{{ cat.product_count || 0 }} 件</span>
        <NInputNumber
          v-model:value="cat.sort"
          size="tiny"
          :min="0"
          :show-button="false"
          class="w-56px shrink-0"
          placeholder="排序"
          @blur="onSortBlur(cat)"
          @keyup.enter="onSortBlur(cat)"
        />
        <NDropdown
          class="shrink-0"
          :options="menuOptions"
          trigger="click"
          @select="(key: string | number) => handleMenu(key, cat)"
        >
          <NButton size="tiny" quaternary>⋯</NButton>
        </NDropdown>
      </div>
    </NScrollbar>

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
  </NModal>
</template>
