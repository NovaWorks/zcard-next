import { request } from '../request';

// ── 商品管理 ──

export function fetchProducts(params?: {
  category_id?: number;
  keyword?: string;
  status?: number;
  page?: number;
  page_size?: number;
}) {
  return request({
    url: '/api/v1/admin/products',
    method: 'get',
    params
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
  price_cents: number;
  factory_price_cents?: number;
  stock_type: string;
  delivery_mode?: string;
  stock_visible?: boolean;
  status?: number;
}) {
  return request({
    url: '/api/v1/admin/products',
    method: 'post',
    data
  });
}

export function updateProduct(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/products/${id}`,
    method: 'put',
    data
  });
}

export function deleteProduct(id: number) {
  return request({
    url: `/api/v1/admin/products/${id}`,
    method: 'delete'
  });
}

// ── 分类管理 ──

export function fetchCategories() {
  return request({ url: '/api/v1/admin/categories' });
}

export function createCategory(data: { name: string; parent_id?: number; icon?: string; sort?: number }) {
  return request({
    url: '/api/v1/admin/categories',
    method: 'post',
    data
  });
}

export function updateCategory(id: number, data: Record<string, any>) {
  return request({
    url: `/api/v1/admin/categories/${id}`,
    method: 'put',
    data
  });
}

export function deleteCategory(id: number) {
  return request({
    url: `/api/v1/admin/categories/${id}`,
    method: 'delete'
  });
}
