import { createRouter, createWebHistory } from 'vue-router';
import { getToken } from '@/api/client';
import Home from '@/views/Home.vue';

// meta.auth：需登录页面（P3-09 T1）——无 token 跳 /login 带回跳。
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: Home },
    { path: '/products', name: 'products', component: () => import('@/views/Products.vue') },
    { path: '/product/:id', name: 'product', component: () => import('@/views/ProductDetail.vue') },
    { path: '/payment/:orderNo', name: 'payment', component: () => import('@/views/Payment.vue') },
    { path: '/order/:orderNo', name: 'order-detail', component: () => import('@/views/OrderDetail.vue'), meta: { auth: true } },
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
  ]
});

router.beforeEach((to) => {
  if (to.meta.auth && !getToken()) {
    return { path: '/login', query: { redirect: to.fullPath } };
  }
  return true;
});

export default router;
