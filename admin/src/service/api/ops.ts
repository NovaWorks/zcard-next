// 运营支撑 API：审计日志（audit:read）+ 货币管理（settings:currency_*）。

import { request } from "../request";

// ── 审计日志 ──

export function fetchOpLogs(params?: { operator_id?: number; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/audit/op-logs", params });
}

export function fetchSecurityLogs(params?: { action?: string; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/audit/security-logs", params });
}

// ── 货币 ──

export function listCurrencies() {
  return request({ url: "/api/v1/admin/currencies" });
}

export function createCurrency(data: { code: string; symbol: string; position?: string; precision?: number; rate_json: string }) {
  return request({ url: "/api/v1/admin/currencies", method: "post", data });
}

export function updateCurrency(code: string, data: { symbol?: string; position?: string; precision?: number; rate_json?: string; enabled?: boolean; sort?: number }) {
  return request({ url: `/api/v1/admin/currencies/${code}`, method: "put", data });
}

export function deleteCurrency(code: string) {
  return request({ url: `/api/v1/admin/currencies/${code}`, method: "delete" });
}
