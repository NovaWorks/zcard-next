<template>
  <div class="install-page">
    <div class="install-card">
      <!-- 头部：无商城品牌，纯安装向导 + 步骤条 -->
      <div class="install-head">
        <h1 class="install-title">安装向导</h1>
        <ol class="steps">
          <li :class="{ active: step === 1, done: step > 1 }">
            <span class="step-no">{{ step > 1 ? '✓' : '1' }}</span> 数据库配置
          </li>
          <li :class="{ active: step === 2, done: step > 2 }">
            <span class="step-no">{{ step > 2 ? '✓' : '2' }}</span> 管理员设置
          </li>
          <li :class="{ active: step === 3 }">
            <span class="step-no">3</span> 完成
          </li>
        </ol>
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="install-body muted">正在检测安装状态…</div>

      <!-- 已安装 -->
      <div v-else-if="status?.installed && !done" class="install-body">
        <div class="done-icon">🔒</div>
        <div class="done-title">系统已安装</div>
        <div class="muted">
          数据库：{{ dialectLabel(status.dialect) }}<br />
          安装时间：{{ status.installed_at ? new Date(status.installed_at).toLocaleString() : '-' }}<br />
          如需重新安装，请清空数据库后重启服务。
        </div>
        <div class="done-actions">
          <router-link class="btn" to="/">进入前台</router-link>
          <router-link class="btn secondary" to="/login">会员登录</router-link>
        </div>
      </div>

      <!-- 库切换重启轮询态 -->
      <div v-else-if="restarting" class="install-body">
        <div class="done-icon spin">⏳</div>
        <div class="done-title">正在切换数据库并重启服务…</div>
        <div class="muted">
          目标库：{{ dialectLabel(form.dialect) }} · 已写入配置，服务自重启后将在新库完成安装。<br />
          已等待 {{ waited }} 秒（通常 10~30 秒；超过 120 秒请查看服务日志）
        </div>
      </div>

      <!-- 安装成功 -->
      <div v-else-if="done" class="install-body">
        <div class="done-icon">✅</div>
        <div class="done-title">安装完成！</div>
        <div class="muted">
          数据库：{{ dialectLabel(doneDialect) }}<br />
          管理员：<b>{{ form.admin_username }}</b><br />
          后台入口：<code>/admin</code>（用上方账号登录）<br />
          <span class="pw-hint">请妥善保管管理员密码——系统不存储明文，丢失只能重置。</span>
        </div>
        <div class="done-actions">
          <router-link class="btn" to="/">进入前台</router-link>
        </div>
      </div>

      <!-- 未安装：分步向导 -->
      <div v-else class="install-body">
        <!-- 步骤① 数据库配置 -->
        <div v-if="step === 1" class="section">
          <div class="section-title">选择数据库</div>
          <div class="dialect-grid">
            <div class="dialect-card" :class="{ active: form.dialect === 'postgres' }" @click="pickDialect('postgres')">
              <div class="dialect-head">
                <b>PostgreSQL</b>
                <span class="rec-badge">推荐</span>
              </div>
              <div class="dialect-desc">完整高级能力：分站多租户 / Schema 隔离 / 高并发稳定，适合正式运营。</div>
            </div>
            <div class="dialect-card" :class="{ active: form.dialect === 'mysql' }" @click="pickDialect('mysql')">
              <div class="dialect-head"><b>MySQL</b></div>
              <div class="dialect-desc">自托管标准形态，分站 Row 级隔离；已有 MySQL 的站长可直接复用实例。</div>
            </div>
            <div class="dialect-card" :class="{ active: form.dialect === 'sqlite' }" @click="pickDialect('sqlite')">
              <div class="dialect-head"><b>SQLite</b><span class="test-badge">本地测试</span></div>
              <div class="dialect-desc">内嵌零依赖，开箱即用。</div>
            </div>
          </div>

          <!-- SQLite 警告 -->
          <div v-if="form.dialect === 'sqlite'" class="warn-box">
            ⚠️ SQLite 仅适用于<b>本地测试 / 功能体验</b>：不支持分站多租户等高级功能，高并发与多进程场景受限。
            正式运营请选择 <b>PostgreSQL（推荐）</b>。
          </div>

          <!-- MySQL / PG 连接表单 -->
          <div v-else class="server-form">
            <div class="form-grid">
              <div class="form-row">
                <label>数据库主机</label>
                <input v-model="form.db_host" class="input" placeholder="127.0.0.1" />
              </div>
              <div class="form-row">
                <label>端口</label>
                <input v-model.number="form.db_port" class="input" type="number" :placeholder="form.dialect === 'mysql' ? '3306' : '5432'" />
              </div>
              <div class="form-row">
                <label>用户名</label>
                <input v-model="form.db_user" class="input" placeholder="root / postgres" />
              </div>
              <div class="form-row">
                <label>密码</label>
                <input v-model="form.db_password" class="input" type="password" placeholder="数据库密码" />
              </div>
              <div class="form-row span2">
                <label>数据库名<span class="req">*</span>（不存在将自动创建）</label>
                <input v-model="form.db_name" class="input" placeholder="zcard" />
              </div>
            </div>
            <!-- Redis（mysql/pg 必填） -->
            <div class="redis-title">Redis <span class="req">*</span><span class="muted slim">（{{ dialectLabel(form.dialect) }} 模式必配；SQLite 模式免配）</span></div>
            <div class="form-grid">
              <div class="form-row">
                <label>Redis 地址</label>
                <input v-model="form.redis_addr" class="input" placeholder="127.0.0.1:6379" />
              </div>
              <div class="form-row">
                <label>Redis 密码（无则留空）</label>
                <input v-model="form.redis_password" class="input" type="password" placeholder="" />
              </div>
            </div>
            <button class="btn secondary test-btn" :disabled="testing" @click="testConnection">
              {{ testing ? '测试中…' : testOk ? '✓ 连接成功（再测一次）' : '测试数据库与 Redis 连接' }}
            </button>
            <div v-if="testMsg" class="test-msg" :class="testOk ? 'ok' : 'bad'">{{ testMsg }}</div>
          </div>

          <div class="nav-row">
            <button class="btn install-btn" :disabled="!canNext" @click="step = 2">下一步</button>
          </div>
        </div>

        <!-- 步骤② 管理员设置 -->
        <div v-else class="section">
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
            <input v-model="form.site_name" class="input" placeholder="站点名称" maxlength="50" />
          </div>
          <div class="form-row">
            <label>站点网址（选填）</label>
            <input v-model="form.site_url" class="input" :placeholder="origin" maxlength="200" />
          </div>
          <div v-if="error" class="error">{{ error }}</div>
          <div class="nav-row">
            <button class="btn secondary" @click="step = 1">上一步</button>
            <button class="btn install-btn" :disabled="submitting || !form.admin_username || !form.admin_password" @click="submit">
              {{ submitting ? '提交中…' : form.dialect === 'sqlite' ? '开始安装' : `切换到 ${dialectLabel(form.dialect)} 并安装` }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';

const loading = ref(true);
const status = ref<{ installed: boolean; dialect: string; version: string; database_ok: boolean; migrations_ok: boolean; installed_at: string } | null>(null);
const step = ref(1);
const form = ref({
  dialect: 'postgres', admin_username: 'admin', admin_password: '', site_name: '', site_url: '',
  db_host: '127.0.0.1', db_port: 5432, db_user: '', db_password: '', db_name: 'zcard',
  redis_addr: '127.0.0.1:6379', redis_password: '',
});
const showPwd = ref(false);
const submitting = ref(false);
const error = ref('');
const done = ref(false);
const doneDialect = ref('sqlite');
const origin = typeof location !== 'undefined' ? location.origin : '';
// 连接测试 / 重启轮询
const testing = ref(false);
const testOk = ref(false);
const testMsg = ref('');
const restarting = ref(false);
const waited = ref(0);
let pollTimer: ReturnType<typeof setInterval> | null = null;

// 下一步可点：PG/MySQL 需已填主机与库名；SQLite 恒可
const canNext = computed(() =>
  form.value.dialect === 'sqlite' || (!!form.value.db_host && !!form.value.db_name),
);

function pickDialect(d: string) {
  form.value.dialect = d;
  testOk.value = false;
  testMsg.value = '';
  form.value.db_port = d === 'mysql' ? 3306 : 5432;
}

function dialectLabel(d: string) {
  return ({ postgres: 'PostgreSQL', mysql: 'MySQL', sqlite: 'SQLite（内嵌）' } as any)[d] || d;
}

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
    const res = await fetch('/api/v1/admin/install/status');
    status.value = await res.json();
  } finally {
    loading.value = false;
  }
}

