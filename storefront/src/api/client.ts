import { mergeThemeConfig } from '../../../packages/theme-sdk/src/index';
// 前台 API 客户端：fetch 封装，金额一律「分」int64。
// 认证（）：user realm JWT 存 localStorage，请求自动带 Bearer；
// 401 且本地有 token → 判定过期，清 token 跳登录（游客端点 401 不误伤）。

// SSG 构建（vite-ssg）时服务端无同源 API：经 VITE_SSG_API 指向构建机可达的 API
// （如 http://127.0.0.1:8000）；客户端恒用同源相对路径。
const BASE = import.meta.env.SSR
  ? `${import.meta.env.VITE_SSG_API || 'http://127.0.0.1:8000'}/api/v1/storefront`
  : '/api/v1/storefront';
const TOKEN_KEY = 'zcard_token';
const CURRENCY_KEY = 'zcard_currency';

export function getToken(): string | null {
  if (import.meta.env.SSR) return null;
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null; // 存储不可用（隐私模式等）：视为游客
  }
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

interface ApiResult<T> {
  data: T | null;
  error: string | null;
}

async function request<T>(method: string, path: string, body?: unknown, params?: Record<string, string | number | boolean | undefined>, silent = false): Promise<ApiResult<T>> {
  let url = `${BASE}${path}`;
  if (params) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== '') q.set(k, String(v));
    }
    const qs = q.toString();
    if (qs) url += `?${qs}`;
  }
  const token = getToken();
  try {
    const res = await fetch(url, {
      method,
      headers: {
        ...(body ? { 'Content-Type': 'application/json' } : {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {})
      },
      body: body ? JSON.stringify(body) : undefined
    });
    const text = await res.text();
    let json: any = null;
    try {
      json = text ? JSON.parse(text) : null;
      if (path === '/config' && Array.isArray(json?.entries)) json.entries = mergeThemeConfig(json.entries);
    } catch {
      json = text;
    }
    if (!res.ok) {
      // token 过期/失效：清态跳登录（带回跳）；silent 模式（购物车等游客可降级
      // 端点）只清 token 不跳转——由调用方降级游客本地购物车；无 token 的 401 不动
      if (res.status === 401 && token) {
        clearToken();
        if (!silent) {
          const redirect = encodeURIComponent(location.pathname + location.search);
          location.href = `/login?redirect=${redirect}`;
        }
      }
      return { data: null, error: json?.message || json?.error || `HTTP ${res.status}` };
    }
    return { data: json as T, error: null };
  } catch (e: any) {
    return { data: null, error: e?.message || 'network error' };
  }
}

export const api = {
  get: <T>(path: string, params?: Record<string, string | number | boolean | undefined>) => request<T>('GET', path, undefined, params),
  post: <T>(path: string, body: unknown) => request<T>('POST', path, body),
  delete_: <T>(path: string) => request<T>('DELETE', path),
  // silent 变体：401 只清 token 不跳登录（游客可降级端点在调用方处理）
  getSilent: <T>(path: string, params?: Record<string, string | number | boolean | undefined>) => request<T>('GET', path, undefined, params, true),
  postSilent: <T>(path: string, body: unknown) => request<T>('POST', path, body, undefined, true),
  deleteSilent: <T>(path: string) => request<T>('DELETE', path, undefined, undefined, true)
};

// ── 金额工具（铁律 15：API 一律 int64「分」，显示 /100 为元、提交 *100 为分）──
// 符号与小数位取后台默认货币（i18n.base_currency → /storefront/currencies）；
// 未加载完成回退 ¥/2。纯整数运算：显示整数拆位，提交 Math.round 防浮点。
// 全站金额显示/提交必须经本组函数，禁止内联 `/ 100` 或硬编码符号（架构测试守护）。

export interface CurrencyMeta {
  symbol: string;
  position: string; // prefix | suffix
  precision: number; // 小数位（CNY=2、JPY=0）
  code: string; // 币种代码（CNY/USD…；切换器展示与本地存储键）
  rate: string; // 换算率（decimal 字符串：1 基准货币 = rate 该币；基准币 = "1"）
}

const DEFAULT_META: CurrencyMeta = { symbol: '¥', position: 'prefix', precision: 2, code: 'CNY', rate: '1' };

