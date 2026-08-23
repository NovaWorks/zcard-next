<script setup lang="ts">
/**
 * MD/HTML 三模式文档编辑器：
 *   - 「所见即所得」：wangEditor 富文本（HTML，插图走素材库，服务端白名单 sanitize）
 *   - 「Markdown」：md-editor-v3（开源 MIT，编辑 + 分屏预览 + 工具栏 + 素材库插图）
 *   - 「HTML源码」：等宽 textarea 直接编辑 HTML
 *   - 模式切换双向转换（turndown html→md / marked md→html）——
 *     v-model 恒为 HTML（前台 v-html 渲染的事实标准），MD/HTML 只是编辑态
 */
import { ref, watch, h, Fragment } from "vue";
import { NRadioButton, NRadioGroup, NInput } from "naive-ui";
import TurndownService from "turndown";
import { marked } from "marked";
import { MdEditor } from "md-editor-v3";
import type { ToolbarNames } from "md-editor-v3";
import "md-editor-v3/lib/style.css";
import RichEditor from "../rich-editor/index.vue";
import MediaToolbar from "./media-toolbar.vue";

defineOptions({ name: "MdHtmlEditor" });

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    height?: string;
  }>(),
  { modelValue: "", height: "320px" },
);

const emit = defineEmits<{ (e: "update:modelValue", v: string): void }>();

const mode = ref<"wysiwyg" | "markdown" | "html">("wysiwyg");
const html = ref(props.modelValue || "");
const mdDraft = ref("");
const htmlDraft = ref("");

const turndown = new TurndownService({
  headingStyle: "atx",
  codeBlockStyle: "fenced",
  bulletListMarker: "-",
});
marked.setOptions({ breaks: true, gfm: true });

// 外部值变化仅同步富文本态（避免 MD/HTML 编辑中被打断）
watch(
  () => props.modelValue,
  (v) => {
    if (mode.value === "wysiwyg") html.value = v || "";
  },
);

function switchMode(m: "wysiwyg" | "markdown" | "html") {
  if (m === mode.value) return;
  const prev = mode.value;
  if (m === "markdown") {
    mdDraft.value = turndown.turndown(html.value || "");
  } else if (m === "html") {
    htmlDraft.value = html.value || "";
  } else {
    // 切回所见即所得：按来源模式回填（md 经 marked 转 HTML；html 直用源码）
    const back = prev === "markdown" ? (marked.parse(mdDraft.value || "") as string) : htmlDraft.value;
    html.value = back;
    emit("update:modelValue", back);
  }
  mode.value = m;
}

function onMdChange() {
  // md-editor-v3 已通过 v-model 更新 mdDraft；同步 html 事实源并实时转 HTML 对外输出
  const out = marked.parse(mdDraft.value || "") as string;
  html.value = out;
  emit("update:modelValue", out);
}

function onWysiwygInput(v: string) {
  html.value = v;
  emit("update:modelValue", v);
}

function onHtmlInput(v: string) {
  htmlDraft.value = v;
  html.value = v;
  emit("update:modelValue", v);
}

// ── md-editor-v3 配置：精简工具栏（禁图片上传——图片一律素材库；自定义素材按钮由库注入 insert）──
const mdToolbars: ToolbarNames[] = [
  "bold",
  "italic",
  "strikeThrough",
  "title",
  "-",
  "quote",
  "unorderedList",
  "orderedList",
  "task",
  "codeRow",
  "code",
  "link",
  0, // 素材库图片（defToolbars 中第 0 个按钮）
  "table",
  "-",
  "revoke",
  "next",
  "prettier",
  "preview",
  "htmlPreview",
  "fullscreen",
];
// 自定义工具栏按钮：defToolbars 传 Fragment（children 为按钮数组），toolbars 数字下标引用
const defToolbars = h(Fragment, [h(MediaToolbar, { title: "素材库图片" })]);
</script>

<template>
  <div class="md-html-editor">
    <div class="mb-8px flex items-center justify-between">
      <NRadioGroup size="small" :value="mode" @update:value="(v: any) => switchMode(v)">
        <NRadioButton value="wysiwyg">所见即所得</NRadioButton>
        <NRadioButton value="markdown">Markdown</NRadioButton>
        <NRadioButton value="html">HTML源码</NRadioButton>
      </NRadioGroup>
      <span class="text-11px text-gray-400">
        {{
          mode === "wysiwyg"
            ? "富文本（插图走素材库）"
            : mode === "markdown"
              ? "Markdown 代码模式（分屏预览，保存时自动转 HTML）"
              : "HTML 源码（保存时经服务端白名单清洗）"
        }}
      </span>
    </div>
    <!-- wangEditor 常驻（v-show）：销毁重建会崩（Slate 实例冲突），MD/HTML 走 v-if 重建 -->
    <RichEditor v-show="mode === 'wysiwyg'" :model-value="html" :height="height" @update:model-value="onWysiwygInput" />
    <MdEditor
      v-if="mode === 'markdown'"
      v-model="mdDraft"
      :toolbars="mdToolbars"
      :def-toolbars="defToolbars"
      preview-theme="github"
      code-theme="github"
      language="zh-CN"
      :preview="true"
      :no-upload-img="true"
      :no-prettier="false"
      :style="{ height }"
      placeholder="支持 GFM：标题/列表/代码块/表格/图片…"
      @on-change="onMdChange"
    />
    <NInput
      v-else
      :value="htmlDraft"
      type="textarea"
      :style="{ height }"
      class="font-mono"
      placeholder="直接编辑 HTML：&lt;h2&gt;标题&lt;/h2&gt;&lt;p&gt;正文&lt;/p&gt;&lt;img src=&quot;素材库URL&quot;&gt;…"
      @update:value="onHtmlInput"
    />
  </div>
</template>
