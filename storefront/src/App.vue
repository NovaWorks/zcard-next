<template>
  <div class="app">
    <!-- 顶部品牌条（深蓝渐变；信任点可后台配置） -->
    <div class="brand-bar">
      <span class="brand-slogan">🎁 {{ siteName }} · 自动发货 秒速到账</span>
      <span class="brand-trust">
        <span>✓ 安全支付</span>
        <span>✓ 隐私保障</span>
        <span>✓ 售后无忧</span>
      </span>
    </div>

    <!-- 主导航（sticky） -->
    <header class="topbar">
      <router-link to="/" class="logo">
        <span class="logo-mark">ZC</span>
        <span class="logo-name">{{ siteName }}</span>
      </router-link>
      <nav class="nav-links">
        <router-link to="/" exact>首页</router-link>
        <router-link to="/products">全部商品</router-link>
        <router-link to="/points">积分商城</router-link>
        <button class="nav-horn" @click="openNotice" title="系统公告">📢 公告</button>
        <router-link to="/fetch">取货查询</router-link>
      </nav>
      <div class="nav-right">
        <router-link to="/cart" class="cart-link" title="查看购物车">
          <span class="cart-icon">🛒</span>
          <span class="cart-label">购物车</span>
          <span v-if="cartCount > 0" :key="cartCount" class="cart-badge">{{ cartCount > 99 ? '99+' : cartCount }}</span>
        </router-link>
        <template v-if="authState.loggedIn">
          <router-link to="/member" class="member-link" title="个人中心">👤 个人中心</router-link>
          <router-link to="/member" class="user-link">{{ authState.username }}</router-link>
          <button class="logout-btn" @click="onLogout">退出</button>
        </template>
        <template v-else>
          <router-link to="/login" class="login-link">登录</router-link>
          <router-link to="/register" class="btn btn-primary">注册</router-link>
        </template>
      </div>
    </header>

    <main class="main">
      <!-- Suspense：路由组件 async setup（SSG 预取）在客户端水合时也能正常等待 -->
      <router-view v-slot="{ Component }">
        <Suspense>
          <component :is="Component" />
        </Suspense>
      </router-view>
    </main>

    <!-- 页脚 -->
    <footer class="footer">
      <div class="footer-trust">
        <div class="trust-item"><span class="trust-icon">⚡</span><div><b>极速发货</b><span class="muted">下单即自动发货</span></div></div>
        <div class="trust-item"><span class="trust-icon">🛡️</span><div><b>正品保障</b><span class="muted">渠道直供货源</span></div></div>
        <div class="trust-item"><span class="trust-icon">💬</span><div><b>在线客服</b><span class="muted">7×24 小时响应</span></div></div>
        <div class="trust-item"><span class="trust-icon">↩️</span><div><b>售后无忧</b><span class="muted">问题订单快速处理</span></div></div>
      </div>
      <div class="footer-cols">
        <div class="footer-col">
          <div class="footer-brand">
            <span class="logo-mark">ZC</span>
            <span class="footer-name">{{ siteName }}</span>
          </div>
          <p class="muted">专业的自动发卡商城系统，为你的数字商品交易保驾护航。</p>
        </div>
        <div class="footer-col">
          <h4>快速导航</h4>
          <router-link to="/products">全部商品</router-link>
          <router-link to="/points">积分商城</router-link>
          <router-link to="/coupons">优惠券</router-link>
          <router-link to="/affiliate">推广中心</router-link>
        </div>
        <div class="footer-col">
          <h4>帮助中心</h4>
          <router-link to="/posts?type=notice">系统公告</router-link>
          <router-link to="/posts?type=article">使用帮助</router-link>
          <router-link to="/tickets">提交工单</router-link>
          <router-link to="/fetch">订单取货</router-link>
        </div>
        <div class="footer-col">
          <h4>会员服务</h4>
          <router-link to="/member">会员中心</router-link>
          <router-link to="/member?tab=orders">我的订单</router-link>
          <router-link to="/member?tab=recharge">余额充值</router-link>
          <router-link to="/member?tab=giftcard">礼品卡兑换</router-link>
        </div>
      </div>
      <div class="footer-copy">© {{ year }} {{ siteName }} · 保留所有权利</div>
    </footer>

    <!-- 回到顶部悬浮球 -->
    <button v-if="showTop" class="back-top" @click="scrollTop" title="回到顶部">↑</button>

    <!-- 右下角客服（第三方脚本原生气泡 / 本站链接浮窗） -->
    <ServiceWidget />

    <!-- 公告弹窗（首次访问自动弹出 + 导航喇叭/首页公告轮播入口） -->
    <NoticeModal :show="noticeShow" :post="noticePost" :content="noticeContent" :announcement="noticeAnnouncement" @update:show="(v: boolean) => (noticeShow = v)" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import { initCurrency } from '@/api/client';
