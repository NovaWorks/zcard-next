import { request } from "../request";

// ── 订单管理 ──

export function fetchOrders(params?: { status?: string; cursor?: number; limit?: number }) {
  return request({
    url: "/api/v1/admin/orders",
    method: "get",
    params,
  });
}

export function fetchOrder(orderNo: string) {
  return request({
    url: `/api/v1/admin/orders/${orderNo}`,
  });
}

export function cancelOrder(orderNo: string, reason: string) {
  return request({
    url: `/api/v1/admin/orders/${orderNo}/cancel`,
    method: "post",
    data: { reason },
  });
}
