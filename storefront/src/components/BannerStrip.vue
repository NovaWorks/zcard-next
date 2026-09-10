<template>
  <section v-if="banners.length" class="banner-strip" :class="{ 'banner-strip--catalog': catalog, 'banner-strip--multiple': banners.length > 1 }" :aria-label="label" :tabindex="catalog && banners.length > 1 ? 0 : undefined">
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
defineProps<{ banners: Banner[]; label: string; catalog?: boolean }>();
defineEmits<{ open: [banner: Banner] }>();
</script>
<style scoped>
.banner-strip { display: grid; gap: 12px; margin: 16px 0; min-width: 0; }
.banner-item { display: block; width: 100%; padding: 0; border: 0; border-radius: 12px; overflow: hidden; background: transparent; }
button.banner-item { cursor: pointer; }
button.banner-item:focus-visible { outline: 3px solid var(--primary, #2563eb); outline-offset: 3px; }
.banner-item picture, .banner-item img { display: block; width: 100%; height: auto; }
/* 商品流横幅沿用商品间距，保留完整图片，避免高图撑满屏幕。 */
.banner-strip--catalog { grid-column: 1 / -1; margin: 0; border-radius: 12px; }
.banner-strip--catalog.banner-strip--multiple { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.banner-strip--catalog .banner-item { background: var(--card-bg, #fff); border: 1px solid var(--border-color, #e5e7eb); }
.banner-strip--catalog .banner-item img { max-height: 180px; object-fit: contain; }
.banner-strip--catalog:focus-visible { outline: 3px solid var(--primary, #2563eb); outline-offset: 3px; }
@media (max-width: 767px) {
  .banner-strip--catalog .banner-item img { max-height: 140px; }
  .banner-strip--catalog.banner-strip--multiple { display: flex; overflow-x: auto; gap: 10px; scroll-snap-type: x mandatory; overscroll-behavior-x: contain; padding-bottom: 6px; }
  .banner-strip--catalog.banner-strip--multiple .banner-item { flex: 0 0 calc(100% - 24px); scroll-snap-align: start; }
}
</style>
