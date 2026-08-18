<script setup lang="ts">
/**
 * MD/HTML 双模式文档编辑器：
 *   - 「所见即所得」：wangEditor 富文本（HTML，插图走素材库，服务端白名单 sanitize）
 *   - 「Markdown」：源码 textarea，实时经 marked 转 HTML 对外输出
 *   - 模式切换双向转换（turndown html→md / marked md→html）——
 *     v-model 恒为 HTML（前台 v-html 渲染的事实标准），MD 只是编辑态
 */
import { ref, watch } from "vue";
import { NRadioButton, NRadioGroup, NInput } from "naive-ui";
import TurndownService from "turndown";
import { marked } from "marked";
import RichEditor from "../rich-editor/index.vue";

defineOptions({ name: "MdHtmlEditor" });

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    height?: string;
  }>(),
  { modelValue: "", height: "320px" },
);

const emit = defineEmits<{ (e: "update:modelValue", v: string): void }>();

const mode = ref<"wysiwyg" | "markdown">("wysiwyg");
const html = ref(props.modelValue || "");
const mdDraft = ref("");

const turndown = new TurndownService({
  headingStyle: "atx",
  codeBlockStyle: "fenced",
  bulletListMarker: "-",
});
marked.setOptions({ breaks: true, gfm: true });

// 外部值变化仅同步富文本态（避免 MD 编辑中被打断）
watch(
  () => props.modelValue,
  (v) => {
    if (mode.value === "wysiwyg") html.value = v || "";
  },
);

function switchMode(m: "wysiwyg" | "markdown") {
  if (m === mode.value) return;
  if (m === "markdown") {
    mdDraft.value = turndown.turndown(html.value || "");
  } else {
    const back = marked.parse(mdDraft.value || "") as string;
    html.value = back;
    emit("update:modelValue", back);
  }
  mode.value = m;
}

function onMdInput(v: string) {
  mdDraft.value = v;
  // 实时转 HTML 对外输出（保存按钮取到的恒为 HTML）
  emit("update:modelValue", marked.parse(v || "") as string);
}

function onWysiwygInput(v: string) {
  html.value = v;
  emit("update:modelValue", v);
}
</script>

<template>
  <div class="md-html-editor">
    <div class="mb-8px flex items-center justify-between">
      <NRadioGroup size="small" :value="mode" @update:value="(v: any) => switchMode(v)">
        <NRadioButton value="wysiwyg">所见即所得</NRadioButton>
        <NRadioButton value="markdown">Markdown</NRadioButton>
      </NRadioGroup>
      <span class="text-11px text-gray-400">
        {{ mode === "wysiwyg" ? "富文本（插图走素材库）" : "Markdown 源码（保存时自动转 HTML）" }}
      </span>
    </div>
    <RichEditor v-if="mode === 'wysiwyg'" :model-value="html" :height="height" @update:model-value="onWysiwygInput" />
    <NInput
      v-else
      :value="mdDraft"
      type="textarea"
      :style="{ height }"
      class="font-mono"
      placeholder="支持 GFM：标题/列表/代码块/表格/图片链接…"
      @update:value="onMdInput"
    />
  </div>
</template>
