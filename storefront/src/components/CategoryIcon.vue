<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{ icon?: string }>();
const image = computed(() => !!props.icon && (
  props.icon.includes('/') || /\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)(?:[?#].*)?$/i.test(props.icon)
));
const src = computed(() => {
  const value = props.icon || '';
  return value.startsWith('/') || /^(https?:|data:image\/)/i.test(value) ? value : `/${value}`;
});
</script>

<template>
  <img v-if="image" :src="src" class="category-icon" alt="" />
  <span v-else-if="icon" class="category-icon" aria-hidden="true">{{ icon }}</span>
</template>

<style scoped>
.category-icon { width: 1em; height: 1em; flex-shrink: 0; display: inline-block; vertical-align: middle; }
img.category-icon { object-fit: contain; }
</style>
