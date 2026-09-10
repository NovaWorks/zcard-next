import { request } from "../request";

// ── 订单管理 ──

export function fetchOrders(params?: { status?: string; cursor?: number; limit?: number; keyword?: string }) {
  return request({
    url: "/api/v1/admin/orders",
    method: "get",
    params,
  });
}

export function deleteOrders(orderNos: string[]) {
  return request({ url: "/api/v1/admin/orders/delete", method: "post", data: { order_nos: orderNos } });
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

// ── 退款（payment 域，order:refund 超管专属）──

export function createRefund(data: {
  expected_refunded_cents: number;
  order_no: string;
  amount_cents: number;
  channel: string; // wallet | gateway | upstream
  reason?: string;
}) {
  return request({ url: "/api/v1/admin/refunds", method: "post", data });
}

export function fetchRefunds(status?: string) {
  return request({ url: "/api/v1/admin/refunds", params: status ? { status } : {} });
}

// ── 履约（fulfillment 域）──

export function fetchPendingDeliveries(page = 1, pageSize = 20) {
  return request({ url: "/api/v1/admin/fulfillment/pending", params: { page, page_size: pageSize } });
}

export function manualDeliver(orderNo: string, data: { order_item_id?: number; content?: string; logistics_no?: string; remark?: string }) {
  return request({ url: `/api/v1/admin/fulfillment/${orderNo}/deliver`, method: "post", data });
}

export function fetchDeliveries(orderNo: string, page = 1, pageSize = 20, orderItemId?: number) {
  return request({ url: "/api/v1/admin/fulfillment", params: { order_no: orderNo, page, page_size: pageSize, order_item_id: orderItemId } });
}
