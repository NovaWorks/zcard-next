<script setup lang="ts">
// 渠道管理：上游货源连接 / 供货账号（下游客户，含 dujiao/acg 兼容账号）/ 采购单。
// 权限：supply:read（货源）、supplier:read（供货账号）、procurement:read（采购单）。
import { NCard, NTabs, NTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import ConnectionsTab from "./components/connections-tab.vue";
import SupplierAccountsTab from "./components/supplier-accounts-tab.vue";
import ProcurementTab from "./components/procurement-tab.vue";

defineOptions({ name: "ChannelManagement" });
</script>

<template>
  <div class="min-h-500px">
    <NCard title="渠道管理">
      <NTabs type="line">
        <NTabPane v-if="checkAuth('supply:read')" name="connections" tab="货源渠道">
          <ConnectionsTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('supplier:read')" name="suppliers" tab="供货账号">
          <SupplierAccountsTab />
        </NTabPane>
        <NTabPane v-if="checkAuth('procurement:read')" name="procurement" tab="采购单">
          <ProcurementTab />
        </NTabPane>
      </NTabs>
    </NCard>
  </div>
</template>
