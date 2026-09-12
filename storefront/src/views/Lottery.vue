<script setup lang="ts">
import { ref, computed, watch, onMounted, nextTick } from "vue";
import { useRoute } from "vue-router";
import { authState, refreshAuth } from "@/auth";
import { getToken } from "@/api/client";
import {
  listLotteryActivities,
  getLotteryActivity,
  claimLotteryChances,
  drawLottery,
  listMyLotteryDraws,
  getMyLotteryDraw,
  lotteryStates,
  type LotteryActivity,
  type LotteryDraw,
} from "@/api/lottery";
const route = useRoute();
const loading = ref(false),
  drawing = ref(false),
  error = ref(""),
  activities = ref<LotteryActivity[]>([]),
  activity = ref<LotteryActivity | null>(null),
  remaining = ref<number | null>(null),
  records = ref<LotteryDraw[]>([]),
  total = ref(0),
  page = ref(1),
  result = ref<LotteryDraw | null>(null),
  dialog = ref<HTMLDialogElement | null>(null),
  copyText = ref("复制内容"),
  pendingKey = ref("");
const mine = computed(() => route.query.tab === "prizes");
const loginURL = computed(() => ({
  path: "/login",
  query: { redirect: route.fullPath },
}));
const safePrizes = computed(() => activity.value?.prizes || []);
const missed = computed(
  () =>
    Math.max(
      0,
      10000 - safePrizes.value.reduce((n, p) => n + (p.probability || 0), 0),
    ) / 100,
);
const time = (n?: number) => (n ? new Date(n * 1000).toLocaleString() : "—");
const keyName = () =>
  `lottery-pending:${activity.value?.id}:${authState.username}`;
