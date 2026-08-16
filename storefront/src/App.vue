<template>
  <div class="app">
    <header class="topbar">
      <router-link to="/" class="logo">ZCard 商店</router-link>
      <nav>
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
</script>
