<script setup lang="ts">
// 渠道管理：上游货源连接 / 供货账号（下游客户，含 dujiao/acg 兼容账号）/ 采购单。
// 权限：supply:read（货源）、supplier:read（供货账号）、procurement:read（采购单）。
// 支持 ?tab= 深链（首页待办「待审对接申请」直达审核视图）。
import { ref, defineAsyncComponent, watch } from "vue";
import { useRoute } from "vue-router";
import { NCard, NTabs, NTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import ConnectionsTab from "./components/connections-tab.vue";
const SupplierAccountsTab = defineAsyncComponent(() => import("./components/supplier-accounts-tab.vue"));
const ProcurementTab = defineAsyncComponent(() => import("./components/procurement-tab.vue"));

defineOptions({ name: "ChannelManagement" });

const route = useRoute();
const visibleTabs = [
  { key: "connections", auth: "supply:read" },
  { key: "suppliers", auth: "supplier:read" },
  { key: "procurement", auth: "procurement:read" },
].filter((t) => checkAuth(t.auth));

const activeTab = ref<string>(
  visibleTabs.some((t) => t.key === route.query.tab) ? String(route.query.tab) : (visibleTabs[0]?.key || "connections"),
);
watch(() => route.query.tab, tab => { if (visibleTabs.some(t => t.key === tab)) activeTab.value = String(tab); });
</script>

<template>
  <div class="min-h-500px">
    <NCard title="渠道管理">
      <NTabs v-model:value="activeTab" type="line">
        <NTabPane v-if="checkAuth('supply:read')" name="connections" tab="货源渠道">
          <ConnectionsTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('supplier:read')" display-directive="show:lazy" name="suppliers" tab="供货账号">
          <SupplierAccountsTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('procurement:read')" display-directive="show:lazy" name="procurement" tab="采购单">
          <ProcurementTab />
        </NTabPane>
      </NTabs>
    </NCard>
  </div>
</template>
