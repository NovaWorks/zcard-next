// 购物车抽象：登录用户走后端 API（服务端存储）；游客走 localStorage 本地购物车
// （老项目同款方案——游客可加购/结算/下单，仅支付时无余额渠道）。
// 登录后自动把本地购物车合并到后端并清空本地。

import { listCart, addCart, updateCart, removeCart } from './api';
import { getToken } from '@/api/client';

const GUEST_KEY = 'zcard_guest_cart';

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

/** 加载购物车（登录 → 后端；游客/令牌失效 → 本地） */
export async function loadCart() {
  if (getToken()) {
    const { data, error } = await listCart();
    if (isAuthError(error)) {
      // token 过期/失效：降级游客本地购物车（真实游客场景）
      return { items: guestItems(), error: '', isGuest: true };
    }
    return { items: data?.items || [], error, isGuest: false };
  }
  return { items: guestItems(), error: '', isGuest: true };
}

/** 加购（登录 → 后端；游客/令牌失效 → 本地，同商品同 SKU 合并数量） */
export async function addToCart(product: { id: number; name: string; price_cents: number; points_required?: number; stock?: number }, quantity: number, skuId = 0) {
  if (getToken()) {
    const { data, error } = await addCart(product.id, quantity, skuId);
    if (isAuthError(error)) {
      // token 过期：降级游客本地购物车，不阻断加购
      return addGuestLocal(product, quantity, skuId);
    }
    return { data, error };
  }
  return addGuestLocal(product, quantity, skuId);
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

/** 改数量（游客本地） */
export function updateGuestQty(id: number, quantity: number) {
  const items = guestItems();
  const it = items.find((i) => i.id === id);
  if (it) it.quantity = Math.max(1, Math.min(99, quantity || 1));
  saveGuest(items);
}

/** 删除（登录 → 后端；游客 → 本地） */
export async function removeCartItem(id: number) {
  if (getToken()) return removeCart(id);
  saveGuest(guestItems().filter((i) => i.id !== id));
  return { data: null, error: '' };
}

/** 下单成功后移除已购项（登录 → 后端批量删；游客 → 本地按 id 删） */
export async function clearPurchased(ids: number[]) {
  if (getToken()) {
    for (const id of ids) await removeCart(id).catch(() => {});
    return;
  }
  const idSet = new Set(ids);
  saveGuest(guestItems().filter((i) => !idSet.has(i.id)));
}

/** 登录后合并本地购物车到后端并清空（App 挂载时调用） */
export async function mergeGuestCart() {
  if (!getToken()) return;
  const items = guestItems();
  if (!items.length) return;
  for (const it of items) {
    await addCart(it.product_id, it.quantity, it.sku_id || 0).catch(() => {});
  }
  try { localStorage.removeItem(GUEST_KEY); } catch { /* 忽略 */ }
}

/** 购物车总件数（角标；登录 → 后端计数需调用方拉取，游客 → 本地直接算） */
export function guestCartCount(): number {
  return guestItems().reduce((s, i) => s + i.quantity, 0);
}
