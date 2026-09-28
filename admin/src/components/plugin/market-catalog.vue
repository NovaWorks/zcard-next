<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { $t } from "@/locales";
import {
  fetchMarketConfig,
  saveMarketConfig,
  fetchMarketCatalog,
  inspectMarketPlugin,
  installMarketPlugin,
  type MarketCatalog,
  type MarketEntry,
  type PluginCommand,
  type PluginPackage,
  type PluginStatus,
  type PluginOperation,
} from "@/service/api/plugin";
const props = defineProps<{ plugins: PluginStatus[]; disabled: boolean }>();
const emit = defineEmits<{
  pending: [id: string];
  done: [operation: PluginOperation | undefined];
  busy: [value: boolean];
}>();
const origin = ref(""),
  savedOrigin = ref(""),
  busy = ref(false),
  failure = ref("");
const catalog = ref<MarketCatalog>(),
  page = ref(1),
  approved = ref(false),
  preview = ref<PluginPackage>(),
  selected = ref<MarketEntry>(),
  prepared = ref<PluginCommand>();
const entries = computed(
  () =>
    catalog.value?.catalog.entries.slice(
      (page.value - 1) * 10,
      page.value * 10,
    ) || [],
);
function running(value: boolean) {
  busy.value = value;
  emit("busy", value);
}
function clearPreview() {
  preview.value = undefined;
  selected.value = undefined;
  prepared.value = undefined;
  approved.value = false;
}
async function refresh() {
  if (busy.value || props.disabled) return;
  running(true);
  failure.value = "";
  clearPreview();
  const r = await fetchMarketCatalog();
  catalog.value = r.data || undefined;
  if (r.error || !r.data) failure.value = $t("plugin.marketOffline");
  page.value = 1;
  running(false);
}
async function save() {
  if (busy.value || props.disabled) return;
  running(true);
  clearPreview();
  catalog.value = undefined;
  const r = await saveMarketConfig(origin.value.trim());
  if (r.error || !r.data) failure.value = $t("plugin.marketConfigError");
  else {
    savedOrigin.value = r.data.origin;
    origin.value = r.data.origin;
    failure.value = "";
  }
  running(false);
}
async function inspect(entry: MarketEntry) {
  if (busy.value || props.disabled) return;
  clearPreview();
  failure.value = "";
  running(true);
  const installed = props.plugins.find(
    (p) => p.plugin_id === entry.descriptor.pluginId,
  );
  const command: PluginCommand = {
    plugin_id: entry.descriptor.pluginId,
    operation_id: crypto.randomUUID(),
    action: installed && !installed.uninstalled ? "upgrade" : "import",
    target_digest: entry.descriptor.archiveSHA256,
    expected_generation: installed?.desired_generation || "0",
    approved_scopes: [],
  };
  const r = await inspectMarketPlugin(
    savedOrigin.value,
    entry.descriptor.version,
    command,
  );
  if (r.error || !r.data) failure.value = $t("plugin.marketInspectError");
  else {
    preview.value = r.data;
    selected.value = entry;
    prepared.value = command;
  }
  running(false);
}
async function install() {
  if (
    busy.value ||
    props.disabled ||
    !approved.value ||
    !prepared.value ||
    !preview.value ||
    !selected.value
  )
    return;
  const command = { ...prepared.value, approved_scopes: preview.value.scopes };
  running(true);
  emit("pending", command.operation_id);
  const r = await installMarketPlugin(
    savedOrigin.value,
    selected.value.descriptor.version,
    command,
  );
  clearPreview();
  running(false);
  emit("done", r.data || undefined);
}
onMounted(async () => {
  running(true);
  const r = await fetchMarketConfig();
  if (r.data) {
    origin.value = r.data.origin;
    savedOrigin.value = origin.value;
  } else failure.value = $t("plugin.marketConfigError");
  running(false);
});
</script>
<template>
  <NCard :title="$t('plugin.marketTitle')">
    <p>{{ $t("plugin.marketIntro") }}</p>
    <NFormItem :label="$t('plugin.marketOrigin')" class="mt-16px"
      ><NInput
        v-model:value="origin"
        :disabled="busy || disabled"
        placeholder="https://market.example.com"
    /></NFormItem>
    <NSpace
      ><NButton :disabled="busy || disabled" @click="save">{{
        $t("plugin.marketSave")
      }}</NButton
      ><NButton
        :loading="busy"
        :disabled="busy || disabled || !savedOrigin || origin !== savedOrigin"
        @click="refresh"
        >{{ $t("plugin.marketRefresh") }}</NButton
      ></NSpace
    >
    <NAlert v-if="failure" type="error" role="alert" class="mt-16px">{{
      failure
    }}</NAlert>
    <p v-if="catalog" class="mt-16px">
      {{ $t("plugin.marketRevision") }} {{ catalog.catalog.revision }}
    </p>
    <NEmpty
      v-if="catalog && !catalog.catalog.entries.length"
      :description="$t('plugin.marketEmpty')"
    />
    <NList v-if="catalog"
      ><NListItem
        v-for="entry in entries"
        :key="entry.descriptor.archiveSHA256"
      >
        <NSpace justify="space-between" align="center"
          ><div>
            <strong>{{ entry.name }}</strong>
            <p>
              {{ entry.descriptor.pluginId }} · {{ entry.descriptor.version }} ·
              {{ $t("plugin.marketFree") }}
            </p>
            <p v-if="catalog.incompatible[entry.descriptor.archiveSHA256]">
              {{ $t("plugin.packageIncompatible") }}:
              {{ catalog.incompatible[entry.descriptor.archiveSHA256] }}
            </p>
          </div>
          <NButton
            :disabled="
              busy ||
              disabled ||
              !!catalog.incompatible[entry.descriptor.archiveSHA256]
            "
            @click="inspect(entry)"
            >{{ $t("plugin.marketPreview") }}</NButton
          ></NSpace
        >
      </NListItem></NList
    >
    <NPagination
      v-if="catalog && catalog.catalog.entries.length > 10"
      v-model:page="page"
      :page-size="10"
      :item-count="catalog.catalog.entries.length"
      class="mt-16px"
    />
    <NModal
      :show="!!preview"
      preset="card"
      :title="$t('plugin.marketPreview')"
      style="width: min(640px, 92vw)"
      :mask-closable="!busy"
      :close-on-esc="!busy"
      :closable="!busy"
      @update:show="
        (value) => {
          if (!value && !busy) clearPreview();
        }
      "
    >
      <template v-if="preview"
        ><p>{{ preview.plugin_id }} · {{ preview.version }}</p>
        <p class="digest">SHA-256: {{ preview.digest }}</p>
        <p>{{ $t("plugin.scopes") }}: {{ preview.scopes.join(", ") }}</p>
        <p>{{ $t("plugin.marketEnableHint") }}</p>
        <NCheckbox v-model:checked="approved" :disabled="busy">{{
          $t("plugin.marketApprove")
        }}</NCheckbox
        ><NSpace class="mt-16px"
          ><NButton
            type="primary"
            :loading="busy"
            :disabled="!approved || busy || disabled"
            @click="install"
            >{{ $t("plugin.confirm") }}</NButton
          ><NButton :disabled="busy" @click="clearPreview">{{
            $t("plugin.cancel")
          }}</NButton></NSpace
        ></template
      >
    </NModal>
  </NCard>
</template>
<style scoped>
.digest {
  overflow-wrap: anywhere;
  margin: 12px 0;
}
</style>
