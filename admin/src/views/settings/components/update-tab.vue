<script setup lang="ts">
// 在线更新 tab（doc/在线更新方案.md §10）：状态卡 + 源配置 + changelog 弹窗 +
// 全流程进度 + 重启等待自动刷新 + 回滚。遵循大厂交互：枚举单选直出/紧凑卡片/胶囊状态。
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NCollapse,
  NCollapseItem,
  NInput,
  NModal,
  NPopconfirm,
  NProgress,
  NRadioButton,
  NRadioGroup,
  NSpace,
  NTag,
  useMessage,
  useDialog
} from "naive-ui";
import { marked } from "marked";
import {
  applyUpdate,
  checkUpdate,
  fetchUpdateSourceConfig,
  fetchUpdateStatus,
  rollbackUpdate,
  updateUpdateSourceConfig,
  type UpdateCheckResult,
  type UpdateSourceConfig,
  type UpdateStatus
} from "@/service/api";

defineOptions({ name: "UpdateTab" });

const message = useMessage();
const dialog = useDialog();

// ── 状态轮询 ──
const status = ref<UpdateStatus | null>(null);
const checking = ref(false);
let pollTimer: ReturnType<typeof setTimeout> | null = null;
let waitingRestart = false; // restarting 后进入「等待服务恢复」模式
let waitStart = 0;

const PHASE_TEXT: Record<string, string> = {
  idle: "空闲",
  checking: "检查更新中",
  backing_up: "备份数据库中",
  downloading: "下载新版本中",
  applying: "应用更新中",
  restarting: "重启服务中",
  verifying: "新版本健康检查中",
  rolled_back: "已回滚上一版本",
  failed: "更新失败"
};

const phaseText = computed(() => PHASE_TEXT[status.value?.phase || "idle"] || status.value?.phase);
const inFlight = computed(() => {
  const p = status.value?.phase;
  return !!p && ["checking", "backing_up", "downloading", "applying", "restarting", "verifying"].includes(p);
});

const supervisorTag = computed(() => {
  if (!status.value) return { type: "default" as const, label: "状态未知" };
  const k = status.value.supervisor_kind;
  if (k === "none") return { type: "warning" as const, label: "裸进程运行（无管理器）" };
  if (k === "systemd") return { type: "success" as const, label: "systemd" };
  if (k === "supervisord" || k === "pm2") return { type: "success" as const, label: k };
  return { type: "info" as const, label: k || "未知" };
});

// 状态接口失败可见化（此前静默 catch——401/403/网络异常时页面全 fallback，
// 与「数据为空」无法区分，误导排查）
const statusError = ref("");
async function refreshStatusVisible() {
  try {
    const { data } = await fetchUpdateStatus();
    status.value = data as any;
    statusError.value = "";
  } catch (e: any) {
    statusError.value = e?.response?.status ? `HTTP ${e.response.status}` : String(e?.message || e);
  }
}

// 历史版本 changelog（manifest 权威源;排除已安装版本——只展示比当前新的记录未安装前的全历史）
const historyList = computed(() => status.value?.history || []);
const historyHtml = computed(() => {
  const out: Record<string, string> = {};
  for (const h of historyList.value) out[h.version] = sanitizeHtml(marked.parse(h.notes || "_（无说明）_") as string);
  return out;
});

// 下载百分比（统一出口——仅 downloading 阶段有值；百分比由进度条 indicator 单点展示，
// 阶段标题只显示阶段名，避免「下载新版本中 23% + 23%」双显）
const dlPercent = computed(() =>
  status.value?.phase === "downloading" ? status.value?.progress_percent || 0 : undefined
);

const sourceText = computed(() => {
  const s = status.value?.source || "";
  if (!s) return "—";
  if (s === "github") return "GitHub 直连";
  if (s.startsWith("static:")) return `自建源 ${s.slice(7)}`;
  return `加速镜像 ${s}`;
});

