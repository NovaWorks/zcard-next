<script setup lang="ts">
// 结构化链接列表编辑器（footer.nav / footer.social / promo.nav_recommend 共用）：
// 值为 JSON 数组 [{text|icon, url}]；空列表存 null（前台回落默认）。
// 替代原 JSON textarea（大厂模式：结构化行编辑 + 上下移排序 + 增删，杜绝手写 JSON 出错）。
import { ref, watch } from "vue";
import { NButton, NInput } from "naive-ui";

type LinkItem = { text?: string; icon?: string; url?: string };

const props = withDefaults(
  defineProps<{
    value: string | null;
    /** true = 图标模式（[{icon,url}]，如页脚社交）；false = 文字模式（[{text,url}]） */
    icon?: boolean;
    textPlaceholder?: string;
    urlPlaceholder?: string;
    max?: number;
  }>(),
  { icon: false, textPlaceholder: "显示文字", urlPlaceholder: "跳转链接 https://… 或站内路径 /products", max: 8 },
);

const emit = defineEmits<{ (e: "update", v: LinkItem[] | null): void }>();

const items = ref<LinkItem[]>([]);

function parse(v: string | null) {
  try {
    const o = JSON.parse(v || "null");
    if (Array.isArray(o)) {
      items.value = o
        .filter((x: any) => x && typeof x === "object")
        .map((x: any) => ({
          text: typeof x.text === "string" ? x.text : "",
          icon: typeof x.icon === "string" ? x.icon : "",
          url: typeof x.url === "string" ? x.url : "",
        }));
      return;
    }
  } catch {
    /* 非法 JSON（旧 textarea 手写残留）：按空处理，保存时被结构化值覆盖 */
  }
  items.value = [];
}
parse(props.value);
watch(() => props.value, (v) => parse(v));

function push() {
  const clean = items.value
    .map((it) => (props.icon ? { icon: (it.icon || "").trim(), url: (it.url || "").trim() } : { text: (it.text || "").trim(), url: (it.url || "").trim() }))
    .filter((it) => (props.icon ? it.icon : it.text));
  emit("update", clean.length ? clean : null);
}

function add() {
  if (items.value.length >= props.max) return;
  items.value.push(props.icon ? { icon: "", url: "" } : { text: "", url: "" });
}

function remove(i: number) {
  items.value.splice(i, 1);
  push();
}

function move(i: number, dir: -1 | 1) {
  const j = i + dir;
  if (j < 0 || j >= items.value.length) return;
  const arr = [...items.value];
  [arr[i], arr[j]] = [arr[j], arr[i]];
  items.value = arr;
  push();
}
</script>

<template>
  <div class="flex w-full flex-col gap-6px">
    <div v-for="(it, i) in items" :key="i" class="flex items-center gap-6px">
      <div class="flex shrink-0 flex-col">
        <NButton size="tiny" quaternary :disabled="i === 0" class="h-12px px-2px" @click="move(i, -1)">▲</NButton>
        <NButton size="tiny" quaternary :disabled="i === items.length - 1" class="h-12px px-2px" @click="move(i, 1)">▼</NButton>
      </div>
      <NInput
        :value="icon ? it.icon : it.text"
        size="small"
        style="width: 110px; flex-shrink: 0;"
        :maxlength="icon ? 4 : 20"
        :placeholder="icon ? '图标 emoji' : textPlaceholder"
        @update:value="(v: string) => { icon ? (it.icon = v) : (it.text = v); push(); }"
      />
      <NInput
        v-model:value="it.url"
        size="small"
        style="flex: 1 1 0; min-width: 140px;"
        :placeholder="urlPlaceholder"
        @update:value="push"
      />
      <NButton size="small" quaternary type="error" class="shrink-0" @click="remove(i)">删除</NButton>
    </div>
    <div class="flex items-center gap-8px">
      <NButton v-if="items.length < max" size="small" dashed class="w-96px" @click="add">+ 添加</NButton>
      <span v-if="!items.length" class="text-11px text-gray-400">未配置——前台显示默认内容</span>
    </div>
  </div>
</template>
