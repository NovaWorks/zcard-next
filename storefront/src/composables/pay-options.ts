// 方式级收银台共享逻辑：渠道 → 顾客可见的支付方式选项（Payment.vue / Member.vue 充值同源）。
import type { ChannelItem } from '@/api';
import { formatMoney } from '@/api/client';

export interface PayOption {
  channel: string;
  method: string; // 多方式渠道的方式 code；单方式渠道为空串
  name: string;
  icon?: string;
  emoji: string;
  sub: string;
  feeText: string;
  recommended: boolean;
  recommendLabel: string;
  recommendDescription: string;
}

// 方式/渠道内置 emoji 回落（未配置自定义图标时）
const EMOJI: Record<string, string> = {
  wallet: '💰', alipay: '🅰️', wxpay: '💬', wechat: '💬', qqpay: '🐧',
  epay: '⚡', epusdt: '₮', bepusdt: '₮', stripe: '🟦', paypal: '🅿️',
};

export function emojiOf(code: string, driver: string) {
  return EMOJI[code] || EMOJI[driver] || '💳';
}

// 渠道 → 收银台方式级选项展平：多方式渠道（易支付/USDT 网关）每个方式一个选项，
// 顾客看到的是「支付宝 / 微信 / USDT·TRC20」而不是网关本身；单方式渠道保持原样。
export function flattenPayOptions(channels: ChannelItem[]): PayOption[] {
  const out: PayOption[] = [];
  for (const c of channels) {
    const methods = (c.methods || []).filter((m) => m.name);
    if (methods.length > 0) {
      for (const m of methods) {
        out.push({
          channel: c.code, method: m.code, name: m.name,
          feeText: feeText(c), recommended: !!m.recommended, recommendLabel: m.recommend_label || "推荐", recommendDescription: m.recommend_description || "",
          icon: m.icon || c.icon || undefined, emoji: emojiOf(m.code, c.driver),
          sub: c.name || (['epusdt', 'bepusdt'].includes(c.driver) ? 'USDT 链上收款' : '在线支付'),
        });
      }
    } else {
      out.push({
        channel: c.code, method: '', name: c.name,
        feeText: feeText(c), recommended: !!c.recommended, recommendLabel: c.recommend_label || '推荐', recommendDescription: c.recommend_description || '',
        icon: c.icon || undefined, emoji: emojiOf(c.code, c.driver),
        sub: c.driver === 'wallet' ? '使用账户余额' : '在线支付',
      });
    }
  }
  return out;
}

function feeText(c: ChannelItem): string {
  if (c.driver === 'wallet' || c.fee_bearer !== 'user' || !Number(c.fee)) {
    return ['epusdt', 'bepusdt'].includes(c.driver) ? '平台免手续费' : '免手续费';
  }
  return c.fee_type === 'percent' ? `手续费 ${Number(c.fee) / 100}%` : `手续费 ${formatMoney(Number(c.fee))}`;
}