function readPending() {
  try {
    pendingKey.value = sessionStorage.getItem(keyName()) || "";
  } catch {
    pendingKey.value = "";
  }
}
function savePending(key: string) {
  pendingKey.value = key;
  try {
    if (key) sessionStorage.setItem(keyName(), key);
    else sessionStorage.removeItem(keyName());
  } catch {
    /* 当前页面仍保留请求标识 */
  }
}
let loadSequence = 0;
async function load() {
  const seq = ++loadSequence;
  loading.value = true;
  error.value = "";
  activity.value = null;
  remaining.value = null;
  try {
    if (getToken() && !authState.loaded) await refreshAuth();
    if (mine.value) {
      if (!getToken()) {
        records.value = [];
        total.value = 0;
        return;
      }
      const r = await listMyLotteryDraws(page.value);
      if (seq !== loadSequence) return;
      if (r.error) {
        error.value = r.error;
        return;
      }
      records.value = r.data?.items || [];
      total.value = Number(r.data?.total || 0);
      return;
    }
    if (route.params.id) {
      const id = Number(route.params.id);
      if (!Number.isSafeInteger(id) || id <= 0) {
        error.value = "活动不存在";
        return;
      }
      const r = await getLotteryActivity(id);
      if (seq !== loadSequence) return;
      if (r.error) {
        error.value = r.error;
        return;
      }
      activity.value = r.data;
      readPending();
      if (getToken() && activity.value?.status === "live") {
        const chances = await claimLotteryChances(id);
        if (seq !== loadSequence) return;
        if (chances.error) error.value = chances.error;
        else remaining.value = chances.data?.remaining || 0;
      }
    } else {
      const r = await listLotteryActivities();
      if (seq !== loadSequence) return;
      if (r.error) error.value = r.error;
      else activities.value = r.data?.items || [];
    }
  } finally {
    if (seq === loadSequence) loading.value = false;
  }
}
async function showResult(d: LotteryDraw) {
  result.value = d;
  copyText.value = "复制内容";
  await nextTick();
  dialog.value?.showModal();
}
async function draw() {
  if (!activity.value || drawing.value) return;
  drawing.value = true;
  error.value = "";
  const id = activity.value.id;
  const currentName = keyName();
  try {
    const key = pendingKey.value || crypto.randomUUID().replace(/-/g, "");
    savePending(key);
    const r = await drawLottery(id, key);
    if (r.error || !r.data) {
      error.value =
        (r.error || "抽奖结果暂时无法读取") +
        "；请重试上次抽奖或查看我的奖品。";
      return;
    }
    pendingKey.value = "";
    try {
      sessionStorage.removeItem(currentName);
    } catch {}
    if (activity.value?.id === id) {
      await showResult(r.data);
      const chances = await claimLotteryChances(id);
      if (!chances.error) remaining.value = chances.data?.remaining || 0;
      const current = await getLotteryActivity(id);
      if (current.data) activity.value = current.data;
    }
  } finally {
    drawing.value = false;
  }
}
async function view(no: string) {
  error.value = "";
  const r = await getMyLotteryDraw(no);
  if (r.error) error.value = r.error;
  else if (r.data) await showResult(r.data);
}
async function copy() {
  try {
    await navigator.clipboard.writeText(result.value?.content || "");
    copyText.value = "已复制";
  } catch {
    copyText.value = "请长按或选中内容复制";
  }
}
function close() {
  dialog.value?.close();
  result.value = null;
}
watch(
  () => route.fullPath,
  () => {
    page.value = 1;
    close();
    void load();
  },
);
onMounted(load);
</script>
<template>
  <div class="lottery-page">
    <div class="lottery-heading">
      <div>
        <p class="lottery-eyebrow">会员活动</p>
        <h1>{{ mine ? "我的奖品" : activity?.name || "幸运抽奖" }}</h1>
      </div>
      <router-link
        :to="mine ? '/lottery' : '/lottery?tab=prizes'"
        class="lottery-secondary"
        >{{ mine ? "查看抽奖活动" : "我的奖品" }}</router-link
      >
    </div>
    <p v-if="error" class="lottery-message error" role="alert">{{ error }}</p>
    <p v-if="loading" class="lottery-surface" role="status">正在加载…</p>
    <template v-else-if="mine">
      <div v-if="!getToken()" class="lottery-surface">
        <p>登录后查看你的中奖记录和领取内容。</p>
        <router-link :to="loginURL" class="lottery-primary"
          >登录后查看</router-link
        >
      </div>
      <template v-else
        ><div v-if="!records.length" class="lottery-surface">
          暂无抽奖记录，先去看看正在进行的活动吧。
        </div>
        <article
          v-for="d in records"
          :key="d.draw_no"
          class="lottery-record lottery-surface"
        >
          <div>
            <span class="lottery-badge">{{
              lotteryStates[d.status] || "处理中"
            }}</span>
            <h2>{{ d.prize_name || "谢谢参与" }}</h2>
            <p>{{ d.activity_name }} · {{ time(d.created_at) }}</p>
            <p class="lottery-no">{{ d.draw_no }}</p>
          </div>
          <button class="lottery-secondary" @click="view(d.draw_no)">
            {{ d.status === "pending" ? "查看领取说明" : "查看详情" }}
          </button>
        </article>
        <div v-if="total > 20" class="lottery-pager">
          <button
            :disabled="page <= 1"
            @click="
              page--;
              load();
            "
          >
            上一页</button
          ><span>{{ page }} / {{ Math.ceil(total / 20) }}</span
          ><button
            :disabled="page * 20 >= total"
            @click="
              page++;
              load();
            "
          >
            下一页
          </button>
        </div></template
      >
    </template>
    <template v-else-if="activity">
      <div class="lottery-surface lottery-banner">
        <img v-if="activity.image" :src="activity.image" alt="活动图片" />
        <p>{{ time(activity.start_at) }} — {{ time(activity.end_at) }}</p>
        <p>{{ lotteryStates[activity.status] || "活动未开放" }}</p>
        <p v-if="activity.description" class="lottery-description">
          {{ activity.description }}
        </p>
      </div>
      <section class="lottery-surface">
        <h2>活动奖品</h2>
        <div class="lottery-prizes">
          <article v-for="p in safePrizes" :key="p.id" class="lottery-prize">
            <img v-if="p.image" :src="p.image" alt="" /><span
              v-else
              class="lottery-gift"
              aria-hidden="true"
              >礼</span
            >
            <h3>{{ p.name }}</h3>
            <p>中奖概率 {{ ((p.probability || 0) / 100).toFixed(2) }}%</p>
            <small
              >{{ p.mode === "manual" ? "平台人工领取" : "系统自动发放" }} ·
              活动剩余 {{ p.remaining || 0 }} 份</small
            >
          </article>
        </div>
        <p class="lottery-muted">
          谢谢参与概率 {{ missed.toFixed(2) }}%。每次独立抽奖，可重复中奖。
        </p>
      </section>
      <section class="lottery-surface lottery-action" aria-live="polite">
        <template v-if="!getToken()"
          ><p>登录后参与，次数按账号记录。</p>
          <router-link :to="loginURL" class="lottery-primary"
            >登录后参与</router-link
          ></template
        ><template v-else
          ><p v-if="remaining !== null">
            剩余 <strong>{{ remaining }}</strong> 次
          </p>
          <p>
            {{
              activity.chance_mode === "daily"
                ? `每日赠送 ${activity.chance_count} 次，当日有效（${activity.timezone}）。`
                : activity.chance_mode === "manual"
                  ? "本活动由平台发放抽奖次数。"
                  : `活动期间每个账号赠送 ${activity.chance_count} 次。`
            }}
          </p>
          <button
            class="lottery-primary"
            :disabled="
              drawing ||
              (!pendingKey && (activity.status !== 'live' || !remaining))
            "
            @click="draw"
          >
            {{
              drawing
                ? "正在确认结果…"
                : pendingKey
                  ? "重试上次抽奖"
                  : activity.status !== "live"
                    ? lotteryStates[activity.status] || "暂不可抽奖"
                    : remaining === 0
                      ? "抽奖次数已用完"
                      : "立即抽奖"
            }}
          </button>
          <p class="lottery-muted">
            奖品不足时暂停抽奖；未生成结果不扣次数。
          </p></template
        >
      </section>
      <router-link to="/lottery" class="lottery-secondary"
        >返回活动列表</router-link
      >
    </template>
    <template v-else-if="!error"
      ><div v-if="!activities.length" class="lottery-surface">
        暂时没有开放的抽奖活动，已有奖品仍可在“我的奖品”中查看。
      </div>
      <div class="lottery-activities">
        <router-link
          v-for="a in activities"
          :key="a.id"
          :to="`/lottery/${a.id}`"
          class="lottery-surface lottery-activity-card"
          ><img v-if="a.image" :src="a.image" alt="" /><span
            class="lottery-badge"
            >{{ lotteryStates[a.status] || "活动未开放" }}</span
          >
          <h2>{{ a.name }}</h2>
          <p>
            {{
              a.chance_mode === "daily"
                ? "每日赠送"
                : a.chance_mode === "manual"
                  ? "平台发放次数"
                  : "登录参与赠送"
            }}{{ a.chance_mode === "manual" ? "" : ` ${a.chance_count} 次` }}
          </p>
          <span>查看活动 →</span></router-link
        >
      </div></template
    >
    <dialog ref="dialog" class="lottery-result" @cancel="result = null">
      <template v-if="result"
        ><div class="lottery-result-heading">
          <h2>{{ result.status === "missed" ? "谢谢参与" : "恭喜中奖" }}</h2>
          <button aria-label="关闭中奖详情" @click="close">×</button>
        </div>
        <h3>{{ result.prize_name }}</h3>
        <p>{{ lotteryStates[result.status] }}</p>
        <p class="lottery-no">中奖编号：{{ result.draw_no }}</p>
        <p v-if="result.status === 'pending'">
          请按以下说明联系平台领取，提供中奖编号即可核对。
        </p>
        <pre v-if="result.content" class="lottery-content">{{
          result.content
        }}</pre>
        <button v-if="result.content" class="lottery-secondary" @click="copy">
          {{ copyText }}
        </button>
        <p class="lottery-muted">记录已保存，可随时从“我的奖品”查看。</p>
        <button class="lottery-primary" @click="close">
          我知道了
        </button></template
      >
    </dialog>
  </div>
