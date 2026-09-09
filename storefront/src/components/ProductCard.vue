<template>
  <div
    class="product-card"
    :class="[mode === 'list' ? 'list-mode' : 'grid-mode']"
    @click="$router.push(`/product/${p.id}`)"
  >
    <div class="pc-cover">
      <img v-if="p.cover" :src="p.cover" :alt="p.name" loading="lazy" @error="onImgError" />
      <img v-else :src="NO_IMAGE" :alt="p.name" class="pc-noimg" loading="lazy" />
      <span v-if="p.points_required" class="pc-points-tag">{{ p.points_required }} 积分</span>
    </div>
    <div class="pc-body">
      <div class="pc-name" :title="p.name">{{ p.name }}</div>
      <div class="pc-price">{{ formatMoney(p.price_cents) }}</div>
      <div v-if="showSales || showStock" class="pc-meta">
        <span v-if="showSales" class="pc-sales">已售 {{ p.sales_count || 0 }}</span>
        <span v-if="showStock && p.stock_visible && p.stock_status === 'stale'" class="pc-stock-reference" :title="stockHint(p)">{{ p.stock_reference === -1 ? '上次库存不限' : `参考库存 ${p.stock_reference ?? 0}` }}</span>
        <span v-else-if="showStock && p.stock_visible && stockValue(p) >= 0">{{ stockValue(p) === 0 ? '暂时售罄' : `库存 ${stockValue(p)}` }}</span>
        <span v-else-if="showStock && p.stock_visible && p.stock === -1" class="pc-stock-free">不限库存</span>
        <span v-else-if="showStock && p.stock_visible" title="上游暂未提供库存，进入商品详情即可查询">库存需查询</span>
      </div>
      <button type="button" class="btn btn-primary pc-buy" :aria-label="`${buyLabel(p, mode)}：${p.name}`" @click.stop="$router.push(`/product/${p.id}`)">{{ buyLabel(p, mode) }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Product } from '@/api';
import { formatMoney } from '@/api/client';
import { NO_IMAGE, onImgError } from '@/no-image';
function stockValue(p: Product) { return p.stock ?? (p.stock_status === 'unknown' || p.stock_status === 'stale' ? -2 : 0); }
function buyLabel(p: Product, mode?: string) { return mode !== 'list' ? '查看详情' : stockValue(p) === 0 ? '查看' : stockValue(p) < -1 ? '查库存' : '购买'; }
function stockHint(p: Product) {
  const date = p.stock_checked_at ? new Date(p.stock_checked_at * 1000).toLocaleString() : '';
  return `上次同步${date ? '：' + date : ''}，仅供参考；进入商品详情重新核对库存`;
}


defineProps<{
  p: Product;
  mode?: 'grid' | 'list';
  /** 卡片「已售」显示（template.show_sales；缺省显示） */
  showSales?: boolean;
  /** 卡片「库存」显示（template.show_stock；叠加商品级 stock_visible；缺省显示） */
  showStock?: boolean;
}>();
</script>

<style scoped>
.product-card {
  background: #fff;
  border: 1px solid #e5e7eb;
  border-radius: 12px;
  overflow: hidden;
  cursor: pointer;
  transition: box-shadow 0.2s, border-color 0.2s, transform 0.2s;
  display: flex;
  flex-direction: column;
  min-width: 0; /* 网格/flex 子项防长词撑爆列宽（grid item 默认 min-width:auto） */
}
.product-card:hover {
  border-color: rgba(37, 99, 235, 0.45);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08);
  transform: translateY(-2px);
}
/* 封面 */
.pc-cover {
  position: relative;
  aspect-ratio: 1 / 1;
  background: #f1f5f9;
  overflow: hidden;
}
.pc-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: transform 0.3s;
  display: block;
}
.product-card:hover .pc-cover img { transform: scale(1.05); }
.pc-cover-placeholder {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 32px;
  font-weight: 700;
  color: #bfdbfe;
  background: linear-gradient(135deg, #eff6ff, #dbeafe);
}
/* 无图占位（SVG data URI）：contain 完整显示，不参与 hover 缩放 */
.pc-noimg { object-fit: contain !important; }
.product-card:hover .pc-cover img.pc-noimg { transform: none; }
.pc-points-tag {
  position: absolute;
  left: 8px;
  top: 8px;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  background: #4338ca;
  color: #fff;
}
/* 信息区 */
.pc-body { padding: 12px; display: flex; flex-direction: column; gap: 6px; flex: 1; min-width: 0; }
.pc-name {
  font-size: 14px;
  font-weight: 600;
  color: #111827;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 40px;
  word-break: break-word; /* 不可断长词（连续英文/URL）换行，防横向溢出 */
  overflow-wrap: anywhere;
}
.pc-price { color: #ff5722; font-size: 18px; font-weight: 700; }
.pc-meta {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: #64748b;
  margin-top: auto;
}
.pc-stock-free { color: #16a34a; }
.pc-stock-reference { color: #475569; }
.pc-buy { align-items: center; line-height: 1.2; flex-shrink: 0; display: none; margin-top: 8px; width: 100%; justify-content: center; }

/* 列表视图 */
.product-card.list-mode { flex-direction: row; align-items: center; gap: 14px; padding: 12px; }
.list-mode .pc-cover { width: 64px; height: 64px; aspect-ratio: 1 / 1; border-radius: 8px; flex-shrink: 0; }
.list-mode .pc-cover img { object-fit: contain; }
.product-card.list-mode:hover .pc-cover img { transform: none; }
.list-mode .pc-cover-placeholder { font-size: 20px; }
.list-mode .pc-body { padding: 0; flex-direction: row; align-items: center; gap: 14px; flex: 1; }
.list-mode .pc-name { min-height: auto; flex: 1; -webkit-line-clamp: 1; min-width: 0; }
.list-mode .pc-price { font-size: 16px; }
.list-mode .pc-meta { margin-top: 0; gap: 12px; }
.list-mode .pc-buy { display: inline-flex; width: auto; margin-top: 0; padding: 6px 14px; font-size: 13px; }

@media (min-width: 640px) {
  .grid-mode:hover .pc-buy { display: flex; }
}

/* 移动端双列窄卡：收紧留白，字号保持 ≥14px */
@media (max-width: 640px) {
  .grid-mode .pc-body { padding: 10px; gap: 5px; }
  .grid-mode .pc-price { font-size: 16px; }
  .grid-mode .pc-name { min-height: 38px; }
  .product-card.list-mode { align-items: center; gap: 10px; padding: 10px 12px; }
  .list-mode .pc-cover { width: 48px; height: 48px; }
  .list-mode .pc-body {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 3px 6px;
  }
  .list-mode .pc-name {
    grid-column: 1;
    grid-row: 1;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    white-space: normal;
    line-height: 20px;
  }
  .list-mode .pc-price { grid-column: 1; grid-row: 2; white-space: nowrap; line-height: 22px; }
  .list-mode .pc-buy {
    grid-column: 2;
    grid-row: 1 / 4;
    align-self: center;
    height: 44px;
    min-width: 60px;
    padding: 6px 8px;
    white-space: nowrap;
  }
  .list-mode .pc-meta {
    grid-column: 1; grid-row: 3; min-width: 0;
    justify-content: flex-start; gap: 8px;
    flex-wrap: wrap; white-space: normal; line-height: 18px;
  }
  .list-mode .pc-meta span { flex-shrink: 0; }
  .list-mode .pc-sales { order: 1; }
}
</style>
