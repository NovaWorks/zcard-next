<template>
  <div class="app">
    <header class="topbar">
      <router-link to="/" class="logo">
        <img src="./assets/logo.png" alt="ZCard" class="logo-img" />
        <span>ZCard 商店</span>
      </router-link>
      <nav>
        <router-link to="/">首页</router-link>
        <router-link to="/points">积分商城</router-link>
        <router-link to="/posts">文章</router-link>
        <router-link to="/fetch">取货</router-link>
        <template v-if="authState.loggedIn">
          <router-link to="/cart">购物车</router-link>
          <router-link to="/member">会员中心</router-link>
          <router-link to="/tickets">工单</router-link>
          <router-link to="/coupons">优惠券</router-link>
          <router-link to="/affiliate">分销</router-link>
        </template>
      </nav>
      <div class="user-box">
        <template v-if="authState.loggedIn">
          <span>{{ authState.username }}</span>
          <button @click="onLogout">退出</button>
        </template>
        <template v-else>
          <router-link to="/login">登录</router-link>
          <router-link to="/register">注册</router-link>
        </template>
      </div>
    </header>
    <main class="main">
      <router-view />
    </main>
  </div>
</template>

<script setup lang="ts">
import { initCurrency } from '@/api/client';
import { authState, refreshAuth, logout } from '@/auth';
import { useRouter } from 'vue-router';

const router = useRouter();

// 启动加载默认货币（i18n.base_currency → 符号/小数位；失败回退默认符号）
initCurrency();
// 登录态恢复（token 存在则拉用户名；失败静默——401 层统一处理）
refreshAuth();

function onLogout() {
  logout();
  router.push('/');
}
</script>
