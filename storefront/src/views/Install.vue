<template>
  <div class="install-page">
    <div class="install-card">
      <!-- 头部 -->
      <div class="install-hero">
        <span class="install-logo">ZC</span>
        <div>
          <h1>ZCard 商城系统</h1>
          <div class="muted">在线安装向导 · {{ status?.version || '...' }}</div>
        </div>
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="install-body muted">正在检测安装状态…</div>

      <!-- 已安装 -->
      <div v-else-if="status?.installed" class="install-body">
        <div class="done-icon">🔒</div>
        <div class="done-title">系统已安装</div>
        <div class="muted">
          安装时间：{{ status.installed_at ? new Date(status.installed_at).toLocaleString() : '-' }}<br />
          如需重新安装，请清空数据库后重启服务。
        </div>
        <div class="done-actions">
          <router-link class="btn" to="/">进入前台</router-link>
          <router-link class="btn secondary" to="/login">会员登录</router-link>
        </div>
      </div>

      <!-- 未安装：向导 -->
      <div v-else class="install-body">
        <!-- 环境自检 -->
        <div class="section">
          <div class="section-title">环境检查</div>
          <div class="check-grid">
            <div class="check-item ok">
              <span class="check-dot"></span>
              <span>数据库（{{ dialectLabel }}）</span>
              <span class="check-val">{{ status?.database_ok ? '已连接' : '—' }}</span>
            </div>
            <div class="check-item ok">
              <span class="check-dot"></span>
              <span>数据表迁移</span>
              <span class="check-val">{{ status?.migrations_ok ? '已就绪' : '—' }}</span>
            </div>
            <div class="check-item ok">
              <span class="check-dot"></span>
              <span>存储引擎</span>
              <span class="check-val">内嵌零依赖</span>
            </div>
          </div>
          <div class="hint">
            🎉 本系统内嵌数据库，<b>无需配置 MySQL / Redis</b>（Redis 可选，缺失自动降级进程内队列）。
            如需 MySQL / PostgreSQL，请在启动前修改 config.yaml 后再运行本向导。
          </div>
        </div>

        <!-- 管理员表单 -->
        <div class="section">
          <div class="section-title">设置管理员</div>
          <div class="form-row">
            <label>管理员用户名</label>
            <input v-model="form.admin_username" class="input" placeholder="admin" maxlength="32" />
          </div>
          <div class="form-row">
            <label>管理员密码<span class="req">*</span>（≥8 位）</label>
            <div class="pw-wrap">
              <input v-model="form.admin_password" class="input" :type="showPwd ? 'text' : 'password'" placeholder="至少 8 位，建议字母+数字组合" />
              <button class="pw-toggle" type="button" @click="showPwd = !showPwd">{{ showPwd ? '隐藏' : '显示' }}</button>
            </div>
            <button v-if="!form.admin_password" class="pw-gen" type="button" @click="form.admin_password = genPassword()">帮我生成强密码</button>
          </div>
          <div class="form-row">
            <label>站点名称</label>
            <input v-model="form.site_name" class="input" placeholder="ZCard 商店" maxlength="50" />
          </div>
          <div class="form-row">
            <label>站点网址（选填）</label>
            <input v-model="form.site_url" class="input" :placeholder="origin" maxlength="200" />
          </div>
          <div v-if="error" class="error">{{ error }}</div>
          <button class="btn install-btn" :disabled="submitting || !form.admin_username || !form.admin_password" @click="submit">
            {{ submitting ? '安装中…' : '开始安装' }}
          </button>
        </div>
      </div>

      <!-- 安装成功 -->
      <div v-if="done" class="install-body">
        <div class="done-icon">✅</div>
        <div class="done-title">安装完成！</div>
        <div class="muted">
          管理员：<b>{{ form.admin_username }}</b><br />
          后台入口：<code>/admin</code>（用上方账号登录）<br />
          <span class="pw-hint">请妥善保管管理员密码——系统不存储明文，丢失只能重置。</span>
        </div>
        <div class="done-actions">
          <router-link class="btn" to="/">进入前台</router-link>
        </div>
      </div>
    </div>
    <div class="install-foot muted">© ZCard · 单二进制部署 · SQLite 零依赖起步</div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';

const loading = ref(true);
const status = ref<{ installed: boolean; dialect: string; version: string; database_ok: boolean; migrations_ok: boolean; installed_at: string } | null>(null);
const form = ref({ admin_username: 'admin', admin_password: '', site_name: '', site_url: '' });
const showPwd = ref(false);
const submitting = ref(false);
const error = ref('');
const done = ref(false);
const origin = typeof location !== 'undefined' ? location.origin : '';

