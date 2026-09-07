<script setup lang="ts">
import { useThemeVars } from "naive-ui";
import DarkModeContainer from "@/components/common/dark-mode-container.vue";

defineOptions({
  name: "GlobalFooter",
});

const themeVars = useThemeVars();
</script>

<template>
  <DarkModeContainer class="h-full flex-center px-12px">
    <footer class="project-footer" aria-label="ZCard 版权与社区">
      <span class="copyright">
        © 2026 <strong>ZCard</strong>
        <span aria-hidden="true">·</span>
        <a href="https://github.com/soybeanjs/soybean-admin/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">MIT</a>
      </span>
      <a href="https://github.com/NovaWorks/zcard-next" target="_blank" rel="noopener noreferrer">GitHub 仓库</a>
      <a href="https://t.me/ZhonCard" target="_blank" rel="noopener noreferrer">群组 @ZhonCard</a>
      <a href="https://t.me/ZCardGroup" target="_blank" rel="noopener noreferrer">频道 @ZCardGroup</a>
    </footer>
  </DarkModeContainer>
</template>

<style scoped>
.project-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  column-gap: 20px;
  padding: 8px 0;
  color: v-bind('themeVars.textColor2');
  font-size: 12px;
  line-height: 28px;
}

.copyright {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  white-space: nowrap;
}

a {
  white-space: nowrap;
  border-radius: 4px;
}

a:hover {
  color: v-bind('themeVars.primaryColor');
  text-decoration: underline;
  text-underline-offset: 3px;
}

a:focus-visible {
  outline: 2px solid v-bind('themeVars.primaryColor');
  outline-offset: 3px;
}
</style>
