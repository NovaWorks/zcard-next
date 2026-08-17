// 前台 API 客户端：fetch 封装，金额一律「分」int64。

const BASE = '/api/v1/storefront';

interface ApiResult<T> {
  data: T | null;
  error: string | null;
}

async function request<T>(method: string, path: string, body?: unknown, params?: Record<string, string | number | undefined>): Promise<ApiResult<T>> {
  let url = `${BASE}${path}`;
  if (params) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== '') q.set(k, String(v));
    }
    const qs = q.toString();
    if (qs) url += `?${qs}`;
  }
  try {
    const res = await fetch(url, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
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
      return { data: null, error: json?.message || json?.error || `HTTP ${res.status}` };
    }
    return { data: json as T, error: null };
  } catch (e: any) {
    return { data: null, error: e?.message || 'network error' };
  }
}

export const api = {
  get: <T>(path: string, params?: Record<string, string | number | undefined>) => request<T>('GET', path, undefined, params),
  post: <T>(path: string, body: unknown) => request<T>('POST', path, body)
};

// ── 金额工具（铁律 15：API 一律 int64「分」，显示 /100 为元、提交 *100 为分）──
// 符号与小数位取后台默认货币（i18n.base_currency → /storefront/currencies）；
// 未加载完成回退 ¥/2。纯整数运算：显示整数拆位，提交 Math.round 防浮点。
// 全站金额显示/提交必须经本组函数，禁止内联 `/ 100` 或硬编码符号（架构测试守护）。

export interface CurrencyMeta {
  symbol: string;
  position: string; // prefix | suffix
  precision: number; // 小数位（CNY=2、JPY=0）
}

const DEFAULT_META: CurrencyMeta = { symbol: '¥', position: 'prefix', precision: 2 };

let currencyMeta: CurrencyMeta = { ...DEFAULT_META };

export function setCurrency(meta: Partial<CurrencyMeta>) {
  currencyMeta = { ...DEFAULT_META, ...meta };
  if (currencyMeta.precision < 0) currencyMeta.precision = 0;
}

export function getCurrency(): CurrencyMeta {
  return { ...currencyMeta };
}

// fenToYuan 分 → 元字符串（不含符号；纯整数运算，禁止浮点参与）。
export function fenToYuan(cents: number): string {
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
  return Math.round(yuan * 10 ** currencyMeta.precision);
}

// centsToYuan 分 → 元数值（输入框回填/图表数据用；界面展示一律走 formatMoney）。
export function centsToYuan(cents: number): number {
  return cents / 10 ** currencyMeta.precision;
}

// formatMoney 带符号格式化（显示唯一入口；符号位置感知）。
export function formatMoney(cents: number): string {
  const v = fenToYuan(cents);
  return currencyMeta.position === 'suffix' ? `${v}${currencyMeta.symbol}` : `${currencyMeta.symbol}${v}`;
}

// formatSignedMoney 有符号金额（流水：正数带 +，负数 fenToYuan 自带 -）。
export function formatSignedMoney(cents: number): string {
  const v = fenToYuan(cents);
  const body = cents > 0 ? `+${v}` : v;
  return currencyMeta.position === 'suffix' ? `${body}${currencyMeta.symbol}` : `${currencyMeta.symbol}${body}`;
}

// initCurrency 启动加载默认货币（公开配置 i18n.base_currency + 货币表符号；失败回退 ¥）。
let initPromise: Promise<void> | null = null;

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
        const cur = (curRes.data?.currencies || []).find((c: any) => c.code === baseCode);
        if (cur) {
          setCurrency({ symbol: cur.symbol, position: cur.position, precision: cur.precision });
        }
      } catch {
        /* 加载失败回退默认 ¥/2 */
      }
    })();
  }
  return initPromise;
}
