<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { resolveMediaUrl } from "@/utils/media";
import { categoryIconImagePath } from "@/utils/category-icon";

const props = withDefaults(defineProps<{ icon?: string; fallback?: string }>(), { fallback: "🏷️" });
const failed = ref(false);
const imagePath = computed(() => categoryIconImagePath(props.icon));
watch(() => props.icon, () => { failed.value = false; });
</script>

<template>
  <span class="category-icon" aria-hidden="true">
    <img v-if="imagePath && !failed" :src="resolveMediaUrl(imagePath)" alt="" @error="failed = true" />
    <template v-else>{{ imagePath ? fallback : icon || fallback }}</template>
  </span>
</template>

<style scoped>
.category-icon { display: inline-flex; align-items: center; justify-content: center; width: 1.25em; height: 1.25em; flex-shrink: 0; vertical-align: middle; }
.category-icon img { display: block; width: 100%; height: 100%; object-fit: contain; }
</style>
