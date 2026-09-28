<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { checkAuth } from "@/directives";
import { $t } from "@/locales";
import {
  normalizePluginRule,
  fetchPluginContributions,
  fetchPluginLevels,
  fetchProductPluginRule,
  fetchProductPluginRules,
  releasePluginRule,
  savePluginRule,
  type PluginConfig,
  type PluginLevel,
  type PluginRule,
  type PluginSchema,
} from "@/service/api/plugin";
import { canonical, decimalID, parsePluginFields, validPluginConfig } from "./schema";

const props = defineProps<{ productId: string; readonly?: boolean }>();
const emit = defineEmits<{ persisted: [] }>();
interface Section {
  rule: PluginRule;
  schema?: PluginSchema;
  draft?: PluginConfig;
  latest?: { rule: PluginRule; schema?: PluginSchema };
  message?: string;
}
const sections = ref<Section[]>([]);
const levels = ref<PluginLevel[]>([]);
const loading = ref(false);
const saving = ref(false);
const loadError = ref(false);
const hostReadFailed = ref(true);
const hostRule = (section: Section) => section.latest?.rule || section.rule;
const loaded = ref(false);
const canRead = computed(() => checkAuth("plugin:read") && checkAuth("catalog:write"));
const canConfigure = computed(() => checkAuth("plugin:configure") && checkAuth("catalog:write"));
const canRelease = computed(() => checkAuth("plugin:release") && checkAuth("catalog:write"));
const dirty = (section: Section) =>
  !!section.draft && canonical(section.draft) !== canonical(section.rule.config);
const hasPending = computed(() => sections.value.some(dirty));
defineExpose({ hasPending, saving });
function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)); }
const allowed = (section: Section) =>
  !props.readonly &&
  canConfigure.value &&
  !saving.value &&
  !loading.value &&
  !loadError.value &&
  !section.latest &&
  section.rule.config_valid &&
  !!section.draft &&
  !!section.schema?.runtime_available &&
  section.rule.generation === section.schema.generation &&
  !!parsePluginFields(section.schema);
