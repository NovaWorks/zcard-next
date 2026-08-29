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
  total: number;   // 平铺（与后端 proto 一致）
  page: number;
  page_size: number;
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

export function listProducts(params?: {
  keyword?: string;
  page?: number;
  page_size?: number;
  points_only?: boolean;
  category_id?: number;
  sort?: string;
}) {
  return api.get<ListProductsReply>('/products', {
    keyword: params?.keyword,
    points_only: params?.points_only,
    page: params?.page,
    page_size: params?.page_size,
    category_id: params?.category_id,
    sort: params?.sort,
  });
}

export interface CategoryItem {
  id: number;
  name: string;
  icon: string;
  parent_id: number; // 0=根；多级分类树形
}

export function listCategories() {
  return api.get<{ categories: CategoryItem[] }>('/categories');
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
  ref_code?: string;    // 推广归因码（游客/无链用户下单实时归因）
  captcha_id?: string;  // 图形验证码（captcha_order 开启时游客必填）
  captcha_code?: string;
}) {
  return api.post<CreateOrderReply>('/orders', body);
}

export interface MethodItem {
  code: string;
  name: string;
  icon?: string;
}

export interface ChannelItem {
  code: string;
  name: string;
  driver: string;
  icon?: string;
  methods?: MethodItem[];
}

export function fetchPaymentChannels() {
  return api.get<{ channels: ChannelItem[] }>('/payment/channels');
}

export function createPayment(order_no: string, channel: string, method?: string) {
  return api.post<CreatePaymentReply>('/payments', { order_no, channel, method: method || '' });
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
  promo_code?: string; // 推广码（懒生成）
}

export function register(body: { username: string; password: string; email?: string; phone?: string; code?: string; invite_code?: string; captcha_id?: string; captcha_code?: string }) {
  return api.post<RegisterReply & { promo_code?: string }>('/user/register', body);
}

/** 发送注册验证码（channel：email|phone——按 security.register_method） */
export function sendRegisterCode(target: string, channel: 'email' | 'phone', captcha?: { captcha_id: string; captcha_code: string }) {
  return api.post<null>('/user/register/send-code', { target, channel, ...captcha });
}

// ── 图形验证码（settings.security.captcha_* 场景开关）──

/** 获取图形验证码（4 位数字；一次性消费） */
export function fetchCaptcha() {
  return api.get<{ captcha_id: string; image_base64: string }>('/captcha/image');
}

/** 四场景开关（config 公开下发） */
export interface CaptchaConfig {
  login: boolean;
  register: boolean;
  order: boolean;
  reset: boolean;
}
export async function fetchCaptchaConfig(): Promise<CaptchaConfig> {
  const def: CaptchaConfig = { login: false, register: true, order: false, reset: true };
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    const parse = (k: string, dflt: boolean): boolean => {
      const raw = find(k);
      if (raw === undefined) return dflt;
      try { return JSON.parse(raw) === true; } catch { return dflt; }
    };
    return {
      login: parse('security.captcha_login', false),
      register: parse('security.captcha_register', true),
      order: parse('security.captcha_order', false),
      reset: parse('security.captcha_reset', true),
    };
  } catch {
    return def;
  }
}

/** 注册设置（config 公开下发：security.register_enabled / register_method 多选数组） */
export interface RegisterConfig {
  enabled: boolean;
  methods: string[]; // username | email | phone（勾选通道全部可用）
}
export async function fetchRegisterConfig(): Promise<RegisterConfig> {
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    let enabled = true;
    let methods: string[] = [];
    const e = find('security.register_enabled');
    if (e !== undefined) { try { enabled = JSON.parse(e) !== false; } catch { /* 默认 */ } }
    const m = find('security.register_method');
    if (m) {
      try {
        const v = JSON.parse(m);
        if (Array.isArray(v)) methods = v.filter((x: unknown): x is string => typeof x === 'string');
        else if (typeof v === 'string' && v) methods = [v]; // 旧单值兼容
      } catch { /* 默认 */ }
    }
    if (!methods.length) methods = ['username'];
    return { enabled, methods };
  } catch {
    return { enabled: true, methods: ['username'] };
  }
}

