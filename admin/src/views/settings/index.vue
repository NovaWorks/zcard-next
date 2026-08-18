<script setup lang="ts">
import { ref, onMounted } from "vue";
import { fetchSettings, updateSetting } from "@/service/api";
import { NTabs as OuterTabs, NTabPane as OuterTabPane } from "naive-ui";
import { checkAuth } from "@/directives";
import CurrencyTab from "./components/currency-tab.vue";
import AuditTab from "./components/audit-tab.vue";

defineOptions({ name: "SettingsManagement" });

const loading = ref(false);
const activeGroup = ref("site");
const items = ref<any[]>([]);
const savingKey = ref("");

const groups = [
  { key: "site", label: "站点基础" },
  { key: "template", label: "模板" },
  { key: "trade", label: "交易" },
  { key: "security", label: "安全" },
  { key: "ops", label: "运维" },
  { key: "recharge", label: "充值" },
  { key: "notify", label: "邮件短信" },
  { key: "i18n", label: "语言货币" },
];

function getVal(item: any) {
  try {
    return JSON.parse(item.value_json);
  } catch {
    return item.value_json;
  }
}

function setVal(item: any, val: any) {
  item.value_json = JSON.stringify(val);
}

async function loadSettings() {
  loading.value = true;
  items.value = [];
  try {
    const { data, error } = await fetchSettings(activeGroup.value);
    if (!error && data) {
      items.value = (data as any).items || [];
    }
  } finally {
    loading.value = false;
  }
}

async function handleSave(item: any) {
  savingKey.value = item.key;
  try {
    const { error } = await updateSetting(activeGroup.value, item.key, item.value_json);
    if (!error) window.$message?.success(`${item.key} 已保存`);
  } finally {
    savingKey.value = "";
  }
}

onMounted(loadSettings);
</script>

<template>
  <div class="min-h-500px">
    <NCard title="系统设置">
      <OuterTabs type="line">
      <OuterTabPane name="settings" tab="参数设置">
      <NTabs v-model:value="activeGroup" type="line" @update:value="loadSettings">
        <NTabPane v-for="g in groups" :key="g.key" :name="g.key" :tab="g.label" />
      </NTabs>

      <div v-if="loading" class="py-40px text-center">
        <NSpin size="large" />
      </div>

      <NForm v-else label-placement="left" label-width="140" class="mt-16px max-w-640px">
        <NFormItem v-for="item in items" :key="item.key" :label="item.key">
          <div class="flex w-full items-center gap-8px">
            <template v-if="typeof getVal(item) === 'boolean'">
              <NSwitch :value="getVal(item)" @update:value="(v: boolean) => setVal(item, v)" />
            </template>
            <template v-else-if="typeof getVal(item) === 'number'">
              <NInputNumber
                :value="getVal(item)"
                class="flex-1"
                @update:value="(v: number | null) => v !== null && setVal(item, v)"
              />
            </template>
            <template v-else>
              <NInput
                :value="String(getVal(item) ?? '')"
                class="flex-1"
                @update:value="(v: string) => setVal(item, v)"
              />
            </template>
            <NButton
              v-auth="'settings:update'"
              size="small"
              type="primary"
              :loading="savingKey === item.key"
              @click="handleSave(item)"
            >
              保存
            </NButton>
          </div>
        </NFormItem>
      </NForm>
          </OuterTabPane>
      <OuterTabPane v-if="checkAuth('settings:currency_read')" name="currency" tab="货币">
        <CurrencyTab />
      </OuterTabPane>
      <OuterTabPane v-if="checkAuth('audit:read')" name="audit" tab="审计日志">
        <AuditTab />
      </OuterTabPane>
      </OuterTabs>
    </NCard>
  </div>
</template>
