<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { $t } from "@/locales";
import { fetchMarketConfig } from "@/service/api/plugin";
import MarketAccountSession from "./market-account-session.vue";
const props = defineProps<{ disabled: boolean }>();
const emit = defineEmits<{
  account: [id: string];
  busy: [value: boolean];
  updated: [];
  navigate: [];
}>();
const origin = ref("");
async function refresh() {
  const r = await fetchMarketConfig();
  origin.value = r.data?.origin || "";
}
onMounted(() => {
  void refresh();
  window.addEventListener("zcard-market-context-clear", refresh);
});
onUnmounted(() =>
  window.removeEventListener("zcard-market-context-clear", refresh),
);
</script>
<template>
  <MarketAccountSession
    v-if="origin"
    :key="origin"
    :origin="origin"
    :disabled="props.disabled"
    @account="(id) => emit('account', id)"
    @updated="emit('updated')"
    @navigate="emit('navigate')"
    @busy="(value) => emit('busy', value)"
  />
  <template v-else
    ><Teleport to="#plugin-account-slot"
      ><NButton disabled>{{ $t("plugin.loginShort") }}</NButton></Teleport
    ><NAlert type="info">{{
      $t("plugin.marketUnconfigured")
    }}</NAlert></template
  >
</template>
