import { ref, onMounted, onUnmounted } from 'vue';
import type { FlashOffer } from '@/api';

// All visible product cards share one clock, so a campaign ends without reloading.
const now = ref(Date.now());
let users = 0;
let timer: ReturnType<typeof setInterval> | undefined;
export function useFlashOffers() {
  onMounted(() => { now.value = Date.now(); if (users++ === 0) timer = setInterval(() => { now.value = Date.now(); }, 1000); });
  onUnmounted(() => { if (--users === 0) { clearInterval(timer); timer = undefined; } });
  function active(offer?: FlashOffer) { return offer && offer.end_at * 1000 > now.value ? offer : undefined; }
  function price(base: number, offer?: FlashOffer) {
    const f = active(offer);
    return f && (f.remaining || 0) > 0 && f.price_cents > 0 ? Math.min(base, f.price_cents) : base;
  }
  return { active, price };
}
