/** 仅更新状态轮询可以容忍已知重启期间的连接中断。 */
export const UPDATE_STATUS_URL = "/api/v1/admin/update/status";
export const RESTART_WAIT_TIMEOUT = 5 * 60 * 1000;

interface RestartError {
  code?: string;
  response?: { status?: number };
  config?: { url?: string; expectedUpdateRestart?: () => boolean };
}

export function isRestartConnectionError(error: RestartError): boolean {
  const status = error.response?.status;
  if (status) return [502, 503, 504].includes(status);
  return ["ERR_NETWORK", "ECONNABORTED", "ETIMEDOUT"].includes(error.code || "");
}

export function suppressUpdateRestartError(error: RestartError): boolean {
  return error.config?.url === UPDATE_STATUS_URL
    && error.config.expectedUpdateRestart?.() === true
    && isRestartConnectionError(error);
}

export function restartWaitExpired(start: number, now: number): boolean {
  return start > 0 && now - start >= RESTART_WAIT_TIMEOUT;
}
