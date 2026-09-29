import { useAuthStore } from "@/store/modules/auth";
import { localStg } from "@/utils/storage";
import { fetchRefreshToken } from "../api";
import type { RequestInstanceState } from "./type";
import { singleFlightRefresh } from "./session-recovery";

export function getAuthorization() {
  const token = localStg.get("token");
  const Authorization = token ? `Bearer ${token}` : null;
  return Authorization;
}

/** refresh token（Kratos：POST /api/v1/admin/auth/refresh） */
async function handleRefreshToken() {
  const authStore = useAuthStore();
  const rToken = localStg.get("refreshToken") || "";
  if (!rToken) return false;

  const { error, data } = await fetchRefreshToken(rToken);
  if (localStg.get("refreshToken") !== rToken) return false;
  if (!error && data) {
    // Kratos 返回 snake_case
    const accessToken = (data as any).access_token || (data as any).token;
    const newRefresh = (data as any).refresh_token || (data as any).refreshToken;
    if (!accessToken) return false;
    localStg.set("token", accessToken);
    authStore.token = accessToken;
    if (newRefresh) localStg.set("refreshToken", newRefresh);
    return true;
  }
  return false;
}

export async function handleExpiredRequest(state: RequestInstanceState) {
  return singleFlightRefresh(state, handleRefreshToken);
}

export function showErrorMsg(state: RequestInstanceState, message: string) {
  if (!state.errMsgStack?.length) {
    state.errMsgStack = [];
  }
  const isExist = state.errMsgStack.includes(message);
  if (!isExist) {
    state.errMsgStack.push(message);
    window.$message?.error(message);
    setTimeout(() => {
      const index = state.errMsgStack.indexOf(message);
      if (index > -1) {
        state.errMsgStack.splice(index, 1);
      }
    }, 3000);
  }
}
