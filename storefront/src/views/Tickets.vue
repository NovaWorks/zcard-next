<template>
  <div>
    <div class="card" style="margin-bottom: 16px;">
      <h2 style="margin-bottom: 12px;">新建工单</h2>
      <div class="field">
        <label>类型</label>
        <select v-model="newType">
          <option value="presale">售前咨询</option>
          <option value="aftersale">售后问题</option>
        </select>
      </div>
      <div class="field">
        <label>问题描述 *</label>
        <textarea class="input" v-model="newContent" rows="3" placeholder="请描述您遇到的问题（订单号、发生时间、期望结果）"></textarea>
      </div>
      <div class="field">
        <label>联系方式（游客必填）</label>
        <input class="input" v-model="newContact" type="text" placeholder="邮箱或 QQ（登录用户可留空）" />
      </div>
      <div v-if="newError" class="error" style="margin-bottom: 8px;">{{ newError }}</div>
      <button class="btn" :disabled="creating" @click="doCreate">{{ creating ? '提交中…' : '提交工单' }}</button>
    </div>

    <div class="card">
      <h2 style="margin-bottom: 12px;">我的工单</h2>
      <table class="list">
        <thead><tr><th>工单号</th><th>类型</th><th>优先级</th><th>状态</th><th>创建时间</th><th></th></tr></thead>
        <tbody>
          <tr v-for="t in tickets" :key="t.id">
            <td>{{ t.ticket_no }}</td>
            <td>{{ t.type === 'presale' ? '售前' : '售后' }}</td>
            <td>
              <span :class="t.priority === 'urgent_paid' ? 'badge red' : t.priority === 'high' ? 'badge orange' : 'badge gray'">
                {{ priorityText(t.priority) }}
              </span>
            </td>
            <td><span :class="ticketBadge(t.status)">{{ statusText(t.status) }}</span></td>
            <td class="muted">{{ fmtTime(t.created_at) }}</td>
            <td><router-link class="btn secondary" :to="`/tickets/${t.ticket_no}`">查看</router-link></td>
          </tr>
          <tr v-if="!tickets.length"><td colspan="6" class="muted" style="text-align: center;">暂无工单</td></tr>
        </tbody>
      </table>
      <div class="actions" style="margin-top: 12px;" v-if="total > pageSize">
        <button class="btn secondary" :disabled="page <= 1" @click="load(page - 1)">上一页</button>
        <span class="muted">{{ page }} / {{ Math.ceil(total / pageSize) }}</span>
        <button class="btn secondary" :disabled="page >= Math.ceil(total / pageSize)" @click="load(page + 1)">下一页</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { createTicket, listMyTickets, type TicketItem } from '@/api';
import { authState } from '@/auth';

const newType = ref('presale');
const newContent = ref('');
const newContact = ref('');
const newError = ref('');
const creating = ref(false);

const tickets = ref<TicketItem[]>([]);
const page = ref(1);
const total = ref(0);
const pageSize = 10;

onMounted(() => load(1));

async function load(p: number) {
  const { data } = await listMyTickets(p, pageSize);
  if (data) {
    tickets.value = data.tickets || [];
    total.value = data.total;
    page.value = p;
  }
}

async function doCreate() {
  if (!newContent.value.trim()) {
    newError.value = '请描述问题';
    return;
  }
  if (!authState.loggedIn && !newContact.value.trim()) {
    newError.value = '游客请填写联系方式（邮箱或 QQ）';
    return;
  }
  creating.value = true;
  newError.value = '';
  const { data, error } = await createTicket({
    type: newType.value,
    content: newContent.value.trim(),
    guest_contact: newContact.value.trim() || undefined
  });
  creating.value = false;
  if (error || !data) {
    newError.value = error || '提交失败';
    return;
  }
  newContent.value = '';
  load(1);
}

function priorityText(p: string): string {
  return ({ low: '低', normal: '普通', high: '高', urgent_paid: '加急' } as Record<string, string>)[p] || p;
}
function statusText(s: string): string {
  return ({ open: '待处理', processing: '处理中', resolved: '已解决', closed: '已关闭' } as Record<string, string>)[s] || s;
}
function ticketBadge(s: string): string {
  return ({ open: 'badge orange', processing: 'badge blue', resolved: 'badge green', closed: 'badge gray' } as Record<string, string>)[s] || 'badge gray';
}
function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '';
}
</script>
