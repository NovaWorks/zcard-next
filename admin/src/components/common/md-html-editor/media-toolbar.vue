<script setup lang="ts">
/**
 * md-editor-v3 自定义工具栏按钮：素材库插图。
 * insert 由 md-editor-v3 克隆自定义工具栏组件时自动注入（见 NormalToolbar props 注释），
 * 点击后经 pickMedia 选图，在光标处插入 Markdown 图片语法。
 * 注意：generate 必须返回 { targetValue } 对象（universal 替换路径只读 targetValue）。
 */
import { pickMedia } from "@/components/common/media-picker";

defineOptions({ name: "MediaLibraryMdToolbar" });

const props = withDefaults(
  defineProps<{
    title?: string;
    insert?: (generate: (selected: string) => { targetValue: string; select?: boolean }) => void;
  }>(),
  { title: "素材库图片", insert: () => {} },
);

async function onPick() {
  const urls = await pickMedia({ multiple: true });
  if (!urls?.length) return;
  props.insert(() => ({
    targetValue: urls.map((url) => `![素材图片](${url})`).join("\n\n"),
    select: false,
  }));
}
</script>

<template>
  <button class="zc-md-media-btn" :title="title" @click="onPick">
    <svg viewBox="0 0 1024 1024" width="15" height="15" fill="currentColor">
      <path d="M896 128H128c-35.3 0-64 28.7-64 64v640c0 35.3 28.7 64 64 64h768c35.3 0 64-28.7 64-64V192c0-35.3-28.7-64-64-64z m0 704H128V192h768v640z" />
      <path d="M320 480m-64 0a64 64 0 1 0 128 0 64 64 0 1 0-128 0zM208 768h608v-64l-160-192-128 160-96-96z" />
    </svg>
  </button>
</template>

<style scoped>
/* 纯图标按钮：与 md-editor-v3 内置工具栏按钮同尺寸（24×28），悬停 title 提示「素材库图片」 */
.zc-md-media-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 24px; height: 28px; padding: 0; border: none; background: transparent;
  color: #595959; cursor: pointer;
}
.zc-md-media-btn:hover { color: #2563eb; }
</style>
