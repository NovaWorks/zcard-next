let effectiveValues: Record<string, unknown> = {};
export interface ThemeRuntime {
  key: string;
  theme_revision: string;
  config_revision?: string;
  values: Record<string, unknown>;
  capabilities: Record<string, boolean>;
  preview?: boolean;
}
export type ConfigEntry = { key: string; value_json: string };
export function readThemeRuntime(): ThemeRuntime | null {
  if (typeof document === "undefined") return null;
  try {
    return JSON.parse(
      document.getElementById("zcard-theme-runtime")?.textContent || "null",
    );
  } catch {
    return null;
  }
}
export function mergeThemeConfig(entries: ConfigEntry[]): ConfigEntry[] {
  const runtime = readThemeRuntime();
  const values = new Map(entries.map((e) => [e.key, e.value_json]));
  for (const [key, value] of Object.entries(runtime?.values || {}))
    values.set(key, JSON.stringify(value));
  if (runtime?.capabilities.cart === false)
    values.set("theme.nav_cart", "false");
  const result = [...values].map(([key, value_json]) => ({ key, value_json }));
  applyThemeConfig(result);
  return result;
}
export function applyThemeConfig(entries: ConfigEntry[]) {
  if (typeof document === "undefined") return;
  const v: Record<string, unknown> = {};
  for (const entry of entries) {
    try {
      v[entry.key] = JSON.parse(entry.value_json);
    } catch {}
  }
  effectiveValues = v;
  const root = document.documentElement;
  const numbers: [string, string, number, number][] = [
    ["theme.content_width", "--zc-content-width", 960, 1600],
    ["theme.font_size", "--zc-font-size", 12, 20],
    ["theme.card_radius", "--zc-card-radius", 0, 32],
    ["theme.notice_width", "--zc-notice-width", 480, 1280],
    ["theme.article_width", "--zc-article-width", 640, 1280],
  ];
  for (const [key, css, min, max] of numbers) {
    const n = v[key];
    if (typeof n === "number" && n >= min && n <= max)
      root.style.setProperty(css, `${n}px`);
  }
  if (
    typeof v["theme.primary_color"] === "string" &&
    /^#[a-f\d]{6}$/i.test(v["theme.primary_color"])
  )
    root.style.setProperty("--zc-primary", v["theme.primary_color"]);
  for (const [key, attr] of [
    ["theme.nav_cart", "data-theme-cart"],
    ["theme.nav_posts", "data-theme-posts"],
    ["theme.back_top", "data-theme-back-top"],
  ]) {
    if (typeof v[key] === "boolean") root.setAttribute(attr, String(v[key]));
  }
}
export function themeValue<T>(key: string, fallback: T): T {
  const v = readThemeRuntime()?.values?.[key] ?? effectiveValues[key];
  return typeof v === typeof fallback ? (v as T) : fallback;
}