import { authState, refreshAuth, logout } from '@/auth';
import { listPosts, getPost, fetchAnnouncement, type StorePost, type AnnouncementConfig } from '@/api';
import { mergeGuestCart, refreshCartState, cartState } from '@/cart';
import { captureRefCode } from '@/ref';
import NoticeModal from '@/components/NoticeModal.vue';
import ServiceWidget from '@/components/ServiceWidget.vue';

const router = useRouter();

// 站点名（config 下发；失败回退）
const siteName = ref('ZCard 商店');

// SEO 默认 head 由 main.ts 处理（客户端拉取后更新；SSR 渲染后输出到静态 HTML）

// 启动加载默认货币（i18n.base_currency → 符号/小数位；失败回退默认符号）
initCurrency();
// 登录态恢复（token 存在则拉用户名；失败静默——401 层统一处理）
refreshAuth();

const year = new Date().getFullYear();

// 购物车角标：共享响应式状态（商品页加购/删除实时联动；:key 变化触发弹跳动画）
const cartCount = computed(() => cartState.value.count);
// 登录/登出后重拉（登录合并本地车、登出回退本地车）
watch(() => authState.loggedIn, () => { refreshCartState(); });

// 统计代码（service.stats_script）：注入 document.body 末尾——统计代码置底，与客服悬浮球无关
let statsInjected = false;
function injectStatsScript(html: string) {
  if (statsInjected) return;
  statsInjected = true;
  try {
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const scripts = Array.from(doc.querySelectorAll('script'));
    if (!scripts.length) return;
    for (const s of scripts) {
      const code = s.textContent || '';
      if (!code.trim()) continue;
      const el = document.createElement('script');
      el.textContent = code;
      el.dataset.zcardStatsScript = 'true';
      document.body.appendChild(el); // 页面最底部
    }
  } catch { /* 统计脚本注入失败忽略 */ }
}

// 回到顶部
const showTop = ref(false);
function onScroll() {
  showTop.value = window.scrollY > 300;
}
function scrollTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' });
}

// ── 公告弹窗（设置公告优先；回落最新公告文章；首次访问自动弹出 + 导航喇叭/首页轮播随时重开）──
const noticeShow = ref(false);
const noticePost = ref<StorePost | null>(null);
const noticeContent = ref('');
const noticeAnnouncement = ref<AnnouncementConfig | null>(null);

async function loadNotice() {
  // 设置公告（ops.announcement）：text/image/carousel 任一配置生效即优先
  const ann = await fetchAnnouncement();
  if ((ann.type === 'text' && ann.text) || ann.images.length) {
    noticeAnnouncement.value = ann;
    noticePost.value = null;
    noticeContent.value = '';
    return;
  }
  noticeAnnouncement.value = null;
  const { data } = await listPosts('notice', 1, 1);
  const post = data?.posts?.[0];
  if (!post) return;
  noticePost.value = post;
  const { data: detail } = await getPost(post.slug).catch(() => ({ data: null }));
  noticeContent.value = detail?.content || '';
}

function openNotice() {
  if (noticePost.value || noticeAnnouncement.value) noticeShow.value = true;
  else {
    // 尚未加载（或失败）：静默拉一次再弹
    loadNotice().then(() => { noticeShow.value = true; });
  }
}

onMounted(async () => {
  captureRefCode(); // 推广归因捕获（任何页面 ?ref= 进站即记 30 天）
  // 站点配置（service.stats_script 统计代码；SEO 配置已在 setup 顶层消费）
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    const stats = find('service.stats_script');
    if (stats) {
      let script = '';
      try { script = JSON.parse(stats); } catch { script = stats; }
      if (typeof script === 'string' && script.trim()) injectStatsScript(script);
    }
  } catch { /* 配置接口失败保留默认 */ }
  refreshCartState();
  window.addEventListener('scroll', onScroll);
  // 登录态：合并本地游客购物车到后端（游客加购的商品登录后自动同步；merge 内部会刷新角标）
  if (authState.loggedIn) {
    await mergeGuestCart();
  }
  // 公告：每会话首次访问自动弹出（sessionStorage 标记）
  await loadNotice();
  if ((noticePost.value || noticeAnnouncement.value) && !sessionStorage.getItem('zc_notice_shown')) {
    sessionStorage.setItem('zc_notice_shown', '1');
    noticeShow.value = true;
  }
  // 首页公告轮播点击 → 打开弹窗
  window.addEventListener('zcard-open-notice', openNotice);
});
onUnmounted(() => {
  window.removeEventListener('scroll', onScroll);
  window.removeEventListener('zcard-open-notice', openNotice);
});

function onLogout() {
  logout();
  router.push('/');
}
</script>
