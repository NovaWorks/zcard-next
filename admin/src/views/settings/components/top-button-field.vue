<script setup lang="ts">
// 站点顶部自定义按钮设置（site.top_button = {text,url} JSON）：
// 可视化编辑替代裸 JSON 文本域——文字留空 = 不显示；保存为 JSON。
import { computed, ref, watch } from "vue";
import { NButton, NInput } from "naive-ui";

const props = defineProps<{ value: string | null }>();
const emit = defineEmits<{ (e: "update", v: { text: string; url: string } | null): void }>();

const text = ref("");
const url = ref("");

function parse(v: string | null) {
  try {
    const o = JSON.parse(v || "null");
    if (o && typeof o === "object") {
      text.value = typeof o.text === "string" ? o.text : "";
      url.value = typeof o.url === "string" ? o.url : "";
      return;
    }
  } catch {
    /* 非法 JSON 按空处理 */
  }
  text.value = "";
  url.value = "";
}
parse(props.value);
watch(() => props.value, parse);

const enabled = computed(() => !!text.value.trim());

function push() {
  const t = text.value.trim();
  const u = url.value.trim();
  // 文字留空 = 不启用（存 null）；父级 setVal 统一 JSON.stringify
  emit("update", t ? { text: t, url: u } : null);
}

function clear() {
  text.value = "";
  url.value = "";
  push();
}
</script>

<template>
  <div class="flex w-full flex-col gap-8px">
    <div class="flex items-center gap-8px">
      <NInput v-model:value="text" size="small" class="w-140px" placeholder="按钮文字，如 客服中心" @update:value="push" />
      <NInput v-model:value="url" size="small" class="flex-1" placeholder="跳转链接 https://…（留空则不跳转）" @update:value="push" />
      <NButton v-if="enabled" size="small" quaternary type="error" @click="clear">清除</NButton>
    </div>
    <div class="flex items-center gap-8px">
      <span class="text-11px text-gray-400">
        {{ enabled ? "将显示在 storefront 顶部导航（新窗口打开）：" : "文字留空 = 不显示；填写后显示在 storefront 顶部导航" }}
      </span>
      <span v-if="enabled" class="rounded-4px border border-gray-300 px-8px py-2px text-12px dark:border-gray-600">
        {{ text }}
      </span>
    </div>
  </div>
</template>