// ── 状态拉取（waitingRestart 模式：连接失败=仍在重启继续等；恢复且版本到位=成功）──
async function refreshStatus() {
  try {
    const { data } = await fetchUpdateStatus();
    status.value = data as any;
    enterWaitIfNeeded(status.value);
    if (waitingRestart) {
      const st = status.value!;
      if (st.current_version && st.target_version && st.current_version === st.target_version) {
        waitingRestart = false;
        message.success(`已更新到 ${st.current_version}，页面即将刷新`);
        setTimeout(() => window.location.reload(), 1200);
        return;
      }
      // 超时保护（5 分钟）
      if (Date.now() - waitStart > 5 * 60 * 1000) {
        waitingRestart = false;
        message.error("等待服务恢复超时——请手动检查服务状态（SSH: journalctl -u zcard -e 或查看进程）");
        return;
      }
    }
  } catch (e: any) {
    if (waitingRestart) {
      // 连接失败 = 进程正在重启（优雅停机/exec 间隙），继续等待
    } else if (!status.value) {
      // 首次即失败：可见化（401/403=权限、5xx=服务、网络=反代）——此前静默
      statusError.value = e?.response?.status ? `HTTP ${e.response.status}` : String(e?.message || e);
    }
  }
  schedulePoll();
}

function schedulePoll() {
  const interval = waitingRestart ? 2000 : inFlight.value ? 1500 : 8000;
  pollTimer = setTimeout(refreshStatus, interval);
}

// ── 检查更新 ──
const checkResult = ref<UpdateCheckResult | null>(null);
const showConfirm = ref(false);
const changelogHtml = ref("");
// 弹窗多态：confirm=版本确认（取消/立即更新）；progress=分步进度；由 phase 自动切换
const modalStage = ref<"confirm" | "progress">("confirm");
const autoChecked = ref(false); // 进 tab 自动检查只做一次（重进页面再触发）

async function doCheck(silent = false) {
  checking.value = true;
  try {
    const { data }: any = await checkUpdate();
    checkResult.value = data;
    changelogHtml.value = sanitizeHtml(marked.parse(data.notes || "_（本版本未提供变更记录）_") as string);
    if (data.has_update) {
      modalStage.value = "confirm";
      showConfirm.value = true; // 有新版：直接弹更新框（自动检查与手动一致）
    } else if (!silent) {
      message.success(`已是最新版本 ${data.current_version}`);
    }
  } catch (e: any) {
    if (!silent) message.error(`检查更新失败：${e?.message || e}`);
  } finally {
    checking.value = false;
  }
}

// changelog 出自服务端验签 manifest（信任域内），仍剥脚本/事件属性兜底
function sanitizeHtml(html: string) {
  return html
    .replace(/<script[\s\S]*?<\/script>/gi, "")
    .replace(/\son\w+\s*=\s*"[^"]*"/gi, "")
    .replace(/\son\w+\s*=\s*'[^']*'/gi, "")
    .replace(/javascript:/gi, "");
}

async function doApply() {
  modalStage.value = "progress"; // 弹窗切换为分步进度（大厂范式：同一弹窗承接全流程）
  try {
    await applyUpdate();
    waitingRestart = false;
    if (pollTimer) clearTimeout(pollTimer);
    refreshStatus();
  } catch (e: any) {
    showConfirm.value = false;
    message.error(`触发更新失败：${e?.message || e}`);
  }
}

// ── 分步进度（大厂更新器范式）：四步 + 当前步骤高亮 + 下载步百分比 ──
const STEPS = [
  { key: "backing_up", label: "备份数据库" },
  { key: "downloading", label: "下载新版本" },
  { key: "applying", label: "应用更新" },
  { key: "restarting", label: "重启服务" },
] as const;
const stepIndex = computed(() => {
  const ph = status.value?.phase || "";
  // verifying（新进程健康检查）归入重启步；failed 停在当前步标红
  const order = ["backing_up", "downloading", "applying", "restarting", "verifying"];
  const idx = order.indexOf(ph);
  return idx < 0 ? -1 : Math.min(idx, STEPS.length - 1);
});
const stepState = (i: number): "done" | "current" | "todo" | "error" => {
  const ph = status.value?.phase;
  if (ph === "failed") return i === Math.max(stepIndex.value, 0) ? "error" : i < stepIndex.value ? "done" : "todo";
  if (i < stepIndex.value) return "done";
  if (i === stepIndex.value) return "current";
  return "todo";
};

/** 失败重试：回到确认阶段重新发起 */
async function retryFromFailed() {
  modalStage.value = "progress";
  try {
    await applyUpdate();
    waitingRestart = false;
    if (pollTimer) clearTimeout(pollTimer);
    refreshStatus();
  } catch (e: any) {
    showConfirm.value = false;
    message.error(`触发更新失败：${e?.message || e}`);
  }
}

