// 营销域 API：会员等级 + 优惠券/秒杀/促销 + 内容管理（横幅/文章）。

import { request } from "../request";

// ── 会员等级（memberlevel:read / write / delete）──

export function fetchMemberLevels() {
  return request({ url: "/api/v1/admin/member-levels" });
}

export function createMemberLevel(data: {
  name: string;
  threshold_type: string;
  threshold_recharge?: number;
  threshold_consume?: number;
  discount?: number;
  sort?: number;
  enabled?: boolean;
  points_rule_json?: string;
}) {
  return request({ url: "/api/v1/admin/member-levels", method: "post", data });
}

export function updateMemberLevel(id: number, data: { name?: string; discount?: number; sort?: number; enabled?: boolean; points_rule_json?: string }) {
  return request({ url: `/api/v1/admin/member-levels/${id}`, method: "put", data });
}

export function deleteMemberLevel(id: number) {
  return request({ url: `/api/v1/admin/member-levels/${id}`, method: "delete" });
}

// ── 优惠券（coupon:read / write）──

export function fetchCoupons(status?: string, batchId?: string, page = 1, pageSize = 20) {
  const params: Record<string, unknown> = { page, page_size: pageSize };
  if (status) params.status = status;
  if (batchId) params.batch_id = batchId;
  return request({ url: "/api/v1/admin/coupons", params });
}

export function createCouponBatch(data: { name: string; type: string; value: number; count: number; expire_at?: number }) {
  return request({ url: "/api/v1/admin/coupons/batch", method: "post", data });
}

export function disableCoupon(batchId: string) {
  return request({ url: "/api/v1/admin/coupons/disable", method: "post", data: { batch_id: batchId } });
}

export function grantCoupon(batchId: string, userId: number, count: number) {
  return request({ url: "/api/v1/admin/coupons/grant", method: "post", data: { batch_id: batchId, user_id: userId, count } });
}

/** 批量删除券（ids 或整批未使用；服务端仅删未使用，已使用/已作废跳过） */
export function deleteCoupons(ids: number[], batchId?: string) {
  const data: Record<string, unknown> = { ids };
  if (batchId) data.batch_id = batchId;
  return request({ url: "/api/v1/admin/coupons/delete", method: "post", data });
}

/** 导出券码 CSV（按当前筛选：状态 + 批次），返回 { filename, csv } */
export function exportCoupons(status?: string, batchId?: string) {
  const params: Record<string, unknown> = {};
  if (status) params.status = status;
  if (batchId) params.batch_id = batchId;
  return request({ url: "/api/v1/admin/coupons/export", params });
}

// ── 秒杀 / 促销 ──

export function fetchFlashSales(page = 1, pageSize = 20) {
  return request({ url: "/api/v1/admin/flash-sales", params: { page, page_size: pageSize } });
}

export function createFlashSale(data: { product_id: number; sku_id?: number; flash_price: number; start_at: number; end_at: number; limit_qty: number; per_user_limit?: number }) {
  return request({ url: "/api/v1/admin/flash-sales", method: "post", data });
}

export function deleteFlashSale(id: number) {
  return request({ url: `/api/v1/admin/flash-sales/${id}`, method: "delete" });
}

export function fetchPromotions() {
  return request({ url: "/api/v1/admin/promotions" });
}

export function upsertPromotion(data: { id?: number; name: string; scope_json?: string; type: string; threshold?: number; discount?: number; special_price?: number; start_at: number; end_at: number; enabled?: boolean }) {
  return request({ url: "/api/v1/admin/promotions", method: "post", data });
}

// ── 内容管理（content:read / write）：横幅 + 文章 + 文章栏目 ──

export function fetchBanners(params?: { position?: string; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/content/banners", params });
}

export function createBanner(data: { name: string; position: string; title_json?: string; image: string; mobile_image?: string; link_type?: string; link_value?: string; is_active?: boolean; sort?: number }) {
  return request({ url: "/api/v1/admin/content/banners", method: "post", data });
}

export function updateBanner(id: number, data: Record<string, any>) {
  return request({ url: `/api/v1/admin/content/banners/${id}`, method: "put", data });
}

export function deleteBanner(id: number) {
  return request({ url: `/api/v1/admin/content/banners/${id}`, method: "delete" });
}

export function fetchPosts(params?: { page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/content/posts", params });
}

export function createPost(data: { slug: string; type: string; title_json: string; summary_json?: string; content_json: string; category_id?: number; is_published?: boolean }) {
  return request({ url: "/api/v1/admin/content/posts", method: "post", data });
}

export function updatePost(id: number, data: { title_json?: string; summary_json?: string; content_json?: string; thumbnail?: string; category_id?: number }) {
  return request({ url: `/api/v1/admin/content/posts/${id}`, method: "put", data });
}

export function publishPost(id: number, publish: boolean) {
  return request({ url: `/api/v1/admin/content/posts/${id}/publish`, method: "post", data: { publish } });
}

export function deletePost(id: number) {
  return request({ url: `/api/v1/admin/content/posts/${id}`, method: "delete" });
}

// ── 文章栏目（content:read / write）──

export function fetchPostCategories() {
  return request({ url: "/api/v1/admin/content/categories" });
}

export function createPostCategory(data: { name: string; slug: string; sort?: number }) {
  return request({ url: "/api/v1/admin/content/categories", method: "post", data });
}

export function updatePostCategory(id: number, data: { name?: string; sort?: number }) {
  return request({ url: `/api/v1/admin/content/categories/${id}`, method: "put", data });
}

export function deletePostCategory(id: number) {
  return request({ url: `/api/v1/admin/content/categories/${id}`, method: "delete" });
}
