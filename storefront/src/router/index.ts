import { createRouter, createWebHistory } from 'vue-router';
import type { Router, RouterScrollBehavior } from 'vue-router';
import { refreshCartSetting } from '@/cart';
import { getToken } from '@/api/client';
import Home from '@/views/Home.vue';

// meta.auth：需登录页面（）——无 token 跳 /login 带回跳。
export const routes = [
  { path: '/', name: 'home', component: Home },
  { path: '/products', name: 'products', component: () => import('@/views/Products.vue') },
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

export const scrollBehavior: RouterScrollBehavior = (to, _from, savedPosition) => {
  // 目录由 useCatalogScroll 在缓存组件激活后恢复，避免被尚未移除的详情页高度截断。
  if (to.name === 'home' || to.name === 'products') return false;
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
  router.beforeEach(async (to) => {
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
