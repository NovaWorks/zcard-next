<template>
  <aside class="cat-tree">
    <div class="cat-tree-card">
      <!-- 标题栏 -->
      <div class="cat-tree-head">
        <span>📁</span>
        <span class="cat-tree-title">全部分类</span>
      </div>
      <div class="cat-tree-body">
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
import { ref, computed } from 'vue';
import CategoryTreeNode from './CategoryTreeNode.vue';
import type { CategoryItem } from '@/api';

const props = defineProps<{
  categories: CategoryItem[];
  /** 当前选中分类 id（0=全部） */
  modelValue: number;
}>();
const emit = defineEmits<{
  (e: 'update:modelValue', v: number): void;
}>();

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

// 展开状态（默认全展开）
const expanded = ref<Set<number>>(new Set(props.categories.filter((c) => !!c.parent_id).map((c) => c.parent_id)));

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
  width: 216px;
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
  display: flex; align-items: center; gap: 6px;
  padding: 13px 16px;
  border-bottom: 1px solid #e5e7eb;
  background: #f8fafc;
  font-size: 15px; font-weight: 700; color: #111827;
}
.cat-tree-head > span:first-child { font-size: 16px; }
.cat-tree-body { padding: 10px; max-height: calc(100vh - 220px); overflow-y: auto; }

.tree-all, .tree-parent {
  width: 100%;
  display: flex; align-items: center; gap: 8px;
  padding: 10px 12px;
  border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 14px; color: #374151;
  transition: all 0.15s; font-family: inherit;
  text-align: left;
}
.tree-all:hover, .tree-parent:hover { background: #eff6ff; color: #2563eb; }
.tree-all.active, .tree-parent.active { background: #2563eb; color: #fff; font-weight: 600; box-shadow: 0 2px 6px rgba(37, 99, 235, 0.25); }
.tree-all > span:first-child { font-size: 16px; }
.tree-icon { font-size: 15px; }
.tree-arrow {
  font-size: 11px; opacity: 0.6; transition: transform 0.2s;
}
.tree-arrow.open { transform: rotate(90deg); }
.tree-empty { padding: 16px 0; text-align: center; }
</style>
