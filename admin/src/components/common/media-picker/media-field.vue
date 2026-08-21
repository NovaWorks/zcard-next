<script setup lang="ts">
/**
 * 图片字段（封面/图集统一入口）：呼出素材选择器弹窗（复用已有素材或就地上传）。
 * v-model:value = URL 数组；multiple 控制单图/图集；单图模式选择即替换。
 */
import { computed } from "vue";
import { NButton, NImage } from "naive-ui";
import { pickMedia } from "@/components/common/media-picker";
import { resolveMediaUrl } from "@/utils/media";

const props = withDefaults(
  defineProps<{
    value?: string[];
    multiple?: boolean;
    tip?: string;
  }>(),
  { value: () => [], multiple: false, tip: "" },
);

const emit = defineEmits<{ (e: "update:value", urls: string[]): void }>();

const urls = computed(() => props.value || []);

async function openPicker() {
  const picked = await pickMedia({ multiple: props.multiple });
  if (!picked?.length) return; // 取消
  emit("update:value", picked);
}

function removeUrl(url: string) {
  emit(
    "update:value",
    urls.value.filter((u) => u !== url),
  );
}
</script>

<template>
  <div class="flex flex-col gap-8px">
    <div v-if="urls.length" class="flex flex-wrap gap-8px">
      <div v-for="url in urls" :key="url" class="group relative">
        <NImage :src="resolveMediaUrl(url)" width="72" height="72" object-fit="cover" class="rounded-4px" />
        <NButton
          size="tiny"
          type="error"
          class="absolute right--6px top--6px opacity-0 group-hover:opacity-100"
          circle
          @click="removeUrl(url)"
        >
          ×
        </NButton>
      </div>
    </div>
    <div class="flex items-center gap-8px">
      <NButton size="small" @click="openPicker">
        {{ multiple ? "从素材库选择" : urls.length ? "更换图片" : "从素材库选择" }}
      </NButton>
      <span v-if="tip" class="text-12px text-gray-400">{{ tip }}</span>
    </div>
  </div>
</template>
