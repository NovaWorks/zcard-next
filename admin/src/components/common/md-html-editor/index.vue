<script setup lang="ts">
import { defineAsyncComponent, h } from "vue";
const props = defineProps<{ modelValue?: string; placeholder?: string; height?: string }>();
const emit = defineEmits<{ (e: "update:modelValue", value: string): void }>();
// List routes can render without downloading the editor and its plugins.
const Editor = defineAsyncComponent({
  loader: () => import("./md-html-editor-impl.vue"),
  delay: 150,
  loadingComponent: { render: () => h("div", { role: "status", class: "p-16px text-gray-500" }, "编辑器加载中…") },
  errorComponent: { render: () => h("div", { role: "alert", class: "p-16px text-red-500" }, "编辑器加载失败，请刷新页面后重试") },
});
</script>
<template>
  <Editor v-bind="props" @update:model-value="emit('update:modelValue', $event)" />
</template>
