<script setup lang="ts">
import CategoryIcon from '@/components/CategoryIcon.vue';
import { computed } from 'vue';
// 递归分类节点（任意层级：三级/四级……均可展开；缩进随 depth 递增）
const props = defineProps<{
  node: any;
  depth: number;
  expanded: Set<number>;
  modelValue: number;
}>();
const emit = defineEmits<{
  (e: 'select', id: number): void;
  (e: 'toggle', id: number): void;
}>();

const hasChildren = computed(() => (props.node.children?.length ?? 0) > 0);
function activate() {
  emit('select', props.node.id);
  if (hasChildren.value) emit('toggle', props.node.id);
}
</script>

<template>
  <div>
    <div class="tree-row" :class="{ active: modelValue === node.id }">
    <button
      class="tree-node"
      :class="{ active: modelValue === node.id, 'tree-node--root': depth === 0 }"
      :style="{ paddingLeft: `${12 + depth * 16}px` }"
      :title="node.name"
      :aria-expanded="hasChildren ? expanded.has(node.id) : undefined"
      @click="activate"
    >
      <span class="tree-dot" :class="{ active: modelValue === node.id }"></span>
      <CategoryIcon :icon="node.icon" class="tree-icon" />
      <span class="flex-1 text-left truncate">{{ node.name }}</span>
      <span v-if="hasChildren" class="tree-arrow" :class="{ open: expanded.has(node.id) }" aria-hidden="true"></span>
    </button>
    </div>
    <template v-if="hasChildren && expanded.has(node.id)">
      <CategoryTreeNode
        v-for="ch in node.children"
        :key="ch.id"
        :node="ch"
        :depth="depth + 1"
        :expanded="expanded"
        :model-value="modelValue"
        @select="emit('select', $event)"
        @toggle="emit('toggle', $event)"
      />
    </template>
  </div>
</template>

<style scoped>
.tree-row { display: flex; align-items: stretch; border-radius: 8px; }
.tree-row.active { background: #2563eb; color: #fff; }
.tree-node {
  min-width: 0; min-height: 44px; flex: 1;
  display: flex; align-items: center; gap: 7px;
  padding: 10px 12px;
  border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 14px; color: #374151;
  transition: all 0.15s; font-family: inherit;
  text-align: left;
}
/* 一级分类与「全部商品」同字号（15px），子级 14px 递进——层级一眼可辨 */
.tree-node--root { font-size: 15px; font-weight: 500; }
.tree-node--root.active,
.tree-node.active { font-weight: 600; }
.tree-node:hover { background: #eff6ff; color: #2563eb; }
.tree-node.active { background: #2563eb; color: #fff; }
.tree-dot {
  width: 5px; height: 5px; border-radius: 999px; flex-shrink: 0;
  background: #d1d5db;
}
.tree-dot.active { background: #fff; opacity: 0.9; }
.tree-icon { font-size: 16px; }
/* 纯 CSS 旋钮箭头（Ant Design chevron 惯例）：右向 → 展开时转下向 */
.tree-arrow {
  width: 6px; height: 6px; flex-shrink: 0;
  border-right: 1.5px solid currentColor;
  border-bottom: 1.5px solid currentColor;
  opacity: 0.55;
  transform: rotate(-45deg);
  transition: transform 0.2s;
}
.tree-arrow.open { transform: rotate(45deg); }
</style>
