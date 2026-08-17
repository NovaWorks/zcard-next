<template>
  <div>
    <div class="card" style="margin-bottom: 16px;">
      <div style="display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 8px;">
        <h2>{{ ticket?.ticket_no }}</h2>
        <div class="actions">
          <span :class="ticket?.priority === 'urgent_paid' ? 'badge red' : 'badge gray'">
            {{ ticket?.priority === 'urgent_paid' ? '已加急' : '普通优先级' }}
          </span>
          <span :class="statusBadge">{{ statusText }}</span>
        </div>
      </div>
      <div class="muted" style="margin-top: 4px;">
        {{ ticket?.type === 'presale' ? '售前' : '售后' }} · 创建于 {{ fmtTime(ticket?.created_at || 0) }}
      </div>
      <div class="actions" style="margin-top: 12px;" v-if="ticket">
        <button class="btn secondary" v-if="ticket.status !== 'resolved' && ticket.status !== 'closed' && ticket.priority !== 'urgent_paid'"
                :disabled="urging" @click="doUrgent">
          {{ urging ? '处理中…' : '付费加急（余额扣费）' }}
        </button>
        <template v-if="ticket.status === 'resolved' && !ticket.satisfaction">
          <span class="muted">对本单服务评价：</span>
          <select v-model.number="rateValue" style="padding: 4px;">
            <option :value="5">★★★★★ 很满意</option>
            <option :value="4">★★★★ 满意</option>
            <option :value="3">★★★ 一般</option>
            <option :value="2">★★ 不满意</option>
            <option :value="1">★ 很不满意</option>
          </select>
          <button class="btn secondary" @click="doRate">提交评价</button>
        </template>
        <span v-if="ticket.satisfaction" class="muted">已评价：{{ '★'.repeat(ticket.satisfaction) }}</span>
      </div>
      <div v-if="actionError" class="error" style="margin-top: 8px;">{{ actionError }}</div>
      <div v-if="urgentOk" class="success" style="margin-top: 8px;">加急成功，已扣费 {{ formatMoney(urgentOk.fee_cents) }}，客服将优先处理</div>
    </div>

    <!-- 会话流（内部备注后端已过滤；user 右侧蓝、admin 左侧灰、system 居中） -->
    <div class="card" style="margin-bottom: 16px;">
      <div class="chat">
        <div v-for="m in messages" :key="m.id" :class="`msg ${m.sender_type}`">
          <div v-if="m.sender_type !== 'user'" class="muted" style="margin-bottom: 2px;">
            {{ m.sender_type === 'admin' ? '客服' : '系统' }} · {{ fmtTime(m.created_at) }}
          </div>
          <div v-else class="muted" style="margin-bottom: 2px; text-align: right;">{{ fmtTime(m.created_at) }}</div>
          <div>{{ m.content }}</div>
        </div>
        <div v-if="!messages.length" class="muted" style="text-align: center;">加载中…</div>
      </div>
    </div>

    <!-- 回复 -->
    <div class="card" v-if="ticket && ticket.status !== 'closed'">
      <div class="field">
        <label>追加回复</label>
        <textarea class="input" v-model="replyContent" rows="3" placeholder="补充信息或回复客服"></textarea>
      </div>
      <div v-if="replyError" class="error" style="margin-bottom: 8px;">{{ replyError }}</div>
      <button class="btn" :disabled="replying || !replyContent.trim()" @click="doReply">{{ replying ? '发送中…' : '发送' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { getTicket, replyTicket, rateTicket, payUrgent, type TicketItem, type TicketMessage } from '@/api';
import { formatMoney } from '@/api/client';

const route = useRoute();
const ticketNo = String(route.params.no || '');

const ticket = ref<TicketItem | null>(null);
const messages = ref<TicketMessage[]>([]);
const replyContent = ref('');
const replyError = ref('');
const replying = ref(false);
const actionError = ref('');
const urging = ref(false);
const urgentOk = ref<{ paid: boolean; fee_cents: number } | null>(null);
const rateValue = ref(5);

const statusText = computed(() =>
  ({ open: '待处理', processing: '处理中', resolved: '已解决', closed: '已关闭' } as Record<string, string>)[ticket.value?.status || ''] || ticket.value?.status
);
const statusBadge = computed(() =>
  ({ open: 'badge orange', processing: 'badge blue', resolved: 'badge green', closed: 'badge gray' } as Record<string, string>)[ticket.value?.status || ''] || 'badge gray'
);

onMounted(reload);

async function reload() {
  const { data } = await getTicket(ticketNo);
  if (data) {
    ticket.value = data.ticket;
    messages.value = data.messages || [];
  }
}

async function doReply() {
  replying.value = true;
  replyError.value = '';
  const { error } = await replyTicket(ticketNo, replyContent.value.trim());
  replying.value = false;
  if (error) {
    replyError.value = error;
    return;
  }
  replyContent.value = '';
  reload();
}

async function doUrgent() {
  if (!confirm('确认付费加急？将从余额扣除加急费。')) return;
  urging.value = true;
  actionError.value = '';
  const { data, error } = await payUrgent(ticketNo);
  urging.value = false;
  if (error || !data) {
    actionError.value = error || '加急失败（余额不足？）';
    return;
  }
  if (!data.paid) {
    actionError.value = data.error || '加急失败';
    return;
  }
  urgentOk.value = data;
  reload();
}

async function doRate() {
  actionError.value = '';
  const { error } = await rateTicket(ticketNo, rateValue.value);
  if (error) {
    actionError.value = error;
    return;
  }
  reload();
}

function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '';
}
</script>
