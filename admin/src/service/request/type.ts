import "axios";

declare module "axios" {
  interface AxiosRequestConfig {
    /** 由更新页实时提供等待状态，仅供更新状态 GET 的错误提示判定。 */
    expectedUpdateRestart?: () => boolean;
  }
}

export interface RequestInstanceState {
  /** the promise of refreshing token */
  refreshTokenPromise: Promise<boolean> | null;
  /** the request error message stack */
  errMsgStack: string[];
  [key: string]: unknown;
}