const fields = (section: Section) => (section.schema ? parsePluginFields(section.schema) : null);
function options(section: Section) {
  const list = levels.value.map((l) => ({
    value: l.id,
    label: `${l.name}${l.enabled ? "" : ` (${$t("plugin.disabledLevel")})`}`,
    disabled: !l.enabled,
  }));
  for (const id of section.draft?.allowed_level_ids || []) {
    if (!list.some((l) => l.value === id))
      list.push({ value: id, label: `${id} (${$t("plugin.disabledLevel")})`, disabled: true });
  }
  return list;
}
function acceptLatest(section: Section, keepDraft: boolean) {
  if (!section.latest) return;
  const draft = section.draft;
  section.rule = section.latest.rule;
  section.schema = section.latest.schema;
  section.draft =
    keepDraft && draft && section.rule.config
      ? { ...draft, revision: section.rule.config.revision }
      : section.rule.config
        ? clone(section.rule.config)
        : undefined;
  section.latest = undefined;
  section.message = undefined;
}
let requestVersion = 0;
async function load() {
  if (!decimalID(props.productId) || !canRead.value || loading.value || saving.value) return;
  const version = ++requestVersion;
  loading.value = true;
  try {
    const [rules, contributions, levelResult] = await Promise.all([
      fetchProductPluginRules(props.productId),
      fetchPluginContributions(props.productId),
      canConfigure.value ? fetchPluginLevels(props.productId) : Promise.resolve(null),
    ]);
    if (version !== requestVersion) return;
    // Host summaries remain readable even when the extension/option endpoint fails.
    hostReadFailed.value = !!rules.error || !rules.data;
    const incoming = new Map(
      (rules.data?.rules || []).map((r) => [r.plugin_id, normalizePluginRule(r)]),
    );
    const schemas = new Map((contributions.data?.contributions || []).map((s) => [s.plugin_id, s]));
    let failed = !!rules.error || !!contributions.error || !!levelResult?.error;
    if (levelResult?.data) levels.value = levelResult.data.options || [];
    for (const id of schemas.keys()) {
      if (incoming.has(id)) continue;
      const result = await fetchProductPluginRule(props.productId, id);
      if (version !== requestVersion) return;
      if (result.error || !result.data) {
        failed = true;
        continue;
      }
      incoming.set(id, normalizePluginRule(result.data));
    }
    if (!rules.error) {
      const next: Section[] = [];
      for (const rule of incoming.values()) {
        const old = sections.value.find((s) => s.rule.plugin_id === rule.plugin_id);
        const schema = contributions.error ? old?.schema : schemas.get(rule.plugin_id);
        if (old && dirty(old)) {
          if (canonical({ rule: old.rule, schema: old.schema }) !== canonical({ rule, schema }))
            old.latest = { rule, schema };
          else old.latest = undefined;
          next.push(old);
        } else
          next.push({
            rule,
            schema,
            draft: rule.config ? clone(rule.config) : undefined,
            message: old?.message,
          });
      }
      // Never discard a draft if a later read omits its contribution or rule.
      for (const old of sections.value)
        if (!incoming.has(old.rule.plugin_id) && dirty(old)) {
          next.push(old);
          failed = true;
        }
      sections.value = next;
    }
    loadError.value = failed;
    loaded.value = !failed;
  } finally {
    if (version === requestVersion) loading.value = false;
  }
}
async function save(section: Section) {
  if (!allowed(section) || !section.draft) return;
  const draft = clone(section.draft);
  if (
    !validPluginConfig(draft) ||
    (draft.enabled &&
      draft.allowed_level_ids.some((id) => !levels.value.some((l) => l.id === id && l.enabled)))
  ) {
    section.message = $t("plugin.invalidSelection");
    return;
  }
  saving.value = true;
  const result = await savePluginRule(section.rule, draft);
  saving.value = false;
  if (result.error || !result.data) {
    section.message = $t("plugin.saveFailed");
    // Even an ambiguous timeout must be reconciled before saving again.
    loadError.value = true;
    await load();
    return;
  }
  section.rule = normalizePluginRule(result.data);
  section.draft = section.rule.config ? clone(section.rule.config) : undefined;
  section.message = $t("plugin.saved");
  emit("persisted");
}
function release(section: Section) {
  if (!canRelease.value || props.readonly || saving.value || loading.value || hostReadFailed.value)
    return;
  window.$dialog?.warning({
    title: $t("plugin.releaseConfirm"),
    content: `${$t("plugin.product")} ${props.productId} · ${section.rule.plugin_id}\n${$t("plugin.releaseWarning")}`,
    positiveText: $t("plugin.confirm"),
    negativeText: $t("plugin.cancel"),
    onPositiveClick: async () => {
      saving.value = true;
      const result = await releasePluginRule(hostRule(section));
      saving.value = false;
      if (result.error || !result.data) {
        section.message = $t("plugin.saveFailed");
        await load();
        return false;
      }
      // Retain unsaved local edits; the explicit release never silently saves them.
      if (dirty(section))
        section.latest = { rule: normalizePluginRule(result.data), schema: section.schema };
      else {
        section.rule = normalizePluginRule(result.data);
        section.draft = section.rule.config ? clone(section.rule.config) : undefined;
      }
      section.message = $t("plugin.released");
      emit("persisted");
      await load();
      return true;
    },
  });
}
watch(
  () => props.productId,
  () => {
    requestVersion++;
    sections.value = [];
    loaded.value = false;
    loadError.value = false;
    hostReadFailed.value = true;
    loading.value = false;
    void load();
  },
  { immediate: true },
);
onMounted(() => window.addEventListener("focus", load));
onBeforeUnmount(() => {
  requestVersion++;
  window.removeEventListener("focus", load);
});
</script>

