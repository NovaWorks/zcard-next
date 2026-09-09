import type { LevelBrief } from '@/api';
import { formatMoney } from '@/api/client';
export function levelDiscount(discount: number) {
  return (!Number.isFinite(discount) || discount <= 0 || discount >= 10000) ? '原价' : `${Number((discount / 1000).toFixed(2))} 折`;
}
export function levelThreshold(level: LevelBrief) {
  const recharge = `累计充值 ${formatMoney(level.threshold_recharge)}`;
  const consume = `累计消费 ${formatMoney(level.threshold_consume)}`;
  switch (level.threshold_type) {
    case 'recharge': return recharge;
    case 'consume': return consume;
    case 'both_and': return `${recharge} 且 ${consume}`;
    case 'both_or': return `${recharge} 或 ${consume}`;
    default: return '按站点等级规则升级';
  }
}
