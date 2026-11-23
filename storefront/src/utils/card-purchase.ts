import type { CardPurchaseRules } from '@/api';

type QuantityRules = { card_purchase?: CardPurchaseRules; min_quantity?: number; max_quantity?: number };

export function minimumQuantity(item: QuantityRules): number {
  return Math.max(1, item.card_purchase?.minimum || item.min_quantity || 1);
}

export function maximumQuantity(item: QuantityRules): number | undefined {
  if (item.card_purchase) return item.card_purchase.maximum > 0 ? item.card_purchase.maximum : undefined;
  return item.max_quantity && item.max_quantity > 0 ? item.max_quantity : 99;
}

export function validQuantity(item: QuantityRules, value: number): boolean {
  const maximum = maximumQuantity(item);
  return Number.isSafeInteger(value) && value >= minimumQuantity(item) && (maximum === undefined || value <= maximum);
}

export function clampQuantity(item: QuantityRules, value: number): number {
  const quantity = Math.max(minimumQuantity(item), value || minimumQuantity(item));
  const maximum = maximumQuantity(item);
  return maximum === undefined ? quantity : Math.min(maximum, quantity);
}
