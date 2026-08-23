/**
 * SEO 工具：站点级配置消费（/api/v1/storefront/config 公开下发）+ 页面级 head 声明。
 * 基于 @unhead/vue（vite-ssg 内置）：SSG 构建时输出到静态 HTML，客户端水合后动态更新。
 * 通过 vite-ssg 注入的 head 实例操作（head.push 不依赖组件 setup 上下文——
 * async setup 的 await 之后 useHead/inject 不可用）。
 * SSR 安全：不依赖 window/document（canonical 基准用 site.url）。
 */

type HeadEntry = Record<string, any>;

let activeHead: { push: (entry: HeadEntry) => () => void } | null = null;

/** 由 vite-ssg createApp 回调注入 head 实例（main.ts） */
export function setActiveHead(head: { push: (entry: HeadEntry) => () => void }) {
  activeHead = head;
}

function headPush(entry: HeadEntry) {
  if (!activeHead) {
    console.warn("[seo] head 未注入（setActiveHead 未调用）");
    return;
  }
  activeHead.push(entry);
}

export interface SiteSeoConfig {
  name: string;
  url: string;
  logo: string;
  seoTitle: string;
  seoKeywords: string;
  seoDesc: string;
  verificationGoogle: string;
  verificationBing: string;
}

let cached: SiteSeoConfig | null = null;

function parseStr(raw: string | undefined, dflt = ""): string {
  if (raw === undefined) return dflt;
  try {
    const v = JSON.parse(raw);
    return typeof v === "string" ? v : dflt;
  } catch {
    return dflt;
  }
}

/** 站点 SEO 配置（失败回退默认值；SSR 构建期经 VITE_SSG_API 访问，客户端同源） */
export async function fetchSiteSeo(): Promise<SiteSeoConfig> {
  if (cached) return cached;
  const def: SiteSeoConfig = { name: "ZCard 商店", url: "", logo: "", seoTitle: "", seoKeywords: "", seoDesc: "", verificationGoogle: "", verificationBing: "" };
  try {
    const apiBase = import.meta.env.SSR ? (import.meta.env.VITE_SSG_API || "http://127.0.0.1:8000") : "";
    const resp = await fetch(`${apiBase}/api/v1/storefront/config`);
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    cached = {
      name: parseStr(find("site.name"), def.name),
      url: parseStr(find("site.url")),
      logo: parseStr(find("site.logo")),
      seoTitle: parseStr(find("site.seo_title")),
      seoKeywords: parseStr(find("site.seo_keywords")),
      seoDesc: parseStr(find("site.seo_desc")),
      verificationGoogle: parseStr(find("site.verification_google")),
      verificationBing: parseStr(find("site.verification_bing")),
    };
    return cached;
  } catch {
    return def;
  }
}

/** 页面级 SEO 元数据 */
export interface SeoMeta {
  title?: string;
  description?: string;
  keywords?: string;
  /** 缺省 = 站点地址（site.url） */
  canonical?: string;
  ogImage?: string;
  ogType?: string; // website | article | product
  jsonLd?: object | object[];
}

/** 绝对 URL 归一（相对路径拼站点基准） */
function absUrl(u: string, base: string): string {
  if (!u) return "";
  return /^https?:\/\//i.test(u) ? u : base + u;
}

/** 站点 URL 基准（SSR 无 window：site.url 优先，空则客户端 origin） */
function siteBase(site: SiteSeoConfig): string {
  if (site.url) return site.url;
  return typeof window !== "undefined" ? window.location.origin : "";
}

/**
 * 声明页面级 SEO（幂等）：title/canonical/meta/og/JSON-LD。
 * 所有键每次全量输出（缺省空串）+ key 去重——避免 SPA 导航残留旧页 meta。
 */
export function applySeo(meta: SeoMeta, site: SiteSeoConfig) {
  const base = siteBase(site);
  const canonicalUrl = meta.canonical || base;
  headPush({
    title: meta.title || "",
    meta: [
      { name: "description", content: meta.description ?? "", key: "description" },
      { name: "keywords", content: meta.keywords ?? "", key: "keywords" },
      { property: "og:title", content: meta.title ?? "", key: "og:title" },
      { property: "og:description", content: meta.description ?? "", key: "og:description" },
      { property: "og:type", content: meta.ogType || "website", key: "og:type" },
      { property: "og:url", content: canonicalUrl, key: "og:url" },
      { property: "og:image", content: meta.ogImage || absUrl(site.logo, base), key: "og:image" },
    ],
    link: [{ rel: "canonical", href: canonicalUrl, key: "canonical" }],
    script: [{ key: "jsonld", type: "application/ld+json", innerHTML: meta.jsonLd ? JSON.stringify(meta.jsonLd) : "" }],
  });
}

/** 站点兜底 SEO（首页/非 SEO 页面） */
export function applyDefaultSeo(site: SiteSeoConfig) {
  const base = siteBase(site);
  applySeo(
    {
      title: site.seoTitle || `${site.name} - 自动发卡商城`,
      description: site.seoDesc,
      keywords: site.seoKeywords,
      ogType: "website",
      jsonLd: {
        "@context": "https://schema.org",
        "@type": "Organization",
        name: site.name,
        url: base,
        ...(site.logo ? { logo: absUrl(site.logo, base) } : {}),
      },
    },
    site,
  );
}

/** 站长验证码 meta（GSC/Bing 验证；SSG 输出到静态页 head） */
export function applyVerification(site: SiteSeoConfig) {
  headPush({
    meta: [
      ...(site.verificationGoogle
        ? [{ name: "google-site-verification", content: site.verificationGoogle, key: "google-verification" }]
        : []),
      ...(site.verificationBing
        ? [{ name: "msvalidate.01", content: site.verificationBing, key: "bing-verification" }]
        : []),
    ],
  });
}

/** HTML 转纯文本（meta description 用；无 DOM 依赖，SSR 安全） */
export function stripHtml(html: string): string {
  return html
    .replace(/<[^>]*>/g, " ")
    .replace(/&nbsp;/gi, " ")
    .replace(/&amp;/gi, "&")
    .replace(/&lt;/gi, "<")
    .replace(/&gt;/gi, ">")
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'")
    .replace(/\s+/g, " ")
    .trim();
}

/** 截断到 maxLen（中文按字符计） */
export function truncate(text: string, maxLen = 150): string {
  return text.length > maxLen ? text.slice(0, maxLen - 1) + "…" : text;
}
