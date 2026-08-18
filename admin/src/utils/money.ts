// 金额工具（铁律 15：DB/API 一律 int64「分」，前端显示 /100 为元、提交 *100 为分）。
// 符号与小数位取后台默认货币（settings i18n.base_currency → currencies 表）；
// 未加载完成回退 ¥/2。纯整数运算：显示用整数拆位，提交 Math.round 防浮点漂移。
// 全站金额显示/提交必须经本文件，禁止内联 `xxx / 100` 或硬编码符号（架构测试守护）。

import { fetchSettings, fetchCurrencies } from "@/service/api";

export interface CurrencyMeta {
  symbol: string; // 货币符号（¥/$）
  position: string; // prefix | suffix
  precision: number; // 小数位（CNY=2、JPY=0）
}

const DEFAULT_META: CurrencyMeta = { symbol: "¥", position: "prefix", precision: 2 };

let current: CurrencyMeta = { ...DEFAULT_META };

export function setCurrency(meta: Partial<CurrencyMeta>) {
  current = { ...DEFAULT_META, ...meta };
  if (current.precision < 0) current.precision = 0;
}

export function getCurrency(): CurrencyMeta {
  return { ...current };
}

// safeCents 金额归一化：proto3 零值字段不输出 → undefined 传入时兜底 0（杜绝 NaN）。
function safeCents(cents: number): number {
  return Number.isFinite(cents) ? cents : 0;
}

// fenToYuan 分 → 元字符串（不含符号；纯整数运算，禁止浮点参与）。
export function fenToYuan(cents: number): string {
  const n = safeCents(cents);
  const neg = n < 0;
  const v = Math.abs(n);
  const base = 10 ** current.precision;
  const whole = Math.floor(v / base);
  const frac = v % base;
  return (
    `${neg ? "-" : ""}${whole}` +
    (current.precision > 0 ? `.${String(frac).padStart(current.precision, "0")}` : "")
  );
}

// yuanToFen 元 → 分（提交入口；Math.round 防浮点漂移，如 12.34*100=1233.9999...）。
export function yuanToFen(yuan: number): number {
  return Math.round(safeCents(yuan) * 10 ** current.precision);
}

// centsToYuan 分 → 元数值（输入框回填/图表数据用；界面展示一律走 formatMoney）。
export function centsToYuan(cents: number): number {
  return safeCents(cents) / 10 ** current.precision;
}

// formatMoney 带符号格式化（显示唯一入口；符号位置感知）。
export function formatMoney(cents: number): string {
  const v = fenToYuan(cents);
  return current.position === "suffix" ? `${v}${current.symbol}` : `${current.symbol}${v}`;
}

// formatSignedMoney 有符号金额（流水/调账：正数带 +，负数 fenToYuan 自带 -）。
export function formatSignedMoney(cents: number): string {
  const v = fenToYuan(cents);
  const body = cents > 0 ? `+${v}` : v;
  return current.position === "suffix" ? `${body}${current.symbol}` : `${current.symbol}${body}`;
}

// ── 默认货币加载（后台 i18n.base_currency → /admin/currencies 符号/位置/小数位）──

let initPromise: Promise<void> | null = null;

export function initCurrency(): Promise<void> {
  if (!initPromise) {
    initPromise = (async () => {
      try {
        const [settingsRes, currencyRes] = await Promise.all([
          fetchSettings("i18n"),
          fetchCurrencies(),
        ]);
        let baseCode = "CNY";
        const items: any[] = ((settingsRes as any)?.data as any)?.items || [];
        const entry = items.find((it: any) => it.key === "base_currency");
        if (entry?.value_json) {
          try {
            const v = JSON.parse(entry.value_json);
            if (typeof v === "string" && v) baseCode = v;
          } catch {
            /* 非法配置回退默认 */
          }
        }
        const list: any[] = ((currencyRes as any)?.data as any)?.currencies || [];
        const cur = list.find((c: any) => c.code === baseCode);
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
