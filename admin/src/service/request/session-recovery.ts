import type { AxiosInstance, AxiosResponse } from "axios";
import type { RequestInstanceState } from "./type";

export function singleFlightRefresh(state: RequestInstanceState, refresh: () => Promise<boolean>) {
  state.refreshTokenPromise ??= refresh().catch(() => false).finally(() => {
    state.refreshTokenPromise = null;
  });
  return state.refreshTokenPromise;
}

// Only 401 enters the recovery hook; other HTTP failures keep their existing handling.
export const authResponseStatus = (status: number) =>
  (status >= 200 && status < 300) || status === 304 || status === 401;

export function createSessionRecovery(deps: {
  authorization: () => string | null;
  refresh: () => Promise<boolean>;
  logout: () => Promise<void>;
}) {
  let logout: Promise<void> | undefined;
  return async (response: AxiosResponse, instance: AxiosInstance) => {
    const config = response.config;
    if (response.status !== 401 || /\/auth\/(login|refresh|logout)(?:[?#]|$)/.test(config.url || "")) return null;
    const sent = String(config.headers?.Authorization || "");
    let authorization = deps.authorization();
    if (!config.authRetried && authorization) {
      // A late 401 from the previous token must not rotate the refresh token again.
      const renewed = authorization !== sent || await deps.refresh();
      authorization = deps.authorization();
      if (renewed && authorization) {
        return instance.request({ ...config, authRetried: true, headers: { ...config.headers, Authorization: authorization } });
      }
    }
    // Do not discard a newer session because an older request finished late.
    if (deps.authorization() === sent) {
      logout ??= deps.logout().finally(() => { logout = undefined; });
      await logout;
    }
    return null;
  };
}
