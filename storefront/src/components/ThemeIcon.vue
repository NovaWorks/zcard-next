<script setup lang="ts">
defineProps<{ name: 'announcement' | 'cart' | 'book' | 'grid' | 'folder' | 'trash' }>();
</script>

<template>
  <svg class="theme-icon" :data-icon="name" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
    <template v-if="name === 'announcement'">
      <path d="M4 9h4l10-4v14L8 15H4a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1Z" />
      <path d="M8 9v6m-3 0 1 5h3l-1-5m13-5v4" />
    </template>
    <template v-else-if="name === 'cart'">
      <path d="M3 4h2l2.2 11h11.6l2-8H6m1.2 8L7 18h12" />
      <circle cx="9" cy="21" r="1" /><circle cx="18" cy="21" r="1" />
    </template>
    <template v-else-if="name === 'book'">
      <path d="M12 6C9 4 6 4 3 5v14c3-1 6-1 9 1 3-2 6-2 9-1V5c-3-1-6-1-9 1Zm0 0v14" />
      <path d="M6 9c1-.2 2-.1 3 .3m6 0c1-.4 2-.5 3-.3M6 13c1-.2 2-.1 3 .3m6 0c1-.4 2-.5 3-.3" />
    </template>
    <template v-else-if="name === 'grid'">
      <rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" />
      <rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" />
    </template>
    <path v-else-if="name === 'folder'" d="M3 7V5a1 1 0 0 1 1-1h5l2 3h9a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7Zm0 3h18" />
    <template v-else-if="name === 'trash'">
      <path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7" />
    </template>
  </svg>
</template>

<style scoped>
.theme-icon { width: 1em; height: 1em; display: inline-block; vertical-align: -0.125em; flex-shrink: 0; }
</style>
