import { request } from "../request";

// ── 卡密库存 ──

export function importPreview(data: { product_id: number; lines: string[]; dedup?: boolean }) {
  return request({
    url: "/api/v1/admin/inventory/preview",
    method: "post",
    data,
  });
}

export function importConfirm(data: { product_id: number; lines: string[]; dedup?: boolean }) {
  return request({
    url: "/api/v1/admin/inventory/import",
    method: "post",
    data,
  });
}

export function fetchImports(params?: { product_id?: number }) {
  return request({
    url: "/api/v1/admin/inventory/imports",
    method: "get",
    params,
  });
}

export function cancelImport(id: number) {
  return request({
    url: `/api/v1/admin/inventory/imports/${id}/cancel`,
    method: "post",
  });
}

export function fetchCards(params: {
  product_id: number;
  status?: string;
  page?: number;
  page_size?: number;
}) {
  return request({
    url: "/api/v1/admin/inventory/cards",
    method: "get",
    params,
  });
}

// ── 卡密运维（inventory:write / card:export / card:premium，超管专属）──

export function toggleCard(id: number, enable: boolean) {
  return request({ url: `/api/v1/admin/inventory/cards/${id}/toggle`, method: "put", data: { enable } });
}

export function exportCards(productId: number) {
  return request<{ lines: string[] }>({ url: "/api/v1/admin/inventory/export", method: "post", data: { product_id: productId } });
}

export function fetchPremiumCards(productId: number) {
  return request({ url: "/api/v1/admin/inventory/cards/premium", params: { product_id: productId } });
}