export function login(body: { username: string; password: string; captcha_id?: string; captcha_code?: string }) {
  return api.post<LoginReply>('/user/login', body);
}

export function me() {
  return api.get<MeReply>('/user/me');
}

// ── 用户自服务（P3-10：找回/改密/改资料）──

export function forgotPassword(email: string, captcha?: { captcha_id: string; captcha_code: string }) {
  // 防枚举：后端对任何输入都成功（仅真实邮箱收码）
  return api.post<null>('/user/password/forgot', { email, ...captcha });
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

// ── 购物车（P1-03b：CRUD；结算复用 createOrder 多商品一单）──

export interface CartItem {
  id: number;
  product_id: number;
  sku_id: number;
  quantity: number;
  product_name: string;
  price_cents: number;
  stock: number;
  points_only: boolean;
  points_required: number;
  valid: boolean;
  added_at: number;
  product_cover?: string; // 商品封面（无图时前端显示默认占位）
}

export function addCart(product_id: number, quantity = 1, sku_id = 0) {
  // silent：token 失效时游客可降级本地购物车（不触发跳登录）
  return api.postSilent<CartItem>('/cart/items', { product_id, quantity, sku_id });
}

export function listCart() {
  return api.getSilent<{ items: CartItem[]; total: number }>('/cart');
}

export function updateCart(id: number, quantity: number) {
  return api.postSilent<CartItem>(`/cart/items/${id}`, { id, quantity });
}

export function removeCart(id: number) {
  return api.deleteSilent(`/cart/items/${id}`);
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

// ── 游客订单查询（下单联系方式 + 密码取货闭环）──

export interface GuestOrderItem {
  order_no: string;
  status: string;
  total_cents: number;
  created_at: number;
  expires_at: number;
}

/** 游客按下单联系方式查订单列表（精简信息；卡密需逐单查询密码） */
export function listGuestOrders(contact: string) {
  return api.get<{ orders: GuestOrderItem[] }>('/guest-orders', { contact });
}

// ── 订单详情（GetOrder：登录态本人或查询密码）──

export interface OrderItemReply {
  product_id: number;
  product_name: string;
  quantity: number;
  unit_price_cents: number;
}

export interface OrderDetail {
  order_no: string;
  status: string;
  total_cents: number;
  items: OrderItemReply[];
  created_at: number;
  expires_at?: number;
}

export function getOrder(orderNo: string, queryPassword?: string) {
  return api.getSilent<OrderDetail>(`/orders/${orderNo}`, queryPassword ? { query_password: queryPassword } : undefined);
}

// 支付页/取货页共用：订单号 → 下单时设置的查询密码（sessionStorage 会话级记忆，
// 支付成功自动取货用；不落 localStorage 防持久化敏感信息）
export function rememberOrderPassword(orderNo: string, password?: string) {
  try {
    if (password) sessionStorage.setItem(`zc_pwd_${orderNo}`, password);
  } catch { /* 存储不可用忽略 */ }
}
export function getOrderPassword(orderNo: string): string {
  try {
    return sessionStorage.getItem(`zc_pwd_${orderNo}`) || '';
  } catch {
    return '';
  }
}

// ── 交易设置（config 下发 trade.query_password / trade.contact_required）──

export interface TradeConfig {
  queryPasswordRequired: boolean; // 下单必须设置查询密码
  contactRequired: string;        // none | phone | email | qq | any（游客必填联系方式）
}

/** 拉取交易设置（失败走保守默认：强制密码 + any） */
export async function fetchTradeConfig(): Promise<TradeConfig> {
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    let pwdRequired = true;
    let contactMode = 'any';
    const pwd = find('trade.query_password');
    if (pwd !== undefined) {
      try { pwdRequired = JSON.parse(pwd) !== false; } catch { /* 默认 */ }
    }
    const contact = find('trade.contact_required');
    if (contact) {
      try { const v = JSON.parse(contact); if (typeof v === 'string' && v) contactMode = v; } catch { /* 默认 */ }
    }
    return { queryPasswordRequired: pwdRequired, contactRequired: contactMode };
  } catch {
    return { queryPasswordRequired: true, contactRequired: 'any' };
  }
}

/** 联系方式要求显示名 */
export function contactRequiredLabel(mode: string): string {
  return ({ phone: '手机号', email: '邮箱', qq: 'QQ 号' } as Record<string, string>)[mode] || '邮箱 / 手机号 / QQ';
}

/** 联系方式格式校验（与后端 contactMatchesMode 同口径） */
export function contactValid(contact: string, mode: string): boolean {
  const s = contact.trim();
  if (!s) return false;
  const isEmail = /.+@.+\..+/.test(s);
  const isPhone = /^[+\d][\d\s-]{6,14}$/.test(s);
  const isQQ = /^\d{5,12}$/.test(s);
  switch (mode) {
    case 'phone': return isPhone;
    case 'email': return isEmail;
    case 'qq': return isQQ;
    default: return isEmail || isPhone || isQQ;
  }
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

export function createRecharge(amountCents: number, channel: string, method?: string) {
  return api.post<CreateRechargeReply>('/wallet/recharge', { amount_cents: amountCents, channel, method: method || '' });
}

export function redeemGiftcard(code: string) {
  return api.post<{ amount_cents: number; balance_after_cents: number }>('/wallet/giftcards/redeem', { code });
}

export function createWithdrawal(body: { amount_cents: number; method_type: string; account: string; qr_code_url?: string }) {
  return api.post<{ withdrawal_id: number; amount_cents: number; fee_cents: number; credited_cents: number }>(
    '/wallet/withdrawals',
    body
  );
}

// ── 提现记录 + 收款码上传（佣金提现闭环）──

export interface MyWithdrawalItem {
  withdrawal_id: number;
  amount_cents: number;
  fee_cents: number;
  method_type: string;
  method_name: string;
  account: string;
  status: string; // pending | approved | paid | rejected
  reject_reason: string;
  receipt: string; // 打款回执（流水号/备注）
  reviewed_at: number;
  paid_at: number;
  created_at: number;
}

export function listMyWithdrawals(page = 1, pageSize = 10) {
  return api.get<{ withdrawals: MyWithdrawalItem[]; total: number }>('/wallet/withdrawals/mine', { page, page_size: pageSize });
}

/** 收款码上传（登录态；图片 base64 → /uploads URL） */
export function uploadQrCode(dataBase64: string) {
  return api.post<{ url: string }>('/media/upload', { data_base64: dataBase64 });
}

/** 提现配置（config 公开下发：withdraw 组） */
export interface WithdrawConfig {
  enabled: boolean;
  minAmountCents: number;
  feeType: string;
  feeValue: number;
  methods: { type: string; name: string }[];
}
export async function fetchWithdrawConfig(): Promise<WithdrawConfig> {
  const def: WithdrawConfig = { enabled: false, minAmountCents: 1000, feeType: 'fixed', feeValue: 0, methods: [] };
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    const parse = <T,>(k: string, dflt: T): T => {
      const raw = find(k);
      if (raw === undefined) return dflt;
      try { return JSON.parse(raw) as T; } catch { return dflt; }
    };
    return {
      enabled: parse<boolean>('withdraw.enabled', false),
      minAmountCents: parse<number>('withdraw.min_amount', 1000),
      feeType: parse<string>('withdraw.fee_type', 'fixed'),
      feeValue: parse<number>('withdraw.fee_value', 0),
      methods: parse<{ type: string; name: string }[]>('withdraw.methods', []),
    };
  } catch {
    return def;
  }
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
  promo_code: string; // 推广码（8 位随机；后端懒生成）
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

export interface PostCategory {
  id: number;
  name: string;
  slug: string;
}

export function listBanners(position?: string, locale?: string) {
  return api.get<{ banners: Banner[] }>('/banners', { position, locale });
}

export function listPosts(type?: string, page = 1, pageSize = 20, locale?: string, categoryId = 0) {
  return api.get<{ posts: StorePost[]; total: number; page: number; page_size: number }>('/posts', {
    type,
    page,
    page_size: pageSize,
    locale,
    category_id: categoryId || undefined,
  });
}

export function getPost(slug: string, locale?: string) {
  return api.get<{ post: StorePost; content: string }>(`/posts/${slug}`, { locale });
}

export function listPostCategories(locale?: string) {
  return api.get<{ categories: PostCategory[] }>('/post-categories', { locale });
}

/** 公告设置（config 公开下发：ops.announcement_type + ops.announcement） */
export interface AnnouncementConfig {
  type: string;     // text | image | carousel
  text: string;     // text 类型内容
  images: string[]; // image/carousel 图片列表
}
export async function fetchAnnouncement(): Promise<AnnouncementConfig> {
  const def: AnnouncementConfig = { type: "text", text: "", images: [] };
  try {
    const resp = await fetch("/api/v1/storefront/config");
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    let type = "text";
    const typeRaw = find("ops.announcement_type");
    if (typeRaw !== undefined) {
      try {
        const v = JSON.parse(typeRaw);
        if (typeof v === "string") type = v;
      } catch { /* 保留默认 */ }
    }
    const text = "";
    const images: string[] = [];
    const raw = find("ops.announcement");
    if (raw) {
      try {
        const v = JSON.parse(raw);
        if (typeof v === "string") {
          if (type === "text") return { type, text: v, images: [] };
          images.push(v);
        } else if (Array.isArray(v)) {
          images.push(...v.filter((x): x is string => typeof x === "string" && !!x));
        }
      } catch { /* 保留默认 */ }
    }
    return { type, text, images };
  } catch {
    return def;
  }
}

// ── 供货对接申请（个人中心）：申请 → 后台审核 → 凭据管理 ──

export interface SupplierAccount {
  id: number;
  protocol: string;      // zcard | dujiao_next | acg_faka
  status: string;        // applying | approved | rejected | disabled
  display_name: string;
  contact: string;
  apply_reason: string;
  review_note: string;
  api_key: string;       // app_id（明文常驻）
  reviewed_at: number;
  created_at: number;
  balance_cache: number; // 供货余额（分）
  ip_whitelist?: string[]; // IP 白名单（空=所有 IP 放行）
}

export interface SupplierCredentials {
  id: number;
  protocol: string;
  status: string;
  api_key: string;
  api_secret: string;
}

export function submitSupplierApplication(body: {
  protocol: string;
  display_name: string;
  contact?: string;
  apply_reason?: string;
  notify_url?: string;
}) {
  return api.post<SupplierAccount>('/supplier/applications', body);
}

export function listMySupplierAccounts() {
  return api.get<{ accounts: SupplierAccount[] }>('/supplier/accounts');
}

export function getSupplierCredentials(id: number) {
  return api.get<SupplierCredentials>(`/supplier/accounts/${id}/credentials`);
}

export function regenerateSupplierSecret(id: number) {
  return api.post<SupplierCredentials>(`/supplier/accounts/${id}/regenerate-secret`, {});
}

export function cancelSupplierApplication(id: number) {
  return api.post<{ ok: boolean }>(`/supplier/accounts/${id}/cancel`, {});
}

export function createSupplierRecharge(id: number, body: { amount_cents: number; channel: string }) {
  return api.post<{ recharge_id: number; payment_id: number; type: string; payload: string }>(
    `/supplier/accounts/${id}/recharge`,
    body,
  );
}

export function setSupplierIPWhitelist(id: number, ips: string[]) {
  return api.post<SupplierAccount>(`/supplier/accounts/${id}/ip-whitelist`, { ips });
}
