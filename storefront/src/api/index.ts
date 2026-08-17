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
  points_required?: number; // 积分商城视图（points_only 时 >0；P3-01）
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

export function listProducts(params?: { keyword?: string; page?: number; page_size?: number; points_only?: boolean }) {
  return api.get<ListProductsReply>('/products', {
    keyword: params?.keyword,
    points_only: params?.points_only,
    page: params?.page,
    page_size: params?.page_size
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
  use_points?: boolean; // 积分兑换（P3-01：全积分商品直落 paid）
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

// ── 用户体系（P3-04：注册即登录 / 登录 / 我的信息）──

export interface RegisterReply {
  user_id: number;
  token: string;
  expires_at: number;
}

export interface LoginReply {
  access_token: string;
  token_type: string;
  expires_at: number;
}

export interface MeReply {
  user_id: number;
  username: string;
  email: string;
  created_at: number;
  reseller_profile_id?: number;
}

export function register(body: { username: string; password: string; email?: string; invite_code?: string }) {
  return api.post<RegisterReply>('/user/register', body);
}

export function login(body: { username: string; password: string }) {
  return api.post<LoginReply>('/user/login', body);
}

export function me() {
  return api.get<MeReply>('/user/me');
}

// ── 用户自服务（P3-10：找回/改密/改资料）──

export function forgotPassword(email: string) {
  // 防枚举：后端对任何输入都成功（仅真实邮箱收码）
  return api.post<null>('/user/password/forgot', { email });
}

export function resetPassword(body: { email: string; code: string; new_password: string }) {
  return api.post<{ token: string; expires_at: number }>('/user/password/reset', body); // 重置即登录
}

export function changePassword(body: { old_password: string; new_password: string }) {
  return api.post<{ token: string; expires_at: number }>('/user/password/change', body);
}

export function updateProfile(body: { email: string }) {
  return api.post<MeReply>('/user/profile', body);
}

// ── 我的订单（P1-03 M1b 补全）──

export interface MyOrderItem {
  order_no: string;
  status: string;
  total_cents: number;
  created_at: number;
  expired_at: number;
  item_count: number;
}

export function listMyOrders(page = 1, pageSize = 10) {
  return api.get<{ orders: MyOrderItem[]; total: number }>('/my-orders', { page, page_size: pageSize });
}

export function cancelMyOrder(orderNo: string) {
  return api.post<null>(`/orders/${orderNo}/cancel`, {});
}

// ── 钱包：流水/充值/礼品卡/提现（P1-05 M2/M3）──

export interface WalletTransaction {
  id: number;
  direction: string; // in | out
  type: string;
  amount_cents: number;
  balance_after_cents: number;
  reference: string;
  remark: string;
  created_at: number;
}

export function listTransactions(page = 1, pageSize = 15) {
  return api.get<{ transactions: WalletTransaction[]; total: number }>('/wallet/transactions', { page, page_size: pageSize });
}

export interface CreateRechargeReply {
  recharge_id: number;
  payment_id: number;
  type: string; // redirect | qrcode | params（同支付管线）
  payload: string;
}

export function createRecharge(amountCents: number, channel: string) {
  return api.post<CreateRechargeReply>('/wallet/recharge', { amount_cents: amountCents, channel });
}

export function redeemGiftcard(code: string) {
  return api.post<{ amount_cents: number; balance_after_cents: number }>('/wallet/giftcards/redeem', { code });
}

export function createWithdrawal(body: { amount_cents: number; method_type: string; account: string }) {
  return api.post<{ withdrawal_id: number; amount_cents: number; fee_cents: number; credited_cents: number }>(
    '/wallet/withdrawals',
    body
  );
}

// ── 等级与积分（P3-01）──

export interface LevelBrief {
  id: number;
  name: string;
  discount: number;
  threshold_type: string;
  threshold_recharge: number;
  threshold_consume: number;
  points_rule_json: string;
}

export interface MyLevelReply {
  recharged_cents: number;
  consumed_cents: number;
  points: number;
  current?: LevelBrief;
  next?: LevelBrief;
  progress?: { recharge_gap_cents: number; consume_gap_cents: number; percent: number };
}

export function getMyLevel() {
  return api.get<MyLevelReply>('/member-level');
}

// ── 工单（P3-05）──

export interface TicketItem {
  id: number;
  ticket_no: string;
  type: string; // presale | aftersale
  priority: string; // low | normal | high | urgent_paid
  status: string; // open | processing | resolved | closed
  order_id: number;
  product_id: number;
  first_reply_at: number;
  satisfaction: number;
  created_at: number;
}

export interface TicketMessage {
  id: number;
  sender_type: string; // user | admin | system（内部备注后端已过滤）
  content: string;
  created_at: number;
}

export function createTicket(body: { type: string; content: string; guest_contact?: string; order_id?: number; product_id?: number }) {
  return api.post<{ ticket_no: string }>('/tickets', body);
}

export function listMyTickets(page = 1, pageSize = 10) {
  return api.get<{ tickets: TicketItem[]; total: number }>('/tickets', { page, page_size: pageSize });
}

export function getTicket(ticketNo: string) {
  return api.get<{ ticket: TicketItem; messages: TicketMessage[] }>(`/tickets/${ticketNo}`);
}

export function replyTicket(ticketNo: string, content: string) {
  return api.post<null>(`/tickets/${ticketNo}/reply`, { content });
}

export function rateTicket(ticketNo: string, satisfaction: number) {
  return api.post<null>(`/tickets/${ticketNo}/rate`, { satisfaction });
}

export function payUrgent(ticketNo: string) {
  return api.post<{ paid: boolean; fee_cents: number; error?: string }>(`/tickets/${ticketNo}/urgent`, {});
}

// ── 优惠券/秒杀（P3-02）──

export interface MyCoupon {
  id: number;
  name: string;
  type: string; // fixed | percent
  value: number; // fixed=分；percent=万分比
  code: string;
  scope_json: string;
  expire_at: number;
}

export function listMyCoupons() {
  return api.get<{ coupons: MyCoupon[] }>('/coupons/mine');
}

export function redeemCoupon(code: string) {
  return api.post<{ id: number }>('/coupons/redeem', { code });
}

export interface FlashSale {
  id: number;
  product_id: number;
  flash_price: number;
  remaining: number;
  start_at: number;
  end_at: number;
}

export function listFlashSales(upcoming = false) {
  return api.get<{ flash_sales: FlashSale[] }>('/flash-sales', { upcoming });
}

// ── 分销（P3-03）──

export interface MyAffiliateReply {
  user_id: number; // 推广码 = user_id
  invite_url: string;
  pending_cents: number;
  available_cents: number;
  withdrawn_cents: number;
  total_cents: number;
  debt_cents: number;
  team_l1: number;
  team_l2: number;
  team_l3: number;
}

export interface TeamMember {
  user_id: number;
  username_masked: string;
  tier: number;
  joined_at: number;
}

export interface CommissionItem {
  id: number;
  order_id: number;
  tier: number;
  base_amount: number;
  amount: number; // 负数 = 负债行
  status: string; // pending_confirm | available | withdrawn | reversed
  available_at: number;
  created_at: number;
}

export function myAffiliate() {
  return api.get<MyAffiliateReply>('/affiliate/me');
}

export function listTeam(tier?: number, page = 1, pageSize = 15) {
  return api.get<{ members: TeamMember[]; total: number }>('/affiliate/team', { tier, page, page_size: pageSize });
}

export function listCommissions(page = 1, pageSize = 15) {
  return api.get<{ commissions: CommissionItem[]; total: number }>('/affiliate/commissions', { page, page_size: pageSize });
}

// ── 内容（P2-04）──

export interface Banner {
  id: number;
  title: string;
  image: string;
  mobile_image: string;
  link_type: string;
  link_value: string;
}

export interface StorePost {
  id: number;
  slug: string;
  type: string;
  title: string;
  summary?: string;
  thumbnail?: string;
  category_id?: number;
  published_at?: number;
}

export function listBanners(position?: string, locale?: string) {
  return api.get<{ banners: Banner[] }>('/banners', { position, locale });
}

export function listPosts(type?: string, page = 1, pageSize = 20, locale?: string) {
  return api.get<{ posts: StorePost[]; total: number; page: number; page_size: number }>('/posts', { type, page, page_size: pageSize, locale });
}

export function getPost(slug: string, locale?: string) {
  return api.get<{ post: StorePost; content: string }>(`/posts/${slug}`, { locale });
}
