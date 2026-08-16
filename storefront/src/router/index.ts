import { createRouter, createWebHistory } from 'vue-router';
import Home from '@/views/Home.vue';

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: Home },
    { path: '/product/:id', name: 'product', component: () => import('@/views/ProductDetail.vue') },
    { path: '/payment/:orderNo', name: 'payment', component: () => import('@/views/Payment.vue') },
    { path: '/fetch', name: 'fetch', component: () => import('@/views/Fetch.vue') },
    { path: '/member', name: 'member', component: () => import('@/views/Member.vue') },
    { path: '/posts', name: 'posts', component: () => import('@/views/Posts.vue') },
    { path: '/posts/:slug', name: 'post-detail', component: () => import('@/views/PostDetail.vue') }
  ]
});

export default router;
