<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { $t } from "@/locales";
import { checkAuth } from "@/directives";
import { useAuthStore } from "@/store/modules/auth";
import { request } from "@/service/request";
import {
  fetchPlugins,
  fetchPluginImpact,
  fetchPluginOperation,
  importPlugin,
  inspectPlugin,
  operatePlugin,
  type PluginCommand,
  type PluginFiles,
  type PluginImpact,
  type PluginOperation,
  type PluginPackage,
  type PluginStatus,
} from "@/service/api/plugin";
import ProductExtensions from "@/components/plugin/product-extensions.vue";
import { errorReason } from "@/components/plugin/schema";

defineOptions({ name: "PluginManagement" });
const auth = useAuthStore();
const plugins = ref<PluginStatus[]>([]);
const impacts = ref<Record<string, PluginImpact>>({});
const loading = ref(false),
  busy = ref(false),
  loadError = ref(false),
  managementAllowed = ref(false);
const message = ref("");
const storageKey = computed(
  () => `zcard.plugin.operation:${location.host}:${auth.userInfo.userId}`,
);
const pending = ref("");
const outcome = ref<PluginOperation>();
const canManage = computed(
  () => managementAllowed.value && checkAuth("plugin:manage") && !loadError.value,
);
const canEditProduct = computed(() => checkAuth("plugin:read") && checkAuth("catalog:write"));
const uploadOpen = ref(false),
  selected = ref<Partial<PluginFiles>>({}),
  preview = ref<PluginPackage>(),
  approved = ref(false);
