<script setup lang="ts">
// 站点顶部自定义按钮设置（site.top_button JSON）：
// {text, type: link|post|notice, url?, slug?}——外部链接 / 站内文章 / 站内公告三态跳转；
// 文字留空 = 不显示；保存为 JSON。
import { computed, ref, watch } from "vue";
import { NButton, NInput, NRadioButton, NRadioGroup, NSelect } from "naive-ui";
import { fetchPosts } from "@/service/api";

const props = defineProps<{ value: string | null }>();
const emit = defineEmits<{ (e: "update", v: { text: string; type: string; url?: string; slug?: string } | null): void }>();

const text = ref("");
const type = ref<"link" | "post" | "notice">("link");
const url = ref("");
const slug = ref<string | null>(null);

// 文章/公告下拉（复用营销-轮播的既有模式：zhValue 解析 title_json）
const posts = ref<any[]>([]);
const postsLoading = ref(false);

function zhValue(json: string): string {
  if (!json) return "-";
  try {
    const v = JSON.parse(json);
    return v.zh_CN || v.zh || Object.values(v)[0] || "-";
  } catch {
    return json;
  }
}

const postOptions = computed(() =>
  posts.value
    .filter((p) => (type.value === "notice" ? p.type === "notice" : true))
    .map((p) => ({ label: `${p.type === "notice" ? "[公告] " : ""}${zhValue(p.title_json)}`, value: p.slug })),
);

async function loadPosts() {
  if (posts.value.length || postsLoading.value) return;
  postsLoading.value = true;
  try {
    const { data, error } = await fetchPosts();
    if (!error && data) posts.value = (data as any).posts || [];
  } finally {
    postsLoading.value = false;
  }
}

function parse(v: string | null) {
  try {
    const o = JSON.parse(v || "null");
    if (o && typeof o === "object") {
      text.value = typeof o.text === "string" ? o.text : "";
      // 兼容旧结构 {text,url}（无 type 字段按外部链接处理）
      const t = o.type === "post" || o.type === "notice" ? o.type : "link";
      type.value = t;
      url.value = typeof o.url === "string" ? o.url : "";
      slug.value = typeof o.slug === "string" ? o.slug : null;
      return;
    }
  } catch {
    /* 非法 JSON 按空处理 */
  }
  text.value = "";
  type.value = "link";
  url.value = "";
  slug.value = null;
}
parse(props.value);
watch(() => props.value, parse);

// 切换跳转类型：预载文章列表；清掉不属于新类型的旧值
watch(type, (t) => {
  if (t !== "link") {
    loadPosts();
    url.value = "";
  } else {
    slug.value = null;
  }
});

const enabled = computed(() => !!text.value.trim());

function push() {
  const t = text.value.trim();
  // 文字留空 = 不启用（存 null）；父级 setVal 统一 JSON.stringify
  if (!t) {
    emit("update", null);
    return;
  }
  if (type.value === "link") {
    emit("update", { text: t, type: "link", url: url.value.trim() });
  } else {
    emit("update", { text: t, type: type.value, slug: slug.value || "" });
  }
}

// 跳转目标描述（提示行用）
const targetDesc = computed(() => {
  if (!enabled.value) return "";
  if (type.value === "link") {
    return url.value.trim() ? `点击打开 ${url.value.trim()}（新窗口）` : "未填链接——按钮只显示不跳转";
  }
  const opt = postOptions.value.find((o) => o.value === slug.value);
  return opt ? `点击跳转${type.value === "notice" ? "公告" : "文章"}「${opt.label}」` : "尚未选择目标文章";
});

function clear() {
  text.value = "";
  url.value = "";
  slug.value = null;
  push();
}
</script>

<template>
  <div class="flex w-full flex-col gap-8px">
    <div class="flex items-center gap-8px">
      <NInput v-model:value="text" size="small" class="w-140px shrink-0" placeholder="按钮文字，如 客服中心" @update:value="push" />
      <NRadioGroup v-model:value="type" size="small" @update:value="push">
        <NRadioButton value="link">外部链接</NRadioButton>
        <NRadioButton value="post">文章</NRadioButton>
        <NRadioButton value="notice">公告</NRadioButton>
      </NRadioGroup>
      <NButton v-if="enabled" size="small" quaternary type="error" class="shrink-0" @click="clear">清除</NButton>
    </div>
    <div class="flex items-center gap-8px">
      <NInput
        v-if="type === 'link'"
        v-model:value="url"
        size="small"
        class="flex-1"
        placeholder="跳转链接 https://…（留空则不跳转）"
        @update:value="push"
      />
      <NSelect
        v-else
        v-model:value="slug"
        size="small"
        class="flex-1"
        filterable
        clearable
        :loading="postsLoading"
        :options="postOptions"
        :placeholder="type === 'notice' ? '选择要跳转的公告文章' : '选择要跳转的文章'"
        @update:value="push"
      />
    </div>
    <div class="flex items-center gap-8px">
      <span class="text-11px text-gray-400">
        {{ enabled ? `效果预览（将显示在 storefront 顶部导航）：${targetDesc}` : "文字留空 = 不显示；填写后显示在 storefront 顶部导航" }}
      </span>
      <span
        v-if="enabled"
        class="rounded-4px border border-primary px-8px py-2px text-12px font-500 text-primary"
        :title="`按钮「${text}」在 storefront 顶部导航的显示效果`"
      >
        {{ text }}
      </span>
    </div>
  </div>
</template>
