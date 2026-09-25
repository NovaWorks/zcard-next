import { request } from "../request";

// ── 商品管理 ──

export function fetchProducts(params?: {
  category_id?: number;
  keyword?: string;
  status?: number;
  page?: number;
  page_size?: number;
  options_only?: boolean;
  is_locked?: boolean;
  low_stock_only?: boolean;
  out_of_stock_only?: boolean;
  upstream_source_id?: number;
  local_only?: boolean;
  stock_type?: "card" | "url" | "code";
}) {
  return request({
    url: "/api/v1/admin/products",
    method: "get",
    params,
  });
}

export function fetchProduct(id: number) {
  return request({ url: `/api/v1/admin/products/${id}` });
}

export function createProduct(data: {
  name: string;
  category_id?: number;
  description?: string;
  cover?: string;
  images?: string[];
  price_cents: number;
  factory_price_cents?: number;
  stock_type: string; fulfillment_mode?:string; manual_stock?:number;
  direct_content?: string;
  delivery_mode?: string;
  stock_visible?: boolean;
  dedup?: boolean;
  sort?: number;
  status?: number;
  points_required?: number;
}) {
  return request({
    url: "/api/v1/admin/products",
    method: "post",
    data,
  });
}

export function updateProduct(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/products/${id}`,
    method: "put",
    data,
  });
}

export interface ProductDeletePreview {
  name: string;
  order_count: number | string;
  delete_block_reason?: string;
  delete_orders_block_reason?: string;
}

export function previewDeleteProduct(id: number) {
  return request<ProductDeletePreview>({ url: `/api/v1/admin/products/${id}/delete-preview` });
}

export function deleteProduct(id: number, params?: { delete_orders: boolean; confirm_name: string; expected_order_count: number }) {
  return request({
    url: `/api/v1/admin/products/${id}`,
    method: "delete",
    params,
  });
}

// 批量上下架（列表多选；status 1=上架 0=下架 2=隐藏）
export function batchUpdateProductStatus(ids: number[], status: number) {
  return request<{ updated: number; skipped_locked?: number }>({
    url: "/api/v1/admin/products/batch-status",
    method: "post",
    data: { ids, status },
  });
}

export function batchUpdateProductCategory(ids: number[], categoryId: number) {
  return request<{ updated: number; skipped_locked?: number }>({
    url: "/api/v1/admin/products/batch-category",
    method: "post",
    data: { ids, category_id: categoryId },
  });
}

// ── 分类管理 ──

export function fetchCategories() {
  return request({ url: "/api/v1/admin/categories" });
}

export function createCategory(data: {
  name: string;
  parent_id?: number;
  icon?: string;
  sort?: number;
}) {
  return request({
    url: "/api/v1/admin/categories",
    method: "post",
    data,
  });
}

export function updateCategory(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/categories/${id}`,
    method: "put",
    data,
  });
}

export function deleteCategory(id: number) {
  return request({
    url: `/api/v1/admin/categories/${id}`,
    method: "delete",
  });
}

// 分类排序（拖拽重排：把 parent_id 层级下全部兄弟按 ids 顺序重排并归一化 sort）
export function reorderCategories(parent_id: number, ids: number[]) {
  return request({
    url: "/api/v1/admin/categories/reorder",
    method: "post",
    data: { parent_id, ids },
  });
}

// ── SKU 多规格（ ；price_cents 0=继承商品价）──

export function fetchSkus(productId: number) {
  return request<{ skus: any[] }>({ url: `/api/v1/admin/products/${productId}/skus` });
}

export function createSku(
  productId: number,
  data: {
    name: string;
    spec_values?: Record<string, string>;
    price_cents?: number;
    cost_cents?: number;
    stock_offset?: number;
  },
) {
  return request({ url: `/api/v1/admin/products/${productId}/skus`, method: "post", data });
}

export function updateSku(id: number, data: Record<string, any>) {
  return request({ url: `/api/v1/admin/skus/${id}`, method: "put", data });
}

export function deleteSku(id: number) {
  return request({ url: `/api/v1/admin/skus/${id}`, method: "delete" });
}

// ── 自定义控件（下单收集：text|password|select|number|checkbox|radio）──

export function fetchControls(productId: number) {
  return request<{ controls: any[] }>({ url: `/api/v1/admin/products/${productId}/controls` });
}

export function createControl(
  productId: number,
  data: {
    name: string;
    type: string;
    required?: boolean;
    options?: string[];
    sort?: number;
  },
) {
  return request({ url: `/api/v1/admin/products/${productId}/controls`, method: "post", data });
}

export function updateControl(id: number, data: Record<string, any>) {
  return request({ url: `/api/v1/admin/controls/${id}`, method: "put", data });
}

export function deleteControl(id: number) {
  return request({ url: `/api/v1/admin/controls/${id}`, method: "delete" });
}

// ── 标签 ──

export function fetchTags() {
  return request({ url: "/api/v1/admin/tags" });
}

export function createTag(data: {
  name: string;
  slug: string;
  icon?: string;
  color?: string;
  position?: string;
}) {
  return request({ url: "/api/v1/admin/tags", method: "post", data });
}

export function deleteTag(id: number) {
  return request({ url: `/api/v1/admin/tags/${id}`, method: "delete" });
}

