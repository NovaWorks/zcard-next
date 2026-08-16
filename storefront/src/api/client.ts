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

// 分 → 元展示
export function fenToYuan(cents: number): string {
  const neg = cents < 0;
  const v = Math.abs(cents);
  return `${neg ? '-' : ''}${Math.floor(v / 100)}.${String(v % 100).padStart(2, '0')}`;
}
