// 采购单 API（procurement:read / procurement:write）：上游拿货单列表、手动重试/完成。

import { request } from "../request";

export function fetchProcurements(params?: { page?: number; page_size?: number; status?: string; connection_id?: number }) {
  return request({ url: "/api/v1/admin/procurements", params });
}

export function fetchProcurement(id: number) {
  return request({ url: `/api/v1/admin/procurements/${id}` });
}

export function retryProcurement(id: number) {
  return request({ url: `/api/v1/admin/procurements/${id}/retry`, method: "post" });
}

export function markProcurementManual(id: number, reason?: string) {
  return request({ url: `/api/v1/admin/procurements/${id}/manual`, method: "post", data: { reason } });
}
