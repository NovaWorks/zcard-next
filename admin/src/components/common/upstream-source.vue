<script setup lang="ts">
import { computed } from "vue";
import { NButton, NTag } from "naive-ui";

export interface UpstreamSource {
  kind?: string; stage?: string; connection_id?: string; connection_name?: string;
  driver?: string; product_code?: string; product_id?: string; sku_id?: string;
  website_url?: string; product_url?: string; link_status?: string;
}
const props = defineProps<{ source?: UpstreamSource | null; compact?: boolean }>();
const stage = computed(() => ({ order: "下单时来源", purchase: "实际采购来源", current: "当前货源", legacy: "按原编号及当前渠道推算", unavailable: "历史来源未记录" }[props.source?.stage || ""] || "历史来源未记录"));
// Only backend-resolved HTTP links are navigation targets.
function safeURL(raw?: string) {
  if (!raw) return "";
  try { const u = new URL(raw); return ["https:", "http:"].includes(u.protocol) && !u.username && !u.password ? raw : ""; } catch { return ""; }
}
const productURL = computed(() => safeURL(props.source?.product_url));
const websiteURL = computed(() => safeURL(props.source?.website_url));
async function copy(label: string, value?: string) {
  if (!value) return;
  try { await navigator.clipboard.writeText(value); window.$message?.success(`已复制${label}`); }
  catch { window.$message?.error(`复制失败，请手动复制${label}：${value}`); }
}
</script>

<template>
  <span v-if="source?.kind === 'self'">自营</span>
  <div v-else class="upstream-source" :class="{ compact }">
    <div v-if="!compact" class="source-heading">
      <strong>{{ source?.connection_name || (source?.connection_id ? `渠道 #${source.connection_id}` : '来源待核实') }}</strong>
      <NTag size="small" :bordered="false" :type="source?.stage === 'legacy' || !source ? 'warning' : 'default'">{{ stage }}</NTag>
    </div>
    <div class="source-actions">
      <a v-if="productURL" :href="productURL" target="_blank" rel="noopener noreferrer">打开上游商品 ↗</a>
      <a v-else-if="websiteURL" :href="websiteURL" target="_blank" rel="noopener noreferrer">打开上游网站 ↗</a>
      <span v-else class="source-note">上游链接未记录</span>
      <NButton v-if="source?.product_id" size="small" quaternary @click="copy('商品编号', source.product_id)">复制商品编号</NButton>
      <NButton v-if="source?.sku_id" size="small" quaternary @click="copy('规格编号', source.sku_id)">复制规格编号</NButton>
    </div>
    <div v-if="!compact" class="source-ids">
      <span v-if="source?.product_id">商品：<code>{{ source.product_id }}</code></span>
      <span v-if="source?.sku_id">规格：<code>{{ source.sku_id }}</code></span>
      <span v-if="source?.product_code && source.product_code !== source.product_id">原始编码：<code>{{ source.product_code }}</code></span>
    </div>
    <p v-if="!compact && !productURL" class="source-note">{{ source?.link_status === 'invalid_template' ? '商品链接模板无效，请在渠道设置中检查。' : websiteURL ? '此渠道尚无可确认的商品直达链接，请复制原商品、规格编号到上游核对。' : '历史记录不足，无法确认商品链接；请按原采购单号核实。' }}</p>
  </div>
</template>

<style scoped>
.upstream-source { min-width: 0; }
.source-heading, .source-actions, .source-ids { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 12px; }
.source-actions a { display: inline-flex; align-items: center; min-height: 44px; color: #2080f0; text-decoration: underline; }
.source-actions :deep(button) { min-height: 44px; }
.source-ids { overflow-wrap: anywhere; }
.source-note { color: #777; font-size: 12px; margin: 4px 0; }
.compact .source-actions { gap: 0 6px; }
</style>
