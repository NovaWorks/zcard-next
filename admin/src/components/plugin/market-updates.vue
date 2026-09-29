<script setup lang="ts">
import { ref, onMounted } from "vue";
import { $t } from "@/locales";
import { fetchMarketUpdates } from "@/service/api/market-account";
const props = defineProps<{ disabled: boolean }>();
const emit = defineEmits<{ detail: [id: string]; busy: [value: boolean] }>();
const rows = ref<
    { pluginId: string; currentVersion: string; latestVersion: string }[]
  >([]),
  busy = ref(false),
  failure = ref("");
async function refresh() {
  if (busy.value || props.disabled) return;
  busy.value = true;
  emit("busy", true);
  try {
    const r = await fetchMarketUpdates();
    if (r.data) {
      rows.value = r.data.items;
      failure.value = "";
    } else failure.value = $t("plugin.marketOffline");
  } finally {
    busy.value = false;
    emit("busy", false);
  }
}
onMounted(refresh);
</script>
<template>
  <NCard :title="$t('plugin.updatesTab')"
    ><template #header-extra
      ><NButton :disabled="disabled" :loading="busy" @click="refresh">{{
        $t("plugin.checkUpdates")
      }}</NButton></template
    ><NAlert v-if="failure" type="error" role="alert">{{ failure }}</NAlert
    ><NEmpty
      v-else-if="!busy && !rows.length"
      :description="$t('plugin.noUpdates')"
    /><NList v-else
      ><NListItem v-for="row in rows" :key="row.pluginId"
        ><NSpace justify="space-between"
          ><span
            >{{ row.pluginId }} · v{{ row.currentVersion }} → v{{
              row.latestVersion
            }}</span
          ><NButton
            :disabled="busy || disabled"
            @click="emit('detail', row.pluginId)"
            >{{ $t("plugin.productDetail") }}</NButton
          ></NSpace
        ></NListItem
      ></NList
    ></NCard
  >
</template>
