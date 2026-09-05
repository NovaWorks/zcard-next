import { request } from "../request";

// ── 素材库（：上传 base64 JSON；ref_count 引用计数；删除被引用需 confirm）──

export interface MediaItem {
  id: number;
  category_id?: number;
  name: string;
  url: string;
  mime: string;
  size: number;
  width: number;
  height: number;
  ref_count: number;
  created_at: number;
}

export interface MediaCategory {
  id: number;
  parent_id: number;
  name: string;
  sort: number;
}

// ── 分类 ──

export function fetchMediaCategories() {
  return request<{ categories: MediaCategory[] }>({
    url: "/api/v1/admin/media/categories",
  });
}

export function createMediaCategory(data: { name: string; parent_id?: number; sort?: number }) {
  return request<MediaCategory>({
    url: "/api/v1/admin/media/categories",
    method: "post",
    data,
  });
}

export function renameMediaCategory(id: number, name: string) {
  return request<MediaCategory>({
    url: `/api/v1/admin/media/categories/${id}/rename`,
    method: "post",
    data: { name },
  });
}

// 移动分类到别的父分类（parent_id 0 = 移到根；服务端防环校验）
export function moveMediaCategory(id: number, parentId: number) {
  return request({
    url: `/api/v1/admin/media/categories/${id}/move`,
    method: "post",
    data: { parent_id: parentId },
  });
}

export function deleteMediaCategory(id: number) {
  return request({ url: `/api/v1/admin/media/categories/${id}`, method: "delete" });
}

// ── 素材 ──

export function uploadMedia(data: {
  name: string;
  data_base64: string;
  content_type?: string;
  category_id?: number;
}) {
  return request<MediaItem>({
    url: "/api/v1/admin/media/upload",
    method: "post",
    data,
  });
}

export function importMediaFromURL(data: { url: string; category_id?: number }) {
  return request<MediaItem>({
    url: "/api/v1/admin/media/import",
    method: "post",
    data,
  });
}

export function fetchMediaList(params?: {
  category_id?: number;
  uncategorized?: boolean;
  keyword?: string;
  page?: number;
  page_size?: number;
}) {
  return request<{ items: MediaItem[]; total: number }>({
    url: "/api/v1/admin/media",
    method: "get",
    params,
  });
}

export function renameMedia(id: number, name: string) {
  return request<MediaItem>({
    url: `/api/v1/admin/media/${id}/rename`,
    method: "post",
    data: { name },
  });
}

// 批量移动（category_id 0 = 移到未分类）
export function moveMedia(ids: number[], categoryId: number) {
  return request({
    url: "/api/v1/admin/media/move",
    method: "post",
    data: { ids, category_id: categoryId },
  });
}

// 批量删除：被引用且未 confirm → 200 + need_confirm + 引用清单（前端二次确认）
export function deleteMedia(ids: number[], confirm = false) {
  return request<{ deleted: number; referenced: MediaItem[]; need_confirm: boolean }>({
    url: "/api/v1/admin/media/delete",
    method: "post",
    data: { ids, confirm },
  });
}