async function doRollback() {
  try {
    await rollbackUpdate();
    waitingRestart = true;
    waitStart = Date.now();
    message.info("已回滚，等待服务重启…");
    if (pollTimer) clearTimeout(pollTimer);
    refreshStatus();
  } catch (e: any) {
    message.error(`回滚失败：${e?.message || e}`);
  }
}

// ── 源配置 ──
const config = ref<UpdateSourceConfig | null>(null);
const accelsText = ref("");
const savingConfig = ref(false);

async function loadConfig() {
  config.value = await fetchUpdateSourceConfig();
  accelsText.value = (config.value.accelerators || []).join("\n");
}

async function saveConfig() {
  if (!config.value) return;
  const cfg: UpdateSourceConfig = {
    ...config.value,
    accelerators: accelsText.value
      .split("\n")
      .map(s => s.trim())
      .filter(Boolean)
  };
  if (cfg.mode === "static" && !cfg.static_base) {
    message.error("静态源模式必须填写基址（如 https://cdn.example.com/zcard）");
    return;
  }
  if (cfg.channel === "beta" && cfg.mode === "accel") {
    message.error("加速镜像不支持 beta 通道（仅 GitHub 直连/自建源）");
    return;
  }
  savingConfig.value = true;
  try {
    await updateUpdateSourceConfig(cfg);
    config.value = cfg;
    message.success("更新源配置已保存");
  } catch (e: any) {
    message.error(`保存失败：${e?.message || e}`);
  } finally {
    savingConfig.value = false;
  }
}

// 进入「等待恢复」模式的判据（页面刷新/重开也能接上——等待态从服务端推导,
// 不依赖内存 flag）：
//  a) phase 处于进行中任一阶段（含新进程健康检查 verifying）
//  b) 目标版本已登记且尚未到位（重启间隙/刷新后 update.state 延续 target）
// 进入即重开分步进度弹窗——用户刷新页面回来直接看到更新进度而非干等。
function enterWaitIfNeeded(st: UpdateStatus | null) {
  if (!st || waitingRestart) return;
  if (st.phase === "failed") return;
  const inFlightPhase = ["checking", "backing_up", "downloading", "applying", "restarting", "verifying"].includes(st.phase);
  const pendingTarget = !!st.target_version && st.current_version !== st.target_version;
  if (inFlightPhase || pendingTarget) {
    waitingRestart = true;
    waitStart = Date.now();
    modalStage.value = "progress";
    showConfirm.value = true;
  }
}

onMounted(async () => {
  await loadConfig().catch(() => {});
  await refreshStatusVisible();
  if (!statusError.value) schedulePoll();
  enterWaitIfNeeded(status.value);
  // 进入 tab 自动检查（静默失败不打扰）；有新版本直接弹更新框
  if (!autoChecked.value && !statusError.value && !inFlight.value) {
    autoChecked.value = true;
    doCheck(true);
  }
});
onBeforeUnmount(() => {
  if (pollTimer) clearTimeout(pollTimer);
});

// status 变化时捕捉 restarting
watch(
  () => status.value?.phase,
  ph => {
    if (ph === "restarting") enterWaitIfNeeded(status.value);
  }
);
</script>

