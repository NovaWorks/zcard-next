<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { formatMarketCents } from "@/utils/money";
import { $t } from "@/locales";
import {
  fetchMarketConfig,
  fetchPluginEntitlements,
  fetchMarketBinding,
  type PluginEntitlements,
  type MarketBinding,
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
import {
  fetchMarketProducts,
  fetchMarketProduct,
  fetchMarketFacets,
  type MarketProduct,
  type MarketPage,
} from "@/service/api/market-account";

const products = ref<MarketPage<MarketProduct>>();
const syncedAt = ref(0);
const query = ref("");
const priceFilter = ref("all");
const category = ref(""),
  kind = ref(""),
  recommended = ref(false);
const facets = ref<{ categories: string[]; kinds: string[] }>({
  categories: [],
  kinds: [],
});
const visibleProducts = computed(() => products.value?.items || []);
const searchedFilters = ref<Record<string, string | boolean>>({});
function filterQuery() {
  return {
    q: query.value,
    ...(priceFilter.value === "all" ? {} : { mode: priceFilter.value }),
    ...(category.value ? { category: category.value } : {}),
    ...(kind.value ? { kind: kind.value } : {}),
    ...(recommended.value ? { recommended: true } : {}),
  };
}
function media(path: string) {
  return /^\/api\/market\/v1\/media\/[a-f0-9]{64}$/.test(path)
    ? savedOrigin.value + path
    : "";
}
function billing(product: MarketProduct) {
  if (product.billingMode === "perpetual") return $t("plugin.perpetual");
  if (product.billingMode === "free") return $t("plugin.freePlugins");
  const seconds = product.price?.durationSeconds;
  if (!seconds) return $t("plugin.paidPlugins");
  if (seconds % 86400 === 0) return `${seconds / 86400} ${$t("plugin.days")}`;
  if (seconds % 3600 === 0) return `${seconds / 3600} ${$t("plugin.hours")}`;
  if (seconds % 60 === 0) return `${seconds / 60} ${$t("plugin.minutes")}`;
  return `${seconds} ${$t("plugin.seconds")}`;
}
function accent(id: string) {
  return ["violet", "blue", "teal", "amber"][
    Array.from(id).reduce((n, c) => n + c.charCodeAt(0), 0) % 4
  ];
}
const detail = ref<MarketProduct>();
const selectedVersion = ref("");
const licenses = ref<PluginEntitlements>();
const binding = ref<MarketBinding>();
function installed(id: string) {
  return props.plugins.find((p) => p.plugin_id === id && !p.uninstalled);
}
function licensed(id: string) {
  return licenses.value?.licenses?.some(
    (l) => l.pluginId === id && l.status === "active",
  );
}
function permanentlyLicensed(id: string) {
  return licenses.value?.licenses?.some(
    (l) =>
      l.pluginId === id &&
      l.status === "active" &&
      l.expiresAt === 253402300799,
  );
}
function purchaseURL(id: string) {
  const q = new URLSearchParams({
    plugin: id,
    instance: binding.value?.instanceId || licenses.value?.instanceId || "",
  });
  return savedOrigin.value + "/shop?" + q.toString();
}
async function showDetail(id: string) {
  if (busy.value || props.disabled) return;
  running(true);
  const r = await fetchMarketProduct(id);
  running(false);
  if (!r.data) {
    failure.value = $t("plugin.marketOffline");
    return;
  }
  detail.value = r.data;
  selectedVersion.value = r.data.versions[0]?.version || "";
}

const props = defineProps<{
  plugins: PluginStatus[];
  disabled: boolean;
  advanced?: boolean;
  accountId?: string;
}>();
const emit = defineEmits<{
  account: [];
  pending: [id: string];
  done: [operation: PluginOperation | undefined];
  busy: [value: boolean];
}>();
const savedOrigin = ref(""),
  busy = ref(false),
  failure = ref("");
const catalog = ref<MarketCatalog>(),
  page = ref(1),
  approved = ref(false),
  paidApproved = ref(false),
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
  paidApproved.value = false;
}
async function refresh() {
  if (busy.value || props.disabled) return;
  running(true);
  failure.value = "";
  clearPreview();
  searchedFilters.value = filterQuery();
  const [r, f] = await Promise.all([
    fetchMarketProducts(searchedFilters.value),
    fetchMarketFacets(),
  ]);
  if (f.data) facets.value = f.data;
  const [rights, site] = await Promise.all([
    fetchPluginEntitlements(),
    fetchMarketBinding(),
  ]);
  licenses.value = rights.data || undefined;
  binding.value = site.data || undefined;
  products.value = r.data || undefined;
  catalog.value = undefined;
  if (r.error || !r.data) {
    const reason = (r.error as any)?.response?.data?.reason;
    if (reason === "market.ACCOUNT_UNSUPPORTED") {
      const legacy = await fetchMarketCatalog();
      catalog.value = legacy.data || undefined;
      if (legacy.error || !legacy.data)
        failure.value = $t("plugin.marketOffline");
    } else failure.value = $t("plugin.marketOffline");
  } else syncedAt.value = Date.now();
  page.value = 1;
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
    detail.value = undefined;
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
    !selected.value ||
    (preview.value.entitlement_mode === "paid" && !paidApproved.value)
  )
    return;
  const command = {
    ...prepared.value,
    approved_scopes: preview.value.scopes,
    confirm_paid: paidApproved.value,
  };
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
async function moreProducts() {
  if (busy.value || props.disabled || !products.value?.nextCursor) return;
  running(true);
  const result = await fetchMarketProducts({
    cursor: products.value.nextCursor,
    ...searchedFilters.value,
  });
  if (result.data) {
    products.value.items.push(...result.data.items);
    products.value.nextCursor = result.data.nextCursor;
    failure.value = "";
  } else failure.value = $t("plugin.marketOffline");
  running(false);
}
async function inspectProduct(product: MarketProduct, version: string) {
  if (busy.value || props.disabled) return;
  running(true);
  clearPreview();
  failure.value = "";
  // Public descriptions never authorize downloads. Resolve the exact version through signed v1/v2 distribution.
  const result = await fetchMarketCatalog();
  const entry = result.data?.catalog.entries.find(
    (e) =>
      e.descriptor.pluginId === product.pluginId &&
      e.descriptor.version === version,
  );
  running(false);
  if (entry) await inspect(entry);
  else
    failure.value = result.error
      ? $t("plugin.marketOffline")
      : $t("plugin.marketAccessRequired");
}
function returnToPage() {
  if (
    document.visibilityState === "visible" &&
    savedOrigin.value &&
    Date.now() - syncedAt.value >= 60000
  )
    void refresh();
}
let refreshTimer: ReturnType<typeof setInterval> | undefined;
onUnmounted(() => {
  clearInterval(refreshTimer);
  document.removeEventListener("visibilitychange", returnToPage);
});
onMounted(async () => {
  running(true);
  const r = await fetchMarketConfig();
  if (r.data) {
    savedOrigin.value = r.data.origin;
  } else failure.value = $t("plugin.marketConfigError");
  running(false);
  if (savedOrigin.value) await refresh();
  document.addEventListener("visibilitychange", returnToPage);
  refreshTimer = setInterval(returnToPage, 60000);
});
defineExpose({ showDetail, refresh });
</script>
<template>
  <section class="market-catalog" :aria-label="$t('plugin.marketTitle')">
    <NAlert v-if="!busy && !savedOrigin" type="info" class="mt-12px">{{
      $t("plugin.marketUnconfigured")
    }}</NAlert>

    <div class="catalog-toolbar">
      <div class="price-filters" :aria-label="$t('plugin.priceFilter')">
        <NButton
          v-for="filter in ['all', 'free', 'paid']"
          :key="filter"
          :type="priceFilter === filter ? 'primary' : 'default'"
          :quaternary="priceFilter !== filter"
          :aria-pressed="priceFilter === filter"
          :disabled="busy || disabled"
          @click="
            priceFilter = filter;
            refresh();
          "
          >{{
            $t(
              filter === "all"
                ? "plugin.allPlugins"
                : filter === "free"
                  ? "plugin.freePlugins"
                  : "plugin.paidPlugins",
            )
          }}</NButton
        >
      </div>
      <NCheckbox
        v-model:checked="recommended"
        :disabled="busy || disabled"
        @update:checked="refresh"
        >{{ $t("plugin.recommended") }}</NCheckbox
      >
      <NForm class="catalog-search" @submit.prevent="refresh"
        ><NInput
          v-model:value="query"
          :aria-label="$t('plugin.marketSearch')"
          :placeholder="$t('plugin.marketSearch')"
          clearable
        /><NButton
          attr-type="submit"
          :disabled="busy || disabled || !savedOrigin"
          >{{ $t("plugin.marketSearch") }}</NButton
        ></NForm
      >
      <NButton
        quaternary
        :loading="busy"
        :disabled="busy || disabled || !savedOrigin"
        @click="refresh"
        >{{ $t("plugin.marketRefresh") }}</NButton
      >
    </div>
    <div class="facet-row" :aria-label="$t('plugin.category')">
      <span>{{ $t("plugin.category") }}</span
      ><NButton
        v-for="value in ['', ...facets.categories]"
        :key="value"
        size="small"
        :type="category === value ? 'primary' : 'default'"
        :quaternary="category !== value"
        :aria-pressed="category === value"
        :disabled="busy || disabled"
        @click="
          category = value;
          refresh();
        "
        >{{ value || $t("plugin.allPlugins") }}</NButton
      >
    </div>
    <div class="facet-row" :aria-label="$t('plugin.kind')">
      <span>{{ $t("plugin.kind") }}</span
      ><NButton
        v-for="value in ['', ...facets.kinds]"
        :key="value"
        size="small"
        :type="kind === value ? 'primary' : 'default'"
        :quaternary="kind !== value"
        :aria-pressed="kind === value"
        :disabled="busy || disabled"
        @click="
          kind = value;
          refresh();
        "
        >{{ value || $t("plugin.allPlugins") }}</NButton
      >
    </div>
    <NAlert v-if="failure" type="error" role="alert" class="mt-16px">{{
      failure
    }}</NAlert>
    <div v-if="products" class="catalog-meta" aria-live="polite">
      <span
        >{{ $t("plugin.loadedPlugins") }} {{ products.items.length }} ·
        {{ $t("plugin.showingPlugins") }} {{ visibleProducts.length }}</span
      ><span v-if="syncedAt"
        >{{ $t("plugin.marketLastChecked") }}
        {{ new Date(syncedAt).toLocaleTimeString() }}</span
      >
    </div>
    <NEmpty
      v-if="products && !visibleProducts.length"
      :description="$t('plugin.marketEmpty')"
      class="catalog-empty"
    />
    <div v-if="products" class="plugin-grid" :aria-busy="busy">
      <article
        v-for="product in visibleProducts"
        :key="product.pluginId"
        class="catalog-product"
      >
        <div class="product-summary">
          <div
            class="product-icon"
            :class="accent(product.pluginId)"
            aria-hidden="true"
          >
            <img
              v-if="product.images?.[0]"
              :src="media(product.images[0])"
              alt=""
              loading="lazy"
              referrerpolicy="no-referrer"
              class="product-image"
            />
            <svg
              v-else
              viewBox="0 0 32 32"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
            >
              <path
                d="M5 7h8V5a3 3 0 0 1 6 0v2h8v8h-2a3 3 0 0 0 0 6h2v6h-8v-2a3 3 0 0 0-6 0v2H5v-8h2a3 3 0 0 0 0-6H5z"
              />
            </svg>
          </div>
          <div class="product-heading">
            <h2>{{ product.name }}</h2>
            <p
              class="product-price"
              :class="{
                free: product.billingMode === 'free',
              }"
            >
              {{
                product.price
                  ? formatMarketCents(product.price.amount) +
                    " " +
                    product.price.currency
                  : product.billingMode !== "free"
                    ? $t("plugin.paidPlugins")
                    : $t("plugin.freePlugins")
              }}
            </p>
            <p class="product-publisher">
              {{ billing(product)
              }}<span v-if="product.recommended">
                · {{ $t("plugin.recommended") }}</span
              >
            </p>
            <p v-if="product.publisher" class="product-publisher">
              {{ product.publisher }}
            </p>
          </div>
        </div>
        <p class="product-description">
          {{ product.description || $t("plugin.productDescriptionFallback") }}
        </p>
        <div class="product-state">
          <NTag
            v-if="installed(product.pluginId)"
            size="small"
            :bordered="false"
            >{{ $t("plugin.installedTab") }}</NTag
          ><NTag
            v-if="
              installed(product.pluginId)?.desired_enabled &&
              !installed(product.pluginId)?.block_reasons?.length &&
              !installed(product.pluginId)?.reconciliation_pending
            "
            size="small"
            type="success"
            :bordered="false"
            >{{ $t("plugin.active") }}</NTag
          ><NTag
            v-if="licensed(product.pluginId)"
            size="small"
            type="success"
            :bordered="false"
            >{{ $t("plugin.licenseValid") }}</NTag
          ><NTag
            v-if="!product.sellable && product.billingMode !== 'free'"
            size="small"
            :bordered="false"
            >{{ $t("plugin.marketUnavailable") }}</NTag
          >
        </div>
        <div class="product-actions">
          <NButton
            text
            type="primary"
            :disabled="busy || disabled"
            @click="showDetail(product.pluginId)"
            >{{ $t("plugin.productDetail") }}
            <span aria-hidden="true"> →</span></NButton
          >
        </div>
        <footer>
          <span :title="product.pluginId">{{ product.pluginId }}</span
          ><span v-if="product.versions[0]"
            >v{{ product.versions[0].version }}</span
          >
        </footer>
      </article>
    </div>
    <NButton
      v-if="products?.nextCursor"
      :disabled="busy || disabled"
      class="mt-12px"
      @click="moreProducts"
      >{{ $t("plugin.marketMore") }}</NButton
    >
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
              {{
                entry.manifest.entitlement.mode === "paid"
                  ? $t("plugin.marketPaid")
                  : $t("plugin.marketFree")
              }}
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
      :show="!!detail"
      preset="card"
      style="width: min(640px, 92vw)"
      :title="detail?.name"
      @update:show="detail = undefined"
    >
      <template v-if="detail">
        <div v-if="detail.images?.length" class="detail-images">
          <img
            v-for="path in detail.images"
            :key="path"
            :src="media(path)"
            :alt="detail.name"
            loading="lazy"
            referrerpolicy="no-referrer"
          />
        </div>
        <p>{{ detail.description }}</p>
        <p class="detail-text">{{ detail.details }}</p>
        <details v-if="detail.history?.length" class="release-history">
          <summary>{{ $t("plugin.releaseHistory") }}</summary>
          <article v-for="release in detail.history" :key="release.version">
            <strong>v{{ release.version }}</strong> ·
            {{
              release.publishedAt
                ? new Date(release.publishedAt * 1000).toLocaleDateString()
                : "—"
            }}
            ·
            {{
              release.status === "withdrawn"
                ? $t("plugin.withdrawn")
                : $t("plugin.published")
            }}
            <p class="detail-text">{{ release.notes }}</p>
          </article>
        </details>
        <p>{{ detail.publisher }} · {{ detail.pluginId }}</p>
        <p v-if="detail.price">
          {{ formatMarketCents(detail.price.amount) }}
          {{ detail.price.currency }} ·
          {{ billing(detail) }}
        </p>
        <NFormItem :label="$t('plugin.selectVersion')">
          <select
            v-model="selectedVersion"
            class="version-select"
            :aria-label="$t('plugin.selectVersion')"
            :disabled="busy || disabled"
          >
            <option
              v-for="version in detail.versions"
              :key="version.version"
              :value="version.version"
            >
              {{ version.version }} ·
              {{
                version.mode === "paid"
                  ? $t("plugin.marketPaid")
                  : $t("plugin.marketFree")
              }}
              · Core {{ version.minCore }} ≤ v &lt;
              {{ version.maxCoreExclusive }}
            </option>
          </select>
        </NFormItem>
        <p v-if="!detail.versions.length">
          {{ $t("plugin.marketUnavailable") }}
        </p>
        <NAlert v-if="failure" type="error" role="alert">{{ failure }}</NAlert>
        <template
          v-if="
            detail.versions.find((v) => v.version === selectedVersion)?.mode ===
            'paid'
          "
        >
          <p>{{ $t("plugin.purchasedHelp") }}</p>
          <NAlert
            v-if="
              accountId && binding?.accountId && accountId !== binding.accountId
            "
            type="warning"
            >{{ $t("plugin.accountMismatch") }}</NAlert
          >
          <NButton
            v-if="!accountId || binding?.state !== 'bound'"
            type="primary"
            @click="
              detail = undefined;
              emit('account');
            "
            >{{ $t("plugin.accountSetup") }}</NButton
          >
          <a
            v-else-if="
              detail.sellable &&
              accountId === binding?.accountId &&
              !(
                detail.billingMode === 'perpetual' &&
                permanentlyLicensed(detail.pluginId)
              )
            "
            :href="purchaseURL(detail.pluginId)"
            target="_blank"
            rel="noopener noreferrer"
            class="text-primary"
            >{{
              licensed(detail.pluginId) && detail.billingMode !== "perpetual"
                ? $t("plugin.licenseRenew")
                : $t("plugin.purchase")
            }}</a
          >
          <NButton
            v-if="
              accountId &&
              binding?.state === 'bound' &&
              !licensed(detail.pluginId)
            "
            type="primary"
            class="mt-12px"
            @click="
              detail = undefined;
              emit('account');
            "
            >{{ $t("plugin.bindingSync") }}</NButton
          >
        </template>
        <div
          v-if="
            detail.versions.find((v) => v.version === selectedVersion)?.mode ===
              'free' || licensed(detail.pluginId)
          "
          class="mt-16px"
        >
          <NButton
            type="primary"
            :disabled="busy || disabled || !selectedVersion"
            @click="inspectProduct(detail, selectedVersion)"
            >{{ $t("plugin.marketPreview") }}</NButton
          >
        </div>
      </template>
    </NModal>
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
        <NAlert
          v-if="preview.entitlement_mode === 'paid'"
          type="warning"
          class="my-12px"
          ><NCheckbox v-model:checked="paidApproved" :disabled="busy">{{
            $t("plugin.confirmPaid")
          }}</NCheckbox></NAlert
        >
        <NCheckbox v-model:checked="approved" :disabled="busy">{{
          $t("plugin.marketApprove")
        }}</NCheckbox
        ><NSpace class="mt-16px"
          ><NButton
            type="primary"
            :loading="busy"
            :disabled="
              !approved ||
              busy ||
              disabled ||
              (preview.entitlement_mode === 'paid' && !paidApproved)
            "
            @click="install"
            >{{ $t("plugin.confirm") }}</NButton
          ><NButton :disabled="busy" @click="clearPreview">{{
            $t("plugin.cancel")
          }}</NButton></NSpace
        ></template
      >
    </NModal>
  </section>
