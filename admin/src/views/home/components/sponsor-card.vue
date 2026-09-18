<script setup lang="ts">
import { NCard } from "naive-ui";
import SponsorSlots from "@/components/common/sponsor-slots.vue";
</script>

<template>
  <NCard title="广告赞助" :bordered="false" size="small" class="home-sponsor">
    <template #header-extra>
      <span class="text-12px opacity-60">支持开源 · 合作共建</span>
    </template>
    <SponsorSlots />
  </NCard>
</template>
