<script setup lang="ts">
/**
 * 富文本编辑器（wangEditor 5，开源 MIT——中文后台事实标准）：
 *   - 所见即所得 HTML 输出（服务端 bluemonday 白名单 sanitize 兜底，防 XSS）
 *   - 插图统一走素材库（自定义菜单「素材库图片」→ pickMedia 弹窗）——
 *     禁用 base64/本地上传（data: URL 会被服务端白名单剥离，且图片必须沉淀素材库可复用）
 */
import { onBeforeUnmount, shallowRef, computed } from "vue";
import "@wangeditor/editor/dist/css/style.css";
import { Editor, Toolbar } from "@wangeditor/editor-for-vue";
import { Boot } from "@wangeditor/editor";
import type { IEditorConfig, IToolbarConfig, IDomEditor } from "@wangeditor/editor";
import { pickMedia } from "@/components/common/media-picker";

defineOptions({ name: "RichEditor" });

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    placeholder?: string;
    height?: string;
  }>(),
  { modelValue: "", placeholder: "请输入内容…", height: "320px" },
);

const emit = defineEmits<{ (e: "update:modelValue", v: string): void }>();

// ── 自定义菜单：素材库插图（模块级注册一次；编辑器销毁后菜单类仍可复用）──

class MediaLibraryMenu {
  title = "素材库图片";
  iconSvg =
    '<svg viewBox="0 0 1024 1024" width="16" height="16"><path d="M896 128H128c-35.3 0-64 28.7-64 64v640c0 35.3 28.7 64 64 64h768c35.3 0 64-28.7 64-64V192c0-35.3-28.7-64-64-64z m0 704H128V192h768v640z" fill="currentColor"/><path d="M320 480m-64 0a64 64 0 1 0 128 0 64 64 0 1 0-128 0zM208 768h608v-64l-160-192-128 160-96-96z" fill="currentColor"/></svg>';
  tag = "button";

  getValue() {
    return "";
  }
  isActive() {
    return false;
  }
  isDisabled() {
    return false;
  }
  async exec(editor: IDomEditor) {
    const urls = await pickMedia({ multiple: true });
    if (!urls?.length) return;
    // dangerouslyInsertHtml：绕开 Slate Node 类型约束（wangEditor 官方推荐的 HTML 注入路径）
    editor.dangerouslyInsertHtml(urls.map((url) => `<img src="${url}" alt="" />`).join(""));
    editor.focus();
  }
}

let moduleRegistered = false;
if (!moduleRegistered) {
  Boot.registerModule({
    menus: [{ key: "zcMediaLibrary", factory: () => new MediaLibraryMenu() as any }],
  });
  moduleRegistered = true;
}

// ── 编辑器装配 ──

const editorRef = shallowRef<IDomEditor>();
const value = computed({
  get: () => props.modelValue,
  set: (v: string) => emit("update:modelValue", v),
});

// 工具栏：精选键集（禁 uploadImage/insertVideo——图片一律素材库；data: URL 会被服务端剥离）
const toolbarConfig: Partial<IToolbarConfig> = {
  toolbarKeys: [
    "headerSelect",
    "|",
    "bold",
    "italic",
    "underline",
    "through",
    "color",
    "bgColor",
    "|",
    "bulletedList",
    "numberedList",
    "insertLink",
    "|",
    "zcMediaLibrary",
    "insertTable",
    "blockquote",
    "codeBlock",
    "|",
    "clearStyle",
    "undo",
    "redo",
  ],
};

const editorConfig: Partial<IEditorConfig> = {
  placeholder: props.placeholder,
  MENU_CONF: {},
};

function handleCreated(editor: IDomEditor) {
  editorRef.value = editor;
}

// 销毁（wangEditor 强纪律：不销毁会内存泄漏；防御性包裹——Slate 实例
// 异常时不让卸载钩子抛错中断组件树）
onBeforeUnmount(() => {
  try {
    editorRef.value?.destroy();
  } catch {
    // 忽略销毁异常（重复销毁/实例已失效）
  }
});
</script>

<template>
  <div class="z-0 overflow-hidden rounded-4px border border-gray-300 dark:border-gray-600">
    <Toolbar
      :editor="editorRef"
      :default-config="toolbarConfig"
      mode="default"
      class="border-b border-gray-200 dark:border-gray-600"
    />
    <Editor
      v-model="value"
      :default-config="editorConfig"
      mode="default"
      :style="{ height, overflowY: 'hidden' }"
      @on-created="handleCreated"
    />
  </div>
</template>

<style>
/* wangEditor 亮色渲染——暗色模式下容器底色兜底（编辑区保持白底保证可读性） */
.w-e-text-container {
  background-color: #fff;
}
.w-e-toolbar {
  background-color: #fafafa;
}
</style>