let currencyMeta: CurrencyMeta = { ...DEFAULT_META };

export function setCurrency(meta: Partial<CurrencyMeta>) {
  currencyMeta = { ...DEFAULT_META, ...meta };
  if (currencyMeta.precision < 0) currencyMeta.precision = 0;
}

export function getCurrency(): CurrencyMeta {
  return { ...currencyMeta };
}

// fenToYuan 分 → 元字符串（不含符号；纯整数运算，禁止浮点参与）。
// undefined/NaN 兜底 0（proto3 零值字段省略 → undefined 传入时显示 0.00 而非 NaN）。
export function fenToYuan(cents: number): string {
  if (!Number.isFinite(cents as number)) cents = 0;
  const neg = cents < 0;
  const v = Math.abs(cents);
  const base = 10 ** currencyMeta.precision;
  const whole = Math.floor(v / base);
  const frac = v % base;
  return (
    `${neg ? '-' : ''}${whole}` +
    (currencyMeta.precision > 0 ? `.${String(frac).padStart(currencyMeta.precision, '0')}` : '')
  );
}

// yuanToFen 元 → 分（提交入口；Math.round 防浮点漂移，如 12.34*100=1233.9999...）。
export function yuanToFen(yuan: number): number {
  return Math.round((yuan || 0) * 10 ** currencyMeta.precision);
}

// centsToYuan 分 → 元数值（输入框回填/图表数据用；界面展示一律走 formatMoney）。
export function centsToYuan(cents: number): number {
  return (Number.isFinite(cents as number) ? cents : 0) / 10 ** currencyMeta.precision;
}

// displayAmount 显示层金额（已按所选币种 rate 换算 + 精度格式化，不含符号）。
// rate="1"（基准币）走 fenToYuan 整数拆位原路；换算币用浮点乘 rate 后按精度取整
// （展示层可接受；提交链路 yuanToFen 恒为基准货币，不受切换影响）。
function displayAmount(cents: number): string {
  if (currencyMeta.rate === '1') return fenToYuan(cents);
  const base = 10 ** currencyMeta.precision;
  const amount = ((Number.isFinite(cents) ? cents : 0) / 100) * Number(currencyMeta.rate || '1');
  return (Math.round(amount * base) / base).toFixed(currencyMeta.precision);
}

// formatMoney 带符号格式化（显示唯一入口；符号位置感知）。
export function formatMoney(cents: number): string {
  const v = displayAmount(cents);
  return currencyMeta.position === 'suffix' ? `${v}${currencyMeta.symbol}` : `${currencyMeta.symbol}${v}`;
}

// formatSignedMoney 有符号金额（流水：正数带 +，负数自带 -）。
export function formatSignedMoney(cents: number): string {
  const v = displayAmount(cents);
  const body = cents > 0 ? `+${v}` : v;
  return currencyMeta.position === 'suffix' ? `${body}${currencyMeta.symbol}` : `${currencyMeta.symbol}${body}`;
}

// initCurrency 启动加载货币（公开配置 i18n.base_currency + i18n.display_currency + 启用货币表；
// 用户本地选择 zcard_currency 优先，其次站点显示货币，再基础货币——多币种展示换算，结算恒基准币；
// 失败回退 ¥）。选择集 listCurrencies() 供顶部切换器渲染；selectCurrency 切换后刷新生效。
let initPromise: Promise<void> | null = null;
let currencyList: (CurrencyMeta & { code: string })[] = [];

export function listCurrencies(): CurrencyMeta[] {
  return currencyList;
}

export function selectCurrency(code: string) {
  if (!code) return;
  localStorage.setItem(CURRENCY_KEY, code);
  if (code !== currencyMeta.code) window.location.reload(); // 低频操作：整页刷新保证全站一致
}

