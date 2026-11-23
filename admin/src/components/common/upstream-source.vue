<script setup lang="ts">
import { computed } from "vue";
import { NButton, NTag } from "naive-ui";

export interface UpstreamSource {
  kind?: string; stage?: string; connection_id?: string; connection_name?: string;
  driver?: string; product_code?: string; product_id?: string; page_id?: string; sku_id?: string;
  website_url?: string; product_url?: string; link_status?: string;
}
const props = defineProps<{ source?: UpstreamSource | null; compact?: boolean }>();
const stage = computed(() => ({ order: "下单时来源", purchase: "实际采购来源", current: "当前货源", legacy: "按原编号及当前渠道推算", unavailable: "历史来源未记录" }[props.source?.stage || ""] || "历史来源未记录"));
// Only backend-resolved HTTP links are navigation targets.
function safeURL(raw?: string) {
  if (!raw) return "";
  try { const u = new URL(raw); return ["https:", "http:"].includes(u.protocol) && !u.username && !u.password ? raw : ""; } catch { return ""; }
}
const isACG = computed(() => props.source?.driver === "acg_faka");
// Old ACG snapshots used the API code as product_id. It is not a web page ID.
const productID = computed(() => isACG.value ? props.source?.page_id || "" : props.source?.product_id || "");
const dockingCode = computed(() => props.source?.product_code && (isACG.value || props.source.product_code !== productID.value) ? props.source.product_code : "");
const missingLinkNote = computed(() => {
  if (props.source?.link_status === "invalid_template") return "商品链接模板无效，请在渠道设置中检查。";
  if (isACG.value && !productID.value) return props.source?.stage === "current"
    ? "尚未记录上游网页商品编号，请同步该商品后重试；对接码不一定支持网页搜索。"
    : "本次记录未保存上游网页商品编号；对接码不一定支持网页搜索，请按采购单号向上游核实。";
  return websiteURL.value ? "尚无已确认的商品直达链接，请向上游核实商品位置。" : "历史记录不足，无法确认商品链接；请按原采购单号核实。";
});
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
      <NButton v-if="productID" size="small" quaternary @click="copy('商品编号', productID)">复制商品编号</NButton>
      <NButton v-if="dockingCode" size="small" quaternary @click="copy('对接码', dockingCode)">复制对接码</NButton>
      <NButton v-if="source?.sku_id" size="small" quaternary @click="copy('规格编号', source.sku_id)">复制规格编号</NButton>
    </div>
    <div v-if="!compact" class="source-ids">
      <span v-if="productID">{{ isACG ? '网页商品编号' : '商品' }}：<code>{{ productID }}</code></span>
      <span v-if="source?.sku_id">规格：<code>{{ source.sku_id }}</code></span>
      <span v-if="dockingCode">对接码：<code>{{ dockingCode }}</code></span>
    </div>
    <p v-if="!compact && !productURL" class="source-note">{{ missingLinkNote }}</p>
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
