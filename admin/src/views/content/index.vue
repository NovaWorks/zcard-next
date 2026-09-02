<script setup lang="ts">
// 内容管理：文章/公告（文章管理）+ 首页横幅两个子页（content:read / write）。
import { NTabs, NTabPane, NCard } from "naive-ui";
import { checkAuth } from "@/directives";
import ArticleManagement from "./components/article-management.vue";
import BannerManagement from "./components/banner-management.vue";

defineOptions({ name: "ContentManagement", route: "content" });
</script>

<template>
  <div class="min-h-500px">
    <NCard title="内容管理">
      <NTabs type="line">
        <NTabPane v-if="checkAuth('content:read')" name="articles" tab="文章管理">
          <ArticleManagement />
        </NTabPane>
        <NTabPane v-if="checkAuth('content:read')" name="banners" tab="首页横幅">
          <BannerManagement />
        </NTabPane>
      </NTabs>
    </NCard>
  </div>
</template>
