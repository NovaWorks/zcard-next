import { request } from '../request';

// ── 卡密库存 ──

export function importPreview(data: { product_id: number; lines: string[]; dedup?: boolean }) {
  return request({
    url: '/api/v1/admin/inventory/preview',
    method: 'post',
    data
  });
}

export function importConfirm(data: { product_id: number; lines: string[]; dedup?: boolean }) {
  return request({
    url: '/api/v1/admin/inventory/import',
    method: 'post',
    data
  });
}

export function fetchImports(params?: { product_id?: number }) {
  return request({
    url: '/api/v1/admin/inventory/imports',
    method: 'get',
    params
  });
}

export function cancelImport(id: number) {
  return request({
    url: `/api/v1/admin/inventory/imports/${id}/cancel`,
    method: 'post'
  });
}

export function fetchCards(params: {
  product_id: number;
  status?: string;
  page?: number;
  page_size?: number;
}) {
  return request({
    url: '/api/v1/admin/inventory/cards',
    method: 'get',
    params
  });
}
