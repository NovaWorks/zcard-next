<script setup lang="ts">
import { onMounted, ref, watch } from "vue";

import { $t } from "@/locales";
import {
  fetchPluginEntitlements,
  installPluginEntitlement,
  installPluginSafetyPolicy,
  type PluginEntitlements,
} from "@/service/api/plugin";

const props = defineProps<{ disabled: boolean }>();
const emit = defineEmits<{ busy: [value: boolean]; updated: [] }>();
const busy = ref(false);
const state = ref<PluginEntitlements>();
const selected = ref<File>();
const failure = ref("");
const success = ref(false);
function running(value: boolean) {
  busy.value = value;
  emit("busy", value);
}
async function refresh() {
  if (busy.value || props.disabled) return;
  running(true);
  try {
    const result = await fetchPluginEntitlements();
    if (result.error || !result.data)
      failure.value = $t("plugin.licenseLoadFailed");
    else {
      state.value = result.data;
      failure.value = "";
    }
  } finally {
    running(false);
  }
}
function choose(event: Event) {
  success.value = false;
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  selected.value = undefined;
  if (!file) return;
  if (!file.size || file.size > 65536) {
    failure.value = $t("plugin.licenseFileLimit");
    return;
  }
  failure.value = "";
  selected.value = file;
}
async function install() {
  if (!selected.value || busy.value || props.disabled) return;
  running(true);
  failure.value = "";
  success.value = false;
  try {
    const document = JSON.parse(await selected.value.text());
    if (
      !document ||
      typeof document !== "object" ||
      (!document.license && !document.list)
    )
      throw Error("format");
    const result = await (document.list
      ? installPluginSafetyPolicy(document)
      : installPluginEntitlement(document));
    if (result.error || !result.data) throw Error("verification");
    state.value = result.data;
    selected.value = undefined;
    success.value = true;
  } catch {
    failure.value = $t("plugin.licenseInstallFailed");
  } finally {
    running(false);
  }
  if (success.value) emit("updated");
}
function status(value: string) {
  switch (value) {
    case "active":
      return $t("plugin.licenseValid");
    case "revoked":
      return $t("plugin.revoked");
    case "entitlement_expired":
      return $t("plugin.expired");
    case "entitlement_incompatible":
      return $t("plugin.licenseIncompatible");
    case "entitlement_not_yet_valid":
      return $t("plugin.licenseNotYetValid");
    default:
      return $t("plugin.licenseMissing");
  }
}
watch(
  () => props.disabled,
  (disabled) => {
    if (!disabled && !state.value && !busy.value && !failure.value)
      void refresh();
  },
);
onMounted(refresh);
</script>

<template>
  <NCard :title="$t('plugin.licenseTitle')" size="small">
    <p>{{ $t("plugin.licenseIntro") }}</p>
    <p v-if="state?.instanceId" class="break-all">
      {{ $t("plugin.licenseInstance") }}: <code>{{ state.instanceId }}</code>
    </p>
    <NAlert v-if="failure" type="error" role="alert" class="my-12px">{{
      failure
    }}</NAlert>
    <NAlert v-if="success" type="success" role="status" class="my-12px">{{
      $t("plugin.licenseInstalled")
    }}</NAlert>
    <p v-if="state && !state.ready">{{ $t("plugin.licenseNotReady") }}</p>
    <p v-if="state?.ready && !state.licenses?.length">
      {{ $t("plugin.licenseEmpty") }}
    </p>
    <div
      v-for="row in state?.licenses"
      :key="`${row.issuer}/${row.pluginId}`"
      class="my-12px break-all"
    >
      <strong>{{ row.pluginId }}</strong> · {{ status(row.status) }}
      <div>
        {{ $t("plugin.licenseExpires") }}:
        {{ row.expiresAt === 253402300799 ? $t("plugin.permanentLicense") : new Date(row.expiresAt * 1000).toLocaleString() }}
      </div>
      <div>
        {{ $t("plugin.licenseIssuer") }}: {{ row.issuer }} ·
        {{ $t("plugin.licenseRevision") }}:
        {{ row.revision }}
      </div>
    </div>
    <NFormItem :label="$t('plugin.licenseFile')" class="mt-16px">
      <input
        type="file"
        accept="application/json,.json"
        :disabled="disabled || busy"
        :aria-label="$t('plugin.licenseFile')"
        @change="choose"
      />
    </NFormItem>
    <p v-if="selected" class="break-all">{{ selected.name }}</p>
    <NSpace>
      <NButton
        type="primary"
        :loading="busy"
        :disabled="disabled || busy || !selected || !state?.ready"
        @click="install"
        >{{ $t("plugin.licenseInstall") }}</NButton
      >
      <NButton :disabled="disabled || busy" @click="refresh">{{
        $t("plugin.refresh")
      }}</NButton>
    </NSpace>
  </NCard>
</template>
