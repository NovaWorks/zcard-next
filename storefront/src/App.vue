<template>
  <div class="app">
    <header class="topbar">
      <router-link to="/" class="logo">
        <img src="./assets/logo.png" alt="ZCard" class="logo-img" />
        <span>ZCard 商店</span>
      </router-link>
      <nav>
        <router-link to="/">首页</router-link>
        <router-link to="/posts">文章</router-link>
        <router-link to="/fetch">取货</router-link>
        <router-link to="/member">会员中心</router-link>
      </nav>
    </header>
    <main class="main">
      <router-view />
    </main>
  </div>
</template>

<script setup lang="ts">
import { initCurrency } from '@/api/client';

// 启动加载默认货币（i18n.base_currency → 符号/小数位；失败回退默认符号）
initCurrency();
</script>
