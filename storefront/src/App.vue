<template>
  <div class="app">
    <!-- 安装页：无商城布局（头部/尾部/客服/公告全隐藏，仅渲染向导自身） -->
    <template v-if="!isInstall">
    <!-- 顶部品牌条（深蓝渐变；信任点可后台配置） -->
    <div class="brand-bar">
      <span class="brand-slogan">🎁 {{ siteName }} · 自动发货 秒速到账</span>
      <span class="brand-trust">
        <span>✓ 安全支付</span>
        <span>✓ 隐私保障</span>
        <span>✓ 售后无忧</span>
      </span>
    </div>

    <!-- 维护模式：横幅样式（ops.maintenance_style=banner） -->
    <div v-if="maintenance && maintenanceStyle === 'banner'" class="maint-banner">
      🔧 站点维护中，下单与支付可能间歇性不可用，请稍后再试
    </div>

    <!-- 维护模式：弹窗样式（ops.maintenance_style=modal；ops.maintenance_modal_freq 控制频率
         every=每次进入都弹 / daily=24 小时内只弹一次，关闭后本次会话不再打扰） -->
    <div v-if="maintenance && maintenanceStyle === 'modal' && maintModalVisible" class="maint-overlay">
      <div class="maint-card">
        <div class="maint-icon">🔧</div>
        <h2>站点维护中</h2>
        <p>我们正在升级系统，下单与支付可能间歇性不可用。<br />给您带来不便敬请谅解，请稍后再试。</p>
        <button class="maint-btn" @click="closeMaintModal">我知道了，继续浏览</button>
      </div>
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
        <!-- 顶部自定义按钮：外部链接新窗口；文章/公告站内路由（site.top_button {text,type,url|slug}） -->
        <router-link
          v-if="topButton && topButton.type !== 'link' && topButton.slug"
          :to="`/posts/${topButton.slug}`"
          class="nav-custom-btn"
        >{{ topButton.text }}</router-link>
        <a
          v-else-if="topButton"
          :href="topButton.url || '#'"
          :target="topButton.url ? '_blank' : undefined"
          rel="noopener noreferrer"
          class="nav-custom-btn"
        >{{ topButton.text }}</a>
        <!-- 导航推荐位（promo.nav_recommend [{text,url}]）：站内路径走路由，外链新窗口 -->
        <template v-for="r in navRecommend" :key="r.text + r.url">
          <router-link v-if="r.url.startsWith('/')" :to="r.url" class="nav-recommend-btn">🔥 {{ r.text }}</router-link>
          <a v-else :href="r.url" target="_blank" rel="noopener noreferrer" class="nav-recommend-btn">🔥 {{ r.text }}</a>
        </template>
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
    </template>

    <main class="main" :class="{ 'install-main': isInstall }">
      <!-- Suspense：路由组件 async setup（SSG 预取）在客户端水合时也能正常等待 -->
      <router-view v-slot="{ Component }">
        <Suspense>
          <component :is="Component" />
        </Suspense>
      </router-view>
    </main>

    <!-- 页脚（安装页隐藏；页脚配置 footer.* 可覆盖关于/导航/联系/社交/备案） -->
    <template v-if="!isInstall">
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
          <p class="muted">{{ footerAbout || '专业的自动发卡商城系统，为你的数字商品交易保驾护航。' }}</p>
          <!-- 社交链接（footer.social = [{icon,url}]，配置后显示） -->
          <div v-if="footerSocial.length" class="footer-social">
            <a v-for="(s, i) in footerSocial" :key="i" :href="s.url || '#'" target="_blank" rel="noopener noreferrer" class="footer-social-link" :title="s.url">{{ s.icon }}</a>
          </div>
        </div>
        <div class="footer-col">
          <h4>快速导航</h4>
          <template v-if="footerNav.length">
            <a v-for="(n, i) in footerNav" :key="i" :href="n.url || '#'">{{ n.text }}</a>
          </template>
          <template v-else>
            <router-link to="/products">全部商品</router-link>
            <router-link to="/points">积分商城</router-link>
            <router-link to="/coupons">优惠券</router-link>
            <router-link to="/affiliate">推广中心</router-link>
          </template>
        </div>
        <div class="footer-col">
          <h4>帮助中心</h4>
          <router-link to="/posts?type=notice">系统公告</router-link>
          <router-link to="/posts?type=blog">使用帮助</router-link>
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
        <div v-if="footerContact" class="footer-col">
          <h4>联系我们</h4>
          <p class="muted" style="white-space: pre-line;">{{ footerContact }}</p>
        </div>
      </div>
      <div class="footer-copy">
        © {{ year }} {{ siteName }} · 保留所有权利
        <a v-if="agreementHref" :href="agreementHref" :target="agreementHref.startsWith('http') ? '_blank' : undefined" rel="noopener noreferrer" class="footer-copy-link">用户协议</a>
        <a v-if="footerIcp" href="https://beian.miit.gov.cn/" target="_blank" rel="noopener noreferrer" class="footer-copy-link">{{ footerIcp }}</a>
      </div>
    </footer>

    <!-- 回到顶部悬浮球 -->
    <button v-if="showTop" class="back-top" @click="scrollTop" title="回到顶部">↑</button>

    <!-- 右下角客服（第三方脚本原生气泡 / 本站链接浮窗） -->
    <ServiceWidget />

    <!-- 公告弹窗（首次访问自动弹出 + 导航喇叭/首页公告轮播入口） -->
    <NoticeModal :show="noticeShow" :post="noticePost" :content="noticeContent" :announcement="noticeAnnouncement" @update:show="(v: boolean) => (noticeShow = v)" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { initCurrency } from '@/api/client';