<template>
  <section
    class="plugin-extensions"
    :aria-label="$t('plugin.extensions')"
    data-testid="plugin-extensions"
  >
    <NAlert v-if="!decimalID(productId)" type="info">{{ $t("plugin.saveFirst") }}</NAlert>
    <NAlert v-else-if="!canRead" type="info">{{ $t("plugin.noRead") }}</NAlert>
    <template v-else>
      <NSpace justify="space-between" align="center">
        <strong>{{ $t("plugin.extensions") }} · {{ $t("plugin.product") }} {{ productId }}</strong>
        <NButton :loading="loading" :disabled="saving" @click="load">
          {{
            $t("plugin.refresh")
          }}
        </NButton>
      </NSpace>
      <NAlert v-if="readonly" type="warning">{{ $t("plugin.locked") }}</NAlert>
      <NAlert v-else-if="!canConfigure" type="info">{{ $t("plugin.noConfigure") }}</NAlert>
      <NAlert v-if="loadError" type="error" role="alert">{{ $t("plugin.loadFailed") }}</NAlert>
      <p v-if="loading" role="status">{{ $t("plugin.loading") }}</p>
      <NAlert v-if="loaded && !sections.some((s) => s.schema)" type="info">
        {{
          $t("plugin.noContributions")
        }}
      </NAlert>
      <NCard
        v-for="section in sections"
        :key="section.rule.plugin_id"
        :title="section.rule.plugin_id"
        size="small"
      >
        <NAlert :type="hostRule(section).required ? 'warning' : 'info'" class="mb-12px">
          {{ hostRule(section).required ? $t("plugin.restricted") : $t("plugin.unrestricted") }}
        </NAlert>
        <p v-if="!section.schema && hostRule(section).required">{{ $t("plugin.inactive") }}</p>
        <NAlert v-if="section.schema && !fields(section)" type="error">{{ $t("plugin.incompatible") }}</NAlert>
        <NAlert v-if="!section.rule.config_valid" type="error">
          {{
            $t("plugin.invalidConfig")
          }}
        </NAlert>
        <NAlert v-if="section.latest" type="warning" class="mb-12px">
          <p>{{ $t("plugin.changed") }}</p>
          <details>
            <summary>{{ $t("plugin.details") }}</summary>
            <strong>{{ $t("plugin.draft") }}</strong>
            <pre>{{ section.draft }}</pre>
            <strong>{{ $t("plugin.remote") }} · {{ $t("plugin.generation") }}
              {{ section.latest.rule.generation }}</strong>
            <pre>{{ section.latest.rule.config }}</pre>
          </details>
          <NSpace>
            <NButton :disabled="saving || loading" @click="acceptLatest(section, false)">
              {{
                $t("plugin.useRemote")
              }}
            </NButton>
            <NButton
              :disabled="
                saving ||
                  loading ||
                  !section.latest.rule.config_valid ||
                  !section.latest.schema ||
                  !parsePluginFields(section.latest.schema)
              "
              @click="acceptLatest(section, true)"
            >
              {{ $t("plugin.keepDraft") }}
            </NButton>
          </NSpace>
        </NAlert>
        <NForm
          v-if="section.draft && fields(section)"
          :disabled="!allowed(section)"
          label-placement="top"
        >
          <NFormItem v-for="field in fields(section)" :key="field.key" :label="field.label">
            <NSwitch
              v-if="field.type === 'switch'"
              v-model:value="section.draft.enabled"
              :aria-label="field.label"
            />
            <NSelect
              v-else
              v-model:value="section.draft.allowed_level_ids"
              :aria-label="field.label"
              multiple
              filterable
              :options="options(section)"
              :placeholder="$t('plugin.chooseLevels')"
            />
          </NFormItem>
          <p>{{ $t("plugin.exactLevels") }}</p>
          <NButton
            type="primary"
            :loading="saving"
            :disabled="!allowed(section) || !dirty(section)"
            @click="save(section)"
          >
            {{ $t("plugin.save") }}
          </NButton>
        </NForm>
        <p v-if="section.message" role="status" class="my-12px">{{ section.message }}</p>
        <template v-if="hostRule(section).required">
          <NDivider />
          <NButton
            :disabled="!canRelease || readonly || saving || loading || hostReadFailed"
            @click="release(section)"
          >
            {{ $t("plugin.release") }}
          </NButton>
          <p v-if="!canRelease">{{ $t("plugin.noRelease") }}</p>
        </template>
      </NCard>
    </template>
  </section>
</template>

<style scoped>
.plugin-extensions {
  display: grid;
  gap: 16px;
  min-width: 0;
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
}
p {
  margin: 8px 0 12px;
}
</style>