async function testConnection() {
  testing.value = true;
  testMsg.value = '';
  try {
    const res = await fetch('/api/v1/admin/install/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        dialect: form.value.dialect, db_host: form.value.db_host, db_port: form.value.db_port,
        db_user: form.value.db_user, db_password: form.value.db_password, db_name: form.value.db_name,
        redis_addr: form.value.redis_addr, redis_password: form.value.redis_password,
      }),
    });
    const json = await res.json();
    testOk.value = !!json.ok;
    testMsg.value = json.message || '';
  } catch (e: any) {
    testOk.value = false;
    testMsg.value = e?.message || '网络错误';
  } finally {
    testing.value = false;
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
    const res = await fetch('/api/v1/admin/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        admin_username: form.value.admin_username.trim(),
        admin_password: form.value.admin_password,
        site_name: form.value.site_name.trim() || 'ZCard 商店',
        site_url: form.value.site_url.trim() || origin,
        dialect: form.value.dialect,
        db_host: form.value.db_host, db_port: form.value.db_port,
        db_user: form.value.db_user, db_password: form.value.db_password, db_name: form.value.db_name,
        redis_addr: form.value.redis_addr, redis_password: form.value.redis_password,
      }),
    });
    const json = await res.json();
    if (!res.ok) {
      error.value = json?.message || '安装失败，请重试';
      return;
    }
    if (json.restart_required) {
      // 库切换：服务即将自重启 → 轮询直到新库装完
      step.value = 3;
      restarting.value = true;
      waited.value = 0;
      pollTimer = setInterval(async () => {
        waited.value += 2;
        try {
          const r = await fetch('/api/v1/admin/install/status');
          const s = await r.json();
          if (s.installed) {
            doneDialect.value = s.dialect || form.value.dialect;
            done.value = true;
            restarting.value = false;
            if (pollTimer) clearInterval(pollTimer);
          }
        } catch { /* 重启窗口期连接失败——继续轮询 */ }
        if (waited.value >= 120 && pollTimer) {
          clearInterval(pollTimer);
          restarting.value = false;
          error.value = '等待超时：服务可能未能在新数据库上启动，请查看日志后重试';
        }
      }, 2000);
      return;
    }
    step.value = 3;
    doneDialect.value = form.value.dialect;
    done.value = true;
  } catch (e: any) {
    error.value = e?.message || '网络错误';
  } finally {
    submitting.value = false;
  }
}

