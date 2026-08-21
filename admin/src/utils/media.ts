import { getServiceBaseURL } from "@/utils/service";

// 素材/静态资源 URL 解析：
// 后端返回相对路径（如 /uploads/2026/08/xxx.png），浏览器直接加载会落在前端
// 站点根下 404（admin 走 /proxy-default 网关，图片需同网关前缀转发到后端静态服务）。
// 绝对 URL（http(s)/data:）原样返回。
const isHttpProxy = import.meta.env.DEV && import.meta.env.VITE_HTTP_PROXY === "Y";
const { baseURL } = getServiceBaseURL(import.meta.env, isHttpProxy);

export function resolveMediaUrl(url?: string | null): string {
  if (!url) return "";
  if (/^https?:\/\//i.test(url) || url.startsWith("data:")) return url;
  if (url.startsWith("/uploads/") || url.startsWith("/static/")) {
    return `${baseURL}${url}`;
  }
  return url;
}
