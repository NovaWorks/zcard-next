// 按业务域解释状态，避免相同英文在不同业务中被误译。
function label(labels: Record<string, string>, status?: string): string {
  if (!status) return "—";
  return Object.prototype.hasOwnProperty.call(labels, status) ? labels[status] : "未知状态";
}
export const couponStatusText = (status?: string) => label({ unused: "未使用", used: "已使用", disabled: "已作废" }, status);
export const supplierStatusText = (status?: string) => label({ applying: "待审核", approved: "已通过", rejected: "已驳回", disabled: "已禁用" }, status);
export const cardStatusText = (status?: string) => label({ available: "可用", reserved: "已锁定", used: "已售出", disabled: "已禁用" }, status);
export const refundStatusText = (status?: string) => label({ created: "待处理", processing: "退款中", succeeded: "退款成功", failed: "退款失败" }, status);