onMounted(load);
onUnmounted(() => { if (pollTimer) clearInterval(pollTimer); });
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
  max-width: 560px;
  background: #fff;
  border-radius: 18px;
  box-shadow: 0 24px 64px rgba(15, 23, 42, 0.12);
  overflow: hidden;
}
/* 头部：无品牌，仅标题 + 步骤条 */
.install-head { padding: 24px 28px 0; }
.install-title {
  font-size: 20px; font-weight: 800; color: #0f172a; margin: 0 0 16px;
  text-align: center;
}
.steps {
  list-style: none; display: flex; justify-content: center; gap: 8px;
  margin: 0 0 4px; padding: 0;
}
.steps li {
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: #94a3b8; font-weight: 600;
  padding: 8px 12px; border-radius: 999px; transition: all 0.15s;
}
.steps li.active { color: #2563eb; background: #eff6ff; }
.steps li.done { color: #15803d; }
.step-no {
  width: 20px; height: 20px; border-radius: 50%;
  background: #e2e8f0; color: #64748b; font-size: 12px; font-weight: 700;
  display: inline-flex; align-items: center; justify-content: center;
}
.steps li.active .step-no { background: #2563eb; color: #fff; }
.steps li.done .step-no { background: #bbf7d0; color: #15803d; }
.install-body { padding: 20px 28px 28px; max-height: 74vh; overflow-y: auto; }
.section { margin-bottom: 4px; }
.section-title { font-weight: 700; font-size: 14px; color: #1f2937; margin-bottom: 10px; }
/* 数据库选择卡片 */
.dialect-grid { display: grid; grid-template-columns: 1fr; gap: 10px; }
.dialect-card {
  border: 2px solid #e5e7eb; border-radius: 12px; padding: 12px 14px;
  cursor: pointer; transition: all 0.12s; background: #fff;
}
.dialect-card:hover { border-color: #93c5fd; }
.dialect-card.active { border-color: #2563eb; background: #eff6ff; }
.dialect-head { display: flex; align-items: center; gap: 8px; font-size: 15px; color: #0f172a; }
.dialect-desc { font-size: 12.5px; color: #6b7280; margin-top: 4px; line-height: 1.6; }
.rec-badge {
  font-size: 11px; font-weight: 700; color: #fff;
  background: linear-gradient(90deg, #2563eb, #4f46e5);
  border-radius: 999px; padding: 2px 8px;
}
.test-badge {
  font-size: 11px; font-weight: 600; color: #92400e;
  background: #fef3c7; border-radius: 999px; padding: 2px 8px;
}
.warn-box {
  margin-top: 10px; font-size: 12.5px; color: #92400e; line-height: 1.7;
  background: #fffbeb; border: 1px solid #fde68a; border-radius: 8px; padding: 10px 12px;
}
/* 连接表单 */
.server-form { margin-top: 12px; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.span2 { grid-column: span 2; }
.redis-title { font-size: 13.5px; font-weight: 700; color: #1f2937; margin: 14px 0 8px; }
.slim { font-weight: 400; }
.test-btn { margin-top: 10px; padding: 9px 16px; font-size: 13.5px; }
.test-msg { margin-top: 8px; font-size: 12.5px; }
.test-msg.ok { color: #15803d; }
.test-msg.bad { color: #dc2626; }
/* 表单行 */
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
/* 分步导航 */
.nav-row { display: flex; gap: 10px; margin-top: 16px; }
.nav-row .install-btn { flex: 1; }
.nav-row .btn.secondary { flex: 1; }
.install-btn { padding: 12px; font-size: 15px; }
.error { color: #dc2626; font-size: 13px; margin: 8px 0; }
.done-icon { font-size: 46px; text-align: center; margin-top: 8px; }
.done-icon.spin { animation: pulse 1.2s ease-in-out infinite; }
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
.done-title { text-align: center; font-size: 18px; font-weight: 700; color: #0f172a; margin: 8px 0 10px; }
.done-actions { display: flex; gap: 10px; justify-content: center; margin-top: 18px; }
.pw-hint { color: #b45309; font-size: 12.5px; }
code { background: #f1f5f9; border-radius: 4px; padding: 1px 6px; }
@media (max-width: 520px) { .form-grid { grid-template-columns: 1fr; } .span2 { grid-column: span 1; } }
</style>