import { authState, refreshAuth, logout } from '@/auth';
import { listPosts, getPost, fetchAnnouncement, type StorePost, type AnnouncementConfig } from '@/api';
import { mergeGuestCart, refreshCartState, cartState } from '@/cart';
import { captureRefCode } from '@/ref';
import NoticeModal from '@/components/NoticeModal.vue';
import ServiceWidget from '@/components/ServiceWidget.vue';

const router = useRouter();
const route = useRoute();

// 安装页：无商城布局（头部/尾部/客服/公告等全部不渲染，见模板 v-if）
const isInstall = computed(() => route.path === '/install');

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

// ── 顶部自定义按钮（site.top_button 公开下发）+ 维护模式（ops.maintenance[_style]）──
// top_button：{text, type: link|post|notice, url?|slug?}——link 外链新窗口；post/notice 站内文章路由
const topButton = ref<{ text: string; type?: string; url?: string; slug?: string } | null>(null);
const navRecommend = ref<{ text: string; url: string }[]>([]);
const maintenance = ref(false);
const maintenanceStyle = ref('modal'); // modal=全屏遮罩 | banner=顶部横幅
const maintenanceModalFreq = ref('every'); // every=每次进入都弹 | daily=24 小时一次

// ── 页脚配置（footer.* 公开下发：about/nav/social/contact/agreement/icp，空值回落默认）──
const footerAbout = ref('');
const footerNav = ref<{ text: string; url: string }[]>([]);
const footerSocial = ref<{ icon: string; url: string }[]>([]);
const footerContact = ref('');
const footerAgreement = ref('');
const footerIcp = ref('');
// 协议链接：http(s) 外链原样；非空文本视为文章 slug → /posts/<slug>
const agreementHref = computed(() => {
  const v = footerAgreement.value.trim();
  if (!v) return '';
  if (/^https?:\/\//.test(v)) return v;
  return `/posts/${v}`;
});

// 维护弹窗显隐：daily 频率下 24 小时窗口用 localStorage 标记；关闭即记时间戳
const maintModalVisible = ref(false);
const MAINT_SHOWN_KEY = 'zc_maint_shown_at';

function maintShownRecently(): boolean {
  try {
    const ts = Number(localStorage.getItem(MAINT_SHOWN_KEY) || 0);
    return ts > 0 && Date.now() - ts < 24 * 60 * 60 * 1000;
  } catch {
    return false;
  }
}

function closeMaintModal() {
  maintModalVisible.value = false;
  try {
    localStorage.setItem(MAINT_SHOWN_KEY, String(Date.now()));
  } catch { /* 隐私模式忽略 */ }
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
  // 安装页：不加载商城业务（购物车/公告/统计/回顶监听等）
  if (isInstall.value) return;
  captureRefCode(); // 推广归因捕获（任何页面 ?ref= 进站即记 30 天）
  // 站点配置（service.stats_script 统计代码；SEO 配置已在 setup 顶层消费）
  try {
    const resp = await fetch('/api/v1/storefront/config');
    const json = await resp.json();
    const find = (k: string) => json?.entries?.find((e: any) => e.key === k)?.value_json;
    // 顶部自定义按钮 {text,type,url|slug}
    const tb = find('site.top_button');
    if (tb) {
      try {
        const v = JSON.parse(tb);
        if (v && typeof v.text === 'string' && v.text.trim()) {
          topButton.value = {
            text: v.text.trim(),
            type: v.type === 'post' || v.type === 'notice' ? v.type : 'link',
            url: typeof v.url === 'string' ? v.url.trim() : '',
            slug: typeof v.slug === 'string' ? v.slug.trim() : '',
          };
        }
      } catch { /* 非法配置忽略 */ }
    }
    // 导航推荐位（promo.nav_recommend [{text,url}]：最多取 3 条，非法结构忽略）
    const nr = find('promo.nav_recommend');
    if (nr) {
      try {
        const v = JSON.parse(nr);
        if (Array.isArray(v)) {
          navRecommend.value = v
            .filter((x: any) => x && typeof x.text === 'string' && x.text.trim() && typeof x.url === 'string' && x.url.trim())
            .slice(0, 3)
            .map((x: any) => ({ text: x.text.trim(), url: x.url.trim() }));
        }
      } catch { /* 非法配置忽略 */ }
    }
    // 页脚配置（footer.*：空值回落模板默认）
    const fa = find('footer.about');
    if (fa) { try { footerAbout.value = String(JSON.parse(fa) || ''); } catch { /* ignore */ } }
    const fn = find('footer.nav');
    if (fn) {
      try {
        const v = JSON.parse(fn);
        if (Array.isArray(v)) footerNav.value = v.filter((x: any) => x && typeof x.text === 'string' && typeof x.url === 'string');
      } catch { /* ignore */ }
    }
    const fs = find('footer.social');
    if (fs) {
      try {
        const v = JSON.parse(fs);
        if (Array.isArray(v)) footerSocial.value = v.filter((x: any) => x && typeof x.icon === 'string' && typeof x.url === 'string');
      } catch { /* ignore */ }
    }
    const fc = find('footer.contact');
    if (fc) { try { footerContact.value = String(JSON.parse(fc) || ''); } catch { /* ignore */ } }
    const fag = find('footer.agreement');
    if (fag) { try { footerAgreement.value = String(JSON.parse(fag) || ''); } catch { /* ignore */ } }
    const ficp = find('footer.icp');
    if (ficp) { try { footerIcp.value = String(JSON.parse(ficp) || ''); } catch { /* ignore */ } }
    // 维护模式与样式（modal=遮罩弹窗 / banner=顶部横幅）；弹窗频率 daily=24h 一次
    const mt = find('ops.maintenance');
    if (mt) { try { maintenance.value = JSON.parse(mt) === true; } catch { /* ignore */ } }
    const ms = find('ops.maintenance_style');
    if (ms) { try { const v = JSON.parse(ms); if (v === 'banner' || v === 'modal') maintenanceStyle.value = v; } catch { /* ignore */ } }
    const mf = find('ops.maintenance_modal_freq');
    if (mf) { try { const v = JSON.parse(mf); if (v === 'every' || v === 'daily') maintenanceModalFreq.value = v; } catch { /* ignore */ } }
    if (maintenance.value && maintenanceStyle.value === 'modal') {
      if (maintenanceModalFreq.value === 'daily' && maintShownRecently()) {
        maintModalVisible.value = false; // 24 小时内已弹过：本次不再打扰
      } else {
        maintModalVisible.value = true;
      }
    }
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
