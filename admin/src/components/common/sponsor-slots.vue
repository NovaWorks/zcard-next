<script setup lang="ts">
import { useThemeVars } from "naive-ui";

const themeVars = useThemeVars();
const slots = [
  { id: "brand", title: "品牌推广", description: "广告位开放合作" },
  { id: "partner", title: "合作伙伴", description: "支持 ZCard 持续开发" },
];
</script>

<template>
  <div class="sponsor-slots" role="group" aria-label="广告赞助">
    <a v-for="slot in slots" :key="slot.id" class="sponsor-slot" href="https://t.me/ZhonCard"
      target="_blank" rel="noopener noreferrer" :aria-label="`${slot.title}广告位，前往 Telegram 洽谈（新窗口打开）`">
      <span class="sponsor-copy">
        <span class="sponsor-title">{{ slot.title }} <span class="sponsor-label">广告</span></span>
        <span class="sponsor-description">{{ slot.description }}</span>
      </span>
      <span class="sponsor-action" aria-hidden="true">洽谈 ↗</span>
    </a>
  </div>
</template>

<style scoped>
.sponsor-slots { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; min-width: 0; }
.sponsor-slot { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-width: 0; padding: 10px 14px; border: 1px dashed v-bind('themeVars.borderColor'); border-radius: 8px; background: v-bind('themeVars.actionColor'); }
.sponsor-slot:hover { border-color: v-bind('themeVars.primaryColor'); }
.sponsor-slot:focus-visible { outline: 2px solid v-bind('themeVars.primaryColor'); outline-offset: 2px; }
.sponsor-copy { display: flex; min-width: 0; flex-direction: column; gap: 2px; }
.sponsor-title { color: v-bind('themeVars.textColor1'); font-size: 13px; font-weight: 600; }
.sponsor-description, .sponsor-label { color: v-bind('themeVars.textColor2'); font-size: 12px; line-height: 1.5; }
.sponsor-label { margin-left: 4px; font-weight: 400; }
.sponsor-action { flex-shrink: 0; color: v-bind('themeVars.primaryColor'); font-size: 12px; }
@media (max-width: 639px) {
  .sponsor-slot { align-items: flex-start; flex-direction: column; gap: 4px; padding: 10px; }
}
</style>
