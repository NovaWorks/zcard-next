import { onScopeDispose, ref, shallowRef } from "vue";
import { getToken } from "@/store/modules/auth/shared";
import {
  startMarketView,
  pollMarketView,
  confirmMarketView,
  logoutMarketView,
  fetchMarketProfile,
  fetchMarketWallet,
  type MarketWallet,
  type MarketBegin,
  type MarketProfile,
  type MarketViewStatus,
} from "@/service/api/market-account";

// A tab-local browser proof only. Remote grant tokens and private result sets never enter Web Storage.
const key = "zcard-market-browser-view";
export function clearMarketBrowserContext() {
  sessionStorage.removeItem(key);
  window.dispatchEvent(new Event("zcard-market-context-clear"));
}
function sessionIdentity() {
  try {
    const v = JSON.parse(
      atob(getToken().split(".")[1].replace(/-/g, "+").replace(/_/g, "/")),
    );
    return `${v.sub}:${v.session_id}:${v.auth_version}`;
  } catch {
    return "";
  }
}
export function useMarketAccount(origin: () => string) {
  const proof = shallowRef<MarketBegin>();
  const profile = shallowRef<MarketProfile>();
  const wallet = shallowRef<MarketWallet>();
  const status = shallowRef<MarketViewStatus>();
  const failure = ref("");
  const syncedAt = ref(0);
  const busy = ref(false);
  let confirmed = false;
  let walletRequest = 0;
  let generation = 0,
    timer: ReturnType<typeof setTimeout> | undefined;
  let scope = `${origin()}|${sessionIdentity()}`;
  function clear() {
    generation++;
    walletRequest++;
    confirmed = false;
    clearTimeout(timer);
    timer = undefined;
    proof.value = undefined;
    profile.value = undefined;
    wallet.value = undefined;
    status.value = undefined;
    syncedAt.value = 0;
    busy.value = false;
    sessionStorage.removeItem(key);
  }
  function checkScope() {
    const next = `${origin()}|${sessionIdentity()}`;
    if (next !== scope) {
      clear();
      scope = next;
    }
  }
  function failed(error: any) {
    const code =
      error?.response?.data?.reason || "market.DEPENDENCY_UNAVAILABLE";
    failure.value = code;
    walletRequest++;
    // An offline response is not an empty purchase history. Private data is hidden until a successful refresh.
    profile.value = undefined;
    wallet.value = undefined;
    status.value = undefined;
    if (error?.response?.status === 401 || error?.response?.status === 403)
      clear();
  }
  function persist() {
    if (proof.value)
      sessionStorage.setItem(
        key,
        JSON.stringify({ scope, proof: proof.value, confirmed }),
      );
  }
  async function refreshWallet(current: number) {
    if (!proof.value || !profile.value) return;
    wallet.value = undefined;
    const request = ++walletRequest;
    const r = await fetchMarketWallet(proof.value);
    checkScope();
    if (current !== generation || request !== walletRequest || !profile.value)
      return;
    if (r.error && [401, 403].includes((r.error as any)?.response?.status))
      failed(r.error);
    else wallet.value = r.data || undefined;
  }
  async function refresh() {
    checkScope();
    if (!proof.value || busy.value) return;
    const current = generation;
    busy.value = true;
    const result = await fetchMarketProfile(proof.value);
    checkScope();
    if (current !== generation) return;
    busy.value = false;
    if (result.error || !result.data) failed(result.error);
    else {
      confirmed = true;
      profile.value = result.data;
      syncedAt.value = Date.now();
      failure.value = "";
      persist();
      clearTimeout(timer);
      timer = setTimeout(refresh, 60000);
      void refreshWallet(current);
    }
  }
  async function poll() {
    checkScope();
    if (!proof.value || busy.value) return;
    if (confirmed) {
      await refresh();
      return;
    }
    if (proof.value.expiresAt * 1000 <= Date.now()) {
      failure.value = "market.SESSION_EXPIRED";
      clear();
      return;
    }
    const current = generation;
    busy.value = true;
    const result = await pollMarketView(proof.value);
    checkScope();
    if (current !== generation) return;
    busy.value = false;
    if (result.error || !result.data) {
      failed(result.error);
      return;
    }
    status.value = result.data;
    failure.value = "";
    if (result.data.status === "confirmed") {
      confirmed = true;
      persist();
      await refresh();
      return;
    }
    if (result.data.status === "pending")
      timer = setTimeout(poll, Math.max(5, proof.value!.pollSeconds) * 1000);
  }
  async function start() {
    if (busy.value) return;
    if (proof.value) await logout();
    checkScope();
    clear();
    const current = generation;
    busy.value = true;
    const result = await startMarketView();
    checkScope();
    if (current !== generation) return;
    busy.value = false;
    if (result.error || !result.data) failed(result.error);
    else {
      proof.value = result.data;
      persist();
      failure.value = "";
      timer = setTimeout(poll, 5000);
    }
  }
  async function confirm(accountId: string) {
    checkScope();
    if (!proof.value || busy.value) return;
    const current = generation;
    busy.value = true;
    const result = await confirmMarketView(proof.value, accountId);
    checkScope();
    if (current !== generation) return;
    busy.value = false;
    if (result.error || !result.data) failed(result.error);
    else {
      confirmed = true;
      profile.value = result.data;
      syncedAt.value = Date.now();
      failure.value = "";
      persist();
      clearTimeout(timer);
      timer = setTimeout(refresh, 60000);
      void refreshWallet(current);
    }
  }
  async function logout() {
    const previous = proof.value;
    clear();
    failure.value = "";
    if (previous) {
      const result = await logoutMarketView(previous);
      if (result.error) failure.value = "market.REVOCATION_PENDING";
    }
  }
  function resume() {
    checkScope();
    if (document.visibilityState === "visible") {
      if (confirmed) void refresh();
      else void poll();
    }
  }
  function changedStorage(e: StorageEvent) {
    if (e.storageArea === localStorage) checkScope();
  }
  try {
    const saved = JSON.parse(sessionStorage.getItem(key) || "null");
    if (
      saved?.scope === scope &&
      saved?.proof?.contextId &&
      saved?.proof?.contextSecret
    ) {
      proof.value = saved.proof;
      confirmed = saved.confirmed === true;
    } else sessionStorage.removeItem(key);
  } catch {
    sessionStorage.removeItem(key);
  }
  window.addEventListener("zcard-market-context-clear", clear);
  window.addEventListener("storage", changedStorage);
  document.addEventListener("visibilitychange", resume);
  timer = setTimeout(resume, 0);
  onScopeDispose(() => {
    generation++;
    clearTimeout(timer);
    window.removeEventListener("zcard-market-context-clear", clear);
    window.removeEventListener("storage", changedStorage);
    document.removeEventListener("visibilitychange", resume);
  });
  return {
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
    clear,
  };
}
