import { request } from "../request";

// ── 钱包管理 ──

export interface WalletBalance {
  user_id: number;
  available_cents: number;
  locked_cents: number;
  total_cents: number;
}

export interface WalletTransaction {
  id: number;
  direction: string;
  type: string;
  amount_cents: number;
  balance_before_cents: number;
  balance_after_cents: number;
  reference: string;
  remark: string;
  created_at: number;
}

/** 查询指定用户余额 */
export function fetchWalletBalance(userId: number) {
  return request({
    url: `/api/v1/admin/wallet/${userId}`,
  });
}

/** 手动调账（正=入账 负=扣减） */
export function adjustWalletBalance(
  userId: number,
  data: { amount_cents: number; reason: string },
) {
  return request({
    url: `/api/v1/admin/wallet/${userId}/adjust`,
    method: "post",
    data,
  });
}

/** 查询指定用户流水（分页） */
export function fetchWalletTransactions(
  userId: number,
  params?: { page?: number; page_size?: number },
) {
  return request({
    url: `/api/v1/admin/wallet/${userId}/transactions`,
    method: "get",
    params,
  });
}

// ── 提现单（wallet:withdraw / withdraw_review）──

export function fetchWithdrawals(params?: { status?: string; page?: number; page_size?: number }) {
  return request({ url: "/api/v1/admin/wallet/withdrawals", params });
}

export function reviewWithdrawal(id: number, approve: boolean, reason?: string) {
  return request({ url: `/api/v1/admin/wallet/withdrawals/${id}/review`, method: "post", data: { approve, reason } });
}

export function payWithdrawal(id: number) {
  return request({ url: `/api/v1/admin/wallet/withdrawals/${id}/pay`, method: "post", data: {} });
}

// ── 礼品卡（giftcard:read / write）──

export function fetchGiftcardBatches(page = 1, pageSize = 20) {
  return request({ url: "/api/v1/admin/wallet/giftcard-batches", params: { page, page_size: pageSize } });
}

export function createGiftcardBatch(data: { batch_no: string; name: string; amount_cents: number; quantity: number }) {
  return request({ url: "/api/v1/admin/wallet/giftcard-batches", method: "post", data });
}
