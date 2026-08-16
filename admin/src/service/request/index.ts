import type { AxiosResponse } from 'axios';
import { createFlatRequest } from '@sa/axios';
import { useAuthStore } from '@/store/modules/auth';
import { localStg } from '@/utils/storage';
import { getServiceBaseURL } from '@/utils/service';
import { getAuthorization, handleExpiredRequest, showErrorMsg } from './shared';
import type { RequestInstanceState } from './type';

const isHttpProxy = import.meta.env.DEV && import.meta.env.VITE_HTTP_PROXY === 'Y';
const { baseURL } = getServiceBaseURL(import.meta.env, isHttpProxy);

/**
 * Kratos 后端请求客户端：
 * - 成功：HTTP 2xx，响应体为 proto JSON（snake_case）
 * - 失败：HTTP 4xx/5xx，响应体 { code, reason, message }
 * - 401：尝试 refresh，失败则登出
 */
export const request = createFlatRequest<App.Service.Response<any>>(
  {
    baseURL,
    headers: { 'Content-Type': 'application/json' }
  },
  {
    defaultState: {
      errMsgStack: [],
      refreshTokenPromise: null
    } as RequestInstanceState,
    transform(response: AxiosResponse) {
      return response.data as any;
    },
    async onRequest(config) {
      const Authorization = getAuthorization();
      if (Authorization) {
        Object.assign(config.headers, { Authorization });
      }
      return config;
    },
    isBackendSuccess(response) {
      return response.status >= 200 && response.status < 300;
    },
    async onBackendFail(response: any, instance: any) {
      const authStore = useAuthStore();

      function handleLogout() {
        authStore.resetStore();
      }

      if (response.status === 401) {
        const success = await handleExpiredRequest(request.state);
        if (success) {
          const Authorization = getAuthorization();
          if (Authorization) {
            return instance.request({
              ...response.config,
              headers: { ...response.config.headers, Authorization }
            });
          }
        }
        handleLogout();
        return null;
      }

      const errMsg = response.data?.message || `请求失败 (${response.status})`;
      showErrorMsg(request.state, errMsg);
      return null;
    },
    onError(err: any) {
      const msg = err.response?.data?.message || err.message || '网络异常';
      showErrorMsg(request.state, msg);
    }
  }
);
