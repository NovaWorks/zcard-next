<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { $t } from "@/locales";
import {
  fetchMarketBinding,
  operateMarketBinding,
  type MarketBinding,
} from "@/service/api/plugin";
const props = defineProps<{ disabled: boolean; accountId?: string }>();
const emit = defineEmits<{ busy: [value: boolean]; updated: [] }>();
const state = ref<MarketBinding>();
const mismatch = computed(
  () =>
    !!props.accountId &&
    !!state.value?.accountId &&
    props.accountId !== state.value.accountId,
);
let lastReturnSync = 0;
function returnSync() {
  if (
    document.visibilityState === "visible" &&
    state.value?.state === "bound" &&
    !mismatch.value &&
    !unavailable.value &&
    Date.now() - lastReturnSync > 60000
  ) {
    lastReturnSync = Date.now();
    void operate("sync");
  }
}
const busy = ref(false);
const failure = ref("");
const message = ref("");
const confirmed = ref(false);
const recovery = ref(false);
const cooldown = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;
const unavailable = computed(() => props.disabled || busy.value);
const stateLabel = computed(() => {
  const v = state.value?.state;
  switch (v) {
    case "bound":
      return $t("plugin.bindingBound");
    case "approved":
      return $t("plugin.bindingApproved");
    case "pending":
      return $t("plugin.bindingPending");
    case "confirming":
      return $t("plugin.bindingConfirming");
    case "rotating":
      return $t("plugin.bindingRotating");
    case "revoking":
      return $t("plugin.bindingRevoking");
    case "pair_expired":
      return $t("plugin.bindingPairExpired");
    case "credential_expired":
      return $t("plugin.bindingCredentialExpired");
    case "recovery_required":
      return $t("plugin.bindingRecoveryRequired");
    case "starting":
      return $t("plugin.bindingStarting");
    default:
      return $t("plugin.bindingUnbound");
  }
});
function running(value: boolean) {
  busy.value = value;
  emit("busy", value);
}
async function refresh() {
  if (unavailable.value) return;
  running(true);
  try {
    const r = await fetchMarketBinding();
    if (r.error || !r.data) failure.value = $t("plugin.bindingLoadFailed");
    else {
      state.value = r.data;
      failure.value = "";
    }
  } finally {
    running(false);
  }
}
async function operate(action: string) {
  if (
    unavailable.value ||
    (mismatch.value && ["confirm", "sync"].includes(action)) ||
    (action === "poll" && cooldown.value)
  )
    return;
  running(true);
  failure.value = "";
  message.value = "";
  try {
    const r = await operateMarketBinding(
      action,
      state.value?.accountId,
      confirmed.value || recovery.value,
    );
    if (r.error || !r.data) {
      failure.value = $t("plugin.bindingActionFailed");
      // A failed response may still have changed the durable recovery state.
      const latest = await fetchMarketBinding();
      if (latest.data) state.value = latest.data;
    } else {
      state.value = r.data;
      message.value = $t("plugin.bindingSaved");
      confirmed.value = false;
      recovery.value = false;
    }
  } finally {
    if (action === "poll") {
      cooldown.value = true;
      clearTimeout(timer);
      timer = setTimeout(() => {
        cooldown.value = false;
      }, 5000);
    }
    running(false);
  }
  emit("updated");
}
watch(
  () => props.disabled,
  (value) => {
    if (!value && !state.value && !busy.value && !failure.value) void refresh();
  },
);
onMounted(() => {
  void refresh();
  window.addEventListener("focus", returnSync);
});
onUnmounted(() => {
  clearTimeout(timer);
  window.removeEventListener("focus", returnSync);
});
</script>
<template>
  <section :aria-label="$t('plugin.bindingTitle')">
    <h3>{{ $t("plugin.bindingTitle") }}</h3>
    <p>{{ $t("plugin.bindingIntro") }}</p>
    <NAlert v-if="mismatch" type="warning" role="alert">{{
      $t("plugin.accountMismatch")
    }}</NAlert>
    <p v-if="state?.instanceId" class="break-all">
      {{ $t("plugin.licenseInstance") }}: {{ state.instanceId }}
    </p>
    <p role="status">{{ stateLabel }}</p>
    <p v-if="state?.origin" class="break-all">
      {{ $t("plugin.bindingMarket") }}: {{ state.origin }}
    </p>
    <p v-if="state?.userCode" class="break-all">
      {{ $t("plugin.bindingCode") }}: <strong>{{ state.userCode }}</strong>
    </p>
    <p v-if="state?.origin">
      <a
        :href="`${state.origin}/pair`"
        target="_blank"
        rel="noopener noreferrer"
        >{{ $t("plugin.bindingOpenMarket") }}</a
      >
    </p>
    <p v-if="state?.accountId" class="break-all">
      {{ $t("plugin.bindingAccount") }}: {{ state.accountName }} ({{
        state.accountId
      }})
    </p>
    <p v-if="state?.expiresAt">
      {{ $t("plugin.bindingExpires") }}:
      {{ new Date(state.expiresAt * 1000).toLocaleString() }}
    </p>
    <p>
      {{ $t("plugin.bindingLastSync") }}:
      {{
        state?.lastSync
          ? new Date(state.lastSync * 1000).toLocaleString()
          : $t("plugin.bindingNever")
      }}
    </p>
    <p>
      {{ $t("plugin.bindingSafetyCheck") }}:
      {{
        state?.safetyCheckedAt
          ? new Date(state.safetyCheckedAt * 1000).toLocaleString()
          : $t("plugin.bindingNever")
      }}
    </p>
    <NAlert v-if="failure" type="error" role="alert" class="my-12px">{{
      failure
    }}</NAlert>
    <NAlert v-if="message" type="success" role="status" class="my-12px">{{
      message
    }}</NAlert>
    <NCheckbox
      v-if="
        [
          'approved',
          'confirming',
          'bound',
          'credential_expired',
          'revoking',
        ].includes(state?.state || '')
      "
      v-model:checked="confirmed"
      :disabled="unavailable"
      class="my-12px"
      >{{ $t("plugin.bindingConfirm") }}</NCheckbox
    >
    <NSpace>
      <NButton
        v-if="['unbound', 'starting'].includes(state?.state || '')"
        :disabled="unavailable || !state?.origin"
        :loading="busy"
        @click="operate('start')"
        >{{ $t("plugin.bindingStart") }}</NButton
      >
      <NButton
        v-if="['pending', 'approved'].includes(state?.state || '')"
        :disabled="unavailable || cooldown"
        @click="operate('poll')"
        >{{ $t("plugin.bindingPoll") }}</NButton
      >
      <NButton
        v-if="['approved', 'confirming'].includes(state?.state || '')"
        type="primary"
        :disabled="unavailable || !confirmed || mismatch"
        @click="operate('confirm')"
        >{{ $t("plugin.bindingFinish") }}</NButton
      >
      <NButton
        v-if="
          ['starting', 'pending', 'approved', 'pair_expired'].includes(
            state?.state || '',
          )
        "
        :disabled="unavailable"
        @click="operate('cancel')"
        >{{ $t("plugin.bindingCancel") }}</NButton
      >
      <NButton
        v-if="state?.state === 'bound'"
        type="primary"
        :disabled="unavailable || mismatch"
        @click="operate('sync')"
        >{{ $t("plugin.bindingSync") }}</NButton
      >
      <NButton
        v-if="['bound', 'rotating'].includes(state?.state || '')"
        :disabled="unavailable"
        @click="operate('rotate')"
        >{{ $t("plugin.bindingRotate") }}</NButton
      >
      <NButton
        v-if="
          ['bound', 'credential_expired', 'revoking'].includes(
            state?.state || '',
          )
        "
        type="warning"
        :disabled="unavailable || !confirmed"
        @click="operate('revoke')"
        >{{ $t("plugin.bindingRevoke") }}</NButton
      >
      <NButton :disabled="unavailable" @click="refresh">{{
        $t("plugin.refresh")
      }}</NButton>
    </NSpace>
    <details class="mt-16px">
      <summary>{{ $t("plugin.bindingRecovery") }}</summary>
      <p>{{ $t("plugin.bindingRecoveryHelp") }}</p>
      <NCheckbox v-model:checked="recovery" :disabled="unavailable">{{
        $t("plugin.bindingRecoveryConfirm")
      }}</NCheckbox>
      <div class="mt-12px">
        <NButton
          type="warning"
          :disabled="unavailable || !recovery"
          @click="operate('forget')"
          >{{ $t("plugin.bindingForget") }}</NButton
        >
      </div>
    </details>
  </section>
</template>