// ── 货源连接（跨域只读：商品列表展示「自营/代发 + 供应商名」）──

// 旧只读别名（商品页"自营/代发"展示用）；完整连接管理见 supply.ts。
export function fetchSupplyConnectionOptions() {
  return request<{ connections: { id: number; name: string; driver: string; status: string }[] }>({
    url: "/api/v1/admin/supply/connections",
  });
}

// ── 评价管理（catalog:review_read / review_manage）──

export function fetchReviews(params: { status?: string; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/reviews", params });
}

export function approveReview(id: number) {
  return request({ url: `/api/v1/admin/reviews/${id}/approve`, method: "post", data: {} });
}

export function rejectReview(id: number) {
  return request({ url: `/api/v1/admin/reviews/${id}/reject`, method: "post", data: {} });
}

export function createVirtualReview(data: { product_id: number; nickname?: string; content: string; rating?: number; sort?: number }) {
  return request({ url: "/api/v1/admin/virtual-reviews", method: "post", data });
}

export function mergeCategories(data: { source_ids: number[]; target_id: number; preview: boolean }) {
  return request<{ categories: number; products: number; children: number }>({ url: "/api/v1/admin/categories/merge", method: "post", data });
}

export interface ProductContentPatch {
  cover_action: number;
  cover: string;
  images_action: number;
  images: string[];
  description_action: number;
  description: string;
}
export interface BatchContentTarget {
  id: number; name: string; cover: string; images: string[]; description: string;
  upstream: boolean; changed: boolean; content_changed: boolean; protection_changed: boolean;
}
export interface BatchContentPreview {
  request_id: string; expires_at: number; matched: number; changed: number; unchanged: number;
  skipped_locked?: number; upstream_count: number; content_changed: number; protection_changed: number;
  patch: ProductContentPatch; targets: BatchContentTarget[];
}
export interface BatchContentResult { skipped_locked?: number; completed: boolean; matched: number; changed: number; unchanged: number }
export async function previewBatchProductContent(data: { ids?: number[]; category_id?: number; include_descendants?: boolean; patch: ProductContentPatch }) {
  const response = await request<BatchContentPreview>({ url: "/api/v1/admin/products/batch-content/preview", method: "post", data });
  if (response.data) {
    const value = response.data;
    response.data = { ...value, changed: value.changed || 0, unchanged: value.unchanged || 0, upstream_count: value.upstream_count || 0, content_changed: value.content_changed || 0, protection_changed: value.protection_changed || 0,
      patch: Object.assign({ cover_action: 0, cover: "", images_action: 0, images: [], description_action: 0, description: "" }, value.patch),
      targets: (value.targets || []).map(row => Object.assign({ cover: "", images: [], description: "", upstream: false, changed: false, content_changed: false, protection_changed: false }, row)) };
  }
  return response;
}
export async function batchUpdateProductContent(request_id: string) {
  const response = await request<BatchContentResult>({ url: "/api/v1/admin/products/batch-content", method: "post", data: { request_id } });
  if (response.data) response.data = Object.assign({ completed: false, matched: 0, changed: 0, unchanged: 0 }, response.data);
  return response;
}
export async function getBatchProductContentResult(requestId: string) {
  const response = await request<BatchContentResult>({ url: `/api/v1/admin/products/batch-content/results/${encodeURIComponent(requestId)}` });
  if (response.data) response.data = Object.assign({ completed: false, matched: 0, changed: 0, unchanged: 0 }, response.data);
  return response;
}

export interface PlacementProduct {
  id: number; name: string; cover?: string; category_id?: number; price_cents?: number;
  is_locked?: boolean; lock_version?: number; status?: number; upstream_source_id?: number;
}
export interface CategoryPlacement {
  product_id: number; is_pinned: boolean; is_recommended: boolean; position: number;
}
export interface CategoryPlacementRow { placement: CategoryPlacement; product: PlacementProduct; unavailable_reason?: string }
export interface CategoryPlacementsReply { category_id: number; version: number; items: CategoryPlacementRow[] }
export function setProductLock(id: number, isLocked: boolean, expectedVersion: number) {
  return request<PlacementProduct>({ url: `/api/v1/admin/products/${id}/lock`, method: 'put', data: { is_locked: isLocked, expected_version: expectedVersion } });
}
export function fetchCategoryPlacements(categoryId: number) {
  return request<CategoryPlacementsReply>({ url: `/api/v1/admin/categories/${categoryId}/placements` });
}
export function saveCategoryPlacements(categoryId: number, version: number, items: CategoryPlacement[]) {
  return request<CategoryPlacementsReply>({ url: `/api/v1/admin/categories/${categoryId}/placements`, method: 'put', data: { expected_version: version, items } });
}

export function fetchDeliverySources(productId: number) {
  return request<any>({url:`/api/v1/admin/products/${productId}/delivery-sources`,method:'get'});
}
export function setDeliverySource(productId: number, data: Record<string, unknown>) {
  return request<any>({url:`/api/v1/admin/products/${productId}/delivery-sources`,method:'post',data});
}

export function classifyProducts(data: Record<string, unknown>) { return request<any>({url:'/api/v1/admin/products/classify',method:'post',data}); }
