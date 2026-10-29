<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { t as $t, uiText } from '@/i18n';
import { authState } from '@/auth';
import { formatMoney, getToken } from '@/api/client';
import { actSocial, getSocial, type SocialOrder } from '@/api/social';
import { newRequestId } from '../../../packages/request-id';
const props=defineProps<{orderNo:string}>();
const emit=defineEmits<{refresh:[]}>();
const snapshot=ref<SocialOrder>(),error=ref(''),actionError=ref(''),loading=ref(false),busy=ref(false);
const pending=ref<{action:'cancel'|'refill';id:string}>();
const key=computed(()=>`zcard_social_action:${authState.username}:${props.orderNo}`);
let closed=false,generation=0,timer:ReturnType<typeof setTimeout>|undefined;
const titles=computed<Record<string,string>>(()=>({pending:$t('等待处理'),queued:$t('排队中'),processing:$t('处理中'),in_progress:$t('处理中'),completed:$t('已完成'),partial:$t('部分完成'),canceled:$t('已取消'),rejected:$t('未受理'),failed:$t('服务未完成'),review:$t('待核对'),submit_unknown:$t('提交结果待确认')}));
const title=computed(()=>titles.value[snapshot.value?.state || 'pending'] || $t('进度待确认'));
const progress=computed(()=>{const s=snapshot.value?.snapshot;if(s?.quantity==null || s.completed_quantity==null || Number(s.quantity)<=0)return undefined;return {max:Number(s.quantity),value:Math.max(0,Math.min(Number(s.completed_quantity),Number(s.quantity)))}});
function session(){return {key:key.value,token:getToken(),generation}}
function same(m:ReturnType<typeof session>){return !closed && m.key===key.value && m.token===getToken() && m.generation===generation}
function save(value?:typeof pending.value){pending.value=value;try{if(value)sessionStorage.setItem(key.value,JSON.stringify(value));else sessionStorage.removeItem(key.value)}catch{}}
function restore(){pending.value=undefined;try{const p=JSON.parse(sessionStorage.getItem(key.value)||'null');if(p?.id && ['cancel','refill'].includes(p.action))pending.value=p}catch{}}
function apply(data:SocialOrder){const before=snapshot.value?.refunded_amount_cents;snapshot.value=data;if(pending.value && data.operation_request_id===pending.value.id && ['succeeded','rejected'].includes(data.operation_status || ''))save();if(before!==undefined && Number(before)!==Number(data.refunded_amount_cents))emit('refresh')}
function schedule(active:boolean){if(timer)clearTimeout(timer);if(active && !closed)timer=setTimeout(()=>void load(),8000)}
async function load(){if(loading.value || !getToken())return;const m=session();loading.value=true;const r=await getSocial(props.orderNo);if(!same(m))return;loading.value=false;if(r.data){error.value='';apply(r.data);schedule(!!r.data.active || !!pending.value)}else{error.value=r.error || $t('无法读取服务进度');schedule(r.status!==401 && r.status!==403)}}
async function action(kind:'cancel'|'refill'){if(busy.value)return;if(!pending.value)save({action:kind,id:newRequestId()});const intent=pending.value!;if(intent.action!==kind)return;const m=session();busy.value=true;actionError.value='';const r=await actSocial(props.orderNo,intent.action,intent.id);if(!same(m))return;busy.value=false;if(r.data){apply(r.data);schedule(!!r.data.active || !!pending.value)}else{actionError.value=r.error || $t('操作结果尚未确认，请稍后确认原操作');schedule(true)}}
watch([()=>props.orderNo,()=>authState.username],()=>{generation++;snapshot.value=undefined;error.value=actionError.value='';loading.value=busy.value=false;if(timer)clearTimeout(timer);restore();void load()});
onMounted(()=>{restore();void load()});onBeforeUnmount(()=>{closed=true;generation++;if(timer)clearTimeout(timer)});
</script>
<template>
 <section class="card social-order" aria-labelledby="social-order-title">
  <div class="social-order-heading"><h2 id="social-order-title">{{ $t('服务进度') }}</h2><span class="social-state" role="status">{{title}}</span></div>
  <p v-if="!snapshot && loading" class="muted" role="status">{{ $t('正在读取进度…') }}</p>
  <template v-if="snapshot">
   <progress v-if="progress" :value="progress.value" :max="progress.max" :aria-label="$t('服务完成进度')" />
   <dl class="social-metrics">
    <div v-if="snapshot.snapshot?.quantity!=null"><dt>{{ $t('服务数量') }}</dt><dd>{{snapshot.snapshot.quantity}}</dd></div>
    <div v-if="snapshot.snapshot?.quantity!=null && snapshot.snapshot.completed_quantity!=null"><dt>{{ $t('已完成') }}</dt><dd>{{snapshot.snapshot.completed_quantity}}</dd></div>
    <div v-if="snapshot.snapshot?.quantity!=null && snapshot.snapshot.remains!=null"><dt>{{ $t('剩余数量') }}</dt><dd>{{snapshot.snapshot.remains}}</dd></div>
    <div v-if="snapshot.snapshot?.quantity!=null && snapshot.snapshot.start_count!=null"><dt>{{ $t('开始时数量') }}</dt><dd>{{snapshot.snapshot.start_count}}</dd></div>
    <div><dt>{{ $t('本次实付') }}</dt><dd>{{formatMoney(snapshot.paid_amount_cents)}}</dd></div>
    <div v-if="Number(snapshot.refunded_amount_cents)>0"><dt>{{ $t('已退回会员余额') }}</dt><dd>{{formatMoney(snapshot.refunded_amount_cents)}}</dd></div>
   </dl>
   <p v-if="snapshot.message" class="muted" role="status">{{uiText(snapshot.message)}}</p>
   <p v-if="['partial','canceled','failed','review'].includes(snapshot.state) && !['succeeded','partially_refunded','pending'].includes(snapshot.refund_status)" class="muted">{{ $t('退款金额尚待供货确认，确认后会退回会员余额。') }}</p>
   <p v-if="snapshot.operation_status==='succeeded'" class="muted">{{snapshot.operation_action==='refill'?$t('补量申请已确认，请继续查看服务进度。'):$t('取消申请已确认，以当前服务和退款状态为准。')}}</p>
   <p v-if="snapshot.operation_status==='rejected'" class="muted">{{ $t('上次操作未获允许，可按当前服务状态继续处理。') }}</p>
   <div class="social-actions"><button class="btn secondary" :disabled="loading || busy" @click="load">{{loading?$t('刷新中…'):$t('刷新进度')}}</button><button v-if="snapshot.can_cancel && !pending" class="btn secondary" :disabled="busy" @click="action('cancel')">{{ $t('申请取消') }}</button><button v-if="snapshot.can_refill && !pending" class="btn secondary" :disabled="busy" @click="action('refill')">{{ $t('申请补量') }}</button><button v-if="pending" class="btn secondary" :disabled="busy" @click="action(pending.action)">{{busy?$t('确认中…'):$t('确认原操作')}}</button></div>
  </template>
  <p v-if="error" class="error" role="alert">{{uiText(error)}}</p><p v-if="actionError" class="error" role="alert">{{uiText(actionError)}}</p>
 </section>
</template>
<style scoped>
.social-order{padding:24px}.social-order-heading{display:flex;align-items:center;justify-content:space-between;gap:16px}.social-order h2{font-size:20px;margin:0}.social-state{padding:7px 12px;border:1px solid var(--border-color,#dbe2eb);border-radius:6px;font-size:14px;white-space:nowrap}.social-order progress{display:block;width:100%;height:12px;margin:20px 0;accent-color:var(--zc-primary,#2563eb)}.social-metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(125px,1fr));gap:20px;margin:24px 0}.social-metrics dt{font-size:13px;color:var(--color-text-secondary,#64748b);line-height:1.6}.social-metrics dd{margin:6px 0 0;font-size:20px;font-weight:600;font-variant-numeric:tabular-nums}.social-actions{display:flex;flex-wrap:wrap;gap:10px;margin-top:20px}.social-actions .btn{min-height:44px}.social-order p{line-height:1.6;overflow-wrap:anywhere}@media(max-width:600px){.social-order{padding:20px}.social-order-heading{align-items:flex-start}.social-order h2{font-size:18px}.social-actions .btn{flex:1 1 auto}}
</style>