</template>
<style scoped>
.facet-row {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  padding: 7px 0;
}
.facet-row > span {
  font-size: 12px;
  opacity: 0.6;
  min-width: 36px;
}
.product-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
  border-radius: inherit;
}
.detail-images {
  display: flex;
  overflow: auto;
  gap: 12px;
  margin-bottom: 16px;
}
.detail-images img {
  max-width: 100%;
  height: 180px;
  object-fit: contain;
  border-radius: 8px;
}
.detail-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.65;
}
.release-history {
  margin: 20px 0;
}
.release-history article {
  padding: 12px 0;
  border-bottom: 1px solid rgb(var(--base-text-color) / 0.08);
}

.market-catalog {
  min-width: 0;
}
.catalog-toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  padding: 0 0 16px;
}
.price-filters {
  display: flex;
  gap: 4px;
}
.catalog-search {
  display: flex;
  gap: 8px;
  max-width: 380px;
  flex: 1;
  min-width: 210px;
  margin-left: auto;
}
.catalog-search :deep(.n-input) {
  min-width: 0;
  flex: 1;
  width: 0;
}
.catalog-meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  color: rgb(var(--base-text-color) / 0.55);
  margin: 0 0 14px;
}
.plugin-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
.catalog-product {
  display: flex;
  flex-direction: column;
  background: rgb(var(--container-bg-color));
  border: 1px solid rgb(var(--base-text-color) / 0.1);
  border-radius: 10px;
  overflow: hidden;
  transition: border-color 0.15s;
}
.catalog-product:hover {
  border-color: rgb(var(--primary-color) / 0.5);
}
.product-summary {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 20px 20px 0;
}
.product-icon {
  width: 56px;
  height: 56px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 12px;
}
.product-icon svg {
  width: 30px;
  height: 30px;
}
.violet {
  background: #ede9fe;
  color: #7c3aed;
}
.blue {
  background: #e0eeff;
  color: #2563eb;
}
.teal {
  background: #d8f5eb;
  color: #0f766e;
}
.amber {
  background: #fff1d6;
  color: #a16207;
}
.product-heading {
  min-width: 0;
}
.product-heading h2 {
  font-size: 15px;
  font-weight: 650;
  margin: 0 0 5px;
  overflow-wrap: anywhere;
}
.product-price {
  margin: 0;
  color: #d35431;
  font-size: 14px;
  font-weight: 600;
}
.product-price.free {
  color: #168768;
}
.product-publisher {
  margin: 4px 0 0;
  font-size: 12px;
  opacity: 0.6;
}
.product-description {
  padding: 0 20px;
  margin: 16px 0;
  min-height: 42px;
  line-height: 1.6;
  opacity: 0.65;
  overflow-wrap: anywhere;
}
.product-state {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  padding: 0 20px;
}
.product-actions {
  padding: 12px 20px;
  margin-top: auto;
}
.catalog-product footer {
  display: flex;
  gap: 10px;
  justify-content: space-between;
  font-size: 11px;
  opacity: 0.5;
  padding: 10px 20px;
  border-top: 1px solid rgb(var(--base-text-color) / 0.09);
}
.catalog-product footer span:first-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.catalog-product footer span:last-child {
  flex-shrink: 0;
}
.catalog-empty {
  padding: 60px 0;
}
:global(.dark) .product-price {
  color: #ffac87;
}
:global(.dark) .product-price.free {
  color: #6cd6b4;
}
@media (max-width: 1100px) {
  .plugin-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 600px) {
  .plugin-grid {
    grid-template-columns: 1fr;
  }
  .catalog-toolbar {
    gap: 8px;
  }
  .catalog-search {
    order: 3;
    flex-basis: 100%;
    max-width: none;
    margin: 0;
  }
}
@media (prefers-reduced-motion: reduce) {
  .catalog-product {
    transition: none;
  }
}

.version-select {
  width: 100%;
  min-width: 0;
  min-height: 44px;
  padding: 8px;
  color: inherit;
  background: var(--n-color);
  border: 1px solid var(--n-border-color);
  border-radius: 4px;
  font: inherit;
}
.version-select:focus-visible {
  outline: 2px solid var(--n-text-color);
  outline-offset: 2px;
}
.digest {
  overflow-wrap: anywhere;
  margin: 12px 0;
}
</style>
