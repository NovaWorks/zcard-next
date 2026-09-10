<template>
  <section v-if="banners.length" class="banner-strip" :aria-label="label">
    <component :is="banner.link_type === 'notice' || banner.link_value ? 'button' : 'div'"
      v-for="banner in banners" :key="banner.id" class="banner-item"
      :type="banner.link_type === 'notice' || banner.link_value ? 'button' : undefined"
      :aria-label="banner.title || '查看横幅内容'" @click="$emit('open', banner)">
      <picture>
        <source v-if="banner.mobile_image" media="(max-width: 639px)" :srcset="banner.mobile_image" />
        <img :src="banner.image" :alt="banner.title || ''" loading="lazy" />
      </picture>
    </component>
  </section>
</template>
<script setup lang="ts">
import type { Banner } from '@/api';
defineProps<{ banners: Banner[]; label: string }>();
defineEmits<{ open: [banner: Banner] }>();
</script>
<style scoped>
.banner-strip { display: grid; gap: 12px; margin: 16px 0; min-width: 0; }
.banner-item { display: block; width: 100%; padding: 0; border: 0; border-radius: 12px; overflow: hidden; background: transparent; }
button.banner-item { cursor: pointer; }
button.banner-item:focus-visible { outline: 3px solid var(--primary, #2563eb); outline-offset: 3px; }
.banner-item picture, .banner-item img { display: block; width: 100%; height: auto; }
</style>
