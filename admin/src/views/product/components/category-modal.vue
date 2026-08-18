<script setup lang="ts">
/**
 * 商品分类管理（P1-01 T2 前端面）：树形列表 + 新建（含父分类）/重命名/删除（非空拒绝）。
 * 新建/变更后向父组件抛 refresh 事件（表单下拉联动刷新）。
 */
import { ref, reactive, computed, watch } from "vue";
import { NButton, NTag, NInput, NSelect, NModal, NDropdown } from "naive-ui";
import type { DropdownOption } from "naive-ui";
import { fetchCategories, createCategory, updateCategory, deleteCategory } from "@/service/api";

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
    const { data, error } = await createCategory({
      name: newName.value.trim(),
      parent_id: newParent.value || undefined,
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
</script>

<template>
  <NModal v-model:show="visible" preset="card" title="分类管理" style="width: 520px">
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

    <!-- 树列表 -->
    <NScrollbar class="max-h-360px rounded-4px border border-gray-200 dark:border-gray-700">
      <NEmpty
        v-if="!flatTree.length && !loading"
        size="small"
        class="mt-40px"
        description="暂无分类"
      />
      <div
        v-for="cat in flatTree"
        :key="cat.id"
        class="group flex cursor-default items-center justify-between py-7px pr-10px text-13px hover:bg-gray-100 dark:hover:bg-gray-800"
        :style="{ paddingLeft: `${10 + cat.depth * 16}px` }"
      >
        <span class="flex items-center gap-6px">
          <NTag v-if="cat.hide" size="tiny" :bordered="false">隐藏</NTag>
          <span>{{ cat.name }}</span>
          <span class="text-11px text-gray-400">{{ cat.product_count || 0 }} 件商品</span>
        </span>
        <NDropdown
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
