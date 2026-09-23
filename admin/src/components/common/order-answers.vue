<script setup lang="ts">
import { computed } from 'vue';
const props=defineProps<{ value?:string }>();
const answers=computed<{name:string;value:string}[]>(()=>{try{const rows=JSON.parse(props.value || '[]');return Array.isArray(rows)?rows:[]}catch{return []}});
async function copy(value:string){try{await navigator.clipboard.writeText(value);window.$message?.success('已复制')}catch{window.$message?.error('复制失败，请手动选择')}}
</script>
<template><dl v-if="answers.length" class="order-answers"><div v-for="(a,i) in answers" :key="i"><dt>{{a.name}}</dt><dd>{{a.value}}</dd><button type="button" @click="copy(a.value)">复制</button></div></dl><span v-else class="opacity-50">无填写资料</span></template>
<style scoped>.order-answers{margin:8px 0;display:grid;gap:10px}.order-answers>div{display:flex;flex-wrap:wrap;gap:8px;align-items:baseline}dt{font-weight:600;min-width:80px}dd{margin:0;flex:1;min-width:100px;overflow-wrap:anywhere;white-space:pre-wrap}button{cursor:pointer;color:var(--primary-color,#1677ff);min-height:36px;padding:4px 10px}</style>
