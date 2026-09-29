<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { formatMarketCents } from "@/utils/money";
import { $t } from "@/locales";
import { useMarketAccount } from "@/hooks/business/market-account";
import {
  fetchMarketOrders,
  fetchMarketEntitlements,
  fetchMarketSites,
  putMarketSiteChallenge,
  type MarketOrder,
  type MarketEntitlement,
  type MarketSite,
  type MarketPage,
} from "@/service/api/market-account";
import PluginMarketBinding from "./plugin-market-binding.vue";
const props = defineProps<{ origin: string; disabled: boolean }>();
const emit = defineEmits<{
  account: [id: string];
  busy: [value: boolean];
  updated: [];
  navigate: [];
}>();
const {
  proof,
  profile,
  wallet,
  status,
  failure,
  syncedAt,
  busy,
  start,
  poll,
  confirm,
  refresh,
  logout,
} = useMarketAccount(() => props.origin);
const loginOpen = ref(false);
const loginMode = ref<"login" | "register">("login");
let loginWindow: Window | null = null;
const loginURL = computed(
  () =>
    props.origin +
    "/account" +
    (proof.value
      ? "?view_code=" + encodeURIComponent(proof.value.userCode)
      : "") +
    "#" +
    loginMode.value,
);
async function openLogin(mode: "login" | "register" = "login") {
  if (busy.value || props.disabled) return;
  loginMode.value = mode;
  loginOpen.value = true;
  // Open synchronously from the user gesture. No market password or session enters the host.
  if (loginWindow && !loginWindow.closed) loginWindow.close();
  const popup = window.open(
    "about:blank",
    "_blank",
    `popup,width=480,height=760,left=${Math.max(0, window.screenX + (window.outerWidth - 480) / 2)},top=${Math.max(0, window.screenY + (window.outerHeight - 760) / 2)}`,
  );
  if (popup) {
    popup.opener = null;
    loginWindow = popup;
  }
  if (
    !!failure.value ||
    !proof.value ||
    proof.value.expiresAt * 1000 <= Date.now() ||
    (status.value && !["pending", "approved"].includes(status.value.status))
  )
    await start();
  if (popup && !popup.closed) {
    if (proof.value) popup.location.replace(loginURL.value);
    else popup.close();
  }
}
watch(status, (value) => {
  if (value?.status === "approved") loginOpen.value = true;
});
watch(profile, (value) => {
  if (value) {
    loginOpen.value = false;
    if (loginWindow && !loginWindow.closed) loginWindow.close();
    loginWindow = null;
  }
});
const confirmed = ref(false),
  listBusy = ref(false),
  listError = ref(false),
  copied = ref(false),
  challenge = ref("");
const challengeMessage = ref("");
const orders = ref<MarketPage<MarketOrder>>(),
  entitlements = ref<MarketPage<MarketEntitlement>>(),
  sites = ref<MarketPage<MarketSite>>();
const accountId = computed(() => profile.value?.customer.accountId || "");
let epoch = 0;
const date = (value: number) =>
  value === 253402300799
    ? $t("plugin.permanentLicense")
    : value
      ? new Date(value * 1000).toLocaleString()
      : "—";
