<script setup lang="ts">
import type { ProductControl } from '@/api';
const props = defineProps<{ controls: ProductControl[]; modelValue: Record<string,string>; prefix?: string }>();
const emit = defineEmits<{ 'update:modelValue': [Record<string,string>] }>();
function set(id: number, value: string) { emit('update:modelValue', { ...props.modelValue, [String(id)]: value }); }
function toggle(id: number, value: string, checked: boolean) { const values = new Set((props.modelValue[String(id)] || '').split(',').filter(Boolean)); checked ? values.add(value) : values.delete(value); set(id, [...values].join(',')); }
</script>
<template>
  <fieldset v-if="controls.length" class="order-fields">
    <legend>下单填写信息</legend>
    <p class="muted">本项全部数量交付到以下账号或地址；不同账号请分别下单。请付款前核对。</p>
    <div v-for="c in controls" :key="c.id" class="field">
      <label :for="`${prefix || 'order'}-${c.id}`">{{ c.name }} <span v-if="c.required" class="required">（必填）</span></label>
      <select v-if="c.type === 'select'" :id="`${prefix || 'order'}-${c.id}`" class="input" :value="modelValue[String(c.id)] || ''" :required="c.required" @change="set(c.id, ($event.target as HTMLSelectElement).value)">
        <option value="">请选择</option><option v-for="o in c.options || []" :key="o">{{ o }}</option>
      </select>
      <div v-else-if="c.type === 'radio' || c.type === 'checkbox'" class="options" :aria-label="c.name">
        <label v-for="o in c.options || []" :key="o"><input :type="c.type" :name="`${prefix || 'order'}-${c.id}`" :checked="c.type === 'radio' ? modelValue[String(c.id)] === o : (modelValue[String(c.id)] || '').split(',').includes(o)" @change="c.type === 'radio' ? set(c.id,o) : toggle(c.id,o,($event.target as HTMLInputElement).checked)" />{{ o }}</label>
      </div>
      <input v-else :id="`${prefix || 'order'}-${c.id}`" class="input" :type="c.type === 'password' ? 'password' : c.type === 'number' ? 'number' : 'text'" :value="modelValue[String(c.id)] || ''" :required="c.required" :maxlength="c.max_length || 500" :placeholder="c.placeholder || `请输入${c.name}`" autocomplete="off" @input="set(c.id,($event.target as HTMLInputElement).value)" />
      <small v-if="c.validation === 'tron'">填写 TRON 接收地址，请核对后再付款。</small>
      <small v-else-if="c.validation === 'url'">填写完整链接，包含 https://。</small>
    </div>
  </fieldset>
</template>
<style scoped>
.order-fields{min-width:0;border:1px solid var(--border-color,#e5e7eb);border-radius:10px;padding:16px;margin:12px 0}.order-fields legend{font-weight:600;padding:0 6px}.field{display:grid;gap:8px;margin:12px 0}.field label{font-size:14px}.input{box-sizing:border-box;width:100%;min-height:44px}.options{display:flex;flex-wrap:wrap;gap:8px 16px}.options label{display:flex;gap:8px;align-items:center;min-height:44px}.required{color:#b42318}.muted,small{font-size:13px;line-height:1.6;color:var(--text-muted,#6b7280)}
</style>
