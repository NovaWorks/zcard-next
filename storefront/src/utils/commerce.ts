import { t as $t, locale } from '@/i18n';
/** 邮箱或国内 / 国际电话号码走联系方式查询；含字母的订单号仍逐单查询。 */
export function looksLikeContact(value: string): boolean {
  const text = value.trim();
  const digits = text.replace(/\D/g, '').length;
  return text.includes('@') || (/^\+?[0-9 ()-]+$/.test(text) && digits >= 6 && digits <= 15);
}

/** 百分比券存储应付比例：9000 = 应付 90% = 9 折。 */
export function couponDiscountLabel(value: number | string): string {
  const rate = Number(value);
  return Number.isInteger(rate) && rate > 0 && rate <= 10000
    ? locale.value === "en" ? `${(10000 - rate) / 100}% off` : $t("{0} 折", [rate / 1000])
    : $t("优惠以结算页为准");
}

/** Proto JSON 省略零值价格，购物车仍须用数值 0 参与累加。 */
export function cartPriceCents(value?: number): number {
  return Number(value ?? 0);
}
