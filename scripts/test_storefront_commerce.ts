import assert from 'node:assert/strict';
import { cartPriceCents, couponDiscountLabel, looksLikeContact } from '../storefront/src/utils/commerce';

for (const contact of ['13800138000', '+84901234567', '0901234567', '+86 13800138000', '+1 (415) 555-0100', ' buyer@example.com ']) {
  assert.equal(looksLikeContact(contact), true, `contact lookup: ${contact}`);
}
for (const order of ['S202610010001', 'S12345678901', 'ORDER-123456', '12345', '1234567890123456', '', '+', '++84901234567']) {
  assert.equal(looksLikeContact(order), false, `order lookup: ${order}`);
}

for (const [rate, label] of [[9000, '9 折'], [9800, '9.8 折'], [9850, '9.85 折'], [9999, '9.999 折'], [10000, '10 折'], [1, '0.001 折'], ['9500', '9.5 折']] as const) {
  assert.equal(couponDiscountLabel(rate), label);
}
for (const rate of [0, -1, 10001, 9000.5, NaN, Infinity, 'unknown', '']) {
  assert.equal(couponDiscountLabel(rate), '优惠以结算页为准');
}
const mixedCart: { price_cents?: number; quantity: number }[] = [
  { price_cents: 1200, quantity: 1 }, { quantity: 1 }, { price_cents: 0, quantity: 2 },
];
assert.deepEqual(mixedCart.map(item => cartPriceCents(item.price_cents) * item.quantity), [1200, 0, 0]);
assert.equal(mixedCart.reduce((sum, item) => sum + cartPriceCents(item.price_cents) * item.quantity, 0), 1200);
assert.equal([{ quantity: 3 }, { price_cents: 0, quantity: 1 }].reduce((sum, item) => sum + cartPriceCents(item.price_cents) * item.quantity, 0), 0);
assert.equal(cartPriceCents(undefined), 0);
assert.equal(cartPriceCents(0), 0);
console.log('PASS contact lookup, order IDs, coupon rate labels and mixed carts with omitted zero prices');
