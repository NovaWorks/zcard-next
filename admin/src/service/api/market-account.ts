import { request } from "../request";
const base = "/api/v1/admin/plugins/market";
export interface MarketProductVersion {
  version: string;
  mode: "free" | "paid";
  minCore: string;
  maxCoreExclusive: string;
}
export interface MarketProduct {
  pluginId: string;
  name: string;
  description: string;
  publisher: string;
  sellable: boolean;
  category: string;
  kind: string;
  recommended: boolean;
  images: string[];
  details: string;
  billingMode: "free" | "term" | "perpetual";
  history: {
    version: string;
    publishedAt: number;
    status: string;
    notes: string;
  }[];
  price?: {
    revision: string;
    amount: string;
    currency: "CNY";
    durationSeconds: number;
    trialSeconds: number;
  };
  versions: MarketProductVersion[];
}
export interface MarketPage<T> {
  items: T[];
  nextCursor: string;
}
export interface MarketContext {
  contextId: string;
  contextSecret: string;
}
export interface MarketBegin extends MarketContext {
  userCode: string;
  expiresAt: number;
  pollSeconds: number;
}
export interface MarketCustomer {
  accountId: string;
  displayName: string;
  status: string;
  emailMasked: string;
  emailVerified: boolean;
}
export interface MarketProfile {
  customer: MarketCustomer;
  expiresAt: number;
}
export interface MarketViewStatus {
  status: string;
  expiresAt: number;
  customer?: MarketCustomer;
}
export interface MarketOrder {
  orderNo: string;
  pluginId: string;
  instanceId: string;
  siteId: string;
  amount: string;
  currency: string;
  state: string;
  trial: boolean;
  expiresAt: number;
  createdAt: number;
  lastError: string;
}
export interface MarketEntitlement {
  licenseId: string;
  pluginId: string;
  instanceId: string;
  siteId: string;
  revision: string;
  source: string;
  status: string;
  domain: string;
  notBefore: number;
  expiresAt: number;
  minVersion: string;
  maxVersionExclusive: string;
}
export interface MarketSite {
  siteId: string;
  instanceId: string;
  name: string;
  siteUrl: string;
  verifiedOrigin: string;
  verifiedAt: number;
  verificationState: string;
  connectionState: string;
  revision: string;
  lastSyncAt: number;
  createdAt: number;
}
export interface MarketVerification {
  challenge: {
    schemaVersion: 1;
    challengeId: string;
    instanceId: string;
    proof: string;
  };
  siteId: string;
  expectedRevision: string;
  origin: string;
  expiresAt: number;
}
export const fetchMarketProducts = (
  params: {
    cursor?: string;
    limit?: number;
    q?: string;
    category?: string;
    kind?: string;
    mode?: string;
    recommended?: boolean;
  } = {},
) =>
  request<MarketPage<MarketProduct>>({
    url: `${base}/listings`,
    params,
    silentError: true,
    timeout: 30000,
  });
export const fetchMarketProduct = (pluginId: string) =>
  request<MarketProduct>({
    url: `${base}/listings/${encodeURIComponent(pluginId)}`,
    silentError: true,
    timeout: 30000,
  });
function view<T>(op: string, data: object) {
  return request<T>({
    url: `${base}/account-view/${op}`,
    method: "post",
    data,
    silentError: true,
    timeout: 30000,
  });
}
// Explicitly project the wire proof: the UI keeps expiry/code alongside it, but
// the frozen contract rejects unknown fields on all account-view requests.
const wireProof = (proof: MarketContext): MarketContext => ({
  contextId: proof.contextId,
  contextSecret: proof.contextSecret,
});
export const startMarketView = () => view<MarketBegin>("start", {});
export const pollMarketView = (proof: MarketContext) =>
  view<MarketViewStatus>("poll", wireProof(proof));
export const confirmMarketView = (proof: MarketContext, accountId: string) =>
  view<MarketProfile>("confirm", { ...wireProof(proof), accountId });
export const logoutMarketView = (proof: MarketContext) =>
  view<{ accepted: boolean }>("logout", wireProof(proof));
export const fetchMarketProfile = (proof: MarketContext) =>
  view<MarketProfile>("me", wireProof(proof));
export const fetchMarketOrders = (proof: MarketContext, cursor = "") =>
  view<MarketPage<MarketOrder>>("orders", {
    ...wireProof(proof),
    ...(cursor ? { cursor } : {}),
  });
export const fetchMarketEntitlements = (proof: MarketContext, cursor = "") =>
  view<MarketPage<MarketEntitlement>>("entitlements", {
    ...wireProof(proof),
    ...(cursor ? { cursor } : {}),
  });
export const fetchMarketSites = (proof: MarketContext, cursor = "") =>
  view<MarketPage<MarketSite>>("sites", {
    ...wireProof(proof),
    ...(cursor ? { cursor } : {}),
  });
export const putMarketSiteChallenge = (data: MarketVerification) =>
  request<{ accepted: boolean }>({
    url: `${base}/site-challenge`,
    method: "put",
    data,
    silentError: true,
    timeout: 30000,
  });

export interface MarketWallet {
  balance: string;
  currency: "CNY";
}
export const fetchMarketWallet = (proof: MarketContext) =>
  view<MarketWallet>("wallet", wireProof(proof));

export const fetchMarketFacets = () =>
  request<{ categories: string[]; kinds: string[] }>({
    url: `${base}/facets`,
    silentError: true,
    timeout: 30000,
  });
export const fetchMarketUpdates = () =>
  request<{
    items: {
      pluginId: string;
      currentVersion: string;
      latestVersion: string;
    }[];
  }>({ url: `${base}/updates`, silentError: true, timeout: 30000 });
