<template>
  <aside class="cat-tree" :class="{ 'cat-tree--panel': variant === 'panel' }">
    <div class="cat-tree-card">
      <!-- 标题栏：品牌竖条 + 标题 + 分类数（与右侧「全部商品」区标题同一设计语言） -->
      <button class="cat-tree-head" type="button" :aria-expanded="bodyOpen" @click="manualOpen = !bodyOpen">
        <span class="head-bar"></span>
        <span class="cat-tree-title">全部分类</span>
        <span v-if="categories.length" class="cat-tree-count">{{ categories.length }} 类</span>
        <span class="cat-tree-toggle-label">{{ bodyOpen ? '折叠' : '展开' }}</span>
      </button>
      <div v-show="bodyOpen" class="cat-tree-body">
        <button v-if="branchIds.length" class="tree-expand-all" type="button" @click="toggleAll">
          {{ allExpanded ? '全部折叠' : '全部展开' }}
        </button>
        <!-- 全部商品入口 -->
        <button
          class="tree-all"
          :class="{ active: modelValue === 0 }"
          @click="select(0)"
        >
          <span>🏠</span>
          <span class="flex-1 text-left">全部商品</span>
        </button>
        <!-- 分类树：递归渲染任意层级（三级/四级均可展开） -->
        <CategoryTreeNode
          v-for="c in tree"
          :key="c.id"
          :node="c"
          :depth="0"
          :expanded="expanded"
          :model-value="modelValue"
          @select="select"
          @toggle="toggle"
        />
        <div v-if="!roots.length" class="tree-empty muted">暂无分类</div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import CategoryTreeNode from './CategoryTreeNode.vue';
import type { CategoryItem } from '@/api';

// variant：sidebar=PC 左侧栏（默认）；panel=移动端折叠面板（全宽、无头部、限高滚动）
const props = withDefaults(
  defineProps<{
    categories: CategoryItem[];
    /** 当前选中分类 id（0=全部） */
    modelValue: number;
    variant?: 'sidebar' | 'panel';
  }>(),
  { variant: 'sidebar' },
);
const emit = defineEmits<{
  (e: 'update:modelValue', v: number): void;
}>();
const manualOpen = ref<boolean | null>(null);
const bodyOpen = computed(() => props.variant === 'panel' || (manualOpen.value ?? props.categories.length <= 1));

// 分类树（任意层级：parent_id 链构建 children；一级为根）
const tree = computed(() => {
  const map = new Map<number, any>();
  for (const c of props.categories) map.set(c.id, { ...c, children: [] });
  const rootsArr: any[] = [];
  for (const node of map.values()) {
    if (node.parent_id && map.has(node.parent_id)) map.get(node.parent_id)!.children.push(node);
    else rootsArr.push(node);
  }
  return rootsArr;
});
// 一级分类（parent_id 缺失/0 为根——proto3 JSON 省略 0 值字段，须用 falsy 判断）
const roots = computed(() => props.categories.filter((c) => !c.parent_id));

// 多级分类默认收起，选中深层分类时只展开其祖先。
const expanded = ref<Set<number>>(new Set());
const branchIds = computed(() => [...new Set(props.categories.filter((c) => c.parent_id).map((c) => c.parent_id!))]);
const allExpanded = computed(() => branchIds.value.length > 0 && branchIds.value.every((id) => expanded.value.has(id)));

function toggleAll() {
  expanded.value = new Set(allExpanded.value ? [] : branchIds.value);
}

watch(() => [props.modelValue, props.categories] as const, () => {
  const byId = new Map(props.categories.map((c) => [c.id, c]));
  const next = new Set(expanded.value);
  const seen = new Set<number>();
  let parent = byId.get(props.modelValue)?.parent_id;
  while (parent && !seen.has(parent)) {
    seen.add(parent);
    next.add(parent);
    parent = byId.get(parent)?.parent_id;
  }
  expanded.value = next;
}, { immediate: true });

function toggle(id: number) {
  const s = new Set(expanded.value);
  if (s.has(id)) s.delete(id);
  else s.add(id);
  expanded.value = s;
}

function select(id: number) {
  emit('update:modelValue', id);
}
</script>

<style scoped>
.cat-tree {
  display: none;
  width: 240px;
  flex-shrink: 0;
}
@media (min-width: 768px) {
  .cat-tree { display: block; }
}
.cat-tree-card {
  background: #fff;
  border: 1px solid #e5e7eb;
  border-radius: 12px;
  overflow: hidden;
  position: sticky;
  top: 72px; /* 品牌条 + 主导航之下 */
}
.cat-tree-head {
  width: 100%; border: none; font: inherit; text-align: left; cursor: pointer;
  display: flex; align-items: center; gap: 8px;
  padding: 14px 16px;
  border-bottom: 1px solid #e5e7eb;
  background: #f8fafc;
}
.cat-tree-toggle-label { font-size: 12px; color: #6b7280; white-space: nowrap; }
.head-bar {
  width: 4px; height: 16px; border-radius: 999px;
  background: #ff5722; display: inline-block; flex-shrink: 0;
}
.cat-tree-title { font-size: 16px; font-weight: 700; color: #111827; letter-spacing: 0.5px; }
.cat-tree-count {
  margin-left: auto;
  font-size: 12px; font-weight: 500; color: #2563eb;
  background: rgba(37, 99, 235, 0.08);
  padding: 2px 9px; border-radius: 999px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.cat-tree-body { padding: 12px 10px; max-height: calc(100vh - 220px); overflow-y: auto; display: flex; flex-direction: column; gap: 2px; }
.tree-expand-all { align-self: flex-end; min-height: 44px; padding: 6px 10px; border: none; border-radius: 6px; background: none; color: #2563eb; font: inherit; font-size: 13px; cursor: pointer; }
.tree-expand-all:hover { background: #eff6ff; }

.tree-all {
  width: 100%;
  display: flex; align-items: center; gap: 9px;
  padding: 11px 12px;
  border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 15px; color: #374151;
  transition: all 0.15s; font-family: inherit;
  text-align: left;
}
.tree-all:hover { background: #eff6ff; color: #2563eb; }
.tree-all.active { background: #2563eb; color: #fff; font-weight: 600; box-shadow: 0 2px 6px rgba(37, 99, 235, 0.25); }
.tree-all > span:first-child { font-size: 16px; }
.tree-empty { padding: 16px 0; text-align: center; }

/* 面板变体（移动端折叠面板内嵌）：全宽平铺、隐藏自带头部、限高滚动。
   双类名提升优先级，覆盖基础 .cat-tree 的移动端 display:none */
.cat-tree.cat-tree--panel { display: block; width: 100%; }
.cat-tree--panel .cat-tree-card { position: static; border: none; border-radius: 0; }
.cat-tree--panel .cat-tree-head { display: none; }
.cat-tree--panel .cat-tree-body { max-height: 56vh; padding: 4px 4px 8px; }
</style>
