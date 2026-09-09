<template>
  <!-- 大厂个人中心分段式导航：白卡容器 + 胶囊选中态（与首页卡片视觉同语言） -->
  <nav ref="navEl" class="member-tabs" aria-label="会员中心导航">
    <button
      v-for="t in tabs"
      :key="t.key"
      :class="{ active: active === t.key }"
      :aria-current="active === t.key ? 'page' : undefined"
      type="button"
      @click="go(t.key)"
    >{{ t.label }}</button>
  </nav>
</template>

<script setup lang="ts">
// 个人中心共用导航（Member 与 Withdraw 两页共享）：
// 提现是独立路由页（/withdraw），其余为 /member 页内 tab（?tab= 查询参数驱动）。

import { ref, watch, nextTick, onMounted, onBeforeUnmount } from 'vue';
import { useRouter } from 'vue-router';

const props = defineProps<{ active?: string }>();
const navEl = ref<HTMLElement | null>(null);
const scrollKey = "zcard-member-nav-scroll";
function remember() { if (navEl.value) { try { sessionStorage.setItem(scrollKey, String(navEl.value.scrollLeft)); } catch { /* optional storage */ } } }
async function revealActive(smooth = true) {
  await nextTick();
  const nav = navEl.value, item = nav?.querySelector<HTMLElement>('[aria-current="page"]');
  if (!nav || !item) return;
  const box = item.getBoundingClientRect(), bounds = nav.getBoundingClientRect();
  if (box.left < bounds.left + 6 || box.right > bounds.right - 6) {
    nav.scrollTo({ left: nav.scrollLeft + box.left - bounds.left - (bounds.width - box.width) / 2, behavior: smooth && !matchMedia("(prefers-reduced-motion: reduce)").matches ? "smooth" : "auto" });
  }
}
watch(() => props.active, () => revealActive());
onMounted(() => {
  try { if (navEl.value) navEl.value.scrollLeft = Number(sessionStorage.getItem(scrollKey)) || 0; } catch { /* optional storage */ }
  void revealActive(false);
  navEl.value?.addEventListener("scroll", remember, { passive: true });
  window.addEventListener("resize", onResize);
});
function onResize() { void revealActive(false); }
onBeforeUnmount(() => { remember(); navEl.value?.removeEventListener("scroll", remember); window.removeEventListener("resize", onResize); });

const router = useRouter();

const tabs = [
  { key: 'overview', label: '总览' },
  { key: 'orders', label: '我的订单' },
  { key: 'transactions', label: '余额流水' },
  { key: 'recharge', label: '充值' },
  { key: 'giftcard', label: '礼品卡' },
  { key: 'points', label: '积分商城' },
  { key: 'promo', label: '推广营销' },
  { key: 'supplier', label: '对接申请' },
  { key: 'withdraw', label: '提现' },
  { key: 'security', label: '账户安全' },
  { key: 'tickets', label: '提交工单' },
];

function go(key: string) {
  remember();
  if (key === props.active) return;
  if (key === "tickets") { router.push("/tickets"); return; }
  if (key === 'withdraw') {
    router.push('/withdraw');
    return;
  }
  // 积分商城同为独立路由页（/points），不占 /member 页内 tab
  if (key === 'points') {
    router.push('/points');
    return;
  }
  router.push(key === 'overview' ? '/member' : { path: '/member', query: { tab: key } });
}
</script>

<style scoped>
.member-tabs {
  display: flex; align-items: center; gap: 2px;
  background: #fff; border: 1px solid #e5e6e8; border-radius: 12px;
  padding: 6px; margin-bottom: 16px;
  overflow-x: auto; -webkit-overflow-scrolling: touch; scrollbar-width: none;
}
.member-tabs::-webkit-scrollbar { display: none; }
.member-tabs button {
  flex-shrink: 0; min-height: 44px; padding: 8px 16px; border: none; background: none; cursor: pointer;
  border-radius: 8px; font-size: 14px; color: #4b5563; font-family: inherit;
  transition: background 0.15s, color 0.15s; white-space: nowrap;
}
.member-tabs button:hover:not(.active) { color: #2563eb; background: #f0f6ff; }
.member-tabs button.active { background: #2563eb; color: #fff; font-weight: 600; }
</style>
