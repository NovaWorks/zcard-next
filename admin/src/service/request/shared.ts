import { useAuthStore } from '@/store/modules/auth';
import { localStg } from '@/utils/storage';
import { fetchRefreshToken } from '../api';
import type { RequestInstanceState } from './type';

export function getAuthorization() {
  const token = localStg.get('token');
  const Authorization = token ? `Bearer ${token}` : null;
  return Authorization;
}

/** refresh token（Kratos：POST /api/v1/admin/auth/refresh） */
async function handleRefreshToken() {
  const { resetStore } = useAuthStore();
  const rToken = localStg.get('refreshToken') || '';
  if (!rToken) return false;

  const { error, data } = await fetchRefreshToken(rToken);
  if (!error && data) {
    // Kratos 返回 snake_case
    const accessToken = (data as any).access_token || (data as any).token;
    const newRefresh = (data as any).refresh_token || (data as any).refreshToken;
    if (accessToken) localStg.set('token', accessToken);
    if (newRefresh) localStg.set('refreshToken', newRefresh);
    return true;
  }
  resetStore();
  return false;
}

export async function handleExpiredRequest(state: RequestInstanceState) {
  if (!state.refreshTokenPromise) {
    state.refreshTokenPromise = handleRefreshToken();
  }
  const success = await state.refreshTokenPromise;
  setTimeout(() => {
    state.refreshTokenPromise = null;
  }, 1000);
  return success;
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
