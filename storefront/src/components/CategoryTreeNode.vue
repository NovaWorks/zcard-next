<script setup lang="ts">
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

const hasChildren = (props.node.children?.length ?? 0) > 0;
</script>

<template>
  <div>
    <button
      class="tree-node"
      :class="{ active: modelValue === node.id }"
      :style="{ paddingLeft: `${12 + depth * 16}px` }"
      @click="emit('select', node.id)"
    >
      <span class="tree-dot" :class="{ active: modelValue === node.id }"></span>
      <span v-if="node.icon" class="tree-icon">{{ node.icon }}</span>
      <span class="flex-1 text-left truncate">{{ node.name }}</span>
      <span
        v-if="hasChildren"
        class="tree-arrow"
        :class="{ open: expanded.has(node.id) }"
        @click.stop="emit('toggle', node.id)"
      >▶</span>
    </button>
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
.tree-node {
  width: 100%;
  display: flex; align-items: center; gap: 6px;
  padding: 9px 12px;
  border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 14px; color: #374151;
  transition: all 0.15s; font-family: inherit;
  text-align: left;
}
.tree-node:hover { background: #eff6ff; color: #2563eb; }
.tree-node.active { background: #2563eb; color: #fff; font-weight: 600; }
.tree-dot {
  width: 5px; height: 5px; border-radius: 999px; flex-shrink: 0;
  background: #d1d5db;
}
.tree-dot.active { background: #fff; opacity: 0.9; }
.tree-icon { font-size: 15px; }
.tree-arrow { font-size: 11px; opacity: 0.6; transition: transform 0.2s; }
.tree-arrow.open { transform: rotate(90deg); }
</style>
