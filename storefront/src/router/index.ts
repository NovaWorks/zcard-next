import { nextTick } from 'vue';
import { createRouter, createWebHistory } from 'vue-router';
import type { Router, RouterScrollBehavior, RouteRecordRaw } from 'vue-router';
import { refreshCartSetting } from '@/cart';
import { getToken } from '@/api/client';
import Home from '@/views/Home.vue';

// meta.auth：需登录页面（）——无 token 跳 /login 带回跳。
export const routes: RouteRecordRaw[] = [
  { path: '/', name: 'home', component: Home },
  { path: '/products', name: 'products', redirect: to => ({ path: '/', query: to.query, hash: to.hash || '#catalog' }) },
  { path: '/product/:id', name: 'product', component: () => import('@/views/ProductDetail.vue') },
  { path: '/payment/:orderNo', name: 'payment', component: () => import('@/views/Payment.vue') },
  { path: '/order/:orderNo', name: 'order-detail', component: () => import('@/views/OrderDetail.vue') },
  { path: '/fetch', name: 'fetch', component: () => import('@/views/Fetch.vue') },
  { path: '/member', name: 'member', component: () => import('@/views/Member.vue'), meta: { auth: true } },
  { path: '/login', name: 'login', component: () => import('@/views/Login.vue') },
  { path: '/forgot-password', name: 'forgot-password', component: () => import('@/views/ForgotPassword.vue') },
  { path: '/register', name: 'register', component: () => import('@/views/Register.vue') },
  { path: '/tickets', name: 'tickets', component: () => import('@/views/Tickets.vue'), meta: { auth: true } },
  { path: '/tickets/:no', name: 'ticket-detail', component: () => import('@/views/TicketDetail.vue'), meta: { auth: true } },
  { path: '/affiliate', name: 'affiliate', component: () => import('@/views/Affiliate.vue'), meta: { auth: true } },
  { path: '/install', name: 'install', component: () => import('@/views/Install.vue'), meta: { auth: false } },
  { path: '/withdraw', name: 'withdraw', component: () => import('@/views/Withdraw.vue'), meta: { auth: true } },
  { path: '/points', name: 'points', component: () => import('@/views/Points.vue') },
  { path: '/cart', name: 'cart', component: () => import('@/views/Cart.vue'), meta: { auth: true } },
  { path: '/coupons', name: 'coupons', component: () => import('@/views/Coupons.vue'), meta: { auth: true } },
  { path: '/posts', name: 'posts', component: () => import('@/views/Posts.vue') },
  { path: '/posts/:slug', name: 'post-detail', component: () => import('@/views/PostDetail.vue') }
];

const catalogPositions = new Map<string, { left: number; top: number }>();

export const scrollBehavior: RouterScrollBehavior = async (to, from, savedPosition) => {
  if (to.name === 'posts' && from.name === 'posts') return false;
  if (to.name === 'home') {
    // Filtering on the same page scrolls to its toolbar in Home.vue.
    if (from.name === 'home') return false;
    await nextTick();
    await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
    const position = savedPosition || catalogPositions.get(to.fullPath);
    if (position) return position;
    if (to.hash === '#catalog') return { el: '#catalog', top: (document.querySelector('.topbar')?.getBoundingClientRect().height || 0) + 12 };
    return { left: 0, top: 0 };
  }
  return savedPosition || { left: 0, top: 0 };
};

export function createAppRouter(): Router {
  const router = createRouter({
    history: createWebHistory(),
    routes,
    scrollBehavior,
  });
  installRouterGuards(router);
  return router;
}

/** 登录守卫 + 尾斜杠规范化（注册到任意 router 实例；vite-ssg 与独立入口共用） */
export function installRouterGuards(router: Router) {
  router.beforeEach(async (to, from) => {
    // Capture before the route changes or a shorter detail page clamps scrollY.
    if (from.name === 'home' && to.name !== 'home') {
      catalogPositions.set(from.fullPath, { left: window.scrollX, top: window.scrollY });
      if (catalogPositions.size > 50) catalogPositions.delete(catalogPositions.keys().next().value!);
    }
    // 关闭时允许展示停用说明，无需先要求登录。
    const cartUnavailable = to.name === 'cart' && !(await refreshCartSetting(true));
    if (to.meta.auth && !cartUnavailable && !getToken()) {
      return { path: '/login', query: { redirect: to.fullPath } };
    }
    // URL 规范化：尾斜杠 replace 到无斜杠版本，避免同一内容双 URL 重复收录
    if (to.path.length > 1 && to.path.endsWith('/')) {
      return { path: to.path.slice(0, -1), query: to.query, replace: true };
    }
    return true;
  });
}
