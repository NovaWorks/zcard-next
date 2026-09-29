<script setup lang="ts">
import { onMounted, ref } from "vue";
import { $t } from "@/locales";
import { fetchMarketConfig, saveMarketConfig } from "@/service/api/plugin";
import { clearMarketBrowserContext } from "@/hooks/business/market-account";
const props = defineProps<{ disabled: boolean }>();
const emit = defineEmits<{ saved: []; busy: [value: boolean] }>();
const origin = ref("https://store.zcard.dev"),
  ready = ref(false),
  busy = ref(false),
  failure = ref("");
async function save() {
  if (!ready.value || busy.value || props.disabled) return;
  busy.value = true;
  emit("busy", true);
  try {
    const result = await saveMarketConfig(origin.value.trim());
    if (result.error || !result.data) {
      failure.value = $t("plugin.marketConfigError");
      return;
    }
    origin.value = result.data.origin;
    failure.value = "";
    clearMarketBrowserContext();
    emit("saved");
    window.$message?.success($t("plugin.marketSave"));
  } finally {
    busy.value = false;
    emit("busy", false);
  }
}
async function load() {
  if (busy.value) return;
  busy.value = true;
  emit("busy", true);
  try {
    const result = await fetchMarketConfig();
    if (result.data) {
      origin.value = result.data.origin;
      ready.value = true;
      failure.value = "";
    } else failure.value = $t("plugin.marketConfigError");
  } finally {
    busy.value = false;
    emit("busy", false);
  }
}
onMounted(load);
</script>
<template>
  <NCard :title="$t('plugin.settingsTab')" class="settings-card"
    ><p class="mb-16px">{{ $t("plugin.marketAddressHelp") }}</p>
    <NForm @submit.prevent="save"
      ><NFormItem :label="$t('plugin.marketOrigin')"
        ><NInput
          v-model:value="origin"
          :disabled="!ready || busy || disabled"
          placeholder="https://store.zcard.dev" /></NFormItem
      ><NAlert v-if="failure" type="error" class="mb-16px" role="alert">{{
        failure
      }}</NAlert
      ><NButton
        type="primary"
        attr-type="submit"
        :disabled="!ready || disabled"
        :loading="busy"
        >{{ $t("plugin.marketSave") }}</NButton
      ></NForm
    >
    <NButton v-if="!ready && !busy" @click="load">{{
      $t("plugin.refresh")
    }}</NButton>
    <p class="mt-16px opacity-60">
      {{ $t("plugin.marketAddressTrust") }}
    </p></NCard
  >
</template>
<style scoped>
.settings-card {
  max-width: 800px;
}
</style>
