// 与后端订单状态机对应；订单列表、会员详情和采购单使用同一套业务名称。
const labels: Record<string, string> = {
  pending_payment: "待支付",
  paid: "已支付",
  fulfilling: "履约中",
  partially_delivered: "部分发货",
  manual_pending: "待人工发货",
  delivered: "已发货",
  completed: "已完成",
  canceled: "已取消",
  expired: "已过期",
  refund_pending: "退款中",
  refunded: "已退款",
};

export function orderStatusText(status?: string): string {
  return status ? Object.prototype.hasOwnProperty.call(labels, status) ? labels[status] : "未知状态" : "—";
}

export function orderStatusType(status?: string): "success" | "error" | "warning" | "info" | "default" {
  if (["paid", "delivered", "completed"].includes(status || "")) return "success";
  if (["canceled", "expired", "refunded"].includes(status || "")) return "error";
  if (["pending_payment", "manual_pending"].includes(status || "")) return "warning";
  return status && Object.prototype.hasOwnProperty.call(labels, status) ? "info" : "default";
}
