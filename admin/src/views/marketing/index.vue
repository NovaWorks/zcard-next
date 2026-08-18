<script setup lang="ts">
// 营销管理（中优先级接入）：会员等级 / 优惠券 / 秒杀促销 / 内容管理。
import { NTabs, NTabPane, NCard } from "naive-ui";
import LevelsTab from "./components/levels-tab.vue";
import CouponsTab from "./components/coupons-tab.vue";
import FlashTab from "./components/flash-tab.vue";
import ContentTab from "./components/content-tab.vue";
import { checkAuth } from "@/directives";

defineOptions({ name: "MarketingManagement", route: "marketing" });
</script>

<template>
  <div class="min-h-500px">
    <NCard title="营销管理">
      <NTabs type="line">
        <NTabPane v-if="checkAuth('memberlevel:read')" name="levels" tab="会员等级">
          <LevelsTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('coupon:read')" name="coupons" tab="优惠券">
          <CouponsTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('coupon:read')" name="flash" tab="秒杀/促销">
          <FlashTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('content:read')" name="content" tab="内容管理">
          <ContentTab />
        </NTabPane>
      </NTabs>
    </NCard>
  </div>
</template>