<template>
  <div class="update-tab">
    <NCard size="small" title="系统更新" class="mb-3">
      <template #header-extra>
        <NSpace :size="8" align="center">
          <NTag :type="supervisorTag.type" size="small" round>{{ supervisorTag.label }}</NTag>
          <NTag size="small" round :bordered="false">源：{{ sourceText }}</NTag>
        </NSpace>
      </template>

      <div class="stat-grid">
        <div class="stat">
          <div class="stat-label">当前版本</div>
          <div class="stat-value">{{ status?.current_version || "—" }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">最新版本</div>
          <div class="stat-value">
            {{ status?.latest_version || "—" }}
            <NTag v-if="status?.has_update" type="success" size="small" round class="ml-1">有新版</NTag>
          </div>
        </div>
        <div class="stat">
          <div class="stat-label">状态</div>
          <div class="stat-value">
            <NTag :type="status?.phase === 'failed' ? 'error' : inFlight ? 'info' : 'default'" size="small" round>
              {{ phaseText }}
            </NTag>
          </div>
        </div>
        <div class="stat">
          <div class="stat-label">操作</div>
          <div class="stat-value">
            <NSpace :size="8">
              <NButton size="tiny" type="primary" :loading="checking" :disabled="inFlight" @click="doCheck()">检查更新</NButton>
              <NPopconfirm @positive-click="doRollback">
                <template #trigger>
                  <NButton size="tiny" :disabled="inFlight" quaternary type="warning">回滚上一版</NButton>
                </template>
                回滚到 zcard.prev 并重启服务？（适用于新版异常时的逃生）
              </NPopconfirm>
            </NSpace>
          </div>
        </div>
      </div>

      <NAlert v-if="status && status.backup_ready === false" type="warning" class="mb-3" :bordered="false">
        <b>备份工具未就绪</b>
        <div class="mt-1">{{ status.backup_hint }}</div>
        <div class="mt-1 text-xs opacity-70">更新前会强制备份数据库（数据安全优先，不可跳过）；装好工具后本提示自动消失。</div>
      </NAlert>

      <NAlert v-if="statusError" type="error" class="mb-3" :bordered="false">
        <b>状态获取失败（{{ statusError }}）</b>
        <div class="mt-1 text-xs opacity-70">
          system:update 为超管专属权限——请确认当前账号为超级管理员；HTTP 401/403=权限或登录态，
          5xx=服务异常，网络错误=反代/服务未起。F12 → Network → update/status 可看原始响应。
        </div>
      </NAlert>

      <NAlert v-if="status?.supervisor_kind === 'none'" type="warning" :show-icon="true" class="mt-3" :bordered="false">
        当前为裸进程（nohup）运行：更新可正常执行，但新版本无法启动时<b>没有自动回滚兜底</b>。建议改用
        systemd（install.sh）或宝塔「进程守护管理器」部署后使用在线更新。
      </NAlert>

      <NAlert v-if="status?.phase === 'failed'" type="error" class="mt-3" :bordered="false">
        <b>更新失败：</b>{{ status?.error_message }}
        <div class="mt-1 text-xs opacity-70">失败原因与对应处置见上方错误信息（按数据库方言与实际错误给出）。磁盘不足可清理后重试；下载源不可达可在「更新源配置」切换模式。更新在任何阶段失败都不会改动磁盘上的现有版本，可安全重试。</div>
      </NAlert>

      <!-- 进行中进度 -->
      <div v-if="inFlight" class="progress-block mt-3">
        <div class="progress-title">{{ phaseText }}</div>
        <NProgress
          type="line"
          :percentage="dlPercent"
          :indeterminate="dlPercent === undefined"
          :rail-height="10"
          :border-radius="5"
          :show-indicator="dlPercent !== undefined"
          processing
        />
        <div v-if="status?.phase === 'restarting' || status?.phase === 'verifying'" class="mt-2 text-xs opacity-70">
          服务重启中（约 10–30 秒）……恢复后本页将自动刷新；请勿关闭浏览器。
        </div>
        <div v-if="status?.phase === 'backing_up'" class="mt-2 text-xs opacity-70">
          正在备份数据库（SQLite VACUUM INTO / pg_dump）——数据安全优先，跳过不提供。
        </div>
      </div>
    </NCard>

    <NCard v-if="historyList.length" size="small" title="版本历史" class="mb-3">
      <div class="history-list">
        <div v-for="h in historyList" :key="h.version" class="history-item">
          <div class="history-head">
            <NTag size="small" round :type="h.version === status?.current_version ? 'success' : 'default'">
              {{ h.version }}{{ h.version === status?.current_version ? "（当前）" : "" }}
            </NTag>
            <span class="history-date">{{ h.issued_at }}</span>
            <NTag v-if="h.channel === 'beta'" size="small" round type="warning" :bordered="false">beta</NTag>
          </div>
          <div class="history-notes" v-html="historyHtml[h.version]" />
        </div>
      </div>
    </NCard>

    <NCollapse class="mb-3">
      <NCollapseItem name="source" title="更新源配置（大陆服务器默认 auto 即可）">
        <div v-if="config" class="config-form">
          <div class="field">
            <div class="field-label">源模式</div>
            <NRadioGroup v-model:value="config.mode" size="small">
              <NRadioButton value="auto" title="直连探测 GitHub，不通自动走加速（大陆推荐）">auto 自动</NRadioButton>
              <NRadioButton value="github">GitHub 直连</NRadioButton>
              <NRadioButton value="accel">加速镜像</NRadioButton>
              <NRadioButton value="static">自建静态源</NRadioButton>
            </NRadioGroup>
          </div>
          <div v-if="config.mode === 'github' || config.mode === 'accel'" class="field">
            <div class="field-label">发行仓库</div>
            <NInput v-model:value="config.repo" size="small" placeholder="owner/repo" style="max-width: 360px" />
          </div>
          <div v-if="config.mode === 'accel' || config.mode === 'auto'" class="field">
            <div class="field-label">加速镜像列表（一行一个；auto 模式直连不通时按序竞速）</div>
            <NInput
              v-model:value="accelsText"
              type="textarea"
              :rows="3"
              size="small"
              style="max-width: 480px"
              placeholder="https://gh-proxy.com&#10;https://ghfast.top&#10;https://ghproxy.net"
            />
          </div>
          <div v-if="config.mode === 'static'" class="field">
            <div class="field-label">静态源基址</div>
            <NInput
              v-model:value="config.static_base"
              size="small"
              style="max-width: 480px"
              placeholder="https://cdn.example.com/zcard（其下平铺 update.json 与二进制产物）"
            />
          </div>
          <div class="field">
            <div class="field-label">
              进程管理器
              <span class="text-xs opacity-55">（自动探测不准时手动指定——决定更新重启的分流方式）</span>
            </div>
            <NRadioGroup v-model:value="config.supervisor" size="small">
              <NRadioButton value="auto" title="父进程链/环境变量自动探测">auto 探测</NRadioButton>
              <NRadioButton value="systemd">systemd</NRadioButton>
              <NRadioButton value="supervisord">supervisord</NRadioButton>
              <NRadioButton value="none" title="nohup 裸跑：更新失败无自动兜底">裸跑</NRadioButton>
            </NRadioGroup>
          </div>
          <div class="field">
            <div class="field-label">更新通道</div>
            <NRadioGroup v-model:value="config.channel" size="small">
              <NRadioButton value="stable">stable 稳定</NRadioButton>
              <NRadioButton value="beta" :disabled="config.mode === 'accel'">beta 尝鲜</NRadioButton>
            </NRadioGroup>
            <span v-if="config.mode === 'accel'" class="text-xs opacity-60 ml-2">加速镜像不支持 beta（走 GitHub API）</span>
          </div>
          <div class="mt-2">
            <NButton size="small" type="primary" :loading="savingConfig" @click="saveConfig">保存配置</NButton>
          </div>
        </div>
      </NCollapseItem>
    </NCollapse>

    <!-- 更新弹窗（多态）：确认（取消/立即更新）→ 分步进度（同一弹窗承接全流程） -->
    <NModal
      :show="showConfirm"
      preset="card"
      style="width: min(640px, 94vw)"
      :title="modalStage === 'confirm' ? '发现新版本' : '正在更新'"
      :mask-closable="false"
      :close-on-esc="false"
      @update:show="(v: boolean) => (showConfirm = v)"
    >
      <!-- 阶段一：版本确认 -->
      <template v-if="modalStage === 'confirm'">
        <div class="version-line">
          <NTag size="small" round>{{ checkResult?.current_version }}</NTag>
          <span class="arrow">→</span>
          <NTag size="small" round type="success">{{ checkResult?.latest_version }}</NTag>
          <NTag size="small" round :bordered="false" class="ml-2">{{ checkResult?.channel }} · {{ checkResult?.source || sourceText }}</NTag>
        </div>
        <div class="changelog" v-html="changelogHtml" />
      </template>

      <!-- 阶段二：分步进度 -->
      <template v-else>
        <div class="steps">
          <div v-for="(s, i) in STEPS" :key="s.key" class="step" :class="stepState(i)">
            <span class="step-dot">
              <template v-if="stepState(i) === 'done'">✓</template>
              <template v-else-if="stepState(i) === 'error'">✕</template>
              <template v-else-if="stepState(i) === 'current' && s.key === 'downloading'">{{ status?.progress_percent ?? 0 }}%</template>
              <template v-else>{{ i + 1 }}</template>
            </span>
            <span class="step-label">{{ s.label }}</span>
          </div>
        </div>
        <NProgress
          v-if="status?.phase === 'downloading'"
          type="line"
          :percentage="status?.progress_percent || 0"
          :rail-height="8"
          :border-radius="4"
          :show-indicator="false"
          processing
          class="mt-2"
        />
        <div class="mt-3 text-center text-13px opacity-70">
          <template v-if="status?.phase === 'restarting' || status?.phase === 'verifying'">
            服务重启中（约 10–30 秒），完成后页面将自动刷新——请勿关闭浏览器
          </template>
          <template v-else-if="status?.phase === 'failed'">
            更新失败：{{ status?.error_message }}
            <div class="mt-2 flex justify-center gap-8px">
              <NButton size="tiny" @click="retryFromFailed">重试</NButton>
              <NButton size="tiny" type="warning" quaternary @click="doRollback">回滚上一版</NButton>
              <NButton size="tiny" quaternary @click="showConfirm = false">关闭</NButton>
            </div>
          </template>
          <template v-else>更新过程中请勿关闭浏览器；失败可安全重试（不会改动现有版本）</template>
        </div>
      </template>

      <template #footer>
        <NSpace v-if="modalStage === 'confirm'" justify="end">
          <NButton size="small" @click="showConfirm = false">取消</NButton>
          <NButton size="small" type="primary" @click="doApply">立即更新</NButton>
        </NSpace>
        <div v-else class="text-center text-12px opacity-50">zcard 在线更新</div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.update-tab {
  max-width: 900px;
}
.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
}
.stat-label {
  font-size: 12px;
  opacity: 0.6;
  margin-bottom: 4px;
}
.stat-value {
  font-size: 15px;
  font-weight: 500;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}