const money = formatMarketCents;
function label(value: string) {
  return (
    (
      {
        fulfilled: $t("plugin.orderFulfilled"),
        pending: $t("plugin.pending"),
        failed: $t("plugin.failed"),
        refunded: $t("plugin.orderRefunded"),
        refunding: $t("plugin.orderRefunding"),
        compensating: $t("plugin.orderRefunding"),
        active: $t("plugin.licenseValid"),
        expired: $t("plugin.expired"),
        revoked: $t("plugin.revoked"),
        bound: $t("plugin.bindingBound"),
        connected: $t("plugin.bindingBound"),
        gift: $t("plugin.sourceManual"),
        legacy: $t("plugin.sourceLegacy"),
        not_yet_valid: $t("plugin.licenseNotYetValid"),
        disconnected: $t("plugin.siteDisconnected"),
        verified: $t("plugin.siteVerified"),
        unverified: $t("plugin.siteUnverified"),
        purchase: $t("plugin.sourcePurchase"),
        trial: $t("plugin.sourceTrial"),
        manual: $t("plugin.sourceManual"),
        test: $t("plugin.sourceTest"),
      } as Record<string, string>
    )[value] || value
  );
}
async function load(
  kind: "orders" | "entitlements" | "sites" | "all" = "all",
  more = false,
) {
  if (!proof.value || !profile.value || listBusy.value) return;
  const current = epoch,
    context = proof.value;
  listBusy.value = true;
  listError.value = false;
  const selected =
    kind === "all" ? (["orders", "entitlements", "sites"] as const) : [kind];
  for (const item of selected) {
    const target =
      item === "orders"
        ? orders
        : item === "entitlements"
          ? entitlements
          : sites;
    const result = await {
      orders: fetchMarketOrders,
      entitlements: fetchMarketEntitlements,
      sites: fetchMarketSites,
    }[item](context, more ? target.value?.nextCursor : "");
    if (current !== epoch) return;
    if (result.data)
      (target as any).value = {
        items: [
          ...(more ? target.value?.items || [] : []),
          ...result.data.items,
        ],
        nextCursor: result.data.nextCursor,
      };
    else {
      listError.value = true;
      orders.value = undefined;
      entitlements.value = undefined;
      sites.value = undefined;
      break;
    }
  }
  listBusy.value = false;
}
watch(
  [profile, proof],
  () => {
    epoch++;
    orders.value = undefined;
    entitlements.value = undefined;
    sites.value = undefined;
    listBusy.value = false;
    confirmed.value = false;
    emit("account", accountId.value);
    if (profile.value) void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  if (loginWindow && !loginWindow.closed) loginWindow.close();
  epoch++;
  emit("account", "");
});
async function copy(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    copied.value = true;
  } catch {
    copied.value = false;
  }
}
async function publishChallenge() {
  challengeMessage.value = "";
  try {
    const input = JSON.parse(challenge.value);
    const r = await putMarketSiteChallenge(input);
    if (r.error) throw Error();
    challenge.value = "";
    challengeMessage.value = $t("plugin.challengeReady");
  } catch {
    challengeMessage.value = $t("plugin.challengeFailed");
  }
}
</script>
<template>
  <div class="account-grid">
    <Teleport to="#plugin-account-slot">
      <NButton
        v-if="!profile"
        class="market-login-button"
        :disabled="disabled || busy"
        @click="openLogin('login')"
      >
        <span class="account-avatar" aria-hidden="true"
          ><svg
            viewBox="0 0 24 24"
            width="17"
            height="17"
            fill="none"
            stroke="currentColor"
            stroke-width="1.7"
          >
            <circle cx="12" cy="8" r="3.5" />
            <path d="M5 21v-2a7 7 0 0 1 14 0v2" /></svg
        ></span>
        {{ $t("plugin.loginShort") }}
      </NButton>
      <NPopover v-else trigger="click" placement="bottom-end">
        <template #trigger
          ><NButton class="account-trigger"
            ><span class="account-avatar" aria-hidden="true">{{
              profile.customer.displayName.slice(0, 1).toUpperCase()
            }}</span
            ><span class="account-trigger-text"
              ><strong>{{ profile.customer.displayName }}</strong
              ><small
                >{{ $t("plugin.walletBalance") }}
                {{
                  wallet ? money(wallet.balance) + " " + wallet.currency : "—"
                }}</small
              ></span
            ><span aria-hidden="true">⌄</span></NButton
          ></template
        >
        <div class="account-menu">
          <strong>{{ profile.customer.displayName }}</strong>
          <p>{{ profile.customer.emailMasked }}</p>
          <p>
            {{ $t("plugin.walletBalance") }}:
            <strong>{{
              wallet
                ? money(wallet.balance) + " " + wallet.currency
                : $t("plugin.walletUnavailable")
            }}</strong>
          </p>
          <NButton block quaternary @click="emit('navigate')">{{
            $t("plugin.accountTab")
          }}</NButton>
          <NButton
            block
            quaternary
            :disabled="busy || listBusy"
            @click="refresh"
            >{{ $t("plugin.accountRefresh") }}</NButton
          >
          <NDivider class="my-8px" />
          <NButton block quaternary :disabled="busy" @click="logout">{{
            $t("plugin.logoutShort")
          }}</NButton>
        </div>
      </NPopover>
    </Teleport>
    <NCard :title="$t('plugin.accountTab')">
      <template v-if="profile"
        ><strong>{{ profile.customer.displayName }}</strong>
        <p>
          {{ profile.customer.emailMasked }} ·
          {{
            profile.customer.emailVerified
              ? $t("plugin.emailVerified")
              : $t("plugin.emailUnverified")
          }}
        </p>
        <p>
          {{ $t("plugin.walletBalance") }}:
          {{
            wallet
              ? money(wallet.balance) + " " + wallet.currency
              : $t("plugin.walletUnavailable")
          }}
        </p>
        <NSpace
          ><NButton :disabled="busy || listBusy" @click="refresh">{{
            $t("plugin.accountRefresh")
          }}</NButton
          ><a
            :href="origin + '/shop'"
            target="_blank"
            rel="noopener noreferrer"
            >{{ $t("plugin.customerCenter") }}</a
          ></NSpace
        ></template
      >
      <template v-else
        ><p>{{ $t("plugin.authWelcome") }}</p>
        <NSpace
          ><NButton
            type="primary"
            :disabled="busy || disabled"
            @click="openLogin('login')"
            >{{ $t("plugin.loginShort") }}</NButton
          ><NButton
            :disabled="busy || disabled"
            @click="openLogin('register')"
            >{{ $t("plugin.registerShort") }}</NButton
          ></NSpace
        ></template
      >
      <NAlert v-if="failure" type="error" class="mt-12px"
        >{{ $t("plugin.accountUnavailable") }} · {{ failure }}</NAlert
      >
    </NCard>
    <NModal
      v-model:show="loginOpen"
      preset="card"
      :title="$t('plugin.marketSignIn')"
      style="width: min(460px, calc(100vw - 32px))"
      :mask-closable="!busy"
    >
      <p class="auth-title">{{ $t("plugin.authWelcome") }}</p>
      <NSpace v-if="status?.status !== 'approved'" class="my-12px"
        ><NButton
          :type="loginMode === 'login' ? 'primary' : 'default'"
          @click="openLogin('login')"
          >{{ $t("plugin.loginShort") }}</NButton
        ><NButton
          :type="loginMode === 'register' ? 'primary' : 'default'"
          @click="openLogin('register')"
          >{{ $t("plugin.registerShort") }}</NButton
        ></NSpace
      >
      <p class="break-all">{{ origin }}</p>
      <NAlert v-if="failure" type="error" role="alert"
        >{{ $t("plugin.accountUnavailable") }} · {{ failure }}</NAlert
      >
      <NSpace class="my-12px">
        <NButton
          v-if="proof && status?.status !== 'approved'"
          :disabled="disabled || busy"
          @click="poll"
          >{{ $t("plugin.accountPoll") }}</NButton
        >
        <NButton
          :disabled="busy"
          @click="
            logout();
            loginOpen = false;
          "
          >{{ $t("plugin.cancel") }}</NButton
        >
      </NSpace>
      <section v-if="proof && !profile">
        <p>
          {{ $t("plugin.accountCode") }}:
          <strong>{{ proof.userCode }}</strong> · {{ date(proof.expiresAt) }}
        </p>
        <NSpace>
          <a
            :href="loginURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-primary"
            >{{ $t("plugin.accountOpen") }}</a
          >
          <NButton @click="copy(proof.userCode)">{{
            $t("plugin.copyCode")
          }}</NButton>
          <NButton @click="copy(loginURL)">{{ $t("plugin.copyLink") }}</NButton>
        </NSpace>
        <p>{{ $t("plugin.popupHelp") }}</p>
        <p v-if="copied" role="status">{{ $t("plugin.copied") }}</p>
        <div v-if="status?.status === 'approved' && status.customer">
          <p>
            {{ status.customer.displayName }} ·
            {{ status.customer.emailMasked }} · {{ status.customer.accountId }}
          </p>
          <NCheckbox v-model:checked="confirmed">{{
            $t("plugin.accountConfirmHelp")
          }}</NCheckbox>
          <NButton
            :disabled="!confirmed || busy || disabled"
            @click="confirm(status.customer!.accountId)"
            >{{ $t("plugin.accountConfirm") }}</NButton
          >
        </div>
        <NAlert
          v-else-if="status && !['pending', 'approved'].includes(status.status)"
          type="warning"
          >{{ $t("plugin.accountRestart") }} · {{ status.status }}</NAlert
        >
      </section>
    </NModal>
    <NCard :title="$t('plugin.currentSiteConnection')">
      <PluginMarketBinding
        :disabled="disabled"
        :account-id="accountId"
        @updated="emit('updated')"
        @busy="(value) => emit('busy', value)"
      />
      <details class="mt-16px">
        <summary>{{ $t("plugin.siteChallenge") }}</summary>
        <p>{{ $t("plugin.challengeHelp") }}</p>
        <NInput
          v-model:value="challenge"
          type="textarea"
          :aria-label="$t('plugin.siteChallenge')"
        />
        <NButton :disabled="disabled || !challenge" @click="publishChallenge">{{
          $t("plugin.challengePublish")
        }}</NButton>
        <p role="status">{{ challengeMessage }}</p>
      </details>
    </NCard>
    <NAlert v-if="listError" type="error" role="alert"
      >{{ $t("plugin.accountListFailed") }}
      <NButton :disabled="listBusy" @click="load()">{{
        $t("plugin.refresh")
      }}</NButton></NAlert
    >
    <template v-if="profile">
      <NCard :title="$t('plugin.purchases')" :aria-busy="listBusy">
        <p>{{ $t("plugin.purchasedHelp") }}</p>
        <NEmpty
          v-if="orders && !orders.items.length"
          :description="$t('plugin.noRecords')"
        />
        <article
          v-for="row in orders?.items"
          :key="row.orderNo"
          class="account-row"
        >
          <strong>{{ row.pluginId }}</strong>
          <p>
            {{ row.orderNo }} · {{ label(row.state) }} ·
            {{ money(row.amount) }} {{ row.currency }}
          </p>
          <p>
            {{ row.instanceId }} · {{ date(row.createdAt) }}
            <span v-if="row.trial">· {{ $t("plugin.sourceTrial") }}</span>
          </p>
        </article>
        <NButton
          v-if="orders?.nextCursor"
          :disabled="listBusy"
          @click="load('orders', true)"
          >{{ $t("plugin.marketMore") }}</NButton
        >
      </NCard>
      <NCard :title="$t('plugin.accountEntitlements')">
        <NEmpty
          v-if="entitlements && !entitlements.items.length"
          :description="$t('plugin.noRecords')"
        />
        <article
          v-for="row in entitlements?.items"
          :key="row.licenseId"
          class="account-row"
        >
          <strong>{{ row.pluginId }}</strong>
          <p>
            {{
              label(
                row.status === "active"
                  ? row.expiresAt <= Date.now() / 1000
                    ? "expired"
                    : row.notBefore > Date.now() / 1000
                      ? "not_yet_valid"
                      : "active"
                  : row.status,
              )
            }}
            · {{ label(row.source) }} ·
            {{ row.instanceId }}
          </p>
          <p>
            {{ date(row.notBefore) }} — {{ date(row.expiresAt) }} ·
            {{ row.minVersion }} ≤ v &lt; {{ row.maxVersionExclusive }}
          </p>
          <p v-if="row.domain">{{ row.domain }}</p>
        </article>
        <NButton
          v-if="entitlements?.nextCursor"
          :disabled="listBusy"
          @click="load('entitlements', true)"
          >{{ $t("plugin.marketMore") }}</NButton
        >
      </NCard>
      <NCard :title="$t('plugin.accountSites')">
        <NEmpty
          v-if="sites && !sites.items.length"
          :description="$t('plugin.noRecords')"
        />
        <article
          v-for="row in sites?.items"
          :key="row.siteId"
          class="account-row"
        >
          <strong>{{ row.name || row.instanceId }}</strong>
          <p>{{ row.instanceId }} · {{ label(row.connectionState) }}</p>
          <p>{{ row.siteUrl || "—" }} · {{ label(row.verificationState) }}</p>
          <p v-if="row.verifiedOrigin">{{ row.verifiedOrigin }}</p>
        </article>
        <NButton
          v-if="sites?.nextCursor"
          :disabled="listBusy"
          @click="load('sites', true)"
          >{{ $t("plugin.marketMore") }}</NButton
        >
      </NCard>
    </template>
  </div>
</template>
<style scoped>
.account-trigger {
  height: auto;
  min-height: 46px;
  padding: 5px 12px;
}
.account-trigger-text {
  display: grid;
  text-align: left;
  gap: 2px;
  max-width: 160px;
  overflow: hidden;
}
.account-trigger-text strong {
  text-overflow: ellipsis;
  overflow: hidden;
}
.account-trigger-text small {
  font-size: 11px;
  opacity: 0.7;
}
.account-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: rgb(var(--primary-color) / 0.1);
  color: rgb(var(--primary-color));
  margin-right: 8px;
}
.account-menu {
  min-width: 220px;
  max-width: calc(100vw - 56px);
  overflow-wrap: anywhere;
}
.auth-title {
  font-size: 18px;
  font-weight: 600;
}

.account-grid {
  display: grid;
  gap: 16px;
  min-width: 0;
}
.account-row {
  padding: 16px 0;
  border-bottom: 1px solid var(--n-border-color);
  overflow-wrap: anywhere;
}
p {
  margin: 8px 0;
  overflow-wrap: anywhere;
}
a:focus-visible {
  outline: 2px solid currentColor;
  outline-offset: 4px;
}
</style>
