// 供货账号 API（supplier:read / supplier:write）：下游账户（含 B/C 兼容账号）、
// 审核/启停/充值、账本、回调记录。

import { request } from "../request";

export function fetchSupplierAccounts(params?: { page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/supplier/accounts", params });
}

export function createSupplierAccount(data: {
  name: string;
  api_key: string;
  api_secret: string;
  contact?: string;
  protocol?: string; // zcard（默认）| dujiao_next | acg_faka（兼容账号）
  display_name?: string;
}) {
  return request({ url: "/api/v1/admin/supplier/accounts", method: "post", data });
}

export function reviewSupplierAccount(id: number, approve: boolean, review_note?: string) {
  return request({ url: `/api/v1/admin/supplier/accounts/${id}/review`, method: "post", data: { approve, review_note } });
}

export function toggleSupplierAccount(id: number, enabled: boolean) {
  return request({ url: `/api/v1/admin/supplier/accounts/${id}/toggle`, method: "post", data: { enabled } });
}

export function resetSupplierSecret(id: number) {
  return request({ url: `/api/v1/admin/supplier/accounts/${id}/reset-secret`, method: "post" });
}

export function rechargeSupplierAccount(id: number, amount: number, reference: string, remark?: string) {
  // amount 单位分（铁律 15：界面输入元，提交前换算）
  return request({ url: `/api/v1/admin/supplier/accounts/${id}/recharge`, method: "post", data: { amount, reference, remark } });
}

export function fetchSupplierLedger(params: { account_id?: number; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/supplier/ledger", params });
}

export function fetchSupplierCallbacks(params?: { status?: string; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/supplier/callbacks", params });
}

export function resendSupplierCallback(id: number) {
  return request({ url: `/api/v1/admin/supplier/callbacks/${id}/resend`, method: "post" });
}

export function upsertSupplierPrice(data: { account_id: number; product_id: number; sku_id?: number; price: number }) {
  // price 单位分（界面输入元，提交前换算）
  return request({ url: "/api/v1/admin/supplier/prices", method: "post", data });
}

export function fetchSupplierPrices(accountId: number) {
  return request({ url: "/api/v1/admin/supplier/prices", params: { account_id: accountId } });
}

export function deleteSupplierPrice(id: number) {
  return request({ url: `/api/v1/admin/supplier/prices/${id}`, method: "delete" });
}

export function setSupplierIPWhitelist(id: number, ips: string[]) {
  return request({
    url: `/api/v1/admin/supplier/accounts/${id}/ip-whitelist`,
    method: "put",
    data: { ips },
  });
}
