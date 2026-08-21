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
        <!-- 一级分类（含子分类折叠） -->
        <div v-for="c in roots" :key="c.id">
          <button class="tree-parent" :class="{ active: modelValue === c.id }" @click="select(c.id)">
            <span class="tree-icon">{{ c.icon || '📦' }}</span>
            <span class="flex-1 text-left truncate">{{ c.name }}</span>
            <span
              v-if="childrenOf(c.id).length"
              class="tree-arrow"
              :class="{ open: expanded.has(c.id) }"
              @click.stop="toggle(c.id)"
            >▶</span>
          </button>
          <!-- 子分类：缩进 + 左侧连接线 -->
          <div v-if="childrenOf(c.id).length && expanded.has(c.id)" class="tree-children">
            <button
              v-for="ch in childrenOf(c.id)"
              :key="ch.id"
              class="tree-child"
              :class="{ active: modelValue === ch.id }"
              @click="select(ch.id)"
            >
              <span class="tree-dot" :class="{ active: modelValue === ch.id }"></span>
              <span class="truncate">{{ ch.icon ? ch.icon + ' ' : '' }}{{ ch.name }}</span>
            </button>
          </div>
        </div>
        <div v-if="!roots.length" class="tree-empty muted">暂无分类</div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import type { CategoryItem } from '@/api';

const props = defineProps<{
  categories: CategoryItem[];
  /** 当前选中分类 id（0=全部） */
  modelValue: number;
}>();
const emit = defineEmits<{
  (e: 'update:modelValue', v: number): void;
}>();

// 一级分类（parent_id=0 为根）
const roots = computed(() => props.categories.filter((c) => c.parent_id === 0));
function childrenOf(id: number) {
  return props.categories.filter((c) => c.parent_id === id);
}

// 展开状态（默认全展开）
const expanded = ref<Set<number>>(new Set(props.categories.filter((c) => c.parent_id !== 0).map((c) => c.parent_id)));

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
  width: 208px;
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
  padding: 12px 14px;
  border-bottom: 1px solid #e5e7eb;
  background: #f8fafc;
  font-size: 14px; font-weight: 700; color: #111827;
}
.cat-tree-body { padding: 10px; max-height: calc(100vh - 220px); overflow-y: auto; }

.tree-all, .tree-parent {
  width: 100%;
  display: flex; align-items: center; gap: 8px;
  padding: 9px 10px;
  border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 13px; color: #4b5563;
  transition: all 0.15s; font-family: inherit;
  text-align: left;
}
.tree-all:hover, .tree-parent:hover { background: #eff6ff; color: #2563eb; }
.tree-all.active, .tree-parent.active { background: #2563eb; color: #fff; font-weight: 600; box-shadow: 0 2px 6px rgba(37, 99, 235, 0.25); }
.tree-icon { font-size: 14px; }
.tree-arrow {
  font-size: 10px; opacity: 0.6; transition: transform 0.2s;
}
.tree-arrow.open { transform: rotate(90deg); }

.tree-children {
  margin: 2px 0 6px 14px;
  padding-left: 10px;
  border-left: 1px solid #e5e7eb;
  display: flex; flex-direction: column; gap: 2px;
}
.tree-child {
  width: 100%;
  display: flex; align-items: center; gap: 6px;
  padding: 7px 8px;
  border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 12px; color: #6b7280;
  transition: all 0.15s; font-family: inherit;
  text-align: left;
}
.tree-child:hover { color: #2563eb; background: #eff6ff; }
.tree-child.active { color: #2563eb; font-weight: 600; background: #eff6ff; }
.tree-dot {
  width: 5px; height: 5px; border-radius: 999px; flex-shrink: 0;
  background: #d1d5db;
}
.tree-dot.active { background: #2563eb; }
.tree-empty { padding: 16px 0; text-align: center; }
</style>
