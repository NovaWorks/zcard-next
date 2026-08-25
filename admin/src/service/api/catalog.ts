import { request } from "../request";

// ── 商品管理 ──

export function fetchProducts(params?: {
  category_id?: number;
  keyword?: string;
  status?: number;
  page?: number;
  page_size?: number;
  low_stock_only?: boolean;
  upstream_source_id?: number;
  local_only?: boolean;
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
  stock_type: string;
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

export function deleteProduct(id: number) {
  return request({
    url: `/api/v1/admin/products/${id}`,
    method: "delete",
  });
}

// 批量上下架（列表多选；status 1=上架 0=下架 2=隐藏）
export function batchUpdateProductStatus(ids: number[], status: number) {
  return request<{ updated: number }>({
    url: "/api/v1/admin/products/batch-status",
    method: "post",
    data: { ids, status },
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

// ── SKU 多规格（P1-01 M1b；price_cents 0=继承商品价）──

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