</template>
<style scoped>
.lottery-page {
  max-width: var(--zc-content-width, 1240px);
  margin: auto;
  padding: 24px 16px 32px;
  color: #253047;
}
.lottery-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 24px;
}
.lottery-heading h1 {
  font-size: 28px;
  margin: 4px 0;
}
.lottery-eyebrow {
  color: var(--zc-primary, #2563eb);
  font-size: 13px;
  margin: 0;
}
.lottery-surface {
  background: #fff;
  border: 1px solid #e4e9f0;
  border-radius: var(--zc-card-radius, 14px);
  padding: 24px;
  margin-bottom: 18px;
}
.lottery-surface h2 {
  font-size: 19px;
  margin: 0 0 14px;
}
.lottery-banner > img {
  width: 100%;
  max-height: 260px;
  object-fit: cover;
  border-radius: 10px;
}
.lottery-description {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.lottery-prizes {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
}
.lottery-prize {
  border: 1px solid #e8eaf2;
  border-radius: 12px;
  padding: 20px 14px;
  text-align: center;
  background: #fafcff;
  overflow-wrap: anywhere;
}
.lottery-prize img {
  height: 72px;
  width: 72px;
  object-fit: contain;
  margin: auto;
}
.lottery-prize h3 {
  font-size: 16px;
  margin: 12px 0 8px;
}
.lottery-prize p {
  font-size: 14px;
}
.lottery-prize small,
.lottery-muted {
  color: #5c687a;
  font-size: 13px;
  line-height: 1.6;
}
.lottery-gift {
  display: inline-grid;
  place-items: center;
  width: 64px;
  height: 64px;
  background: #edf3ff;
  border-radius: 18px;
  font-size: 26px;
  color: var(--zc-primary, #2563eb);
}
.lottery-primary,
.lottery-secondary {
  display: inline-flex;
  justify-content: center;
  align-items: center;
  min-height: 44px;
  border-radius: 9px;
  padding: 10px 18px;
  font: inherit;
  text-decoration: none;
  cursor: pointer;
  box-sizing: border-box;
}
.lottery-primary {
  background: var(--zc-primary, #2563eb);
  border: 1px solid transparent;
  color: #fff;
}
.lottery-secondary {
  background: #fff;
  border: 1px solid #d6deeb;
  color: #334155;
}
.lottery-primary:disabled {
  background: #cbd5e1;
  color: #475569;
  cursor: not-allowed;
}
.lottery-action {
  text-align: center;
}
.lottery-action strong {
  font-size: 24px;
  color: var(--zc-primary, #2563eb);
}
.lottery-action .lottery-primary {
  min-width: 220px;
}
.lottery-activities {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.lottery-activity-card {
  color: inherit;
  text-decoration: none;
}
.lottery-activity-card > img {
  width: 100%;
  height: 150px;
  object-fit: cover;
  border-radius: 8px;
  margin-bottom: 16px;
}
.lottery-badge {
  display: inline-block;
  color: #3659b2;
  background: #eff4ff;
  padding: 4px 9px;
  border-radius: 6px;
  font-size: 13px;
  margin-bottom: 10px;
}
.lottery-record {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.lottery-record p {
  font-size: 14px;
  color: #5c687a;
}
.lottery-no {
  word-break: break-all;
  font-size: 13px;
}
.lottery-message {
  padding: 16px;
  border-radius: 10px;
  background: #fff3e8;
  color: #8c4214;
}
.lottery-result {
  margin: auto;
  width: min(520px, calc(100vw - 32px));
  box-sizing: border-box;
  border: 0;
  border-radius: 16px;
  padding: 24px;
  max-height: 85vh;
  overflow: auto;
  color: #253047;
}
.lottery-result::backdrop {
  background: #15223688;
}
.lottery-result-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.lottery-result-heading button {
  border: 0;
  background: transparent;
  min-width: 44px;
  min-height: 44px;
  font-size: 28px;
  cursor: pointer;
}
.lottery-content {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: #f4f7fb;
  padding: 16px;
  border-radius: 10px;
  font-family: inherit;
  user-select: text;
}
.lottery-result .lottery-primary {
  display: flex;
  width: 100%;
  margin-top: 18px;
}
.lottery-pager {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 16px;
}
.lottery-pager button {
  min-height: 44px;
  padding: 8px 16px;
}
@media (max-width: 640px) {
  .lottery-page {
    padding: 18px 12px;
  }
  .lottery-heading h1 {
    font-size: 23px;
  }
  .lottery-surface {
    padding: 18px;
  }
  .lottery-prizes {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }
  .lottery-prize {
    padding: 16px 10px;
  }
  .lottery-activities {
    grid-template-columns: 1fr;
  }
  .lottery-record {
    align-items: flex-start;
    flex-direction: column;
  }
  .lottery-action .lottery-primary {
    width: 100%;
  }
  .lottery-heading {
    gap: 8px;
  }
}
</style>
