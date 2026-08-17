// 登录态（P3-09 T1）：轻量响应式状态（无 Pinia 依赖，单例模块）。
import { reactive } from 'vue';
import { me } from '@/api';
import { getToken, clearToken } from '@/api/client';

export const authState = reactive({
  loggedIn: false,
  username: '',
  loaded: false
});

// refreshAuth 启动/登录后调用；无 token 静默，token 失效由 client 401 层清理。
export async function refreshAuth() {
  if (!getToken()) {
    authState.loggedIn = false;
    authState.username = '';
    authState.loaded = true;
    return;
  }
  const { data } = await me();
  if (data) {
    authState.loggedIn = true;
    authState.username = data.username;
  } else {
    authState.loggedIn = false;
    authState.username = '';
  }
  authState.loaded = true;
}

export function logout() {
  clearToken();
  authState.loggedIn = false;
  authState.username = '';
}
