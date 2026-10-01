import { t as $t, locale } from '@/i18n';
import type { LevelBrief } from '@/api';
import { formatMoney } from '@/api/client';
export function levelDiscount(discount: number) {
  return (!Number.isFinite(discount) || discount <= 0 || discount >= 10000) ? $t("原价") : locale.value === "en" ? `${Number(((10000 - discount) / 100).toFixed(2))}% off` : $t("{0} 折", [Number((discount / 1000).toFixed(2))]);
}
export function levelThreshold(level: LevelBrief) {
 if(level.display_mode === "contact") return $t("联系客服开通");
 if(level.acquire_mode === "manual") return $t("由管理员开通");
  const recharge = $t("累计充值 {0}", [formatMoney(level.threshold_recharge)]);
  const consume = $t("累计消费 {0}", [formatMoney(level.threshold_consume)]);
  switch (level.threshold_type) {
    case 'recharge': return recharge;
    case 'consume': return consume;
    case 'both_and': return $t("{0} 且 {1}", [recharge, consume]);
    case 'both_or': return $t("{0} 或 {1}", [recharge, consume]);
    default: return $t("按站点等级规则升级");
  }
}