.progress-block {
  padding: 12px;
  border-radius: 6px;
  background: var(--n-color-embedded, rgba(128, 128, 128, 0.08));
}
.progress-title {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  margin-bottom: 8px;
}
.pct {
  font-variant-numeric: tabular-nums;
}
.config-form .field {
  margin-bottom: 14px;
}
.field-label {
  font-size: 13px;
  margin-bottom: 6px;
  opacity: 0.75;
}
.version-line {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.arrow {
  font-size: 18px;
  opacity: 0.6;
}
.steps {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 4px 4px;
}
.step {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.step-dot {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  border: 1.5px solid rgba(128, 128, 128, 0.35);
  color: rgba(128, 128, 128, 0.8);
  transition: all 0.2s;
}
.step.done .step-dot {
  border-color: #18a058;
  background: rgba(24, 160, 88, 0.12);
  color: #18a058;
}
.step.current .step-dot {
  border-color: var(--primary-color, #2080f0);
  background: rgba(32, 128, 240, 0.1);
  color: var(--primary-color, #2080f0);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
.step.error .step-dot {
  border-color: #d03050;
  background: rgba(208, 48, 80, 0.12);
  color: #d03050;
}
.step-label {
  font-size: 12px;
  opacity: 0.75;
  white-space: nowrap;
}
.step.current .step-label {
  opacity: 1;
  font-weight: 500;
}

.changelog {
  max-height: 46vh;
  overflow: auto;
  font-size: 14px;
  line-height: 1.7;
  padding: 4px 8px;
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.07);
}
.changelog :deep(h1),
.changelog :deep(h2),
.changelog :deep(h3) {
  font-size: 15px;
  margin: 12px 0 6px;
}
.changelog :deep(ul),
.changelog :deep(ol) {
  padding-left: 20px;
}
.changelog :deep(code) {
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(128, 128, 128, 0.15);
}
.history-list {
  max-height: 420px;
  overflow: auto;
}
.history-item {
  padding: 10px 4px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.14);
}
.history-item:last-child {
  border-bottom: none;
}
.history-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}
.history-date {
  font-size: 12px;
  opacity: 0.55;
}
.history-notes {
  font-size: 13px;
  line-height: 1.7;
  color: inherit;
}
.history-notes :deep(ul) {
  padding-left: 18px;
}
.history-notes :deep(h1),
.history-notes :deep(h2),
.history-notes :deep(h3) {
  font-size: 14px;
  margin: 6px 0 4px;
}
</style>
