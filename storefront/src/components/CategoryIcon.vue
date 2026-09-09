<script setup lang="ts">
import ThemeIcon from '@/components/ThemeIcon.vue';
import { computed, ref, watch } from 'vue';

const props = defineProps<{ icon?: string }>();
const failed = ref(false);
watch(() => props.icon, () => { failed.value = false; });
const image = computed(() => !!props.icon && (
  props.icon.includes('/') || /\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)(?:[?#].*)?$/i.test(props.icon)
));
const src = computed(() => {
  const value = props.icon || '';
  return value.startsWith('/') || /^(https?:|data:image\/)/i.test(value) ? value : `/${value}`;
});
</script>

<template>
  <img v-if="image && !failed" :src="src" @error="failed = true" class="category-icon" alt="" />
  <span v-else-if="icon && !image" class="category-icon" aria-hidden="true">{{ icon }}</span>
  <ThemeIcon v-else name="folder" class="category-icon" />
</template>

<style scoped>
.category-icon { width: 1em; height: 1em; flex-shrink: 0; display: inline-block; vertical-align: middle; }
img.category-icon { object-fit: contain; }
</style>
