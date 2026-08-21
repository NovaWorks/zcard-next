// 货源渠道 API（supply:read / supply:write）：连接 CRUD、探活、映射、同步任务、健康度。
// P2-10：settings 携带 schedule（定时计划）与限流参数；连接响应含调度锚点与熔断状态。

import { request } from "../request";

// ── 连接 ──

export function fetchSupplyConnections(params?: { page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/supply/connections", params });
}

export function createSupplyConnection(data: {
  name: string;
  driver: string; // zcard | dujiao_next | acg_faka
  base_url: string;
  credentials: string; // 驱动结构 JSON 明文（仅此接口）
  callback_url?: string;
  retry_max?: number;
  retry_intervals?: string;
  exchange_rate?: number;
  price_markup_percent?: number;
  price_markup_amount?: number; // 固定加价（分）
  price_rounding_mode?: string;
  auto_sync_price?: boolean;
  stock_mode?: string;
  settings?: string;
}) {
  return request({ url: "/api/v1/admin/supply/connections", method: "post", data });
}

export function updateSupplyConnection(id: number, data: Record<string, unknown>) {
  return request({ url: `/api/v1/admin/supply/connections/${id}`, method: "put", data });
}

export function deleteSupplyConnection(id: number) {
  return request({ url: `/api/v1/admin/supply/connections/${id}`, method: "delete" });
}

export function pingSupplyConnection(id: number) {
  return request({ url: `/api/v1/admin/supply/connections/${id}/ping`, method: "post" });
}

// ── 映射 ──

export function fetchSupplyMappings(params: { connection_id?: number; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/supply/mappings", params });
}

export function upsertSupplyMapping(data: Record<string, unknown>) {
  return request({ url: "/api/v1/admin/supply/mappings", method: "post", data });
}

export function deleteSupplyMapping(id: number) {
  return request({ url: `/api/v1/admin/supply/mappings/${id}`, method: "delete" });
}

// ── 同步任务 ──

export function createSupplySyncTask(data: {
  connection_id: number;
  mode?: string; // full | incremental（默认 full；驱动不支持增量自动回落）
  scope?: string; // collect=采集（默认）| price=仅价格 | status=仅上下架与库存
  force_reprice?: boolean;
}) {
  return request({ url: "/api/v1/admin/supply/sync-tasks", method: "post", data });
}

export function fetchSupplySyncTasks(params: { connection_id?: number; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/supply/sync-tasks", params });
}

export function cancelSupplySyncTask(id: number) {
  return request({ url: `/api/v1/admin/supply/sync-tasks/${id}/cancel`, method: "post" });
}

// ── 健康度 ──

export function fetchSupplyHealth() {
  return request({ url: "/api/v1/admin/supply/health" });
}

// ── 交互式导入（P2-10 D）──

export function previewSupplyProducts(connectionId: number) {
  return request({ url: `/api/v1/admin/supply/connections/${connectionId}/preview` });
}

export function importSupplyProducts(
  connectionId: number,
  data: {
    codes: string[];
    pricing_mode?: string; // percent | fixed | equal | pending
    markup_percent?: number;
    markup_amount_cents?: number;
    save_default?: boolean;
    category_map?: Record<string, number>;
  },
) {
  return request({ url: `/api/v1/admin/supply/connections/${connectionId}/import`, method: "post", data });
}
