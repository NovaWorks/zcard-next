<script setup lang="ts">
import { ref, onMounted } from 'vue';
import type { MyLevelReply } from '@/api';
import { formatMoney } from '@/api/client';
import { levelDiscount, levelThreshold } from '@/composables/member-level';
defineProps<{ level: MyLevelReply }>();
const expanded = ref(false);
onMounted(() => { expanded.value = matchMedia('(min-width: 768px)').matches; });
</script>
<template>
  <section v-if="level.levels?.length" class="card level-benefits" aria-label="会员等级与折扣">
    <header><div><h3>会员等级与折扣</h3><p class="muted">当前：{{ level.current?.name || '普通会员' }} · {{ levelDiscount(level.current?.discount ?? 10000) }}</p></div>
      <span v-if="level.next" class="muted">下一等级：{{ level.next.name }}</span></header>
    <p v-if="level.next" class="next-benefit"><b>{{ level.next.name }}享 {{ levelDiscount(level.next.discount) }}</b><span>{{ levelThreshold(level.next) }}</span></p>
    <details :open="expanded" @toggle="expanded = ($event.target as HTMLDetailsElement).open">
      <summary>{{ expanded ? '收起等级列表' : `查看全部 ${level.levels.length} 个等级与折扣` }}</summary>
      <div class="level-list">
      <article v-for="item in level.levels" :key="item.id" class="level-item" :class="{ current: item.id === level.current?.id }">
        <div class="level-name"><b>{{ item.name }}</b><span v-if="item.id === level.current?.id" class="level-current">当前等级</span></div>
        <strong>{{ levelDiscount(item.discount) }}<small>会员折扣</small></strong>
        <p>{{ levelThreshold(item) }}</p>
      </article>
      </div>
    </details>
    <p class="muted level-note">已累计充值 {{ formatMoney(level.recharged_cents) }} · 已累计消费 {{ formatMoney(level.consumed_cents) }}。充值与消费按各等级条件累计，商品实际优惠以结算价为准。</p>
  </section>
</template>
<style scoped>
.level-benefits header { display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:8px; margin-bottom:14px; }
h3 { margin:0; font-size:16px; } header p { margin:6px 0 0; }
.next-benefit { display:flex; flex-direction:column; gap:6px; padding:10px 12px; background:var(--primary-light,#eff6ff); border-radius:8px; margin:0 0 8px; font-size:13px; }
.next-benefit span { color:#6b7280; font-size:12px; }
summary { cursor:pointer; min-height:44px; align-content:center; color:var(--primary-color,#0066e6); font-size:13px; }
.level-list { display:grid; grid-template-columns:repeat(auto-fit,minmax(200px,1fr)); gap:12px; }
.level-item { border:1px solid var(--border-color,#e5e7eb); border-radius:10px; padding:16px; min-width:0; }
.level-item.current { border-color:var(--primary-color,#0066e6); background:var(--primary-light,#eff6ff); }
.level-name { display:flex; flex-wrap:wrap; align-items:center; gap:8px; }
.level-current { font-size:11px; color:var(--primary-color,#0066e6); }
.level-item strong { display:block; font-size:24px; color:var(--primary-color,#0066e6); margin:12px 0; }
.level-item small { font-size:12px; font-weight:400; color:#6b7280; margin-left:8px; }
.level-item p { font-size:12px; line-height:1.7; margin:0; overflow-wrap:anywhere; }
.level-note { margin:12px 0 0; font-size:12px; line-height:1.7; }
@media(max-width:480px) { .level-list { grid-template-columns:minmax(0,1fr); } .level-item { padding:12px; } }
</style>
