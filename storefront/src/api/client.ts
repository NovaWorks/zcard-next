// 前台 API 客户端：fetch 封装，金额一律「分」int64。
// 认证（P3-09 T1）：user realm JWT 存 localStorage，请求自动带 Bearer；
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

// initCurrency 启动加载货币（公开配置 i18n.base_currency + 启用货币表；
// 用户本地选择 zcard_currency 优先——多币种展示换算；失败回退 ¥）。
// 选择集 listCurrencies() 供顶部切换器渲染；selectCurrency 切换后刷新生效。
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
        const entries: { key: string; value_json: string }[] = cfgRes.data?.entries || [];
        const entry = entries.find((e) => e.key === 'i18n.base_currency');
        if (entry?.value_json) {
          try {
            const v = JSON.parse(entry.value_json);
            if (typeof v === 'string' && v) baseCode = v;
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