const uploadAction = ref<"import" | "upgrade" | "rollback">("import");
const uploadTarget = ref<PluginStatus>();
const preparedCommand = ref<PluginCommand>();
const product = ref<{ id: string; is_locked?: boolean; name?: string }>();
const panel = ref<InstanceType<typeof ProductExtensions>>();
const productBusy = ref(false);
const impactView = ref<{ id: string; data: PluginImpact }>();
function persistPending(id: string) {
  pending.value = id;
  try {
    if (id) sessionStorage.setItem(storageKey.value, id);
    else sessionStorage.removeItem(storageKey.value);
  } catch {
    /* The operation ID is also visible and copyable. */
  }
}
function stateText(row: PluginStatus) {
  if (row.reconciliation_pending) return $t("plugin.pending");
  if (row.uninstalled) return $t("plugin.uninstalled");
  if (row.block_reasons?.length || !row.runtime_available || row.phase === "failed")
    return $t("plugin.failed");
  return row.desired_enabled ? $t("plugin.active") : $t("plugin.disabled");
}
function reasonText(reason: string) {
  const names: Record<string, App.I18n.I18nKey> = {
    package_missing: "plugin.packageMissing",
    runtime_fault: "plugin.runtimeFault",
    incompatible: "plugin.packageIncompatible",
    entitlement_expired: "plugin.expired",
    entitlement_revoked: "plugin.revoked",
  };
  return names[reason] ? $t(names[reason]) : reason;
}
async function refresh() {
  if (loading.value || busy.value) return;
  loading.value = true;
  try {
    const result = await fetchPlugins();
    if (result.error || !result.data) {
      loadError.value = true;
      return;
    }
    plugins.value = result.data.plugins || [];
    managementAllowed.value = result.data.instance_management_allowed;
    loadError.value = false;
    impacts.value = {};
    if (managementAllowed.value)
      await Promise.all(
        plugins.value.map(async (row) => {
          const response = await fetchPluginImpact(row.plugin_id);
          if (!response.error && response.data) impacts.value[row.plugin_id] = response.data;
        }),
      );
  } finally {
    loading.value = false;
  }
}
function resultMessage(result: PluginOperation) {
  outcome.value = result;
  if (result.phase === "reconciled" || result.phase === "failed") {
    persistPending("");
    message.value = $t(
      result.phase === "failed" ? "plugin.operationFailed" : "plugin.operationDone",
    );
  } else message.value = $t("plugin.unknown");
}
async function queryOperation() {
  if (!pending.value || busy.value) return;
  busy.value = true;
  const result = await fetchPluginOperation(pending.value);
  busy.value = false;
  if (result.error || !result.data)
    message.value = $t(
      errorReason(result.error) === "plugin.OPERATION_NOT_FOUND"
        ? "plugin.notFoundOperation"
        : "plugin.unknown",
    );
  else resultMessage(result.data);
  await refresh();
}
function clearOperation() {
  if (busy.value || loading.value || loadError.value) return;
  window.$dialog?.warning({
    title: $t("plugin.clearOperation"),
    content: $t("plugin.clearWarning"),
    positiveText: $t("plugin.confirm"),
    negativeText: $t("plugin.cancel"),
    onPositiveClick: () => {
      persistPending("");
      message.value = "";
    },
  });
}
async function apply(command: PluginCommand, files?: PluginFiles) {
  if (busy.value || pending.value || !canManage.value) return;
  busy.value = true;
  persistPending(command.operation_id);
  const result = files ? await importPlugin(command, files) : await operatePlugin(command);
  busy.value = false;
  if (result.error || !result.data) {
    message.value = $t("plugin.unknown");
    await queryOperation();
  } else {
    resultMessage(result.data);
    await refresh();
  }
}
async function confirmOperation(command: PluginCommand, files?: PluginFiles) {
  if (busy.value || pending.value || !canManage.value) return;
  busy.value = true;
  const result = await fetchPluginImpact(command.plugin_id);
  busy.value = false;
  if (result.error || !result.data) {
    message.value = $t("plugin.impactFailed");
    return;
  }
  impacts.value[command.plugin_id] = result.data;
  const description = ["disable", "uninstall"].includes(command.action)
    ? "plugin.impactWarning"
    : "plugin.switchWarning";
  window.$dialog?.warning({
    title: $t("plugin.confirmAction"),
    content: `${command.plugin_id} · ${$t(`plugin.${command.action}`)}\n${$t("plugin.affected")}: ${result.data.affected_total}\n${$t(description)}`,
    positiveText: $t("plugin.confirm"),
    negativeText: $t("plugin.cancel"),
    onPositiveClick: async () => {
      uploadOpen.value = false;
      await apply(command, files);
    },
  });
}
function operate(row: PluginStatus, action: "enable" | "disable" | "uninstall") {
  void confirmOperation({
    plugin_id: row.plugin_id,
    action,
    operation_id: crypto.randomUUID(),
    target_digest: action === "enable" ? row.desired_digest : "",
    expected_generation: row.desired_generation,
    approved_scopes: row.approved_scopes || [],
  });
}
function openUpload(row?: PluginStatus, action: "import" | "upgrade" | "rollback" = "import") {
  selected.value = {};
  preview.value = undefined;
  preparedCommand.value = undefined;
  approved.value = false;
  uploadAction.value = action;
  uploadTarget.value = row;
  uploadOpen.value = true;
  message.value = "";
}
function pick(name: keyof PluginFiles, event: Event) {
  selected.value[name] = (event.target as HTMLInputElement).files?.[0];
  preview.value = undefined;
  preparedCommand.value = undefined;
  approved.value = false;
}
async function verify() {
  if (busy.value || !canManage.value) return;
  const files = selected.value;
  if (
    !files.descriptor ||
    files.descriptor.size > 65536 ||
    files.signature?.size !== 64 ||
    !files.archive ||
    files.archive.size > 8 * 1024 * 1024
  ) {
    message.value = $t("plugin.badFiles");
    return;
  }
  busy.value = true;
  preview.value = undefined;
  approved.value = false;
  try {
    const descriptor = JSON.parse(await files.descriptor.text());
    if (
      typeof descriptor.pluginId !== "string" ||
      typeof descriptor.archiveSHA256 !== "string" ||
      (uploadTarget.value && descriptor.pluginId !== uploadTarget.value.plugin_id)
    )
      throw Error("identity");
    const installed = plugins.value.find((row) => row.plugin_id === descriptor.pluginId);
    if (uploadAction.value === "import" && installed && !installed.uninstalled)
      throw Error("already installed");
    const command: PluginCommand = {
      plugin_id: descriptor.pluginId,
      operation_id: crypto.randomUUID(),
      action: uploadAction.value,
      expected_generation: installed?.desired_generation || "0",
      target_digest: descriptor.archiveSHA256,
      approved_scopes: [],
    };
    const result = await inspectPlugin(command, files as PluginFiles);
    if (result.error || !result.data) throw Error("verify");
    preview.value = result.data;
    preparedCommand.value = { ...command, approved_scopes: result.data.scopes || [] };
    message.value = "";
  } catch {
    message.value = $t("plugin.verifyFailed");
  } finally {
    busy.value = false;
  }
}
async function viewImpact(row: PluginStatus) {
  const result = await fetchPluginImpact(row.plugin_id);
  if (result.error || !result.data) {
    message.value = $t("plugin.impactFailed");
    return;
  }
  impactView.value = { id: row.plugin_id, data: result.data };
}
async function openProduct(id: string) {
  if (productBusy.value || !(await discardProduct())) return;
  productBusy.value = true;
  const result = await request<{ id: string; name: string; is_locked: boolean }>({
    url: `/api/v1/admin/products/${encodeURIComponent(id)}`,
    silentError: true,
  });
  productBusy.value = false;
  if (result.error || !result.data) {
    message.value = $t("plugin.loadFailed");
    return;
  }
  product.value = { ...result.data, id }; // Preserve the exact ID from the host requirement API.
}
async function discardProduct() {
  if (panel.value?.saving) return false;
  if (!panel.value?.hasPending) return true;
  return new Promise<boolean>((resolve) =>
    window.$dialog?.warning({
      title: $t("plugin.discardTitle"),
      content: $t("plugin.discard"),
      positiveText: $t("plugin.confirm"),
      negativeText: $t("plugin.cancel"),
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
      onEsc: () => resolve(false),
      onMaskClick: () => resolve(false),
    }),
  );
}
async function closeProduct() {
  if (await discardProduct()) product.value = undefined;
}
onBeforeRouteLeave(async () => !busy.value && (await discardProduct()));
function beforeUnload(event: BeforeUnloadEvent) {
  if (busy.value || panel.value?.hasPending || panel.value?.saving) {
    event.preventDefault();
    event.returnValue = "";
  }
}
onMounted(() => {
  try {
    pending.value = sessionStorage.getItem(storageKey.value) || "";
  } catch {
    /* Storage can be unavailable. */
  }
  void refresh();
  if (pending.value) void queryOperation();
  window.addEventListener("focus", refresh);
  window.addEventListener("beforeunload", beforeUnload);
});
onBeforeUnmount(() => {
  window.removeEventListener("focus", refresh);
  window.removeEventListener("beforeunload", beforeUnload);
});
</script>