export function initCurrency(): Promise<void> {
  if (!initPromise) {
    initPromise = (async () => {
      try {
        const [cfgRes, curRes] = await Promise.all([
          api.get<any>('/config'),
          api.get<{ currencies: CurrencyMeta[] }>('/currencies')
        ]);
        let baseCode = 'CNY';
        let displayCode = '';
        const entries: { key: string; value_json: string }[] = cfgRes.data?.entries || [];
        for (const entry of entries) {
          if (entry.key !== 'i18n.base_currency' && entry.key !== 'i18n.display_currency') continue;
          try {
            const v = JSON.parse(entry.value_json ?? '');
            if (typeof v === 'string' && v) {
              if (entry.key === 'i18n.base_currency') baseCode = v;
              else displayCode = v;
            }
          } catch {
            /* 非法配置回退默认 */
          }
        }
        const all = (curRes.data?.currencies || []) as any[];
        currencyList = all.map((c) => ({
          code: c.code, symbol: c.symbol, position: c.position,
          precision: Number(c.precision) || 2, rate: String(c.rate_json ?? '1'),
        }));
        const stored = localStorage.getItem(CURRENCY_KEY);
        const picked =
          currencyList.find((c) => c.code === stored) ||
          currencyList.find((c) => c.code === displayCode) || // 站点默认展示货币（结算仍基准币）
          currencyList.find((c) => c.code === baseCode) ||
          { code: baseCode, symbol: '¥', position: 'prefix', precision: 2, rate: '1' };
        setCurrency(picked);
      } catch {
        /* 加载失败回退默认 ¥/2 */
      }
    })();
  }
  return initPromise;
}

// 兼容已保存的充值备注：只转换系统生成的整段文案，金额跟随当前货币。
// 原始流水与账务金额保持不变，其他人工备注原样显示。
export function formatTransactionRemark(remark?: string): string {
  if (!remark) return '';
  const match = /^(充值到账|对接账户自助充值到账)（(?:用户 #(\d+)，)?本金\s*(\d+)\s*分\s*[+＋]\s*赠送\s*(\d+)\s*分）$/.exec(remark);
  if (!match) return remark;
  const principal = Number(match[3]);
  const gift = Number(match[4]);
  const total = principal + gift;
  if (![principal, gift, total].every(Number.isSafeInteger)) return remark;
  const amount = gift > 0
    ? `${formatMoney(total)}（本金 ${formatMoney(principal)}＋赠送 ${formatMoney(gift)}）`
    : formatMoney(principal);
  return `${match[1]} ${amount}${match[2] ? `（用户 #${match[2]}）` : ''}`;
}

export const TRANSACTION_TYPES: Record<string, string> = {
  adjust: '人工调账', recharge: '余额充值', order_pay: '订单支付', payment: '订单支付',
  order_refund: '订单退款', refund: '订单退款', ticket_urgent: '工单付费加急',
  commission: '分销佣金入账', commission_debt: '佣金欠款扣回', commission_reversal: '佣金退回',
  withdraw: '提现', freeze: '余额冻结', unfreeze: '余额解冻', giftcard: '礼品卡兑换',
  earn_recharge: '充值赠送积分', redeem: '积分兑换',
};
export function transactionType(type?: string): string {
  return TRANSACTION_TYPES[type || ''] || '其他收支';
}
export function transactionAmount(row: { direction: string; amount_cents: number }): number {
  return (row.direction === 'out' ? -1 : 1) * Math.abs(row.amount_cents);
}
export function transactionReference(row: { id?: number; display_reference?: string; reference?: string }): string {
  if (row.display_reference) return row.display_reference;
  const [kind, id] = (row.reference || '').split(':');
  const labels: Record<string, string> = { order_pay: '订单', order_refund: '退款记录', ticket_urgent: '工单', recharge: '充值支付记录', giftcard: '礼品卡', commission: '佣金记录', adjust: '调账记录' };
  return labels[kind] && id ? `${labels[kind]} ${kind === 'ticket_urgent' ? '' : '#'}${id}` : `流水 #${row.id || '—'}`;
}
export function transactionRemark(row: { id?: number; type?: string; remark?: string; reference?: string; display_reference?: string }): string {
  const remark = row.remark?.trim();
  if (!remark || remark === row.reference) return transactionType(row.type);
  if (TRANSACTION_TYPES[remark]) return TRANSACTION_TYPES[remark];
  if (/^(order_pay|order_refund|ticket_urgent|recharge|giftcard|commission|adjust):[^\s]+$/.test(remark)) {
    return transactionReference({ ...row, reference: remark, display_reference: remark === row.reference ? row.display_reference : undefined });
  }
  return formatTransactionRemark(row.remark);
}
