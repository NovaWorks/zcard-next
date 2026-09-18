import { onScopeDispose, watch, type Ref } from 'vue';
import type { Product } from '@/api';

export function needsStockRefresh(p: Product) {
  return p.stock_status === 'unknown' || p.stock_status === 'stale' || (p.stock ?? 0) < -1;
}

// At most three background refreshes per displayed result set. Merge only stock
// fields; preserve browsing position, selection, prices and loaded mobile pages.
export function useStockRefresh(products: Ref<Product[]>, enabled: () => boolean, fetchStocks: () => Promise<Product[]>) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let generation = 0;
  let attempts = 0;
  let key = '';
  function stop() { if (timer) clearTimeout(timer); timer = undefined; }
  function schedule() {
    stop();
    if (typeof window === 'undefined' || !enabled() || attempts >= 3 || !products.value.some(needsStockRefresh)) return;
    const requestGeneration = generation;
    timer = setTimeout(async () => {
      timer = undefined;
      if (!enabled() || requestGeneration !== generation) return;
      if (document.hidden) { schedule(); return; }
      attempts++;
      try {
        const fresh = await fetchStocks();
        if (requestGeneration !== generation || !enabled()) return;
        const byID = new Map(fresh.map(p => [Number(p.id), p]));
        products.value = products.value.map(p => {
          const next = byID.get(Number(p.id));
          return next ? { ...p, stock: next.stock ?? 0, stock_status: next.stock_status, stock_reference: next.stock_reference, stock_checked_at: next.stock_checked_at } : p;
        });
      } catch { /* Keep existing stock/reference when the request fails. */ }
      finally { if (requestGeneration === generation) schedule(); }
    }, 15000);
  }
  watch(() => [enabled(), products.value.map(p => p.id).join(',')] as const, ([active, ids]) => {
    generation++;
    if (ids !== key) { key = ids; attempts = 0; }
    if (active) schedule(); else stop();
  }, { immediate: true });
  onScopeDispose(() => { generation++; stop(); });
}
