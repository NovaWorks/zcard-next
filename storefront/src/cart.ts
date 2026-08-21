// 购物车抽象：登录用户走后端 API（服务端存储）；游客走 localStorage 本地购物车
// （老项目同款方案——游客可加购/结算/下单，仅支付时无余额渠道）。
// 登录后自动把本地购物车合并到后端并清空本地。

import { ref } from 'vue';
import { listCart, addCart, updateCart, removeCart } from './api';
import { getToken } from './api/client';

const GUEST_KEY = 'zcard_guest_cart';

// ── 共享响应式状态（单一数据源）──
// 导航购物车角标 + 商品页「已在购物车 → 移除购物车」判定都读这里；
// 任何 loadCart/加购/删改操作后自动同步。
export interface CartSnapshotItem {
  id: number;
  product_id: number;
  sku_id: number;
  quantity: number;
}
export const cartState = ref<{ count: number; items: CartSnapshotItem[] }>({ count: 0, items: [] });

function syncCartState(items: CartSnapshotItem[]) {
  cartState.value = {
    count: items.reduce((s, i) => s + i.quantity, 0),
    items: items.map((i) => ({ id: i.id, product_id: i.product_id, sku_id: i.sku_id || 0, quantity: i.quantity })),
  };
}

/** 刷新共享状态（不返回给调用方数据——纯状态同步；加购/删除后调用） */
export async function refreshCartState() {
  if (getToken()) {
    const { data, error } = await listCart().catch(() => ({ data: null, error: 'network' }));
    if (!error && data) {
      syncCartState(data.items || []);
      return;
    }
  }
  syncCartState(guestItems());
}

/** 按商品 + SKU 查购物车项（无则 undefined）——商品页「移除购物车」用 */
export function cartItemOf(productId: number, skuId = 0) {
  return cartState.value.items.find((i) => i.product_id === productId && i.sku_id === (skuId || 0));
}

/** 商品（+SKU）是否已在购物车 */
export function inCart(productId: number, skuId = 0) {
  return !!cartItemOf(productId, skuId);
}

interface GuestCartItem {
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
}

function guestItems(): GuestCartItem[] {
  try {
    const raw = JSON.parse(localStorage.getItem(GUEST_KEY) || '[]');
    return Array.isArray(raw) ? raw : [];
  } catch {
    return [];
  }
}

function saveGuest(items: GuestCartItem[]) {
  try {
    localStorage.setItem(GUEST_KEY, JSON.stringify(items));
  } catch { /* 存储不可用：本地购物车降级为空 */ }
}

/** 401 判定（错误串兼容 reason/message 两种格式） */
function isAuthError(error: string | null): boolean {
  return !!error && (error.includes('401') || error.includes('UNAUTHORIZED') || error.includes('未登录'));
}

/** 加载购物车（登录 → 后端；游客/令牌失效 → 本地；同时同步共享角标状态） */
export async function loadCart() {
  if (getToken()) {
    const { data, error } = await listCart();
    if (isAuthError(error)) {
      // token 过期/失效：降级游客本地购物车（真实游客场景）
      syncCartState(guestItems());
      return { items: guestItems(), error: '', isGuest: true };
    }
    syncCartState(data?.items || []);
    return { items: data?.items || [], error, isGuest: false };
  }
  syncCartState(guestItems());
  return { items: guestItems(), error: '', isGuest: true };
}

/** 加购（登录 → 后端；游客/令牌失效 → 本地，同商品同 SKU 合并数量）；成功后同步角标 */
export async function addToCart(product: { id: number; name: string; price_cents: number; points_required?: number; stock?: number }, quantity: number, skuId = 0) {
  let result;
  if (getToken()) {
    const { data, error } = await addCart(product.id, quantity, skuId);
    if (isAuthError(error)) {
      // token 过期：降级游客本地购物车，不阻断加购
      result = addGuestLocal(product, quantity, skuId);
    } else {
      result = { data, error };
    }
  } else {
    result = addGuestLocal(product, quantity, skuId);
  }
  if (!result.error) await refreshCartState(); // 拉权威列表（拿到购物车项 id，供「移除」用）
  return result;
}

/** 游客本地加购（同商品同 SKU 合并数量） */
function addGuestLocal(product: { id: number; name: string; price_cents: number; points_required?: number; stock?: number }, quantity: number, skuId: number) {
  const items = guestItems();
  const existing = items.find((i) => i.product_id === product.id && (i.sku_id || 0) === skuId);
  if (existing) {
    existing.quantity = Math.min(99, existing.quantity + quantity);
  } else {
    items.push({
      id: Date.now(),
      product_id: product.id,
      sku_id: skuId,
      quantity,
      product_name: product.name,
      price_cents: product.price_cents,
      stock: product.stock ?? -1, // -1 = 未知库存（接口零值省略），视为有效，下单时后端校验
      points_only: !!product.points_required,
      points_required: product.points_required || 0,
      valid: true,
      added_at: Date.now(),
    });
  }
  saveGuest(items);
  return { data: null, error: '' };
}

/** 改数量（游客本地；同步角标计数） */
export function updateGuestQty(id: number, quantity: number) {
  const items = guestItems();
  const it = items.find((i) => i.id === id);
  if (it) it.quantity = Math.max(1, Math.min(99, quantity || 1));
  saveGuest(items);
  syncCartState(items);
}

/** 删除（登录 → 后端；游客 → 本地）；成功后同步角标 */
export async function removeCartItem(id: number) {
  let result;
  if (getToken()) {
    result = await removeCart(id);
  } else {
    saveGuest(guestItems().filter((i) => i.id !== id));
    result = { data: null, error: '' };
  }
  if (!result.error) await refreshCartState();
  return result;
}

/** 下单成功后移除已购项（登录 → 后端批量删；游客 → 本地按 id 删）；同步角标 */
export async function clearPurchased(ids: number[]) {
  if (getToken()) {
    for (const id of ids) await removeCart(id).catch(() => {});
  } else {
    const idSet = new Set(ids);
    saveGuest(guestItems().filter((i) => !idSet.has(i.id)));
  }
  await refreshCartState();
}

/** 登录后合并本地购物车到后端并清空（App 挂载时调用）；同步角标 */
export async function mergeGuestCart() {
  if (!getToken()) return;
  const items = guestItems();
  if (items.length) {
    for (const it of items) {
      await addCart(it.product_id, it.quantity, it.sku_id || 0).catch(() => {});
    }
    try { localStorage.removeItem(GUEST_KEY); } catch { /* 忽略 */ }
    await refreshCartState();
  }
}

/** 购物车总件数（角标；登录 → 后端计数需调用方拉取，游客 → 本地直接算） */
export function guestCartCount(): number {
  return guestItems().reduce((s, i) => s + i.quantity, 0);
}
