<script setup lang="ts">
import { NCard, useThemeVars } from "naive-ui";

const themeVars = useThemeVars();
</script>

<template>
  <NCard title="广告赞助" :bordered="false" size="small">
    <template #header-extra>
      <span class="sponsor-label">支持开源 · 合作共建</span>
    </template>
    <a
      class="sponsor-placeholder"
      href="https://t.me/ZhonCard"
      target="_blank"
      rel="noopener noreferrer"
      aria-label="成为 ZCard 赞助伙伴，前往 Telegram 群组 @ZhonCard（新窗口打开）"
    >
      <span class="sponsor-mark" aria-hidden="true">ZC</span>
      <span class="sponsor-copy">
        <span class="sponsor-title">成为 ZCard 赞助伙伴</span>
        <span class="sponsor-description">此广告位开放合作，欢迎支持 ZCard 的持续开发。</span>
        <span class="sponsor-action">前往 Telegram 群组洽谈 <span aria-hidden="true">↗</span></span>
      </span>
    </a>
  </NCard>
</template>

<style scoped>
.sponsor-label,
.sponsor-description {
  color: v-bind('themeVars.textColor2');
  font-size: 12px;
  line-height: 1.6;
}

.sponsor-placeholder {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
  border: 1px dashed v-bind('themeVars.borderColor');
  border-radius: 8px;
  background: v-bind('themeVars.actionColor');
}

.sponsor-placeholder:hover {
  border-color: v-bind('themeVars.primaryColor');
}

.sponsor-placeholder:focus-visible {
  outline: 2px solid v-bind('themeVars.primaryColor');
  outline-offset: 3px;
}

.sponsor-mark {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border: 1px solid v-bind('themeVars.borderColor');
  border-radius: 12px;
  background: v-bind('themeVars.cardColor');
  color: v-bind('themeVars.primaryColor');
  font-size: 18px;
  font-weight: 700;
}

.sponsor-copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
}

.sponsor-title {
  color: v-bind('themeVars.textColor1');
  font-size: 14px;
  font-weight: 600;
}

.sponsor-action {
  margin-top: 4px;
  color: v-bind('themeVars.primaryColor');
  font-size: 13px;
}

@media (max-width: 480px) {
  .sponsor-placeholder {
    align-items: flex-start;
    gap: 12px;
    padding: 16px;
  }
}
</style>
