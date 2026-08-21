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
      <div class="pc-name">{{ p.name }}</div>
      <div class="pc-price">{{ formatMoney(p.price_cents) }}</div>
      <div class="pc-meta">
        <span>已售 {{ p.sales_count || 0 }}</span>
        <span v-if="p.stock_visible && p.stock_type === 'card' && p.stock >= 0">库存 {{ p.stock }}</span>
        <span v-else-if="p.stock_type !== 'card'" class="pc-stock-free">不限库存</span>
      </div>
      <button class="btn btn-primary pc-buy" @click.stop="$router.push(`/product/${p.id}`)">查看详情</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Product } from '@/api';
import { formatMoney } from '@/api/client';
import { NO_IMAGE, onImgError } from '@/no-image';

defineProps<{
  p: Product;
  mode?: 'grid' | 'list';
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
.pc-body { padding: 12px; display: flex; flex-direction: column; gap: 6px; flex: 1; }
.pc-name {
  font-size: 14px;
  font-weight: 600;
  color: #111827;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 40px;
}
.pc-price { color: #ff5722; font-size: 18px; font-weight: 700; }
.pc-meta {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: #9ca3af;
  margin-top: auto;
}
.pc-stock-free { color: #16a34a; }
.pc-buy { display: none; margin-top: 8px; width: 100%; justify-content: center; }

/* 列表视图 */
.product-card.list-mode { flex-direction: row; align-items: center; gap: 14px; padding: 12px; }
.list-mode .pc-cover { width: 64px; height: 64px; aspect-ratio: auto; border-radius: 8px; flex-shrink: 0; }
.list-mode .pc-cover-placeholder { font-size: 20px; }
.list-mode .pc-body { padding: 0; flex-direction: row; align-items: center; gap: 14px; flex: 1; }
.list-mode .pc-name { min-height: auto; flex: 1; -webkit-line-clamp: 1; }
.list-mode .pc-price { font-size: 16px; }
.list-mode .pc-meta { margin-top: 0; gap: 12px; }
.list-mode .pc-buy { display: inline-flex; width: auto; margin-top: 0; padding: 6px 14px; font-size: 13px; }

@media (min-width: 640px) {
  .grid-mode:hover .pc-buy { display: flex; }
}
</style>
