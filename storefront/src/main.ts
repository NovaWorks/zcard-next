import { ViteSSG } from 'vite-ssg';
import App from './App.vue';
import { routes, installRouterGuards } from './router';
import { setActiveHead, fetchSiteSeo, applyDefaultSeo, applyVerification } from './seo';
import './style.css';

export const createApp = ViteSSG(
  App,
  { routes },
  ({ app, head, router, isClient }) => {
    setActiveHead(head);
    if (isClient) {
      // 客户端用完整 router（含登录守卫/尾斜杠规范化）；SSR 用 vite-ssg 内置 router 渲染。
      // 注意：默认 SEO 不在客户端全局 push（会与水合中的页面级 SEO 竞争覆盖）——
      // 首页由 Home.vue 负责默认 SEO；页面级 SEO 由各页面组件负责。
      // 守卫直接注册到 vite-ssg 的 router（不创建第二个 router——双 history 会互相干扰）
      installRouterGuards(router);
    } else {
      // SSR：渲染前输出默认 head（站点配置拉取在 fn 的 await 中完成，早于组件渲染；
      // 页面级 SEO 后注册会按 key 覆盖默认值）
      app.use(router);
      return fetchSiteSeo().then((site) => {
        applyDefaultSeo(site);
        applyVerification(site);
      });
    }
  },
);

// SSG 静态化范围：默认路径 + 全部上架商品 /product/:id + 已发布文章 /posts/:slug
// （首页/列表页直接静态；会员/支付等交互页不预渲染）
export async function includedRoutes(paths: string[], _routes: unknown[]) {
  const api = import.meta.env.VITE_SSG_API || 'http://127.0.0.1:8000';
  const out = paths.filter((p) => !p.includes(':'));
  try {
    const [prodResp, postResp] = await Promise.all([
      fetch(`${api}/api/v1/storefront/products?page=1&page_size=200`),
      fetch(`${api}/api/v1/storefront/posts?page=1&page_size=200`),
    ]);
    const prods = await prodResp.json();
    (prods?.items || []).forEach((p: { id: number }) => out.push(`/product/${p.id}`));
    const posts = await postResp.json();
    (posts?.posts || []).forEach((p: { slug: string }) => out.push(`/posts/${p.slug}`));
  } catch (e) {
    console.warn('[ssg] 静态化数据拉取失败（跳过动态页）', e);
  }
  return out;
}
