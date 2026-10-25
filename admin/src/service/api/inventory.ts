import { request } from "../request";

export interface InventorySkuCount {
  sku_id?: number;
  sku_name?: string;
  available?: number;
  reserved?: number;
  used?: number;
  disabled?: number;
}
export interface InventoryTarget extends InventorySkuCount {
  can_import?: boolean;
  reason?: string;
}
export interface InventoryTargets {
  product_id: number;
  product_name: string;
  revision?: number;
  has_skus?: boolean;
  targets: InventoryTarget[];
}
export interface InventoryBatch {
  id: number;
  product_id: number;
  product_name?: string;
  sku_id?: number | null;
  sku_name?: string;
  total?: number;
  imported?: number;
  skipped?: number;
  failed?: number;
  status?: string;
  remaining?: InventorySkuCount[];
}
export interface InventoryImportInput {
  product_id: number;
  sku_id: number;
  lines: string[];
  dedup?: boolean;
}
export interface InventoryPreview {
  total?: number;
  dup_in_file?: number;
  dup_in_db?: number;
  invalid?: number;
  sku_id?: number;
  sku_name?: string;
  revision?: number;
}
export interface InventoryAssignPreview {
  card_ids: number[];
  eligible_count?: number;
  excluded_count?: number;
  revision?: number;
  sku_name?: string;
}

export function fetchInventoryTargets(productId: number) {
  return request<InventoryTargets>({ url: "/api/v1/admin/inventory/targets", params: { product_id: productId } });
}
export function importPreview(data: InventoryImportInput) {
  return request<InventoryPreview>({ url: "/api/v1/admin/inventory/preview", method: "post", data });
}
export function importConfirm(data: InventoryImportInput & { expected_revision: number }) {
  return request<InventoryBatch>({ url: "/api/v1/admin/inventory/import", method: "post", data });
}
export function fetchImports(params?: { product_id?: number; sku_id?: number; legacy_only?: boolean }) {
  return request<{ batches: InventoryBatch[] }>({ url: "/api/v1/admin/inventory/imports", params });
}
export function previewCancelImport(id: number) {
  return request<{ batch: InventoryBatch; revision: string }>({ url: `/api/v1/admin/inventory/imports/${id}/preview` });
}
export function cancelImport(id: number, expectedRevision?: string) {
  return request({ url: `/api/v1/admin/inventory/imports/${id}/cancel`, method: "post", data: { expected_revision: expectedRevision } });
}
export function fetchCards(params: { product_id: number; sku_id?: number; status?: string; page?: number; page_size?: number }) {
  return request<{ cards: any[]; total: number }>({ url: "/api/v1/admin/inventory/cards", params });
}
export function toggleCard(id: number, enable: boolean) {
  return request({ url: `/api/v1/admin/inventory/cards/${id}/toggle`, method: "put", data: { enable } });
}
export function exportCards(productId: number, skuId?: number) {
  return request<{ lines: string[] }>({ url: "/api/v1/admin/inventory/export", method: "post", data: { product_id: productId, sku_id: skuId } });
}
export function fetchPremiumCards(productId: number, skuId?: number) {
  return request<{ cards: any[]; total: number }>({ url: "/api/v1/admin/inventory/cards/premium", params: { product_id: productId, sku_id: skuId } });
}
export function previewAssignCards(data: { product_id: number; sku_id: number; card_ids?: number[]; import_id?: number }) {
  return request<InventoryAssignPreview>({ url: "/api/v1/admin/inventory/assign-preview", method: "post", data });
}
export function assignCards(data: { product_id: number; sku_id: number; card_ids: number[]; expected_revision: number; request_key: string }) {
  return request<{ assigned: number }>({ url: "/api/v1/admin/inventory/assign", method: "post", data });
}