<template>
  <div class="plugin-page">
    <NCard :title="$t('plugin.title')">
      <p>{{ $t("plugin.intro") }}</p>
      <NSpace class="mt-16px">
        <NButton
          type="primary"
          :disabled="!canManage || busy || loading || !!pending"
          @click="openUpload()"
        >
          {{ $t("plugin.upload") }}
        </NButton>
        <NButton :loading="loading" :disabled="busy" @click="refresh">
          {{
            $t("plugin.refresh")
          }}
        </NButton>
      </NSpace>
      <p v-if="!managementAllowed && !loading">{{ $t("plugin.readonly") }}</p>
    </NCard>
    <NAlert v-if="loadError" type="error" role="alert">{{ $t("plugin.loadFailed") }}</NAlert>
    <NAlert v-if="message && !uploadOpen" type="info" role="status">{{ message }}</NAlert>
    <NCard v-if="pending" :title="$t('plugin.pending')" size="small">
      <p>
        {{ $t("plugin.operation") }}: <code>{{ pending }}</code>
      </p>
      <NSpace>
        <NButton :loading="busy" @click="queryOperation">{{ $t("plugin.query") }}</NButton><NButton :disabled="busy || loading || loadError" @click="clearOperation">
          {{
            $t("plugin.clearOperation")
          }}
        </NButton>
      </NSpace>
    </NCard>
    <p v-if="outcome" role="status">
      {{ $t("plugin.operation") }}: {{ outcome.operation_id }} · {{ outcome.phase }}
      {{ outcome.failure_code }}
    </p>
    <NEmpty v-if="!loading && !loadError && !plugins.length" :description="$t('plugin.empty')" />
    <NCard
      v-for="row in plugins"
      :key="row.plugin_id"
      :title="row.plugin_id"
      size="small"
      class="plugin-card"
    >
      <NTag
        :type="
          row.desired_enabled && !row.block_reasons?.length && !row.reconciliation_pending
            ? 'success'
            : 'warning'
        "
      >
        {{ stateText(row) }}
      </NTag>
      <dl>
        <dt>{{ $t("plugin.desired") }}</dt>
        <dd>
          <code>{{ row.desired_digest || "—" }}</code>
        </dd>
        <dt>{{ $t("plugin.observed") }}</dt>
        <dd>
          <code>{{ row.observed_digest || "—" }}</code>
        </dd>
        <dt>{{ $t("plugin.generation") }}</dt>
        <dd>{{ row.observed_generation }} / {{ row.desired_generation }}</dd>
        <dt>{{ $t("plugin.scopes") }}</dt>
        <dd>{{ row.approved_scopes?.join(", ") || "—" }}</dd>
      </dl>
      <NAlert v-for="reason in row.block_reasons" :key="reason" type="error" class="mb-12px">
        {{
          reasonText(reason)
        }}
      </NAlert>
      <NSpace v-if="canManage" align="center" class="mb-12px">
        <NButton :disabled="busy || loading" @click="viewImpact(row)">
          {{ $t("plugin.impact") }} ({{ impacts[row.plugin_id]?.affected_total ?? "—" }})
        </NButton>
        <template v-if="!row.uninstalled">
          <NButton
            v-if="!row.desired_enabled"
            :disabled="busy || loading || !!pending || row.reconciliation_pending"
            type="primary"
            @click="operate(row, 'enable')"
          >
            {{ $t("plugin.enable") }}
          </NButton>
          <NButton
            v-else
            :disabled="busy || loading || !!pending"
            @click="operate(row, 'disable')"
          >
            {{ $t("plugin.disable") }}
          </NButton>
          <NButton
            :disabled="busy || loading || !!pending || row.reconciliation_pending"
            @click="openUpload(row, 'upgrade')"
          >
            {{ $t("plugin.upgrade") }}
          </NButton>
          <NButton
            :disabled="busy || loading || !!pending || row.reconciliation_pending"
            @click="openUpload(row, 'rollback')"
          >
            {{ $t("plugin.rollback") }}
          </NButton>
          <NButton :disabled="busy || loading || !!pending" @click="operate(row, 'uninstall')">
            {{
              $t("plugin.uninstall")
            }}
          </NButton>
        </template>
      </NSpace>
    </NCard>
    <NModal
      :show="uploadOpen"
      preset="card"
      :title="$t(`plugin.${uploadAction}`)"
      class="plugin-modal"
      :mask-closable="!busy"
      :close-on-esc="!busy"
      :closable="!busy"
      @update:show="uploadOpen = $event"
    >
      <div class="upload-fields">
        <label v-for="name in ['descriptor', 'signature', 'archive'] as const" :key="name">
          <span>{{ $t(`plugin.${name}`) }}</span>
          <input
            type="file"
            :aria-label="$t(`plugin.${name}`)"
            :disabled="busy"
            @change="pick(name, $event)"
          />
        </label>
      </div>
      <NButton :loading="busy" :disabled="!canManage" @click="verify">
        {{
          $t("plugin.verify")
        }}
      </NButton>
      <NAlert v-if="message && uploadOpen" type="error" role="alert" class="mt-12px">
        {{
          message
        }}
      </NAlert>
      <template v-if="preview && preparedCommand">
        <NAlert type="success" class="mt-12px">{{ $t("plugin.verified") }}</NAlert>
        <p>{{ preview.plugin_id }} · {{ $t("plugin.version") }} {{ preview.version }}</p>
        <p>
          <code>{{ preview.digest }}</code>
        </p>
        <p>{{ $t("plugin.scopes") }}: {{ preview.scopes?.join(", ") }}</p>
        <p v-if="uploadTarget">
          {{ $t("plugin.newScopes") }}:
          {{
            preview.scopes?.filter((s) => !uploadTarget?.approved_scopes?.includes(s)).join(", ") ||
              "—"
          }}
        </p>
        <NCheckbox v-model:checked="approved" :disabled="busy">
          {{
            $t("plugin.approve")
          }}
        </NCheckbox>
        <div class="mt-16px">
          <NButton
            type="primary"
            :disabled="!approved || busy || !!pending || !canManage"
            @click="confirmOperation(preparedCommand, selected as PluginFiles)"
          >
            {{ $t(`plugin.${uploadAction}`) }}
          </NButton>
        </div>
      </template>
    </NModal>
    <NModal
      :show="!!impactView && !product"
      preset="card"
      :title="$t('plugin.impact')"
      class="plugin-modal"
      @update:show="!$event && (impactView = undefined)"
    >
      <template v-if="impactView">
        <NAlert type="warning">{{ $t("plugin.impactWarning") }}</NAlert>
        <p>
          {{ impactView.id }} · {{ $t("plugin.affected") }}: {{ impactView.data.affected_total }}
        </p>
        <p>{{ $t("plugin.currentSite") }}</p>
        <p v-if="impactView.data.truncated">{{ $t("plugin.truncated") }}</p>
        <div class="product-links">
          <NButton
            v-for="id in impactView.data.visible_product_ids"
            :key="id"
            :disabled="!canEditProduct || productBusy"
            @click="openProduct(id)"
          >
            {{ $t("plugin.product") }} {{ id }} · {{ $t("plugin.openProduct") }}
          </NButton>
        </div>
      </template>
    </NModal>
    <NModal
      :show="!!product"
      preset="card"
      :title="`${$t('plugin.product')} ${product?.id || ''} · ${product?.name || ''}`"
      class="plugin-modal"
      :mask-closable="false"
      :closable="!panel?.saving"
      :close-on-esc="!panel?.saving"
      @update:show="!$event && closeProduct()"
    >
      <ProductExtensions
        v-if="product"
        ref="panel"
        :key="product.id"
        :product-id="product.id"
        :readonly="product.is_locked"
        @persisted="refresh"
      />
    </NModal>
  </div>
</template>

<style scoped>
.plugin-page {
  display: grid;
  gap: 16px;
  align-content: start;
  padding-bottom: 24px;
  min-width: 0;
}
.plugin-page :deep(.n-card-header__main) {
  overflow-wrap: anywhere;
}
:global(.plugin-modal) {
  width: 720px;
  max-width: calc(100vw - 24px);
}
:global(.plugin-modal .n-card__content) {
  max-height: 75vh;
  overflow-y: auto;
}
p {
  margin: 8px 0 12px;
}
code,
dd {
  overflow-wrap: anywhere;
}
dl {
  display: grid;
  grid-template-columns: minmax(90px, auto) minmax(0, 1fr);
  gap: 8px 16px;
  margin: 16px 0;
}
dd {
  margin: 0;
}
.upload-fields {
  display: grid;
  gap: 16px;
  margin-bottom: 20px;
}
.upload-fields label {
  display: grid;
  gap: 8px;
}
input {
  max-width: 100%;
}
.product-links {
  display: grid;
  gap: 8px;
}
@media (max-width: 520px) {
  dl {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