const dialectLabel = ({ sqlite: 'SQLite（内嵌）', mysql: 'MySQL', postgres: 'PostgreSQL' } as any)[status.value?.dialect || 'sqlite'] || 'SQLite（内嵌）';

function genPassword() {
  const chars = 'ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789';
  let s = '';
  const arr = new Uint32Array(12);
  crypto.getRandomValues(arr);
  for (const n of arr) s += chars[n % chars.length];
  return s;
}

async function load() {
  loading.value = true;
  try {
    // 安装状态接口在 admin 前缀（公开），直接 fetch 绕过 storefront BASE
    const res = await fetch('/api/v1/admin/install/status');
    status.value = await res.json();
  } finally {
    loading.value = false;
  }
}

async function submit() {
  error.value = '';
  if (form.value.admin_password.length < 8) {
    error.value = '管理员密码至少 8 位';
    return;
  }
  submitting.value = true;
  try {
    // 安装接口在 admin 前缀（公开），直接 fetch 绕过 storefront BASE
    const res = await fetch('/api/v1/admin/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        admin_username: form.value.admin_username.trim(),
        admin_password: form.value.admin_password,
        site_name: form.value.site_name.trim() || 'ZCard 商店',
        site_url: form.value.site_url.trim() || origin,
      }),
    });
    const json = await res.json();
    if (!res.ok) {
      error.value = json?.message || '安装失败，请重试';
      return;
    }
    done.value = true;
  } catch (e: any) {
    error.value = e?.message || '网络错误';
  } finally {
    submitting.value = false;
  }
}

onMounted(load);
</script>

<style scoped>
.install-page {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  background: linear-gradient(160deg, #eff6ff 0%, #f8fafc 60%, #eef2ff 100%);
  padding: 24px;
}
.install-card {
  width: 100%;
  max-width: 520px;
  background: #fff;
  border-radius: 18px;
  box-shadow: 0 24px 64px rgba(15, 23, 42, 0.12);
  overflow: hidden;
}
.install-hero {
  display: flex;
  gap: 14px;
  align-items: center;
  padding: 26px 28px 18px;
  border-bottom: 1px solid #f1f5f9;
}
.install-logo {
  width: 52px; height: 52px; border-radius: 14px;
  background: linear-gradient(135deg, #2563eb, #1e40af);
  color: #fff; font-weight: 800; font-size: 22px;
  display: flex; align-items: center; justify-content: center;
}
.install-hero h1 { font-size: 20px; margin: 0 0 2px; color: #0f172a; }
.install-body { padding: 22px 28px 28px; }
.section { margin-bottom: 20px; }
.section-title { font-weight: 700; font-size: 14px; color: #1f2937; margin-bottom: 10px; }
.check-grid { display: flex; flex-direction: column; gap: 8px; }
.check-item {
  display: flex; align-items: center; gap: 8px; font-size: 13.5px; color: #374151;
  background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 8px 12px;
}
.check-dot { width: 8px; height: 8px; border-radius: 50%; background: #22c55e; flex-shrink: 0; }
.check-val { margin-left: auto; color: #64748b; font-size: 12.5px; }
.hint {
  margin-top: 10px; font-size: 12.5px; color: #7c2d12; line-height: 1.7;
  background: #fff7ed; border: 1px solid #fed7aa; border-radius: 8px; padding: 10px 12px;
}
.form-row { margin-bottom: 12px; }
.form-row label { display: block; font-size: 13px; color: #374151; margin-bottom: 6px; font-weight: 600; }
.req { color: #dc2626; margin-left: 2px; }
.pw-wrap { display: flex; gap: 8px; }
.pw-toggle {
  border: 1px solid #e5e7eb; background: #f9fafb; border-radius: 8px;
  padding: 0 12px; font-size: 12.5px; color: #6b7280; cursor: pointer; white-space: nowrap;
}
.pw-gen {
  margin-top: 6px; border: none; background: none; color: #2563eb;
  font-size: 12.5px; cursor: pointer; padding: 0;
}
.pw-gen:hover { text-decoration: underline; }
.install-btn { width: 100%; margin-top: 6px; padding: 12px; font-size: 15px; }
.error { color: #dc2626; font-size: 13px; margin: 8px 0; }
.done-icon { font-size: 46px; text-align: center; margin-top: 8px; }
.done-title { text-align: center; font-size: 18px; font-weight: 700; color: #0f172a; margin: 8px 0 10px; }
.done-body { text-align: center; }
.done-actions { display: flex; gap: 10px; justify-content: center; margin-top: 18px; }
.pw-hint { color: #b45309; font-size: 12.5px; }
.install-foot { margin-top: 18px; font-size: 12px; }
code { background: #f1f5f9; border-radius: 4px; padding: 1px 6px; }
</style>
