import { api } from './client';

// ── 类型（字段名 snake_case，与 proto 一致）──

export interface ProductControl {
  id: number;
  name: string;
  type: string;
  required: boolean;
  options: string[];
  sort: number;
}

export interface ReviewItem {
  id: number;
  nickname: string;
  content: string;
  rating: number;
  created_at: number;
  is_virtual: boolean;
}

export interface Sku {
  id: number;
  name: string;
  price_cents: number;
}

export interface Product {
  id: number;
  name: string;
  slug: string;
  description: string;
  cover: string;
  price_cents: number;
  stock_type: string;
  stock: number;
  stock_visible: boolean;
  category_id: number;
  sales_count: number;
  controls: ProductControl[];
  reviews: ReviewItem[];
  skus: Sku[];
}

export interface PageResp {
  total: number;
  page: number;
  page_size: number;
}

export interface ListProductsReply {
  items: Product[];
  page: PageResp;
}

export interface CreateOrderReply {
  order_no: string;
  total_cents: number;
  expires_at: number;
}

export interface CreatePaymentReply {
  payment_id: number;
  type: string;
  payload: string;
}

export interface DeliveryItem {
  item_id: number;
  content: string;
  masked: boolean;
}

export interface FetchDeliveryReply {
  order_no: string;
  status: string;
  items: DeliveryItem[];
  fetch_count: number;
}

export interface BalanceReply {
  available_cents: number;
  locked_cents: number;
  total_cents: number;
  points: number;
}

// ── API 函数 ──

export function listProducts(params?: { keyword?: string; page?: number; page_size?: number }) {
  return api.get<ListProductsReply>('/products', {
    keyword: params?.keyword,
    'page.page': params?.page,
    'page.page_size': params?.page_size
  });
}

export function getProduct(id: number) {
  return api.get<Product>(`/products/${id}`);
}

export function createOrder(body: {
  items: { product_id: number; sku_id?: number; quantity: number }[];
  guest_contact?: string;
  query_password?: string;
  contact?: string;
  coupon_code?: string;
  control_answers?: Record<string, string>;
}) {
  return api.post<CreateOrderReply>('/orders', body);
}

export function createPayment(order_no: string, channel: string) {
  return api.post<CreatePaymentReply>('/payments', { order_no, channel });
}

export function fetchDelivery(order_no: string, query_password: string) {
  return api.post<FetchDeliveryReply>('/delivery/fetch', { order_no, query_password });
}

export function getBalance() {
  return api.get<BalanceReply>('/wallet');
}
